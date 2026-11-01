package udpgw

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Config tunes the server. Zero values get sensible defaults.
type Config struct {
	// MaxClients is the max simultaneous udpgw streams; the oldest is
	// dropped when a new one arrives at the limit (badvpn behaviour).
	MaxClients int
	// MaxFlows per client; the least-recently-used flow is closed at the
	// limit (badvpn behaviour).
	MaxFlows int
	// FlowIdle closes UDP flows without traffic for this long.
	FlowIdle time.Duration
	// ClientIdle disconnects clients that sent nothing (not even a
	// keepalive) for this long. Apps send keepalives every ~10s.
	ClientIdle time.Duration
	// DNS returns the resolver for DNS-flagged packets. Nil = system resolver.
	DNS func() (netip.AddrPort, bool)
	// QueueLen is the per-client queue of frames waiting to go back through
	// the tunnel. When the tunnel is congested newer datagrams are dropped
	// instead of queued: late voice packets are useless, and a long queue
	// is what makes calls lag behind.
	QueueLen int
	// Logf receives diagnostic messages (optional).
	Logf func(format string, args ...any)
}

func (c *Config) defaults() {
	if c.MaxClients <= 0 {
		c.MaxClients = 2048
	}
	if c.MaxFlows <= 0 {
		c.MaxFlows = 512
	}
	if c.FlowIdle <= 0 {
		c.FlowIdle = 2 * time.Minute
	}
	if c.ClientIdle < 0 {
		c.ClientIdle = 0
	}
	if c.QueueLen <= 0 {
		c.QueueLen = 512
	}
	if c.DNS == nil {
		c.DNS = SystemDNS
	}
	if c.Logf == nil {
		c.Logf = func(string, ...any) {}
	}
}

// Stats are cumulative counters.
type Stats struct {
	Clients     int    `json:"clients"`
	Flows       int    `json:"flows"`
	TotalConns  uint64 `json:"total_clients"`
	PacketsUp   uint64 `json:"packets_up"`
	PacketsDown uint64 `json:"packets_down"`
	BytesUp     uint64 `json:"bytes_up"`
	BytesDown   uint64 `json:"bytes_down"`
	Dropped     uint64 `json:"dropped"`
}

// ClientInfo describes one connected client.
type ClientInfo struct {
	RemotePort int       `json:"remote_port"` // ephemeral port of the sshd side
	LocalPort  int       `json:"local_port"`  // udpgw port it connected to
	Flows      int       `json:"flows"`
	BytesUp    uint64    `json:"bytes_up"`
	BytesDown  uint64    `json:"bytes_down"`
	Since      time.Time `json:"since"`
}

// Server is a udpgw server.
type Server struct {
	cfg Config

	mu        sync.Mutex
	clients   map[*client]struct{}
	listeners []net.Listener
	closed    bool

	totalConns  atomic.Uint64
	flows       atomic.Int64
	packetsUp   atomic.Uint64
	packetsDown atomic.Uint64
	bytesUp     atomic.Uint64
	bytesDown   atomic.Uint64
	dropped     atomic.Uint64

	reaperOnce sync.Once
	done       chan struct{}
}

// NewServer creates a server.
func NewServer(cfg Config) *Server {
	cfg.defaults()
	return &Server{cfg: cfg, clients: map[*client]struct{}{}, done: make(chan struct{})}
}

// Listen binds every address and serves each one in the background.
// It returns the addresses that failed (the others keep running).
func (s *Server) Listen(addrs []string) (failed map[string]error) {
	failed = map[string]error{}
	for _, a := range addrs {
		ln, err := net.Listen("tcp", a)
		if err != nil {
			failed[a] = err
			continue
		}
		go s.Serve(ln)
	}
	return failed
}

// Serve accepts clients on ln until the server is closed.
func (s *Server) Serve(ln net.Listener) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		ln.Close()
		return net.ErrClosed
	}
	s.listeners = append(s.listeners, ln)
	s.mu.Unlock()
	s.reaperOnce.Do(func() { go s.reaper() })

	var tempDelay time.Duration
	for {
		conn, err := ln.Accept()
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else if tempDelay *= 2; tempDelay > time.Second {
					tempDelay = time.Second
				}
				time.Sleep(tempDelay)
				continue
			}
			select {
			case <-s.done:
				return nil
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			// EMFILE and friends: back off instead of spinning.
			s.cfg.Logf("udpgw: accept: %v", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		tempDelay = 0
		s.startClient(conn)
	}
}

// Close stops listeners and disconnects all clients.
func (s *Server) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	close(s.done)
	lns := s.listeners
	cls := make([]*client, 0, len(s.clients))
	for c := range s.clients {
		cls = append(cls, c)
	}
	s.mu.Unlock()
	for _, ln := range lns {
		ln.Close()
	}
	for _, c := range cls {
		c.close()
	}
}

// Stats returns a snapshot of the counters.
func (s *Server) Stats() Stats {
	s.mu.Lock()
	n := len(s.clients)
	s.mu.Unlock()
	return Stats{
		Clients:     n,
		Flows:       int(s.flows.Load()),
		TotalConns:  s.totalConns.Load(),
		PacketsUp:   s.packetsUp.Load(),
		PacketsDown: s.packetsDown.Load(),
		BytesUp:     s.bytesUp.Load(),
		BytesDown:   s.bytesDown.Load(),
		Dropped:     s.dropped.Load(),
	}
}

// Clients returns info about connected clients.
func (s *Server) Clients() []ClientInfo {
	s.mu.Lock()
	cls := make([]*client, 0, len(s.clients))
	for c := range s.clients {
		cls = append(cls, c)
	}
	s.mu.Unlock()
	out := make([]ClientInfo, 0, len(cls))
	for _, c := range cls {
		c.mu.Lock()
		nf := len(c.flows)
		c.mu.Unlock()
		out = append(out, ClientInfo{
			RemotePort: c.remotePort,
			LocalPort:  c.localPort,
			Flows:      nf,
			BytesUp:    c.bytesUp.Load(),
			BytesDown:  c.bytesDown.Load(),
			Since:      c.since,
		})
	}
	return out
}

func (s *Server) startClient(conn net.Conn) {
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(30 * time.Second)
	}
	c := &client{
		srv:   s,
		conn:  conn,
		out:   make(chan []byte, s.cfg.QueueLen),
		flows: map[uint16]*flow{},
		since: time.Now(),
		done:  make(chan struct{}),
	}
	if ra, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		c.remotePort = ra.Port
	}
	if la, ok := conn.LocalAddr().(*net.TCPAddr); ok {
		c.localPort = la.Port
	}
	c.lastRecv.Store(time.Now().UnixNano())

	var evict *client
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		conn.Close()
		return
	}
	if len(s.clients) >= s.cfg.MaxClients {
		for o := range s.clients { // drop the oldest client
			if evict == nil || o.since.Before(evict.since) {
				evict = o
			}
		}
	}
	s.clients[c] = struct{}{}
	s.mu.Unlock()
	if evict != nil {
		s.cfg.Logf("udpgw: client limit reached, dropping oldest client")
		evict.close()
	}
	s.totalConns.Add(1)
	go c.writer()
	go c.reader()
}

func (s *Server) removeClient(c *client) {
	s.mu.Lock()
	delete(s.clients, c)
	s.mu.Unlock()
}

// reaper closes idle flows and silent clients.
func (s *Server) reaper() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case now := <-t.C:
			s.mu.Lock()
			cls := make([]*client, 0, len(s.clients))
			for c := range s.clients {
				cls = append(cls, c)
			}
			s.mu.Unlock()
			for _, c := range cls {
				if s.cfg.ClientIdle > 0 && now.Sub(time.Unix(0, c.lastRecv.Load())) > s.cfg.ClientIdle {
					c.close()
					continue
				}
				c.reapFlows(now, s.cfg.FlowIdle)
			}
		}
	}
}

// ─── client ─────────────────────────────────────────────────────────────────

type client struct {
	srv        *Server
	conn       net.Conn
	out        chan []byte
	remotePort int
	localPort  int
	since      time.Time
	lastRecv   atomic.Int64
	bytesUp    atomic.Uint64
	bytesDown  atomic.Uint64

	mu     sync.Mutex
	flows  map[uint16]*flow
	closed bool

	closeOnce sync.Once
	done      chan struct{}
}

type flow struct {
	conid   uint16
	orig    netip.AddrPort // address reported back to the client
	target  netip.AddrPort // where datagrams really go (DNS may differ)
	pc      *net.UDPConn
	lastUse atomic.Int64
	closed  atomic.Bool
}

func (f *flow) touch() { f.lastUse.Store(time.Now().UnixNano()) }

func (c *client) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		c.conn.Close()
		c.mu.Lock()
		c.closed = true
		fl := c.flows
		c.flows = map[uint16]*flow{}
		c.mu.Unlock()
		for _, f := range fl {
			c.closeFlow(f)
		}
		c.srv.removeClient(c)
	})
}

func (c *client) closeFlow(f *flow) {
	if f.closed.CompareAndSwap(false, true) {
		f.pc.Close()
		c.srv.flows.Add(-1)
	}
}

func (c *client) reapFlows(now time.Time, idle time.Duration) {
	cutoff := now.Add(-idle).UnixNano()
	var stale []*flow
	c.mu.Lock()
	for id, f := range c.flows {
		if f.lastUse.Load() < cutoff {
			delete(c.flows, id)
			stale = append(stale, f)
		}
	}
	c.mu.Unlock()
	for _, f := range stale {
		c.closeFlow(f)
	}
}

func (c *client) reader() {
	defer c.close()
	br := bufio.NewReaderSize(c.conn, 64*1024)
	buf := make([]byte, MaxPayload)
	var lenBuf [2]byte
	for {
		if _, err := io.ReadFull(br, lenBuf[:]); err != nil {
			return
		}
		n := int(binary.LittleEndian.Uint16(lenBuf[:]))
		if _, err := io.ReadFull(br, buf[:n]); err != nil {
			return
		}
		c.lastRecv.Store(time.Now().UnixNano())
		p, err := ParsePacket(buf[:n])
		if err != nil {
			c.srv.cfg.Logf("udpgw: bad packet from client: %v", err)
			continue
		}
		if p.Flags&FlagKeepalive != 0 {
			continue
		}
		c.handle(p)
	}
}

func (c *client) handle(p Packet) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	f := c.flows[p.ConID]
	if f != nil && (p.Flags&FlagRebind != 0 || f.orig != p.Addr) {
		delete(c.flows, p.ConID)
		c.closeFlow(f)
		f = nil
	}
	if f == nil {
		if len(c.flows) >= c.srv.cfg.MaxFlows {
			var lru *flow
			for _, o := range c.flows {
				if lru == nil || o.lastUse.Load() < lru.lastUse.Load() {
					lru = o
				}
			}
			if lru != nil {
				delete(c.flows, lru.conid)
				c.closeFlow(lru)
			}
		}
		target := p.Addr
		if p.Flags&FlagDNS != 0 {
			if dns, ok := c.srv.cfg.DNS(); ok {
				target = dns
			}
		}
		target = netip.AddrPortFrom(target.Addr().Unmap(), target.Port())
		network := "udp4"
		if !target.Addr().Is4() {
			network = "udp6"
		}
		pc, err := net.ListenUDP(network, nil)
		if err != nil {
			c.mu.Unlock()
			c.srv.cfg.Logf("udpgw: open UDP socket: %v", err)
			return
		}
		_ = pc.SetReadBuffer(256 * 1024)
		_ = pc.SetWriteBuffer(256 * 1024)
		f = &flow{conid: p.ConID, orig: p.Addr, target: target, pc: pc}
		f.touch()
		c.flows[p.ConID] = f
		c.srv.flows.Add(1)
		go c.flowReader(f)
	}
	c.mu.Unlock()

	f.touch()
	if _, err := f.pc.WriteToUDPAddrPort(p.Data, f.target); err != nil {
		c.srv.dropped.Add(1)
		return
	}
	c.srv.packetsUp.Add(1)
	c.srv.bytesUp.Add(uint64(len(p.Data)))
	c.bytesUp.Add(uint64(len(p.Data)))
}

var framePool = sync.Pool{New: func() any { b := make([]byte, 0, 2048); return &b }}

// flowReader forwards replies. Datagrams from any source are accepted and
// labelled with the original address (endpoint-independent, like badvpn),
// which is what P2P/WebRTC call setups expect.
func (c *client) flowReader(f *flow) {
	buf := make([]byte, MaxPayload)
	for {
		n, _, err := f.pc.ReadFromUDPAddrPort(buf)
		if err != nil {
			if !f.closed.Load() {
				c.mu.Lock()
				if c.flows[f.conid] == f {
					delete(c.flows, f.conid)
				}
				c.mu.Unlock()
				c.closeFlow(f)
			}
			return
		}
		f.touch()
		need := FrameLen(f.orig, n)
		var frame []byte
		if need <= 2048 {
			bp := framePool.Get().(*[]byte)
			frame = (*bp)[:0]
		} else {
			frame = make([]byte, 0, need)
		}
		frame, err = AppendFrame(frame, 0, f.conid, f.orig, buf[:n])
		if err != nil {
			c.srv.dropped.Add(1)
			continue
		}
		select {
		case c.out <- frame:
			c.srv.packetsDown.Add(1)
			c.srv.bytesDown.Add(uint64(n))
			c.bytesDown.Add(uint64(n))
		case <-c.done:
			return
		default:
			c.srv.dropped.Add(1) // tunnel congested: drop, don't lag
			releaseFrame(frame)
		}
	}
}

func releaseFrame(b []byte) {
	if cap(b) == 2048 {
		b = b[:0]
		framePool.Put(&b)
	}
}

// writer batches queued frames into as few writes as possible while never
// holding a packet back: it flushes as soon as the queue is empty.
func (c *client) writer() {
	defer c.close()
	bw := bufio.NewWriterSize(c.conn, 64*1024)
	for {
		select {
		case <-c.done:
			return
		case fr := <-c.out:
			if _, err := bw.Write(fr); err != nil {
				return
			}
			releaseFrame(fr)
		drain:
			for {
				select {
				case fr = <-c.out:
					if _, err := bw.Write(fr); err != nil {
						return
					}
					releaseFrame(fr)
				default:
					break drain
				}
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
			if err := bw.Flush(); err != nil {
				return
			}
		}
	}
}

// ─── DNS ────────────────────────────────────────────────────────────────────

var (
	dnsMu      sync.Mutex
	dnsCached  netip.AddrPort
	dnsOK      bool
	dnsChecked time.Time
)

// SystemDNS returns the first nameserver in /etc/resolv.conf (re-read at
// most every 30 seconds), like badvpn's res_init() lookup.
func SystemDNS() (netip.AddrPort, bool) {
	dnsMu.Lock()
	defer dnsMu.Unlock()
	if time.Since(dnsChecked) < 30*time.Second {
		return dnsCached, dnsOK
	}
	dnsChecked = time.Now()
	dnsOK = false
	data, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return dnsCached, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "nameserver" {
			if ip, err := netip.ParseAddr(strings.Split(f[1], "%")[0]); err == nil {
				dnsCached = netip.AddrPortFrom(ip, 53)
				dnsOK = true
				break
			}
		}
	}
	return dnsCached, dnsOK
}

// StaticDNS returns a DNS func for a fixed "ip" or "ip:port".
func StaticDNS(s string) func() (netip.AddrPort, bool) {
	s = strings.TrimSpace(s)
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return func() (netip.AddrPort, bool) { return ap, true }
	}
	if ip, err := netip.ParseAddr(s); err == nil {
		ap := netip.AddrPortFrom(ip, 53)
		return func() (netip.AddrPort, bool) { return ap, true }
	}
	return SystemDNS
}
