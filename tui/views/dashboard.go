package views

import (
	"fmt"
	"strings"
	"time"

	"sshfortress/internal/system"
	"sshfortress/internal/tunnel"
	apptheme "sshfortress/tui"

	"github.com/charmbracelet/lipgloss"
)

// DashboardState holds data for the dashboard view.
type DashboardState struct {
	SysInfo        system.Info
	SSHStatus      tunnel.SSHStatus
	BadVPNStatus   tunnel.BadVPNStatus
	UserCount      int
	ActiveSessions int
}

func (a *App) renderDashboard() string {
	var b strings.Builder

	title := apptheme.StyleTitle.Render(" 📊 DASHBOARD ")
	b.WriteString(title)
	b.WriteString("\n\n")

	d := a.dashState

	// ─── Server Info Card ───────────────────────────────────────────────
	serverInfo := a.renderCard("🖥️  Server Information", []cardRow{
		{"Hostname", d.SysInfo.Hostname},
		{"OS", d.SysInfo.OS},
		{"Kernel", d.SysInfo.Kernel},
		{"CPU", fmt.Sprintf("%s (%d cores)", d.SysInfo.CPUModel, d.SysInfo.CPUCores)},
		{"Public IP", d.SysInfo.PublicIP},
		{"Uptime", system.FormatUptime(d.SysInfo.Uptime)},
		{"Load Avg", d.SysInfo.LoadAvg},
	})

	// ─── Resources Card ─────────────────────────────────────────────────
	memBar := renderProgressBar(d.SysInfo.MemPercent, 20)
	diskBar := renderProgressBar(d.SysInfo.DiskPercent, 20)

	resourcesInfo := a.renderCard("📈  Resources", []cardRow{
		{"Memory", fmt.Sprintf("%s / %s  %s %.0f%%",
			system.FormatBytes(d.SysInfo.MemUsed),
			system.FormatBytes(d.SysInfo.MemTotal),
			memBar,
			d.SysInfo.MemPercent,
		)},
		{"Disk", fmt.Sprintf("%s / %s  %s %.0f%%",
			system.FormatBytes(d.SysInfo.DiskUsed),
			system.FormatBytes(d.SysInfo.DiskTotal),
			diskBar,
			d.SysInfo.DiskPercent,
		)},
	})

	// ─── Service Status Card ────────────────────────────────────────────
	sshStatusText := apptheme.StyleDanger.Render(apptheme.CrossMark + " Stopped")
	if d.SSHStatus.Running {
		sshStatusText = apptheme.StyleSuccess.Render(apptheme.CheckMark + " Running")
	}

	badvpnStatusText := apptheme.StyleDanger.Render(apptheme.CrossMark + " Stopped")
	if d.BadVPNStatus.Running {
		badvpnStatusText = apptheme.StyleSuccess.Render(apptheme.CheckMark + " Running")
	} else if !d.BadVPNStatus.Installed {
		badvpnStatusText = apptheme.StyleWarning.Render("○ Not Installed")
	}

	portsStr := "none"
	if len(d.SSHStatus.ListeningPorts) > 0 {
		portStrs := make([]string, len(d.SSHStatus.ListeningPorts))
		for i, p := range d.SSHStatus.ListeningPorts {
			portStrs[i] = fmt.Sprintf("%d", p)
		}
		portsStr = strings.Join(portStrs, ", ")
	}

	servicesInfo := a.renderCard("🔌  Services", []cardRow{
		{"SSH Daemon", sshStatusText},
		{"SSH Ports", portsStr},
		{"BadVPN (udpgw)", badvpnStatusText},
		{"Active Tunnels", fmt.Sprintf("%d", d.SSHStatus.ActiveConns)},
	})

	// ─── Quick Stats Card ───────────────────────────────────────────────
	statsInfo := a.renderCard("📊  Quick Stats", []cardRow{
		{"Total Users", fmt.Sprintf("%d", d.UserCount)},
		{"Active Sessions", fmt.Sprintf("%d", d.ActiveSessions)},
		{"Server Time", time.Now().Format("2006-01-02 15:04:05")},
	})

	// Layout: 2x2 grid.
	topRow := lipgloss.JoinHorizontal(lipgloss.Top, serverInfo, "  ", resourcesInfo)
	bottomRow := lipgloss.JoinHorizontal(lipgloss.Top, servicesInfo, "  ", statsInfo)

	b.WriteString(topRow)
	b.WriteString("\n")
	b.WriteString(bottomRow)

	return b.String()
}

type cardRow struct {
	Label string
	Value string
}

func (a *App) renderCard(title string, rows []cardRow) string {
	width := (a.width - 30) / 2
	if width < 40 {
		width = 40
	}

	var content strings.Builder
	content.WriteString(apptheme.StyleSubtitle.Render(title))
	content.WriteString("\n")

	for _, row := range rows {
		label := apptheme.StyleLabel.Render(row.Label)
		value := apptheme.StyleValue.Render(row.Value)
		content.WriteString(fmt.Sprintf("%s %s\n", label, value))
	}

	return apptheme.StylePanel.
		Width(width).
		Render(content.String())
}

func renderProgressBar(percent float64, width int) string {
	if percent > 100 {
		percent = 100
	}
	filled := int(percent / 100 * float64(width))
	if filled > width {
		filled = width
	}

	var color lipgloss.Color
	switch {
	case percent >= 90:
		color = apptheme.ColorDanger
	case percent >= 70:
		color = apptheme.ColorWarning
	default:
		color = apptheme.ColorSuccess
	}

	bar := lipgloss.NewStyle().Foreground(color).Render(strings.Repeat("█", filled))
	empty := lipgloss.NewStyle().Foreground(apptheme.ColorFgMuted).Render(strings.Repeat("░", width-filled))

	return bar + empty
}
