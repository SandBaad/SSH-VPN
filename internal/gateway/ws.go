package gateway

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// wsAccept computes Sec-WebSocket-Accept for a Sec-WebSocket-Key.
func wsAccept(key string) string {
	h := sha1.Sum([]byte(strings.TrimSpace(key) + wsGUID))
	return base64.StdEncoding.EncodeToString(h[:])
}

// upgradeResponse builds the reply to a payload/WebSocket request.
//
// VPN apps ("SSH over WebSocket", "HTTP payload") and CDNs such as
// Cloudflare only need a 101 handshake; most apps then send raw SSH bytes,
// some send real WebSocket frames — the gateway detects which afterwards.
func upgradeResponse(req *httpRequest) string {
	if req.Method == "CONNECT" {
		return "HTTP/1.1 200 Connection established\r\n\r\n"
	}
	var b strings.Builder
	b.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n")
	if key := req.header("sec-websocket-key"); key != "" {
		fmt.Fprintf(&b, "Sec-WebSocket-Accept: %s\r\n", wsAccept(key))
	}
	if p := req.header("sec-websocket-protocol"); p != "" {
		// Echo the first offered sub-protocol; some clients insist on it.
		fmt.Fprintf(&b, "Sec-WebSocket-Protocol: %s\r\n", strings.TrimSpace(strings.Split(p, ",")[0]))
	}
	b.WriteString("\r\n")
	return b.String()
}

// wsConn adapts a WebSocket byte stream (RFC 6455 frames) to plain bytes.
type wsConn struct {
	r *bufio.Reader
	w io.Writer

	wmu sync.Mutex // frames from Write and control replies must not interleave

	remaining uint64
	masked    bool
	mask      [4]byte
	maskPos   int
	closed    bool
}

var errWSClosed = errors.New("websocket closed")

func (c *wsConn) Read(p []byte) (int, error) {
	for c.remaining == 0 {
		if c.closed {
			return 0, io.EOF
		}
		if err := c.nextFrame(); err != nil {
			return 0, err
		}
	}
	if uint64(len(p)) > c.remaining {
		p = p[:c.remaining]
	}
	n, err := c.r.Read(p)
	if c.masked {
		for i := 0; i < n; i++ {
			p[i] ^= c.mask[c.maskPos&3]
			c.maskPos++
		}
	}
	c.remaining -= uint64(n)
	return n, err
}

// nextFrame reads frame headers, answering control frames, until a data
// frame with payload begins.
func (c *wsConn) nextFrame() error {
	var h [2]byte
	if _, err := io.ReadFull(c.r, h[:]); err != nil {
		return err
	}
	opcode := h[0] & 0x0F
	c.masked = h[1]&0x80 != 0
	length := uint64(h[1] & 0x7F)
	switch length {
	case 126:
		var b [2]byte
		if _, err := io.ReadFull(c.r, b[:]); err != nil {
			return err
		}
		length = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err := io.ReadFull(c.r, b[:]); err != nil {
			return err
		}
		length = binary.BigEndian.Uint64(b[:])
	}
	if c.masked {
		if _, err := io.ReadFull(c.r, c.mask[:]); err != nil {
			return err
		}
	}
	c.maskPos = 0

	if opcode >= 0x8 { // control frame
		if length > 125 {
			return errors.New("websocket: oversized control frame")
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(c.r, payload); err != nil {
			return err
		}
		if c.masked {
			for i := range payload {
				payload[i] ^= c.mask[i&3]
			}
		}
		switch opcode {
		case 0x8: // close
			_ = c.writeFrame(0x8, payload)
			c.closed = true
			return io.EOF
		case 0x9: // ping
			return c.writeFrame(0xA, payload)
		}
		return nil // pong: ignore
	}
	c.remaining = length
	return nil
}

func (c *wsConn) writeFrame(opcode byte, p []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	hdr := make([]byte, 0, 10+len(p))
	hdr = append(hdr, 0x80|opcode)
	switch n := len(p); {
	case n < 126:
		hdr = append(hdr, byte(n))
	case n <= 0xFFFF:
		hdr = append(hdr, 126)
		hdr = binary.BigEndian.AppendUint16(hdr, uint16(n))
	default:
		hdr = append(hdr, 127)
		hdr = binary.BigEndian.AppendUint64(hdr, uint64(n))
	}
	_, err := c.w.Write(append(hdr, p...))
	return err
}

// Write sends p as one binary frame (server frames are never masked).
func (c *wsConn) Write(p []byte) (int, error) {
	if err := c.writeFrame(0x2, p); err != nil {
		return 0, err
	}
	return len(p), nil
}
