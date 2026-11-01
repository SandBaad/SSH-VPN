package gateway

import (
	"bufio"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeSSHD behaves like `sshd -i`: it sends a banner and echoes everything.
type fakeSSHD struct{ n atomic.Int64 }

func (f *fakeSSHD) Spawn(file *os.File) (int, func() error, error) {
	conn, err := net.FileConn(file) // dup: the caller closes file
	if err != nil {
		return 0, nil, err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer conn.Close()
		io.WriteString(conn, "SSH-2.0-FakeSSHD\r\n")
		io.Copy(conn, conn)
	}()
	pid := int(-f.n.Add(1))
	return pid, func() error { <-done; return nil }, nil
}

func startGW(t *testing.T) (*Gateway, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	g, err := New(Config{
		Listen:           "127.0.0.1",
		Ports:            []int{port},
		CertDir:          t.TempDir(),
		HandshakeTimeout: 5 * time.Second,
		Backend:          &fakeSSHD{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.Close)
	return g, "127.0.0.1:" + strconv.Itoa(port)
}

func readLine(t *testing.T, br *bufio.Reader) string {
	t.Helper()
	l, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v (got %q)", err, l)
	}
	return strings.TrimRight(l, "\r\n")
}

func readHTTPResponse(t *testing.T, br *bufio.Reader) (status string, headers map[string]string) {
	t.Helper()
	status = readLine(t, br)
	headers = map[string]string{}
	for {
		l := readLine(t, br)
		if l == "" {
			return
		}
		k, v, _ := strings.Cut(l, ":")
		headers[strings.ToLower(k)] = strings.TrimSpace(v)
	}
}

// expectSSH sends a client banner + data and checks banner and echo.
func expectSSH(t *testing.T, rw io.ReadWriter, br *bufio.Reader) {
	t.Helper()
	io.WriteString(rw, "SSH-2.0-TestClient\r\nping\n")
	if got := readLine(t, br); got != "SSH-2.0-FakeSSHD" {
		t.Fatalf("banner = %q", got)
	}
	if got := readLine(t, br); got != "SSH-2.0-TestClient" {
		t.Fatalf("echo1 = %q", got)
	}
	if got := readLine(t, br); got != "ping" {
		t.Fatalf("echo2 = %q", got)
	}
}

func waitVia(t *testing.T, g *Gateway, via string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if g.Stats().ByVia[via] > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no session with via=%q: %+v", via, g.Stats().ByVia)
}

func TestRawSSH(t *testing.T) {
	g, addr := startGW(t)
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	expectSSH(t, c, bufio.NewReader(c))
	waitVia(t, g, "ssh")
}

func TestClientWaitingForBanner(t *testing.T) {
	_, addr := startGW(t)
	c, _ := net.Dial("tcp", addr)
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(6 * time.Second))
	br := bufio.NewReader(c)
	if got := readLine(t, br); got != "SSH-2.0-FakeSSHD" { // we sent nothing
		t.Fatalf("banner = %q", got)
	}
}

func TestWebSocketPayloadRawAfterUpgrade(t *testing.T) {
	g, addr := startGW(t)
	c, _ := net.Dial("tcp", addr)
	defer c.Close()
	io.WriteString(c, "GET /ssh HTTP/1.1\r\nHost: cdn.example.com\r\nUpgrade: websocket\r\n"+
		"Connection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"+
		"CF-Connecting-IP: 203.0.113.9\r\n\r\n")
	br := bufio.NewReader(c)
	status, h := readHTTPResponse(t, br)
	if !strings.Contains(status, "101") || h["sec-websocket-accept"] != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("bad upgrade: %q %v", status, h)
	}
	expectSSH(t, c, br)
	waitVia(t, g, "ws")
	found := false
	for _, p := range g.Peers() {
		if p.IP == "203.0.113.9" && p.Via == "ws" {
			found = true
		}
	}
	if !found {
		t.Fatalf("CDN client IP not recorded: %+v", g.Peers())
	}
}

func TestSplitPayloadAndConnect(t *testing.T) {
	_, addr := startGW(t)
	c, _ := net.Dial("tcp", addr)
	defer c.Close()
	br := bufio.NewReader(c)
	io.WriteString(c, "GET http://bug.example.com/ HTTP/1.1\r\nHost: bug.example.com\r\n\r\n")
	if s, _ := readHTTPResponse(t, br); !strings.Contains(s, "101") {
		t.Fatalf("first: %q", s)
	}
	io.WriteString(c, "CONNECT 127.0.0.1:22 HTTP/1.1\r\n\r\n")
	if s, _ := readHTTPResponse(t, br); !strings.Contains(s, "200") {
		t.Fatalf("second: %q", s)
	}
	expectSSH(t, c, br)
}

// Some clients send the payload and the SSH banner in one go.
func TestPayloadAndBannerTogether(t *testing.T) {
	_, addr := startGW(t)
	c, _ := net.Dial("tcp", addr)
	defer c.Close()
	io.WriteString(c, "GET / HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\n\r\nSSH-2.0-TestClient\r\nping\n")
	br := bufio.NewReader(c)
	readHTTPResponse(t, br)
	if got := readLine(t, br); got != "SSH-2.0-FakeSSHD" {
		t.Fatalf("banner = %q", got)
	}
	if got := readLine(t, br); got != "SSH-2.0-TestClient" {
		t.Fatalf("echo = %q", got)
	}
}

// ─── real WebSocket framing ────────────────────────────────────────────────

func wsWriteMasked(w io.Writer, opcode byte, p []byte) {
	var key [4]byte
	rand.Read(key[:])
	hdr := []byte{0x80 | opcode}
	switch {
	case len(p) < 126:
		hdr = append(hdr, 0x80|byte(len(p)))
	default:
		hdr = append(hdr, 0x80|126)
		hdr = binary.BigEndian.AppendUint16(hdr, uint16(len(p)))
	}
	hdr = append(hdr, key[:]...)
	m := make([]byte, len(p))
	for i := range p {
		m[i] = p[i] ^ key[i&3]
	}
	w.Write(append(hdr, m...))
}

func wsReadFrame(t *testing.T, br *bufio.Reader) (byte, []byte) {
	t.Helper()
	var h [2]byte
	if _, err := io.ReadFull(br, h[:]); err != nil {
		t.Fatal(err)
	}
	if h[1]&0x80 != 0 {
		t.Fatal("server frames must not be masked")
	}
	n := int(h[1] & 0x7F)
	if n == 126 {
		var b [2]byte
		io.ReadFull(br, b[:])
		n = int(binary.BigEndian.Uint16(b[:]))
	}
	p := make([]byte, n)
	io.ReadFull(br, p)
	return h[0] & 0x0F, p
}

func TestRealWebSocketFrames(t *testing.T) {
	g, addr := startGW(t)
	c, _ := net.Dial("tcp", addr)
	defer c.Close()
	io.WriteString(c, "GET / HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\nSec-WebSocket-Key: abc\r\n\r\n")
	br := bufio.NewReader(c)
	readHTTPResponse(t, br)
	wsWriteMasked(c, 0x9, []byte("hi")) // ping first
	wsWriteMasked(c, 0x2, []byte("SSH-2.0-TestClient\r\n"))
	var got strings.Builder
	pong := false
	for !strings.Contains(got.String(), "SSH-2.0-TestClient") {
		op, p := wsReadFrame(t, br)
		switch op {
		case 0xA:
			pong = string(p) == "hi"
		case 0x2:
			got.Write(p)
		}
	}
	if !pong || !strings.HasPrefix(got.String(), "SSH-2.0-FakeSSHD\r\n") {
		t.Fatalf("pong=%v data=%q", pong, got.String())
	}
	waitVia(t, g, "ws")
}

// ─── TLS ────────────────────────────────────────────────────────────────────

func TestTLSRawSSH(t *testing.T) {
	g, addr := startGW(t)
	c, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true, ServerName: "zula.ir"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	expectSSH(t, c, bufio.NewReader(c))
	waitVia(t, g, "tls")
}

func TestTLSWithPayload(t *testing.T) {
	g, addr := startGW(t)
	c, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true, ServerName: "speedtest.net", MaxVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	io.WriteString(c, "GET / HTTP/1.1\r\nHost: speedtest.net\r\nUpgrade: websocket\r\nX-Forwarded-For: 198.51.100.7, 10.0.0.1\r\n\r\n")
	br := bufio.NewReader(c)
	if s, _ := readHTTPResponse(t, br); !strings.Contains(s, "101") {
		t.Fatalf("status %q", s)
	}
	expectSSH(t, c, br)
	waitVia(t, g, "tls+ws")
	ok := false
	for _, p := range g.Peers() {
		ok = ok || p.IP == "198.51.100.7"
	}
	if !ok {
		t.Fatalf("X-Forwarded-For not used: %+v", g.Peers())
	}
}

func TestTLSWithRealWebSocket(t *testing.T) {
	_, addr := startGW(t)
	c, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	io.WriteString(c, "GET / HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\n\r\n")
	br := bufio.NewReader(c)
	readHTTPResponse(t, br)
	wsWriteMasked(c, 0x2, []byte("SSH-2.0-TestClient\r\n"))
	var got strings.Builder
	for !strings.Contains(got.String(), "TestClient") {
		if op, p := wsReadFrame(t, br); op == 0x2 {
			got.Write(p)
		}
	}
}

func TestGarbageIsDropped(t *testing.T) {
	g, addr := startGW(t)
	c, _ := net.Dial("tcp", addr)
	defer c.Close()
	c.Write([]byte{0x00, 0xFF, 0x13, 0x37, 0x42, 0x42, 0x42, 0x42, 0x42})
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Read(make([]byte, 10)); err == nil {
		t.Fatal("garbage connection should be closed without data")
	}
	if g.Stats().Unknown == 0 {
		t.Fatal("unknown counter not incremented")
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		in   string
		k    kind
		more bool
	}{
		{"SSH-2.0", kindSSH, false},
		{"SS", kindUnknown, true},
		{"GET / HTTP/1.1", kindHTTP, false},
		{"GE", kindUnknown, true},
		{"CONNECT x", kindHTTP, false},
		{"\x16\x03\x01", kindTLS, false},
		{"\x82\x85abcd", kindWSFrame, false},
		{"\x82\x05abcd", kindUnknown, false}, // unmasked = not a client frame
		{"hello", kindUnknown, false},
	}
	for _, c := range cases {
		k, more := classify([]byte(c.in))
		if k != c.k || more != c.more {
			t.Errorf("classify(%q) = %v,%v want %v,%v", c.in, k, more, c.k, c.more)
		}
	}
}

func TestPortInUseReported(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	busy := ln.Addr().(*net.TCPAddr).Port
	g, err := New(Config{Listen: "127.0.0.1", Ports: []int{busy}, CertDir: t.TempDir(), Backend: &fakeSSHD{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Start(); err == nil {
		t.Fatal("expected error for busy port")
	}
	if len(g.Stats().Failed) != 1 {
		t.Fatalf("failed ports not reported: %+v", g.Stats())
	}
}
