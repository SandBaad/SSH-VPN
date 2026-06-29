package views

import (
	"fmt"
	"strings"

	"sshfortress/internal/store"
	"sshfortress/internal/system"
	apptheme "sshfortress/tui"

	tea "github.com/charmbracelet/bubbletea"
)

// BackupState holds data for the backup view.
type BackupState struct {
	Backups []store.BackupRecord
	Cursor  int
	Message string
}

func (a *App) renderBackup() string {
	var b strings.Builder

	title := apptheme.StyleTitle.Render(" 💾 BACKUP & RESTORE ")
	b.WriteString(title)
	b.WriteString("\n\n")

	if a.backupState.Message != "" {
		b.WriteString(apptheme.StyleSuccess.Render("  " + a.backupState.Message))
		b.WriteString("\n\n")
	}

	// Actions.
	b.WriteString(apptheme.StyleSubtitle.Render("  Actions"))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  %s Create new backup\n", apptheme.StyleKeyBind.Render("[c]")))
	b.WriteString(fmt.Sprintf("  %s Restore selected backup\n", apptheme.StyleKeyBind.Render("[enter]")))
	b.WriteString("\n")

	// Backup list.
	b.WriteString(apptheme.StyleSubtitle.Render("  Existing Backups"))
	b.WriteString("\n")

	if len(a.backupState.Backups) == 0 {
		b.WriteString(apptheme.StyleDim.Render("  No backups found. Press 'c' to create one."))
		b.WriteString("\n")
	} else {
		headerStyle := apptheme.StyleTableHeader
		header := fmt.Sprintf("  %-22s %-12s %-8s %-40s",
			headerStyle.Render("DATE"),
			headerStyle.Render("SIZE"),
			headerStyle.Render("USERS"),
			headerStyle.Render("FILE"),
		)
		b.WriteString(header)
		b.WriteString("\n")
		b.WriteString(apptheme.StyleDim.Render("  " + strings.Repeat("─", 80)))
		b.WriteString("\n")

		for i, bk := range a.backupState.Backups {
			row := fmt.Sprintf("  %-22s %-12s %-8d %-40s",
				bk.CreatedAt.Format("2006-01-02 15:04:05"),
				system.FormatBytes(uint64(bk.SizeBytes)),
				bk.UserCount,
				bk.FilePath,
			)

			if i == a.backupState.Cursor {
				b.WriteString(apptheme.StyleMenuItemActive.Width(84).Render(row))
			} else {
				b.WriteString(row)
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(apptheme.StyleKeyHelp.Render("  c:create  enter:restore  ↑↓:select  esc:back"))

	return b.String()
}

func (a *App) updateBackup(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if a.backupState.Cursor > 0 {
			a.backupState.Cursor--
		}
	case "down", "j":
		if a.backupState.Cursor < len(a.backupState.Backups)-1 {
			a.backupState.Cursor++
		}
	case "c":
		path, err := a.backupEng.Create()
		if err != nil {
			a.backupState.Message = "Backup error: " + err.Error()
		} else {
			a.backupState.Message = "Backup created: " + path
		}
		a.backupState.Backups, _ = a.backupEng.ListBackups()

	case "enter":
		if len(a.backupState.Backups) > 0 {
			bk := a.backupState.Backups[a.backupState.Cursor]
			contents, err := a.backupEng.Restore(bk.FilePath)
			if err != nil {
				a.backupState.Message = "Restore error: " + err.Error()
			} else {
				a.backupState.Message = fmt.Sprintf("Restored %d users from %s",
					len(contents.Users), bk.CreatedAt.Format("2006-01-02"))
			}
		}
	}
	return a, nil
}
