package views

import (
	"fmt"
	"strings"

	"sshfortress/internal/netopt"
	apptheme "sshfortress/tui/theme"

	tea "github.com/charmbracelet/bubbletea"
)

// OptimizerState holds kernel tuning status.
type OptimizerState struct {
	Params  []netopt.Param
	Message string
}

func loadOptimizer(a *App) OptimizerState { return OptimizerState{Params: netopt.Status(a.cfg)} }

func (a *App) renderOptimizer(w int) string {
	var b strings.Builder
	b.WriteString(title("⚡ NETWORK OPTIMIZER"))
	applied, total := netopt.Score(a.opt.Params)
	pct := 0.0
	if total > 0 {
		pct = float64(applied) / float64(total) * 100
	}
	b.WriteString(fmt.Sprintf("  %s %.0f%%  (%d/%d)   BBR: %s\n\n", renderProgressBar(pct, 30), pct, applied, total, ok(netopt.BBRActive(), "on", "off")))
	widths := []int{2, 36, 20, 20}
	b.WriteString(header(widths, "", "SETTING", "CURRENT", "TARGET"))
	for _, p := range a.opt.Params {
		mark := apptheme.StyleDanger.Render(apptheme.CrossMark)
		cur := apptheme.StyleDanger.Render(p.Current)
		if p.Applied {
			mark = apptheme.StyleSuccess.Render(apptheme.CheckMark)
			cur = apptheme.StyleSuccess.Render(p.Current)
		} else if !p.Supported {
			mark = apptheme.StyleDim.Render("-")
			cur = apptheme.StyleDim.Render("n/a")
		}
		b.WriteString(row(widths, mark, apptheme.StyleDim.Render(p.Key), cur, apptheme.StyleInfo.Render(p.Value)) + "\n")
	}
	b.WriteString(keyHelp("a apply all (persists across reboots)   x restore original values"))
	return b.String()
}

func (a *App) updateOptimizer(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "a":
		return func() tea.Msg {
			r, err := netopt.Apply(a.cfg)
			if err != nil {
				return opDoneMsg{err: err, reload: true}
			}
			text := fmt.Sprintf("applied %d/%d settings", r.Applied, r.Total)
			if len(r.Warnings) > 0 {
				text += " — " + strings.Join(r.Warnings, "; ")
			}
			return opDoneMsg{text: text, reload: true}
		}
	case "x":
		a.modal = confirmModal("Reset", "Restore the kernel settings from before SSH-VPN?", func() tea.Cmd {
			return func() tea.Msg {
				return opDoneMsg{text: "restored original settings", err: netopt.Reset(a.cfg), reload: true}
			}
		})
	}
	return nil
}
