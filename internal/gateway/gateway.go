// Package gateway is SSH-VPN's multi-protocol front door.
//
// Every gateway port understands, at the same time:
//
//   - raw SSH                      (any SSH client / "SSH direct")
//   - HTTP payload / WebSocket     ("SSH-WS", "HTTP payload", Cloudflare CDN)
//   - TLS / SSL with any SNI       ("SSH-SSL", "SSL+payload", "SSL+WS")
//
// The first bytes of a connection are inspected without consuming them.
// For raw SSH and HTTP/WebSocket the untouched socket is handed straight to
// `sshd -i` (inetd mode): no proxy copies, no extra hop, and sshd sees the
// real client address. Before the hand-off the gateway turns on TCP_NODELAY
// — sshd never does that for tunnel-only sessions, so without it small
// voice/video packets wait on Nagle's algorithm. TLS and framed-WebSocket
// connections are decrypted/de-framed here and piped to sshd through a
// UNIX socket pair (no Nagle, no loopback TCP).
package gateway

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"sshfortress/internal/session"
)

// postSniffWait is how long we wait, after answering an HTTP/WebSocket
// handshake, for the client to show what it sends next. Clients that wait
// for the server's SSH banner simply get handed to sshd after this.
const postSniffWait = 2 * time.Second

// Backend starts an SSH server on an inherited socket.
type Backend interface {
	Spawn(f *os.File) (pid int, wait func() error, err error)
}

// SSHDBackend runs `sshd -i` (inetd mode) on the socket.
type SSHDBackend struct {
	Path   string // sshd binary
	Config string // sshd_config (optional)
}

// Spawn implements Backend.
func (b SSHDBackend) Spawn(f *os.File) (int, func() error, error) {
	args := []string{"-i"}
	if b.Config != "" {
		args = append(args, "-f", b.Config)
	}
	cmd := exec.Command(b.Path, args...)
	cmd.Stdin = f
	cmd.Stdout = f
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8"}
	if err := cmd.Start(); err != nil {
		return 0, nil, err
	}
	return cmd.Process.Pid, cmd.Wait, nil
}

// Config configures the gateway.
type Config struct {
	Listen  string // bind address ("" = all)
	Ports   []int
	TLSCert string
	TLSKey  string
	CertDir string // where a self-signed cert is kept
	Host    string // CN for the self-signed cert
	// MaxConnections caps handshaking + live sessions.
	MaxConnections int
	// MaxStartups caps sshd processes that haven't authenticated yet
	// (sshd's own MaxStartups doesn't apply in inetd mode).
	MaxStartups      int
	HandshakeTimeout time.Duration
	LoginGrace       time.Duration
	Backend          Backend
	Logf             func(format string, args ...any)
}

// Stats is a snapshot of gateway activity.
type Stats struct {
	Ports    []int             `json:"ports"`
	Failed   map[string]string `json:"failed,omitempty"`
	Sessions int64             `json:"sessions"` // live sshd children
	Pending  int64             `json:"handshaking"`
	Total    uint64            `json:"total"`
	Rejected uint64            `json:"rejected"`
	Unknown  uint64            `json:"unknown"`
	ByVia    map[string]uint64 `json:"by_via"`
}

// PeerEntry is a live child with its client info.
type PeerEntry struct {
	session.Peer
	Started time.Time `json:"started"`
}

// Gateway is the running front door.
type Gateway struct {
	cfg     Config
	tlsConf *tls.Config

	mu        sync.Mutex
	listeners []net.Listener
	bound     []int
	failed    map[string]string
	byVia     map[string]uint64
	closed    bool

	peers sync.Map // pid -> PeerEntry

	handshaking atomic.Int64
	children    atomic.Int64
	total       atomic.Uint64
	rejected    atomic.Uint64
	unknown     atomic.Uint64
}

// New creates a gateway (call Start to bind ports).
func New(cfg Config) (*Gateway, error) {
	if cfg.MaxConnections <= 0 {
		cfg.MaxConnections = 4096
	}
	if cfg.MaxStartups <= 0 {
		cfg.MaxStartups = 256
	}
	if cfg.HandshakeTimeout <= 0 {
		cfg.HandshakeTimeout = 15 * time.Second
	}
	if cfg.LoginGrace <= 0 {
		cfg.LoginGrace = 30 * time.Second
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if cfg.Backend == nil {
		return nil, errors.New("gateway: no backend")
	}
	cert, err := loadOrCreateCert(cfg.TLSCert, cfg.TLSKey, cfg.CertDir, cfg.Host)
	if err != nil {
		return nil, fmt.Errorf("gateway: TLS certificate: %w", err)
	}
	return &Gateway{
		cfg:     cfg,
		tlsConf: newTLSConfig(cert),
		failed:  map[string]string{},
		byVia:   map[string]uint64{},
	}, nil
}

// Start binds all ports. Ports that fail (e.g. already used by nginx) are
// reported in Stats().Failed; the rest keep working.
func (g *Gateway) Start() error {
	var errs []string
	for _, p := range g.cfg.Ports {
		addr := net.JoinHostPort(g.cfg.Listen, strconv.Itoa(p))
		ln, err := net.Listen("tcp", addr)
		g.mu.Lock()
		if err != nil {
			g.failed[strconv.Itoa(p)] = err.Error()
			errs = append(errs, fmt.Sprintf("port %d: %v", p, err))
			g.mu.Unlock()
			continue
		}
		g.listeners = append(g.listeners, ln)
		g.bound = append(g.bound, p)
		g.mu.Unlock()
		go g.acceptLoop(ln)
	}
	if len(errs) > 0 && len(errs) == len(g.cfg.Ports) {
		return fmt.Errorf("gateway: no port could be opened: %s", strings.Join(errs, "; "))
	}
	if len(errs) > 0 {
		return fmt.Errorf("gateway: some ports failed: %s", strings.Join(errs, "; "))
	}
	return nil
}

// Close stops accepting. Established sessions belong to sshd and survive.
func (g *Gateway) Close() {
	g.mu.Lock()
	g.closed = true
	lns := g.listeners
	g.listeners = nil
	g.mu.Unlock()
	for _, ln := range lns {
		ln.Close()
	}
}

// Stats returns a snapshot.
func (g *Gateway) Stats() Stats {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := Stats{
		Ports:    append([]int(nil), g.bound...),
		Sessions: g.children.Load(),
		Pending:  g.handshaking.Load(),
		Total:    g.total.Load(),
		Rejected: g.rejected.Load(),
		Unknown:  g.unknown.Load(),
		ByVia:    map[string]uint64{},
	}
	sort.Ints(s.Ports)
	if len(g.failed) > 0 {
		s.Failed = map[string]string{}
		for k, v := range g.failed {
			s.Failed[k] = v
		}
	}
	for k, v := range g.byVia {
		s.ByVia[k] = v
	}
	return s
}

// Peer returns client info for a session's monitor PID.
func (g *Gateway) Peer(pid int) (session.Peer, bool) {
	v, ok := g.peers.Load(pid)
	if !ok {
		return session.Peer{}, false
	}
	return v.(PeerEntry).Peer, true
}

// Peers returns all live children (persisted across daemon restarts).
func (g *Gateway) Peers() map[int]PeerEntry {
	out := map[int]PeerEntry{}
	g.peers.Range(func(k, v any) bool {
		out[k.(int)] = v.(PeerEntry)
		return true
	})
	return out
}

// RestorePeers re-adds peer info for children of a previous daemon run.
func (g *Gateway) RestorePeers(m map[int]PeerEntry) {
	for pid, e := range m {
		if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid))); err == nil {
			g.peers.LoadOrStore(pid, e)
		}
	}
}

func (g *Gateway) acceptLoop(ln net.Listener) {
	var backoff time.Duration
	for {
		c, err := ln.Accept()
		if err != nil {
			g.mu.Lock()
			closed := g.closed
			g.mu.Unlock()
			if closed || errors.Is(err, net.ErrClosed) {
				return
			}
			if backoff == 0 {
				backoff = 5 * time.Millisecond
			} else if backoff *= 2; backoff > time.Second {
				backoff = time.Second
			}
			g.cfg.Logf("gateway: accept: %v", err)
			time.Sleep(backoff)
			continue
		}
		backoff = 0
		tc, ok := c.(*net.TCPConn)
		if !ok {
			c.Close()
			continue
		}
		if g.handshaking.Load()+g.children.Load() >= int64(g.cfg.MaxConnections) {
			g.rejected.Add(1)
			tc.Close()
			continue
		}
		g.handshaking.Add(1)
		g.total.Add(1)
		go g.handle(tc)
	}
}

func peerOf(c net.Conn, via string) session.Peer {
	p := session.Peer{Via: via}
	if ta, ok := c.RemoteAddr().(*net.TCPAddr); ok {
		ip := ta.IP
		if v4 := ip.To4(); v4 != nil {
			ip = v4
		}
		p.IP = ip.String()
		p.Port = ta.Port
	}
	return p
}

func (g *Gateway) handle(c *net.TCPConn) {
	owned := false // true once the socket belongs to sshd
	doneHandshake := false
	finishHandshake := func() {
		if !doneHandshake {
			doneHandshake = true
			g.handshaking.Add(-1)
		}
	}
	defer func() {
		if r := recover(); r != nil {
			g.cfg.Logf("gateway: panic: %v", r)
		}
		finishHandshake()
		if !owned {
			c.Close()
		}
	}()

	tuneSocket(c)
	deadline := time.Now().Add(g.cfg.HandshakeTimeout)
	// A few SSH clients wait for the server banner before saying anything,
	// so silence for a few seconds means "SSH": hand it to sshd.
	k, _, err := peekClassify(c, minTime(deadline, time.Now().Add(3*time.Second)))
	if err == errTimeout {
		k, err = kindSSH, nil
	}
	if err != nil {
		return
	}
	switch k {
	case kindSSH:
		finishHandshake()
		owned = g.handoff(c, peerOf(c, "ssh"))
	case kindHTTP:
		owned = g.handlePlainHTTP(c, deadline, finishHandshake)
	case kindTLS:
		g.handleTLS(c, deadline, finishHandshake)
	default:
		g.unknown.Add(1)
	}
}

// handlePlainHTTP answers payload/WebSocket handshakes on a raw socket.
func (g *Gateway) handlePlainHTTP(c *net.TCPConn, deadline time.Time, finish func()) bool {
	peer := peerOf(c, "http")
	for round := 0; round < 4; round++ {
		req, err := readHeaderExact(c, deadline)
		if err != nil {
			return false
		}
		if ip := req.clientIP(); ip != "" {
			peer.IP, peer.Port = ip, 0
		}
		if req.isWebSocket() {
			peer.Via = "ws"
		}
		_ = c.SetWriteDeadline(deadline)
		if _, err := io.WriteString(c, upgradeResponse(req)); err != nil {
			return false
		}
		_ = c.SetWriteDeadline(time.Time{})

		k, _, err := peekClassify(c, time.Now().Add(postSniffWait))
		switch {
		case err != nil && err != errTimeout:
			return false
		case err == errTimeout || k == kindSSH:
			finish()
			return g.handoff(c, peer)
		case k == kindHTTP:
			continue // split payload: answer the next request too
		case k == kindWSFrame:
			peer.Via = "ws"
			finish()
			ws := &wsConn{r: bufio.NewReaderSize(c, 32*1024), w: c}
			g.pump(ws, func() { c.Close() }, peer)
			return false
		default:
			finish()
			return g.handoff(c, peer) // let sshd judge whatever this is
		}
	}
	return false
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

type readWriter struct {
	io.Reader
	io.Writer
}

// handleTLS terminates TLS and inspects what is inside.
func (g *Gateway) handleTLS(c *net.TCPConn, deadline time.Time, finish func()) {
	tc := tls.Server(c, g.tlsConf)
	_ = tc.SetDeadline(deadline)
	if err := tc.Handshake(); err != nil {
		return
	}
	peer := peerOf(c, "tls")
	br := bufio.NewReaderSize(tc, 32*1024)
	until := minTime(deadline, time.Now().Add(5*time.Second))
	for round := 0; round < 4; round++ {
		k, err := classifyReader(br, tc.SetReadDeadline, until)
		if err != nil && err != errTimeout {
			return
		}
		if err == errTimeout && round == 0 {
			// Nothing after the TLS handshake: treat as SSH whose client
			// waits for the server banner.
			k = kindSSH
		}
		switch {
		case err == errTimeout || k == kindSSH:
			finish()
			_ = tc.SetDeadline(time.Time{})
			g.pump(readWriter{br, tc}, func() { tc.Close() }, peer)
			return
		case k == kindHTTP:
			_ = tc.SetReadDeadline(deadline)
			req, err := readHeaderBuffered(br)
			if err != nil {
				return
			}
			if ip := req.clientIP(); ip != "" {
				peer.IP, peer.Port = ip, 0
			}
			peer.Via = "tls+http"
			if req.isWebSocket() {
				peer.Via = "tls+ws"
			}
			_ = tc.SetWriteDeadline(deadline)
			if _, err := io.WriteString(tc, upgradeResponse(req)); err != nil {
				return
			}
			_ = tc.SetWriteDeadline(time.Time{})
			until = time.Now().Add(postSniffWait)
		case k == kindWSFrame:
			finish()
			_ = tc.SetDeadline(time.Time{})
			peer.Via = "tls+ws"
			g.pump(&wsConn{r: br, w: tc}, func() { tc.Close() }, peer)
			return
		default:
			g.unknown.Add(1)
			return
		}
	}
}

// classifyReader peeks a buffered stream until it can be classified.
func classifyReader(br *bufio.Reader, setDeadline func(time.Time) error, deadline time.Time) (kind, error) {
	_ = setDeadline(deadline)
	defer setDeadline(time.Time{})
	want := 1
	for {
		b, err := br.Peek(want)
		if len(b) == 0 && err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return kindUnknown, errTimeout
			}
			return kindUnknown, err
		}
		k, more := classify(b)
		if !more || len(b) >= 16 {
			return k, nil
		}
		if err != nil {
			return k, errTimeout
		}
		want = len(b) + 1
	}
}

// handoff gives the raw socket to sshd. It reports whether sshd took it.
func (g *Gateway) handoff(c *net.TCPConn, peer session.Peer) bool {
	f, err := c.File() // a dup: closing ours later doesn't close the socket
	if err != nil {
		return false
	}
	defer f.Close()
	_ = syscall.SetNonblock(int(f.Fd()), false)
	if !g.spawn(f, peer) {
		return false
	}
	c.Close()
	return true
}

// pump runs sshd on one end of a UNIX socket pair and copies between it
// and the (decrypted / de-framed) client stream.
func (g *Gateway) pump(client io.ReadWriter, closeClient func(), peer session.Peer) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		g.cfg.Logf("gateway: socketpair: %v", err)
		return
	}
	childF := os.NewFile(uintptr(fds[0]), "sshd-stdio")
	ourF := os.NewFile(uintptr(fds[1]), "gateway-stdio")
	ours, err := net.FileConn(ourF)
	ourF.Close()
	if err != nil {
		childF.Close()
		return
	}
	defer ours.Close()
	ok := g.spawn(childF, peer)
	childF.Close()
	if !ok {
		return
	}
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(ours, client)
		if uc, ok := ours.(*net.UnixConn); ok {
			_ = uc.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, ours)
		closeClient()
		done <- struct{}{}
	}()
	<-done
	ours.Close()
	closeClient()
	<-done
}

func (g *Gateway) spawn(f *os.File, peer session.Peer) bool {
	if g.pendingStartups() >= g.cfg.MaxStartups {
		g.rejected.Add(1)
		return false
	}
	pid, wait, err := g.cfg.Backend.Spawn(f)
	if err != nil {
		g.cfg.Logf("gateway: start sshd: %v", err)
		return false
	}
	g.children.Add(1)
	g.peers.Store(pid, PeerEntry{Peer: peer, Started: time.Now()})
	g.mu.Lock()
	g.byVia[peer.Via]++
	g.mu.Unlock()
	go func() {
		_ = wait()
		g.peers.Delete(pid)
		g.children.Add(-1)
	}()
	return true
}

// pendingStartups counts children that haven't authenticated yet: young
// processes without a user-owned child (the post-auth sshd process).
func (g *Gateway) pendingStartups() int {
	limit := time.Now().Add(-(g.cfg.LoginGrace + 5*time.Second))
	n := 0
	g.peers.Range(func(k, v any) bool {
		e := v.(PeerEntry)
		if e.Started.Before(limit) {
			return true
		}
		pid := k.(int)
		if authed, known := hasUserChild(pid); known && authed {
			return true
		} else if !known && time.Since(e.Started) > 10*time.Second {
			return true
		}
		n++
		return true
	})
	return n
}

// hasUserChild reports whether pid has a non-root child (an authenticated
// session). known=false when the kernel doesn't expose child lists.
func hasUserChild(pid int) (authed, known bool) {
	if pid <= 0 {
		return true, true
	}
	p := strconv.Itoa(pid)
	data, err := os.ReadFile(filepath.Join("/proc", p, "task", p, "children"))
	if err != nil {
		return false, false
	}
	for _, f := range strings.Fields(string(data)) {
		if st, err := os.Stat(filepath.Join("/proc", f)); err == nil {
			if sys, ok := st.Sys().(*syscall.Stat_t); ok && sys.Uid != 0 {
				return true, true
			}
		}
	}
	return false, true
}
