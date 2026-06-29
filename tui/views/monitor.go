package views

import (
	"fmt"
	"strings"

	"sshfortress/internal/store"
	apptheme "sshfortress/tui"
)

// MonitorState holds data for the monitor view.
type MonitorState struct {
	Sessions []store.SessionRecord
}

func (a *App) renderMonitor() string {
	var b strings.Builder

	title := apptheme.StyleTitle.Render(" 📡 LIVE SESSION MONITOR ")
	b.WriteString(title)
	b.WriteString("\n\n")

	s := a.monitorState

	// Stats bar.
	b.WriteString(fmt.Sprintf("  %s %s     %s %s\n\n",
		apptheme.StyleLabel.Render("Active Sessions:"),
		apptheme.StyleValue.Render(fmt.Sprintf("%d", len(s.Sessions))),
		apptheme.StyleLabel.Render("Auto-Refresh:"),
		apptheme.StyleInfo.Render(fmt.Sprintf("every %ds", a.cfg.Monitor.RefreshIntervalSecs)),
	))

	// Session table.
	headerStyle := apptheme.StyleTableHeader
	header := fmt.Sprintf("  %-16s %-10s %-22s %-20s",
		headerStyle.Render("USERNAME"),
		headerStyle.Render("PID"),
		headerStyle.Render("CLIENT IP"),
		headerStyle.Render("CONNECTED AT"),
	)
	b.WriteString(header)
	b.WriteString("\n")
	b.WriteString(apptheme.StyleDim.Render("  " + strings.Repeat("─", 68)))
	b.WriteString("\n")

	if len(s.Sessions) == 0 {
		b.WriteString("\n")
		b.WriteString(apptheme.StyleDim.Render("  No active SSH sessions"))
		b.WriteString("\n")
	} else {
		for _, sess := range s.Sessions {
			row := fmt.Sprintf("  %-16s %-10d %-22s %-20s",
				apptheme.StyleValue.Render(sess.Username),
				sess.PID,
				sess.ClientIP,
				sess.LoginTime.Format("15:04:05"),
			)
			b.WriteString(row)
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")

	// Per-user summary.
	if len(s.Sessions) > 0 {
		b.WriteString(apptheme.StyleSubtitle.Render("  Per-User Summary"))
		b.WriteString("\n")

		userCounts := make(map[string]int)
		for _, sess := range s.Sessions {
			userCounts[sess.Username]++
		}
		for user, count := range userCounts {
			bar := renderProgressBar(float64(count)/float64(5)*100, 10) // 5 as visual max
			b.WriteString(fmt.Sprintf("  %-14s %s %d sessions\n",
				apptheme.StyleValue.Render(user),
				bar,
				count,
			))
		}
		b.WriteString("\n")
	}

	b.WriteString(apptheme.StyleKeyHelp.Render("  r:refresh  esc:back"))

	return b.String()
}
