package views

import (
	"fmt"
	"strings"

	"sshfortress/internal/card"

	"github.com/charmbracelet/lipgloss"
)

func (a *App) renderSettings(w int) string {
	var b strings.Builder
	b.WriteString(title("⚙️  SETTINGS"))
	cfg := a.cfg
	cw := cardWidth(w)
	onoff := func(v bool) string { return map[bool]string{true: "on", false: "off"}[v] }
	server := a.renderCard("🖥  Server", []cardRow{
		{"Host on cards", card.Host(cfg)},
		{"Config file", cfg.Path()},
		{"Data dir", cfg.DataDir},
	}, cw)
	defaults := a.renderCard("👤  New user defaults", []cardRow{
		{"Days", fmt.Sprint(cfg.UserDefaults.DefaultExpirationDays)},
		{"Devices", fmt.Sprint(cfg.UserDefaults.DefaultMaxConnections)},
		{"Traffic GB", fmt.Sprintf("%g (0 = unlimited)", cfg.UserDefaults.DefaultTrafficGB)},
		{"Group", cfg.UserDefaults.Group},
	}, cw)
	features := a.renderCard("🧩  Features", []cardRow{
		{"Gateway", fmt.Sprintf("%s %v", onoff(cfg.Gateway.Enabled), cfg.Gateway.Ports)},
		{"UDP gateway", fmt.Sprintf("%s (%s) %v", onoff(cfg.UDPGW.Enabled), cfg.UDPGW.Mode, cfg.UDPGW.Ports)},
		{"Limiter", fmt.Sprintf("%s, kick %s, every %ds", onoff(cfg.Limiter.Enabled), cfg.Limiter.Policy, cfg.Limiter.IntervalSecs)},
		{"Auto-optimize", onoff(cfg.Network.AutoOptimize)},
		{"Firewall", fmt.Sprintf("%s (local %s, smtp %s)", onoff(cfg.Firewall.Enabled), onoff(cfg.Firewall.BlockLocal), onoff(cfg.Firewall.BlockSMTP))},
	}, cw)
	ssh := a.renderCard("🔐  sshd", []cardRow{
		{"Ports", ints(cfg.SSH.Ports)},
		{"Tuning", onoff(cfg.SSH.Tuning)},
		{"Max auth tries", fmt.Sprint(cfg.SSH.MaxAuthTries)},
		{"Banner", trunc(strings.ReplaceAll(cfg.SSH.Banner, "\n", " "), cw-22)},
	}, cw)
	if twoColumns(w) {
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, server, "  ", defaults) + "\n")
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, features, "  ", ssh))
	} else {
		b.WriteString(lipgloss.JoinVertical(lipgloss.Left, server, defaults, features, ssh))
	}
	b.WriteString("\n\n  Edit " + cfg.Path() + " then run  sshfortress apply")
	return b.String()
}
