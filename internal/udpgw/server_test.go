package udpgw

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestCodecRoundTrip(t *testing.T) {
	for _, a := range []string{"1.2.3.4:5678", "[2001:db8::1]:443"} {
		ap := netip.MustParseAddrPort(a)
		fr, err := AppendFrame(nil, FlagDNS, 0xBEEF, ap, []byte("hello"))
		if err != nil {
			t.Fatal(err)
		}
		if int(binary.LittleEndian.Uint16(fr)) != len(fr)-2 || len(fr) != FrameLen(ap, 5) {
			t.Fatalf("bad length prefix for %s", a)
		}
		p, err := ParsePacket(fr[2:])
		if err != nil {
			t.Fatal(err)
		}
		if p.ConID != 0xBEEF || p.Addr != ap || string(p.Data) != "hello" || p.Flags&FlagDNS == 0 {
			t.Fatalf("round trip mismatch: %+v", p)
		}
		if ap.Addr().Is6() != (p.Flags&FlagIPv6 != 0) {
			t.Fatalf("ipv6 flag wrong for %s", a)
		}
	}
	if _, err := ParsePacket([]byte{0, 1}); err == nil {
		t.Fatal("short packet accepted")
	}
	if _, err := ParsePacket([]byte{0, 1, 0, 1, 2}); err == nil {
		t.Fatal("truncated address accepted")
	}
}

// ─── test harness: works against the built-in server or a reference binary ──

type gw struct {
	addr  string
	close func()
}

// startGateway starts the built-in server, or the reference badvpn-udpgw
// binary when UDPGW_REF_BIN is set (differential testing).
func startGateway(t *testing.T, cfg Config) gw {
	t.Helper()
	if bin := os.Getenv("UDPGW_REF_BIN"); bin != "" {
		ln, _ := net.Listen("tcp", "127.0.0.1:0")
		addr := ln.Addr().String()
		ln.Close()
		args := []string{"--listen-addr", addr, "--loglevel", "none"}
		if cfg.MaxFlows > 0 {
			args = append(args, "--max-connections-for-client", fmt.Sprint(cfg.MaxFlows))
		}
		cmd := exec.Command(bin, args...)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 50; i++ {
			if c, err := net.Dial("tcp", addr); err == nil {
				c.Close()
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		return gw{addr: addr, close: func() { cmd.Process.Kill(); cmd.Wait() }}
	}
	srv := NewServer(cfg)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	return gw{addr: ln.Addr().String(), close: srv.Close}
}

type tclient struct {
	t  *testing.T
	c  net.Conn
	br *bufio.Reader
}

func dialGW(t *testing.T, g gw) *tclient {
	c, err := net.Dial("tcp", g.addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &tclient{t: t, c: c, br: bufio.NewReader(c)}
}

func (tc *tclient) send(flags uint8, conid uint16, addr netip.AddrPort, data string) {
	fr, err := AppendFrame(nil, flags, conid, addr, []byte(data))
	if err != nil {
		tc.t.Fatal(err)
	}
	if _, err := tc.c.Write(fr); err != nil {
		tc.t.Fatal(err)
	}
}

func (tc *tclient) recv(timeout time.Duration) (Packet, error) {
	tc.c.SetReadDeadline(time.Now().Add(timeout))
	var lb [2]byte
	if _, err := io.ReadFull(tc.br, lb[:]); err != nil {
		return Packet{}, err
	}
	buf := make([]byte, binary.LittleEndian.Uint16(lb[:]))
	if _, err := io.ReadFull(tc.br, buf); err != nil {
		return Packet{}, err
	}
	return ParsePacket(buf)
}

// udpEcho replies "echo:<data>" to every datagram.
func udpEcho(t *testing.T, network, addr string) (*net.UDPConn, netip.AddrPort) {
	pc, err := net.ListenUDP(network, net.UDPAddrFromAddrPort(netip.MustParseAddrPort(addr)))
	if err != nil {
		t.Skipf("cannot listen %s: %v", network, err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 65535)
		for {
			n, from, err := pc.ReadFromUDPAddrPort(buf)
			if err != nil {
				return
			}
			pc.WriteToUDPAddrPort(append([]byte("echo:"), buf[:n]...), from)
		}
	}()
	return pc, pc.LocalAddr().(*net.UDPAddr).AddrPort()
}

func TestEchoIPv4(t *testing.T) {
	g := startGateway(t, Config{})
	defer g.close()
	_, echo := udpEcho(t, "udp4", "127.0.0.1:0")
	tc := dialGW(t, g)

	tc.send(FlagKeepalive, 0, netip.AddrPort{}, "") // must be ignored
	for i := 0; i < 20; i++ {
		msg := fmt.Sprintf("voice-%d", i)
		tc.send(0, 7, echo, msg)
		p, err := tc.recv(2 * time.Second)
		if err != nil {
			t.Fatalf("no reply %d: %v", i, err)
		}
		if p.ConID != 7 || p.Addr != echo || string(p.Data) != "echo:"+msg {
			t.Fatalf("bad reply: conid=%d addr=%v data=%q", p.ConID, p.Addr, p.Data)
		}
	}
}

func TestEchoIPv6(t *testing.T) {
	g := startGateway(t, Config{})
	defer g.close()
	_, echo := udpEcho(t, "udp6", "[::1]:0")
	tc := dialGW(t, g)
	tc.send(0, 9, echo, "v6")
	p, err := tc.recv(2 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if p.Addr != echo || p.Flags&FlagIPv6 == 0 || string(p.Data) != "echo:v6" {
		t.Fatalf("bad v6 reply: %+v", p)
	}
}

// Replies from a different source still reach the client, labelled with
// the original address (endpoint-independent mapping, needed for P2P calls).
func TestReplyFromOtherSource(t *testing.T) {
	g := startGateway(t, Config{})
	defer g.close()
	a, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	aAddr := a.LocalAddr().(*net.UDPAddr).AddrPort()

	tc := dialGW(t, g)
	tc.send(0, 3, aAddr, "ping")
	buf := make([]byte, 100)
	a.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, from, err := a.ReadFromUDPAddrPort(buf)
	if err != nil {
		t.Fatal(err)
	}
	b.WriteToUDPAddrPort([]byte("from-b"), from) // a different peer answers
	p, err := tc.recv(2 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if p.ConID != 3 || p.Addr != aAddr || string(p.Data) != "from-b" {
		t.Fatalf("got %+v", p)
	}
}

func TestDNSFlagRedirect(t *testing.T) {
	if os.Getenv("UDPGW_REF_BIN") != "" {
		t.Skip("reference binary uses the system resolver")
	}
	_, echo := udpEcho(t, "udp4", "127.0.0.1:0")
	g := startGateway(t, Config{DNS: func() (netip.AddrPort, bool) { return echo, true }})
	defer g.close()
	tc := dialGW(t, g)
	fake := netip.MustParseAddrPort("8.8.8.8:53")
	tc.send(FlagDNS, 11, fake, "query")
	p, err := tc.recv(2 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if p.Addr != fake || string(p.Data) != "echo:query" {
		t.Fatalf("DNS reply must carry the original address: %+v", p)
	}
}

func TestRebindAndAddressChange(t *testing.T) {
	g := startGateway(t, Config{})
	defer g.close()
	_, e1 := udpEcho(t, "udp4", "127.0.0.1:0")
	_, e2 := udpEcho(t, "udp4", "127.0.0.1:0")
	tc := dialGW(t, g)
	tc.send(0, 5, e1, "one")
	if p, err := tc.recv(2 * time.Second); err != nil || p.Addr != e1 {
		t.Fatalf("first: %v %+v", err, p)
	}
	tc.send(0, 5, e2, "two") // same conid, new address → new flow
	if p, err := tc.recv(2 * time.Second); err != nil || p.Addr != e2 || string(p.Data) != "echo:two" {
		t.Fatalf("second: %v %+v", err, p)
	}
	tc.send(FlagRebind, 5, e2, "three")
	if p, err := tc.recv(2 * time.Second); err != nil || string(p.Data) != "echo:three" {
		t.Fatalf("rebind: %v %+v", err, p)
	}
}

func TestFlowLimitEvictsLRU(t *testing.T) {
	if os.Getenv("UDPGW_REF_BIN") != "" {
		t.Skip("inspects built-in internals")
	}
	srv := NewServer(Config{MaxFlows: 3})
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go srv.Serve(ln)
	defer srv.Close()
	_, echo := udpEcho(t, "udp4", "127.0.0.1:0")
	tc := dialGW(t, gw{addr: ln.Addr().String()})
	for id := uint16(1); id <= 5; id++ {
		tc.send(0, id, echo, "x")
		if _, err := tc.recv(2 * time.Second); err != nil {
			t.Fatal(err)
		}
	}
	if f := srv.Stats().Flows; f != 3 {
		t.Fatalf("flows = %d, want 3", f)
	}
	cl := srv.Clients()
	if len(cl) != 1 || cl[0].Flows != 3 || cl[0].BytesUp == 0 || cl[0].BytesDown == 0 {
		t.Fatalf("client info wrong: %+v", cl)
	}
}

func TestClientDisconnectClosesFlows(t *testing.T) {
	if os.Getenv("UDPGW_REF_BIN") != "" {
		t.Skip("inspects built-in internals")
	}
	srv := NewServer(Config{})
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go srv.Serve(ln)
	defer srv.Close()
	_, echo := udpEcho(t, "udp4", "127.0.0.1:0")
	c, _ := net.Dial("tcp", ln.Addr().String())
	tc := &tclient{t: t, c: c, br: bufio.NewReader(c)}
	tc.send(0, 1, echo, "x")
	tc.recv(2 * time.Second)
	c.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s := srv.Stats(); s.Clients == 0 && s.Flows == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("leaked: %+v", srv.Stats())
}

// Many concurrent flows (like a video call + DNS + a game) all get through.
func TestManyFlowsBurst(t *testing.T) {
	g := startGateway(t, Config{})
	defer g.close()
	_, echo := udpEcho(t, "udp4", "127.0.0.1:0")
	tc := dialGW(t, g)
	const n = 100
	for id := 0; id < n; id++ {
		tc.send(0, uint16(id), echo, fmt.Sprint(id))
	}
	seen := map[uint16]bool{}
	for len(seen) < n {
		p, err := tc.recv(3 * time.Second)
		if err != nil {
			t.Fatalf("only %d/%d replies: %v", len(seen), n, err)
		}
		if string(p.Data) != "echo:"+fmt.Sprint(p.ConID) {
			t.Fatalf("mixed up reply %d: %q", p.ConID, p.Data)
		}
		seen[p.ConID] = true
	}
}

func TestStaticDNS(t *testing.T) {
	ap, ok := StaticDNS("1.1.1.1")()
	if !ok || ap.String() != "1.1.1.1:53" {
		t.Fatalf("got %v", ap)
	}
	ap, ok = StaticDNS("9.9.9.9:5353")()
	if !ok || ap.Port() != 5353 {
		t.Fatalf("got %v", ap)
	}
}
