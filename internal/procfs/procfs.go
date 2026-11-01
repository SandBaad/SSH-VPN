// Package procfs reads process, socket and I/O information straight from
// /proc. It replaces v1's parsing of `ss` output, which never worked: with a
// state filter `ss` drops the State column, so every line was skipped and
// the limiter/monitor silently saw zero sessions.
package procfs

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Root is the procfs mount point (overridable in tests).
var Root = "/proc"

// clockTicks is USER_HZ, which is 100 on every Linux architecture.
const clockTicks = 100

// TCP socket states as printed in /proc/net/tcp.
const (
	TCPEstablished = 0x01
	TCPListen      = 0x0A
)

// Proc is one process.
type Proc struct {
	PID       int
	PPID      int
	UID       int // effective uid
	Comm      string
	StartTime time.Time
}

// ListProcs returns processes whose comm satisfies match (all when nil).
func ListProcs(match func(comm string) bool) ([]Proc, error) {
	entries, err := os.ReadDir(Root)
	if err != nil {
		return nil, err
	}
	boot := BootTime()
	var out []Proc
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		p, err := ReadProc(pid, boot)
		if err != nil {
			continue // process exited while we were scanning
		}
		if match != nil && !match(p.Comm) {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// ReadProc reads one process. boot may be zero (it is then looked up).
func ReadProc(pid int, boot time.Time) (Proc, error) {
	dir := filepath.Join(Root, strconv.Itoa(pid))
	data, err := os.ReadFile(filepath.Join(dir, "stat"))
	if err != nil {
		return Proc{}, err
	}
	p, err := parseStat(data)
	if err != nil {
		return Proc{}, err
	}
	p.PID = pid
	if boot.IsZero() {
		boot = BootTime()
	}
	p.StartTime = boot.Add(time.Duration(p.startTicks) * time.Second / clockTicks)
	st, err := os.Stat(dir)
	if err != nil {
		return Proc{}, err
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		p.UID = int(sys.Uid)
	}
	return p.Proc, nil
}

type statFields struct {
	Proc
	startTicks uint64
}

// parseStat parses /proc/<pid>/stat. comm may contain spaces and ')' so we
// split on the LAST ')'.
func parseStat(data []byte) (statFields, error) {
	var sf statFields
	open := bytes.IndexByte(data, '(')
	closeIdx := bytes.LastIndexByte(data, ')')
	if open < 0 || closeIdx < open {
		return sf, fmt.Errorf("malformed stat")
	}
	sf.Comm = string(data[open+1 : closeIdx])
	rest := strings.Fields(string(data[closeIdx+1:]))
	// rest[0]=state rest[1]=ppid ... rest[19]=starttime (field 22 overall)
	if len(rest) < 20 {
		return sf, fmt.Errorf("short stat")
	}
	sf.PPID, _ = strconv.Atoi(rest[1])
	sf.startTicks, _ = strconv.ParseUint(rest[19], 10, 64)
	return sf, nil
}

var (
	bootOnce sync.Once
	bootTime time.Time
)

// BootTime returns the system boot time from /proc/stat.
func BootTime() time.Time {
	bootOnce.Do(func() {
		f, err := os.Open(filepath.Join(Root, "stat"))
		if err != nil {
			return
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "btime ") {
				if v, err := strconv.ParseInt(strings.TrimSpace(line[6:]), 10, 64); err == nil {
					bootTime = time.Unix(v, 0)
				}
				return
			}
		}
	})
	return bootTime
}

// IO holds a process's I/O counters.
type IO struct {
	RChar uint64 // bytes passed to read()-family syscalls
	WChar uint64 // bytes passed to write()-family syscalls
}

// ReadIO reads /proc/<pid>/io (needs root for other users' processes).
func ReadIO(pid int) (IO, error) {
	data, err := os.ReadFile(filepath.Join(Root, strconv.Itoa(pid), "io"))
	if err != nil {
		return IO{}, err
	}
	var io IO
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		n, _ := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
		switch k {
		case "rchar":
			io.RChar = n
		case "wchar":
			io.WChar = n
		}
	}
	return io, nil
}

// SocketInodes returns the inodes of all sockets a process has open.
func SocketInodes(pid int) []uint64 {
	dir := filepath.Join(Root, strconv.Itoa(pid), "fd")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []uint64
	for _, e := range entries {
		link, err := os.Readlink(filepath.Join(dir, e.Name()))
		if err != nil || !strings.HasPrefix(link, "socket:[") {
			continue
		}
		if ino, err := strconv.ParseUint(strings.TrimSuffix(link[8:], "]"), 10, 64); err == nil {
			out = append(out, ino)
		}
	}
	return out
}

// TCPSock is one row of /proc/net/tcp{,6}.
type TCPSock struct {
	Local  netip.AddrPort
	Remote netip.AddrPort
	State  uint8
	UID    int
	Inode  uint64
}

// TCPTable is a snapshot of all TCP sockets.
type TCPTable struct {
	ByInode map[uint64]TCPSock
	// ListenPorts holds every port with a socket in LISTEN state.
	ListenPorts map[uint16]bool
}

// ReadTCP reads /proc/net/tcp and /proc/net/tcp6.
func ReadTCP() (*TCPTable, error) {
	t := &TCPTable{ByInode: map[uint64]TCPSock{}, ListenPorts: map[uint16]bool{}}
	var firstErr error
	for _, name := range []string{"net/tcp", "net/tcp6"} {
		data, err := os.ReadFile(filepath.Join(Root, name))
		if err != nil {
			if firstErr == nil && name == "net/tcp" {
				firstErr = err
			}
			continue
		}
		parseTCP(data, t)
	}
	if len(t.ByInode) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return t, nil
}

func parseTCP(data []byte, t *TCPTable) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	first := true
	for sc.Scan() {
		if first { // header
			first = false
			continue
		}
		f := strings.Fields(sc.Text())
		if len(f) < 10 {
			continue
		}
		local, err1 := parseHexAddr(f[1])
		remote, err2 := parseHexAddr(f[2])
		state, err3 := strconv.ParseUint(f[3], 16, 8)
		inode, err4 := strconv.ParseUint(f[9], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			continue
		}
		uid, _ := strconv.Atoi(f[7])
		s := TCPSock{Local: local, Remote: remote, State: uint8(state), UID: uid, Inode: inode}
		if s.State == TCPListen {
			t.ListenPorts[local.Port()] = true
		}
		if inode != 0 {
			t.ByInode[inode] = s
		}
	}
}

// parseHexAddr parses "0100007F:1F90" (IPv4) or a 32-hex-digit IPv6 form.
// The kernel prints each 32-bit word in host byte order.
func parseHexAddr(s string) (netip.AddrPort, error) {
	ipHex, portHex, ok := strings.Cut(s, ":")
	if !ok {
		return netip.AddrPort{}, fmt.Errorf("bad addr %q", s)
	}
	port, err := strconv.ParseUint(portHex, 16, 16)
	if err != nil {
		return netip.AddrPort{}, err
	}
	raw, err := hex.DecodeString(ipHex)
	if err != nil {
		return netip.AddrPort{}, err
	}
	var ip netip.Addr
	switch len(raw) {
	case 4:
		var b [4]byte
		binary.NativeEndian.PutUint32(b[:], binary.BigEndian.Uint32(raw))
		ip = netip.AddrFrom4(b)
	case 16:
		var b [16]byte
		for i := 0; i < 4; i++ {
			binary.NativeEndian.PutUint32(b[i*4:], binary.BigEndian.Uint32(raw[i*4:]))
		}
		ip = netip.AddrFrom16(b).Unmap()
	default:
		return netip.AddrPort{}, fmt.Errorf("bad ip %q", ipHex)
	}
	return netip.AddrPortFrom(ip, uint16(port)), nil
}

// Passwd maps uids to user names from /etc/passwd.
func Passwd() map[int]string {
	out := map[int]string{}
	data, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Split(line, ":")
		if len(f) < 3 {
			continue
		}
		if uid, err := strconv.Atoi(f[2]); err == nil {
			if _, dup := out[uid]; !dup {
				out[uid] = f[0]
			}
		}
	}
	return out
}
