package views

import (
	"fmt"
	"strings"
	"time"

	"sshfortress/internal/card"
	"sshfortress/internal/session"
	"sshfortress/internal/state"
	"sshfortress/internal/system"
	apptheme "sshfortress/tui/theme"

	tea "github.com/charmbracelet/bubbletea"
)

// OnlineState holds live sessions.
type OnlineState struct {
	Sessions []state.Session
	Live     bool // from the daemon (with speeds)
	Total    uint64
	Cursor   int
}

func loadOnline(a *App) OnlineState {
	if st, err := state.Read(); err == nil && st.Fresh() {
		return OnlineState{Sessions: st.Sessions, Live: true, Total: st.TotalBps}
	}
	ss, _ := a.users.Scanner.Scan()
	out := make([]state.Session, len(ss))
	for i, s := range ss {
		out[i] = state.Session{Session: s}
	}
	return OnlineState{Sessions: out}
}

func (a *App) renderOnline(w int) string {
	var b strings.Builder
	b.WriteString(title("📡 ONLINE"))
	s := a.online
	src := apptheme.StyleSuccess.Render("live from daemon")
	if !s.Live {
		src = apptheme.StyleWarning.Render("daemon not running — no speeds")
	}
	b.WriteString(fmt.Sprintf("  %d sessions · %s total · %s\n\n", len(s.Sessions), system.FormatBytes(s.Total)+"/s", src))
	widths := []int{14, 16, 8, 9, 10, 11, 4}
	b.WriteString(header(widths, "USER", "CLIENT", "VIA", "SINCE", "TRAFFIC", "SPEED", "UDP"))
	if len(s.Sessions) == 0 {
		b.WriteString(apptheme.StyleDim.Render("\n  nobody is connected"))
	}
	for i, x := range s.Sessions {
		speed := "-"
		if x.RateBps > 0 {
			speed = system.FormatBytes(x.RateBps) + "/s"
		}
		since := x.StartedAt.Format("15:04:05")
		if time.Since(x.StartedAt) > 24*time.Hour {
			since = x.StartedAt.Format("01-02")
		}
		line := row(widths, x.Username, x.ClientIP, x.Via, since, card.FormatBytes(int64(x.Bytes)), speed, fmt.Sprint(x.UDPFlows))
		if i == s.Cursor && !a.focusSidebar {
			line = apptheme.StyleTableRowSelected.Width(w - 2).Render(stripANSI(line))
		}
		b.WriteString(line + "\n")
	}
	b.WriteString(keyHelp("↑↓ select   x kick session   r refresh   (UDP = active call/game flows)"))
	return b.String()
}

func (a *App) updateOnline(msg tea.KeyMsg) tea.Cmd {
	s := &a.online
	switch msg.String() {
	case "up", "k":
		if s.Cursor > 0 {
			s.Cursor--
		}
	case "down", "j":
		if s.Cursor < len(s.Sessions)-1 {
			s.Cursor++
		}
	case "x", "delete":
		if len(s.Sessions) == 0 {
			return nil
		}
		x := s.Sessions[s.Cursor]
		a.modal = confirmModal("Kick session", fmt.Sprintf("Disconnect %s (%s)?", x.Username, x.ClientIP), func() tea.Cmd {
			return func() tea.Msg {
				err := session.Kill(x.PID)
				return opDoneMsg{text: "disconnected " + x.Username, err: err, reload: true}
			}
		})
	}
	return nil
}
