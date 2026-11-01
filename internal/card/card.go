// Package card renders the "connection card" an admin sends to a user:
// everything a VPN app needs, in one copy-paste block.
package card

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"sshfortress/internal/config"
	"sshfortress/internal/store"
	"sshfortress/internal/system"
)

var (
	ipOnce sync.Once
	ipVal  string
)

// Host returns server.host or the detected public IP (cached).
func Host(cfg *config.Config) string {
	if h := strings.TrimSpace(cfg.Server.Host); h != "" {
		return h
	}
	ipOnce.Do(func() { ipVal = system.PublicIP() })
	return ipVal
}

// FormatBytes renders a byte count (binary units).
func FormatBytes(b int64) string {
	if b < 0 {
		b = 0
	}
	return system.FormatBytes(uint64(b))
}

// Traffic renders "used / limit".
func Traffic(u *store.UserRecord) string {
	if u.TrafficLimit <= 0 {
		return FormatBytes(u.TrafficUsed) + " / unlimited"
	}
	pct := float64(u.TrafficUsed) / float64(u.TrafficLimit) * 100
	return fmt.Sprintf("%s / %s (%.0f%%)", FormatBytes(u.TrafficUsed), FormatBytes(u.TrafficLimit), pct)
}

func ints(v []int) string {
	s := make([]string, len(v))
	for i, n := range v {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ", ")
}

// Render builds the card. password may be empty (it is only known right
// after creating the user or resetting the password).
func Render(cfg *config.Config, u *store.UserRecord, password string) string {
	host := Host(cfg)
	var b strings.Builder
	line := strings.Repeat("━", 46)
	fmt.Fprintf(&b, "%s\n  SSH-VPN account\n%s\n", line, line)
	row := func(k, v string) { fmt.Fprintf(&b, "  %-12s %s\n", k+":", v) }
	row("Host", host)
	row("Username", u.Username)
	if password != "" {
		row("Password", password)
	} else {
		row("Password", "(unchanged)")
	}
	days := int(math.Ceil(time.Until(u.ExpiresAt).Hours() / 24))
	if days < 0 {
		days = 0
	}
	row("Expires", fmt.Sprintf("%s (%d days)", u.ExpiresAt.Format("2006-01-02"), days))
	row("Devices", strconv.Itoa(u.MaxConnections))
	row("Traffic", Traffic(u))
	b.WriteString("\n")
	row("SSH", ints(cfg.SSH.Ports))
	if cfg.Gateway.Enabled && len(cfg.Gateway.Ports) > 0 {
		row("Gateway", ints(cfg.Gateway.Ports)+"  (SSH · WebSocket · SSL/TLS — any SNI)")
	}
	if cfg.UDPGW.Enabled && len(cfg.UDPGW.Ports) > 0 {
		row("UDPGW", "127.0.0.1:"+strconv.Itoa(cfg.UDPGW.Ports[len(cfg.UDPGW.Ports)-1])+"  (enable for calls & games)")
	}
	if cfg.Gateway.Enabled && len(cfg.Gateway.Ports) > 0 {
		b.WriteString("\n  WebSocket payload:\n")
		fmt.Fprintf(&b, "  GET / HTTP/1.1[crlf]Host: %s[crlf]Upgrade: websocket[crlf][crlf]\n", host)
	}
	b.WriteString(line + "\n")
	return b.String()
}
