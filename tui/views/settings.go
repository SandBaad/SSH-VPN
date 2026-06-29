package views

import (
	"fmt"
	"strings"

	apptheme "sshfortress/tui/theme"

	"github.com/charmbracelet/lipgloss"
)

func (a *App) renderSettings() string {
	var b strings.Builder

	title := apptheme.StyleTitle.Render(" ⚙️  SETTINGS ")
	b.WriteString(title)
	b.WriteString("\n\n")

	cfg := a.cfg

	// SSH Settings card.
	portStrs := make([]string, len(cfg.SSH.Ports))
	for i, p := range cfg.SSH.Ports {
		portStrs[i] = fmt.Sprintf("%d", p)
	}
	pwdAuth := apptheme.StyleSuccess.Render("Enabled")
	if !cfg.SSH.PasswordAuth {
		pwdAuth = apptheme.StyleDanger.Render("Disabled")
	}

	sshCard := a.renderCard("🔐  SSH Configuration", []cardRow{
		{"Ports", strings.Join(portStrs, ", ")},
		{"Config Path", cfg.SSH.ConfigPath},
		{"Max Auth Tries", fmt.Sprintf("%d", cfg.SSH.MaxAuthTries)},
		{"Password Auth", pwdAuth},
	})

	// BadVPN Settings card.
	badvpnEnabled := apptheme.StyleDanger.Render("Disabled")
	if cfg.BadVPN.Enabled {
		badvpnEnabled = apptheme.StyleSuccess.Render("Enabled")
	}

	badvpnCard := a.renderCard("🌐  BadVPN Configuration", []cardRow{
		{"Enabled", badvpnEnabled},
		{"Binary Path", cfg.BadVPN.BinaryPath},
		{"Listen Address", cfg.BadVPN.ListenAddr},
		{"Max Clients", fmt.Sprintf("%d", cfg.BadVPN.MaxClients)},
		{"Max Conns/Client", fmt.Sprintf("%d", cfg.BadVPN.MaxConnectionsPerClient)},
	})

	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, sshCard, "  ", badvpnCard))
	b.WriteString("\n")

	// User Defaults card.
	userCard := a.renderCard("👤  User Defaults", []cardRow{
		{"Expiration (days)", fmt.Sprintf("%d", cfg.UserDefaults.DefaultExpirationDays)},
		{"Max Connections", fmt.Sprintf("%d", cfg.UserDefaults.DefaultMaxConnections)},
		{"Default Shell", cfg.UserDefaults.Shell},
	})

	// Network card.
	autoOpt := apptheme.StyleDim.Render("Disabled")
	if cfg.Network.AutoOptimize {
		autoOpt = apptheme.StyleSuccess.Render("Enabled")
	}

	netCard := a.renderCard("⚡  Network", []cardRow{
		{"Auto-Optimize", autoOpt},
		{"Congestion Control", cfg.Network.CongestionControl},
		{"Custom Sysctls", fmt.Sprintf("%d", len(cfg.Network.CustomSysctls))},
	})

	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, userCard, "  ", netCard))
	b.WriteString("\n")

	// Paths card.
	pathsCard := a.renderCard("📁  Paths", []cardRow{
		{"Data Directory", cfg.DataDir},
		{"Log Directory", cfg.LogDir},
		{"Auth Log", cfg.Monitor.AuthLogPath},
		{"Monitor Interval", fmt.Sprintf("%ds", cfg.Monitor.RefreshIntervalSecs)},
	})

	b.WriteString(pathsCard)
	b.WriteString("\n\n")
	b.WriteString(apptheme.StyleDim.Render("  Config file: /etc/sshfortress/config.yaml"))
	b.WriteString("\n")
	b.WriteString(apptheme.StyleKeyHelp.Render("  esc:back"))

	return b.String()
}
