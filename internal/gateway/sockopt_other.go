//go:build !linux

package gateway

import (
	"net"
	"time"
)

func tuneSocket(c *net.TCPConn) {
	_ = c.SetNoDelay(true)
	_ = c.SetKeepAlive(true)
	_ = c.SetKeepAlivePeriod(30 * time.Second)
}
