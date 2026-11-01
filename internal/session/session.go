// Package session discovers live SSH sessions from /proc.
//
// With OpenSSH privilege separation every authenticated connection has a
// root-owned monitor ("sshd: alice [priv]") and an unprivileged child that
// runs as the user ("sshd: alice"). The child does all the network work —
// the client socket, every forwarded connection and the udpgw stream — so:
//   - one user-owned sshd process = one session,
//   - its socket table gives us the real client address,
//   - its read/write counters give us the traffic of that tunnel.
//
// OpenSSH 9.8+ names the process "sshd-session"; both names are handled.
package session

import (
	"net/netip"
	"sort"
	"syscall"
	"time"

	"sshfortress/internal/procfs"
)

// Session is one authenticated SSH connection.
type Session struct {
	PID        int       `json:"pid"`         // user-owned sshd process
	MonitorPID int       `json:"monitor_pid"` // root-owned [priv] parent
	Username   string    `json:"username"`
	UID        int       `json:"uid"`
	ClientIP   string    `json:"client_ip"`
	ClientPort int       `json:"client_port"`
	ServerPort int       `json:"server_port"`
	Via        string    `json:"via"` // ssh, ws, http, tls, …
	StartedAt  time.Time `json:"started_at"`
	// Bytes is the tunnel's total traffic estimate (see Scanner).
	Bytes uint64 `json:"bytes"`
	// UDPGWPorts are local ports of this session's connections to udpgw;
	// the daemon uses them to attribute UDP (call) traffic.
	UDPGWPorts []int `json:"-"`
}

// Peer describes a connection that arrived through the gateway; it lets the
// scanner show the real client IP for TLS/WebSocket/CDN sessions.
type Peer struct {
	IP   string `json:"ip"`
	Port int    `json:"port"`
	Via  string `json:"via"`
}

// Scanner finds sessions.
type Scanner struct {
	// PeerLookup returns gateway info for a monitor PID (optional).
	PeerLookup func(monitorPID int) (Peer, bool)
	// UDPGWPorts are the local udpgw listen ports (optional).
	UDPGWPorts []int
	// SkipUsers are never reported (privsep accounts).
	SkipUsers map[string]bool
}

func isSSHD(comm string) bool {
	return comm == "sshd" || comm == "sshd-session"
}

// Scan returns all live user sessions, oldest first.
func (s *Scanner) Scan() ([]Session, error) {
	procs, err := procfs.ListProcs(isSSHD)
	if err != nil {
		return nil, err
	}
	byPID := make(map[int]procfs.Proc, len(procs))
	for _, p := range procs {
		byPID[p.PID] = p
	}
	passwd := procfs.Passwd()
	skip := s.SkipUsers
	if skip == nil {
		skip = map[string]bool{"sshd": true, "nobody": true}
	}

	var tcp *procfs.TCPTable
	udpgw := map[uint16]bool{}
	for _, p := range s.UDPGWPorts {
		udpgw[uint16(p)] = true
	}

	var out []Session
	for _, p := range procs {
		if p.UID == 0 {
			continue
		}
		name, ok := passwd[p.UID]
		if !ok || skip[name] {
			continue
		}
		// The user-owned child's parent must be a root sshd monitor.
		// (This also filters the pre-auth privsep child.)
		parent, ok := byPID[p.PPID]
		if !ok || parent.UID != 0 {
			continue
		}
		sess := Session{
			PID:        p.PID,
			MonitorPID: p.PPID,
			Username:   name,
			UID:        p.UID,
			Via:        "ssh",
			StartedAt:  p.StartTime,
		}
		if io, err := procfs.ReadIO(p.PID); err == nil {
			// Every tunnelled byte is read once and written once by this
			// process (client⇄destination), so the average of the two
			// counters is the tunnel traffic in both directions.
			sess.Bytes = (io.RChar + io.WChar) / 2
		}
		if tcp == nil {
			if tcp, err = procfs.ReadTCP(); err != nil {
				tcp = &procfs.TCPTable{ByInode: map[uint64]procfs.TCPSock{}, ListenPorts: map[uint16]bool{}}
			}
		}
		s.attachSockets(&sess, tcp, udpgw)
		if s.PeerLookup != nil {
			if peer, ok := s.PeerLookup(sess.MonitorPID); ok {
				if peer.IP != "" {
					sess.ClientIP = peer.IP
					sess.ClientPort = peer.Port
				}
				if peer.Via != "" {
					sess.Via = peer.Via
				}
			}
		}
		out = append(out, sess)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].PID < out[j].PID
		}
		return out[i].StartedAt.Before(out[j].StartedAt)
	})
	return out, nil
}

// attachSockets finds the client socket (local port is a listening port)
// and any connections to udpgw.
func (s *Scanner) attachSockets(sess *Session, tcp *procfs.TCPTable, udpgw map[uint16]bool) {
	for _, ino := range procfs.SocketInodes(sess.PID) {
		sk, ok := tcp.ByInode[ino]
		if !ok || sk.State != procfs.TCPEstablished {
			continue
		}
		if udpgw[sk.Remote.Port()] && sk.Remote.Addr().IsLoopback() {
			sess.UDPGWPorts = append(sess.UDPGWPorts, int(sk.Local.Port()))
			continue
		}
		if sess.ClientIP == "" && tcp.ListenPorts[sk.Local.Port()] {
			sess.ClientIP = addrString(sk.Remote.Addr())
			sess.ClientPort = int(sk.Remote.Port())
			sess.ServerPort = int(sk.Local.Port())
		}
	}
}

func addrString(a netip.Addr) string {
	return a.Unmap().String()
}

// Kill terminates a session and waits (up to ~1.5s) for it to exit.
// SIGTERM lets sshd close the connection cleanly; SIGKILL follows if the
// same process (checked by start time, so a recycled PID is never hit) is
// still alive.
func Kill(pid int) error {
	if pid <= 1 {
		return nil
	}
	before, err := procfs.ReadProc(pid, time.Time{})
	if err != nil {
		return nil // already gone
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if err == syscall.ESRCH {
			return nil
		}
		return err
	}
	for i := 0; i < 15; i++ {
		time.Sleep(100 * time.Millisecond)
		now, err := procfs.ReadProc(pid, time.Time{})
		if err != nil || !now.StartTime.Equal(before.StartTime) {
			return nil
		}
	}
	err = syscall.Kill(pid, syscall.SIGKILL)
	if err == syscall.ESRCH {
		return nil
	}
	return err
}

// KillNow sends SIGKILL immediately.
func KillNow(pid int) error {
	if pid <= 1 {
		return nil
	}
	err := syscall.Kill(pid, syscall.SIGKILL)
	if err == syscall.ESRCH {
		return nil
	}
	return err
}

// ByUser groups sessions per username.
func ByUser(sessions []Session) map[string][]Session {
	m := map[string][]Session{}
	for _, s := range sessions {
		m[s.Username] = append(m[s.Username], s)
	}
	return m
}
