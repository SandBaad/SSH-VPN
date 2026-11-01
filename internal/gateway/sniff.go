package gateway

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"strings"
	"syscall"
	"time"
)

type kind int

const (
	kindUnknown kind = iota
	kindSSH
	kindHTTP
	kindTLS
	kindWSFrame
)

var httpMethods = []string{
	"GET", "POST", "PUT", "HEAD", "DELETE", "OPTIONS", "PATCH", "CONNECT", "TRACE",
	"PROPFIND", "PROPPATCH", "MKCOL", "COPY", "MOVE", "LOCK", "UNLOCK", "ACL",
	"PURGE", "SEARCH", "REPORT", "BMOVE", "DEBUG",
}

// classify inspects the first bytes of a stream. more=true means the bytes
// so far are a prefix of something we recognise and more are needed.
func classify(b []byte) (k kind, more bool) {
	if len(b) == 0 {
		return kindUnknown, true
	}
	if b[0] == 0x16 { // TLS handshake record
		return kindTLS, false
	}
	const sshPrefix = "SSH-"
	if len(b) < len(sshPrefix) && strings.HasPrefix(sshPrefix, string(b)) {
		return kindUnknown, true
	}
	if bytes.HasPrefix(b, []byte(sshPrefix)) {
		return kindSSH, false
	}
	prefixOfMethod := false
	for _, m := range httpMethods {
		tok := m + " "
		if bytes.HasPrefix(b, []byte(tok)) {
			return kindHTTP, false
		}
		if len(b) < len(tok) && strings.HasPrefix(tok, string(b)) {
			prefixOfMethod = true
		}
	}
	if prefixOfMethod {
		return kindUnknown, true
	}
	if isWSFrameStart(b) {
		return kindWSFrame, false
	}
	if len(b) == 1 && b[0]&0x70 == 0 {
		switch b[0] & 0x0F { // could be the first byte of a WebSocket frame
		case 0x0, 0x1, 0x2, 0x8, 0x9, 0xA:
			return kindUnknown, true
		}
	}
	return kindUnknown, false
}

// isWSFrameStart: FIN/RSV bits sane, a data/control opcode, and the MASK bit
// that RFC 6455 requires on every client-to-server frame.
func isWSFrameStart(b []byte) bool {
	if len(b) < 2 {
		return false
	}
	if b[0]&0x70 != 0 { // RSV1-3 must be zero
		return false
	}
	switch b[0] & 0x0F {
	case 0x0, 0x1, 0x2, 0x8, 0x9, 0xA:
	default:
		return false
	}
	return b[1]&0x80 != 0
}

// ─── raw-socket peeking (lets us hand the untouched socket to sshd) ─────────

var errTimeout = errors.New("timeout")

// peek returns bytes waiting on the socket without consuming them. It blocks
// until at least one byte is available or the read deadline passes.
func peek(c *net.TCPConn, buf []byte) (int, error) {
	rc, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var n int
	var opErr error
	err = rc.Read(func(fd uintptr) bool {
		n, _, opErr = syscall.Recvfrom(int(fd), buf, syscall.MSG_PEEK)
		if opErr == syscall.EAGAIN || opErr == syscall.EWOULDBLOCK {
			return false // wait until readable
		}
		if opErr == syscall.EINTR {
			return false
		}
		return true
	})
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return 0, errTimeout
		}
		return 0, err
	}
	if opErr != nil {
		return 0, opErr
	}
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

// peekClassify peeks until the stream can be classified or until deadline.
func peekClassify(c *net.TCPConn, deadline time.Time) (kind, []byte, error) {
	buf := make([]byte, 16)
	_ = c.SetReadDeadline(deadline)
	defer c.SetReadDeadline(time.Time{})
	for {
		n, err := peek(c, buf)
		if err != nil {
			return kindUnknown, nil, err
		}
		k, more := classify(buf[:n])
		if !more || n == len(buf) {
			return k, buf[:n], nil
		}
		// Data is buffered but incomplete: the socket stays readable, so
		// polling again would spin. Wait a little for the rest.
		if time.Now().After(deadline) {
			return k, buf[:n], errTimeout
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// httpRequest is the part of a request header we care about.
type httpRequest struct {
	Method  string
	Target  string
	Headers map[string]string // canonical lower-case keys
}

func (r *httpRequest) header(k string) string { return r.Headers[strings.ToLower(k)] }

// clientIP returns the real client address that a CDN put in the headers.
func (r *httpRequest) clientIP() string {
	for _, h := range []string{"cf-connecting-ip", "true-client-ip", "x-real-ip"} {
		if v := strings.TrimSpace(r.Headers[h]); v != "" {
			if ip := net.ParseIP(v); ip != nil {
				return ip.String()
			}
		}
	}
	if v := r.Headers["x-forwarded-for"]; v != "" {
		first := strings.TrimSpace(strings.Split(v, ",")[0])
		if ip := net.ParseIP(first); ip != nil {
			return ip.String()
		}
	}
	return ""
}

func (r *httpRequest) isWebSocket() bool {
	return strings.EqualFold(strings.TrimSpace(r.header("upgrade")), "websocket")
}

const maxHeader = 16 * 1024

// headerEnd finds the end of an HTTP header block ("\r\n\r\n" or "\n\n").
func headerEnd(b []byte) int {
	if i := bytes.Index(b, []byte("\r\n\r\n")); i >= 0 {
		return i + 4
	}
	if i := bytes.Index(b, []byte("\n\n")); i >= 0 {
		return i + 2
	}
	return -1
}

func parseHeader(b []byte) *httpRequest {
	req := &httpRequest{Headers: map[string]string{}}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	if len(lines) > 0 {
		f := strings.Fields(lines[0])
		if len(f) > 0 {
			req.Method = strings.ToUpper(f[0])
		}
		if len(f) > 1 {
			req.Target = f[1]
		}
	}
	for _, l := range lines[1:] {
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		if _, dup := req.Headers[k]; !dup {
			req.Headers[k] = strings.TrimSpace(v)
		}
	}
	return req
}

// readHeaderExact consumes exactly one HTTP header block from a raw socket,
// leaving anything after it unread (so the socket can still be handed to
// sshd untouched).
func readHeaderExact(c *net.TCPConn, deadline time.Time) (*httpRequest, error) {
	buf := make([]byte, maxHeader)
	_ = c.SetReadDeadline(deadline)
	defer c.SetReadDeadline(time.Time{})
	for {
		n, err := peek(c, buf)
		if err != nil {
			return nil, err
		}
		if end := headerEnd(buf[:n]); end > 0 {
			if _, err := io.ReadFull(c, buf[:end]); err != nil {
				return nil, err
			}
			return parseHeader(buf[:end]), nil
		}
		if n == len(buf) {
			return nil, errors.New("http header too large")
		}
		if time.Now().After(deadline) {
			return nil, errTimeout
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// readHeaderBuffered reads one HTTP header block from a buffered reader.
func readHeaderBuffered(br *bufio.Reader) (*httpRequest, error) {
	var raw []byte
	for {
		line, err := br.ReadSlice('\n')
		raw = append(raw, line...)
		if err != nil {
			return nil, err
		}
		if len(raw) > maxHeader {
			return nil, errors.New("http header too large")
		}
		if t := bytes.TrimRight(line, "\r\n"); len(t) == 0 && len(raw) > len(line) {
			return parseHeader(raw), nil
		}
	}
}
