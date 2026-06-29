package views

import (
	"fmt"
	"strings"

	"sshfortress/internal/tunnel"
	apptheme "sshfortress/tui"

	tea "github.com/charmbracelet/bubbletea"
)

// BadVPNState holds data for the BadVPN view.
type BadVPNState struct {
	Status  tunnel.BadVPNStatus
	Message string
}

func (a *App) renderBadVPN() string {
	var b strings.Builder

	title := apptheme.StyleTitle.Render(" 🌐 BADVPN (UDP GATEWAY) ")
	b.WriteString(title)
	b.WriteString("\n\n")

	s := a.badvpnState

	if s.Message != "" {
		b.WriteString(apptheme.StyleSuccess.Render("  " + s.Message))
		b.WriteString("\n\n")
	}

	// Installation status.
	installText := apptheme.StyleDanger.Render(apptheme.CrossMark + " Not Installed")
	if s.Status.Installed {
		installText = apptheme.StyleSuccess.Render(apptheme.CheckMark + " Installed")
	}

	// Running status.
	runText := apptheme.StyleDanger.Render(apptheme.CrossMark + " Stopped")
	if s.Status.Running {
		runText = apptheme.StyleSuccess.Render(apptheme.CheckMark + " Running (PID: " + fmt.Sprintf("%d", s.Status.PID) + ")")
	}

	// Auto-start.
	autoText := apptheme.StyleDim.Render("○ Disabled")
	if s.Status.Enabled {
		autoText = apptheme.StyleSuccess.Render(apptheme.CheckMark + " Enabled")
	}

	info := a.renderCard("📋  Status", []cardRow{
		{"Installed", installText},
		{"Running", runText},
		{"Auto-Start", autoText},
		{"Binary Path", s.Status.BinaryPath},
	})

	config := a.renderCard("⚙️  Configuration", []cardRow{
		{"Listen Address", s.Status.ListenAddr},
		{"Max Clients", fmt.Sprintf("%d", s.Status.MaxClients)},
		{"Max Conns/Client", fmt.Sprintf("%d", s.Status.MaxConnsPC)},
	})

	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, info, "  ", config))
	b.WriteString("\n\n")

	// Action buttons.
	b.WriteString(apptheme.StyleSubtitle.Render("  Actions"))
	b.WriteString("\n")

	if s.Status.Running {
		b.WriteString(fmt.Sprintf("  %s Stop BadVPN\n", apptheme.StyleKeyBind.Render("[s]")))
		b.WriteString(fmt.Sprintf("  %s Restart BadVPN\n", apptheme.StyleKeyBind.Render("[x]")))
	} else {
		b.WriteString(fmt.Sprintf("  %s Start BadVPN\n", apptheme.StyleKeyBind.Render("[s]")))
	}

	if s.Status.Enabled {
		b.WriteString(fmt.Sprintf("  %s Disable Auto-Start\n", apptheme.StyleKeyBind.Render("[a]")))
	} else {
		b.WriteString(fmt.Sprintf("  %s Enable Auto-Start\n", apptheme.StyleKeyBind.Render("[a]")))
	}

	b.WriteString("\n")
	b.WriteString(apptheme.StyleKeyHelp.Render("  r:refresh  esc:back"))

	return b.String()
}

func (a *App) updateBadVPN(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "s":
		if a.badvpnState.Status.Running {
			if err := a.badvpnMgr.Stop(); err != nil {
				a.badvpnState.Message = "Error: " + err.Error()
			} else {
				a.badvpnState.Message = "BadVPN stopped"
			}
		} else {
			if err := a.badvpnMgr.Start(); err != nil {
				a.badvpnState.Message = "Error: " + err.Error()
			} else {
				a.badvpnState.Message = "BadVPN started successfully!"
			}
		}
		a.badvpnState.Status = a.badvpnMgr.GetStatus()

	case "x":
		if err := a.badvpnMgr.Restart(); err != nil {
			a.badvpnState.Message = "Error: " + err.Error()
		} else {
			a.badvpnState.Message = "BadVPN restarted"
		}
		a.badvpnState.Status = a.badvpnMgr.GetStatus()

	case "a":
		if a.badvpnState.Status.Enabled {
			a.badvpnMgr.Disable()
			a.badvpnState.Message = "Auto-start disabled"
		} else {
			a.badvpnMgr.Enable()
			a.badvpnState.Message = "Auto-start enabled"
		}
		a.badvpnState.Status = a.badvpnMgr.GetStatus()
	}
	return a, nil
}
