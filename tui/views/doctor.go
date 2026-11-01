package views

import (
	"fmt"
	"strings"

	"sshfortress/internal/doctor"
	apptheme "sshfortress/tui/theme"

	tea "github.com/charmbracelet/bubbletea"
)

// DoctorState holds diagnostic results.
type DoctorState struct {
	Checks []doctor.Check
	Ran    bool
}

func loadDoctor(a *App) DoctorState { return DoctorState{Checks: doctor.Run(a.cfg, a.db), Ran: true} }

func (a *App) renderDoctor(w int) string {
	var b strings.Builder
	b.WriteString(title("🩺 DOCTOR"))
	if !a.doc.Ran {
		return b.String() + "  running checks…"
	}
	for _, c := range a.doc.Checks {
		var mark string
		switch c.Status {
		case doctor.OK:
			mark = apptheme.StyleSuccess.Render("✓")
		case doctor.Warn:
			mark = apptheme.StyleWarning.Render("!")
		case doctor.Fail:
			mark = apptheme.StyleDanger.Render("✗")
		default:
			mark = apptheme.StyleDim.Render("-")
		}
		b.WriteString(fmt.Sprintf("  %s %s %s\n", mark, pad(c.Name, 20), trunc(c.Detail, w-28)))
		if c.Fix != "" && c.Status != doctor.OK {
			b.WriteString(apptheme.StyleDim.Render("      → "+trunc(c.Fix, w-12)) + "\n")
		}
	}
	okN, warn, fail := doctor.Summary(a.doc.Checks)
	b.WriteString(fmt.Sprintf("\n  %s  %s  %s",
		apptheme.StyleSuccess.Render(fmt.Sprintf("%d ok", okN)),
		apptheme.StyleWarning.Render(fmt.Sprintf("%d warnings", warn)),
		apptheme.StyleDanger.Render(fmt.Sprintf("%d problems", fail))))
	b.WriteString(keyHelp("r run again"))
	return b.String()
}

func (a *App) updateDoctor(msg tea.KeyMsg) tea.Cmd { return nil }
