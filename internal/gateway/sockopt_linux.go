//go:build linux

package gateway

import (
	"net"
	"syscall"
	"time"
)

const tcpUserTimeout = 0x12 // TCP_USER_TIMEOUT

// tuneSocket prepares a client socket before it is handed to sshd:
//   - TCP_NODELAY: sshd never enables it for tunnel-only sessions, so small
//     packets (voice frames, keystrokes, ACK-like replies) would wait for
//     Nagle's algorithm. This is the single biggest latency win for calls.
//   - keepalive + TCP_USER_TIMEOUT: a phone that vanished (network switch,
//     tunnel through a train) is detected in about a minute instead of
//     ~15 minutes, freeing its slot under the connection limit.
func tuneSocket(c *net.TCPConn) {
	_ = c.SetNoDelay(true)
	_ = c.SetKeepAlive(true)
	_ = c.SetKeepAlivePeriod(30 * time.Second)
	if rc, err := c.SyscallConn(); err == nil {
		_ = rc.Control(func(fd uintptr) {
			_ = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, tcpUserTimeout, int((90 * time.Second).Milliseconds()))
		})
	}
}
