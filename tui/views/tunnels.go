package views

import (
	"fmt"
	"strings"

	"sshfortress/internal/tunnel"
	apptheme "sshfortress/tui/theme"
)

// TunnelState holds data for the tunnels view.
type TunnelState struct {
	Status          tunnel.SSHStatus
	ConfiguredPorts []int
}

func (a *App) renderTunnels() string {
	var b strings.Builder

	title := apptheme.StyleTitle.Render(" 🔐 SSH TUNNELS ")
	b.WriteString(title)
	b.WriteString("\n\n")

	s := a.tunnelState

	// SSH Status.
	statusText := apptheme.StyleDanger.Render(apptheme.CrossMark + " Stopped")
	if s.Status.Running {
		statusText = apptheme.StyleSuccess.Render(apptheme.CheckMark + " Running")
	}

	b.WriteString(apptheme.StyleSubtitle.Render("  Service Status"))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  %s %s\n", apptheme.StyleLabel.Render("SSH Daemon:"), statusText))
	b.WriteString(fmt.Sprintf("  %s %s\n", apptheme.StyleLabel.Render("Config Path:"), apptheme.StyleValue.Render(s.Status.ConfigPath)))
	b.WriteString(fmt.Sprintf("  %s %s\n", apptheme.StyleLabel.Render("Active Connections:"), apptheme.StyleValue.Render(fmt.Sprintf("%d", s.Status.ActiveConns))))
	b.WriteString("\n")

	// Listening ports.
	b.WriteString(apptheme.StyleSubtitle.Render("  Listening Ports"))
	b.WriteString("\n")

	if len(s.Status.ListeningPorts) == 0 {
		b.WriteString(apptheme.StyleDim.Render("  No ports detected"))
	} else {
		for _, port := range s.Status.ListeningPorts {
			b.WriteString(fmt.Sprintf("  %s Port %s\n",
				apptheme.StyleSuccess.Render(apptheme.BulletActive),
				apptheme.StyleValue.Render(fmt.Sprintf("%d", port)),
			))
		}
	}
	b.WriteString("\n")

	// Configured ports.
	b.WriteString(apptheme.StyleSubtitle.Render("  Configured Ports (sshd_config)"))
	b.WriteString("\n")
	if len(s.ConfiguredPorts) == 0 {
		b.WriteString(apptheme.StyleDim.Render("  Default (22)"))
	} else {
		portStrs := make([]string, len(s.ConfiguredPorts))
		for i, p := range s.ConfiguredPorts {
			portStrs[i] = fmt.Sprintf("%d", p)
		}
		b.WriteString(fmt.Sprintf("  %s\n", apptheme.StyleValue.Render(strings.Join(portStrs, ", "))))
	}

	b.WriteString("\n\n")
	b.WriteString(apptheme.StyleKeyHelp.Render("  r:refresh  esc:back"))

	return b.String()
}
