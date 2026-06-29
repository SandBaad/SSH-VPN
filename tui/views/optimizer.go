package views

import (
	"fmt"
	"strings"

	"sshfortress/internal/system"
	apptheme "sshfortress/tui/theme"

	tea "github.com/charmbracelet/bubbletea"
)

// OptimizerState holds data for the optimizer view.
type OptimizerState struct {
	Params  []system.SysctlParam
	Message string
}

func (a *App) renderOptimizer() string {
	var b strings.Builder

	title := apptheme.StyleTitle.Render(" ⚡ NETWORK OPTIMIZER ")
	b.WriteString(title)
	b.WriteString("\n\n")

	if a.optState.Message != "" {
		b.WriteString(apptheme.StyleSuccess.Render("  " + a.optState.Message))
		b.WriteString("\n\n")
	}

	// Stats.
	applied := 0
	for _, p := range a.optState.Params {
		if p.Applied {
			applied++
		}
	}
	total := len(a.optState.Params)

	percent := float64(0)
	if total > 0 {
		percent = float64(applied) / float64(total) * 100
	}

	bar := renderProgressBar(percent, 30)
	b.WriteString(fmt.Sprintf("  Optimization: %s %.0f%% (%d/%d params applied)\n\n",
		bar, percent, applied, total))

	// Parameter table.
	headerStyle := apptheme.StyleTableHeader
	header := fmt.Sprintf("  %-4s %-38s %-18s %-18s",
		headerStyle.Render(""),
		headerStyle.Render("PARAMETER"),
		headerStyle.Render("CURRENT"),
		headerStyle.Render("OPTIMAL"),
	)
	b.WriteString(header)
	b.WriteString("\n")
	b.WriteString(apptheme.StyleDim.Render("  " + strings.Repeat("─", 78)))
	b.WriteString("\n")

	for _, p := range a.optState.Params {
		icon := apptheme.StyleDanger.Render(apptheme.CrossMark)
		if p.Applied {
			icon = apptheme.StyleSuccess.Render(apptheme.CheckMark)
		}

		current := p.CurrentValue
		if len(current) > 16 {
			current = current[:16] + "…"
		}
		optimal := p.OptimalValue
		if len(optimal) > 16 {
			optimal = optimal[:16] + "…"
		}

		// Highlight differences.
		currentStyle := apptheme.StyleDanger
		if p.Applied {
			currentStyle = apptheme.StyleSuccess
		}

		row := fmt.Sprintf("  %s  %-38s %-18s %-18s",
			icon,
			apptheme.StyleDim.Render(p.Key),
			currentStyle.Render(current),
			apptheme.StyleInfo.Render(optimal),
		)
		b.WriteString(row)
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(apptheme.StyleSubtitle.Render("  Actions"))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  %s Apply all optimizations\n", apptheme.StyleKeyBind.Render("[a]")))
	b.WriteString(fmt.Sprintf("  %s Reset to system defaults\n", apptheme.StyleKeyBind.Render("[x]")))
	b.WriteString("\n")
	b.WriteString(apptheme.StyleKeyHelp.Render("  r:refresh  esc:back"))

	return b.String()
}

func (a *App) updateOptimizer(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "a":
		count, err := a.optimizer.Apply()
		if err != nil {
			a.optState.Message = fmt.Sprintf("Applied %d params (some errors: %v)", count, err)
		} else {
			a.optState.Message = fmt.Sprintf("Successfully applied %d optimizations!", count)
		}
		a.optState.Params = a.optimizer.GetRecommendedParams()

	case "x":
		if err := a.optimizer.Reset(); err != nil {
			a.optState.Message = "Reset error: " + err.Error()
		} else {
			a.optState.Message = "Reset to system defaults"
		}
		a.optState.Params = a.optimizer.GetRecommendedParams()
	}
	return a, nil
}
