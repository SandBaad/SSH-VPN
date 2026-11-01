package views

import (
	"fmt"
	"strings"

	"sshfortress/internal/card"
	"sshfortress/internal/store"
	apptheme "sshfortress/tui/theme"

	tea "github.com/charmbracelet/bubbletea"
)

// BackupState holds the backup list.
type BackupState struct {
	Backups []store.BackupRecord
	Cursor  int
	Message string
}

func loadBackups(a *App) BackupState {
	bs, _ := a.backupEng.ListBackups()
	return BackupState{Backups: bs}
}

func (a *App) renderBackup(w int) string {
	var b strings.Builder
	b.WriteString(title("💾 BACKUP & RESTORE"))
	b.WriteString(apptheme.StyleDim.Render("  Backups include users, limits, traffic and password hashes — restore on a new\n  server and every account works with its old password.") + "\n\n")
	widths := []int{18, 7, 10, 50}
	b.WriteString(header(widths, "DATE", "USERS", "SIZE", "FILE"))
	if len(a.bk.Backups) == 0 {
		b.WriteString(apptheme.StyleDim.Render("\n  no backups yet — press c"))
	}
	for i, x := range a.bk.Backups {
		line := row(widths, x.CreatedAt.Format("2006-01-02 15:04"), fmt.Sprint(x.UserCount), card.FormatBytes(x.SizeBytes), x.FilePath)
		if i == a.bk.Cursor && !a.focusSidebar {
			line = apptheme.StyleTableRowSelected.Width(w - 2).Render(stripANSI(line))
		}
		b.WriteString(line + "\n")
	}
	b.WriteString(keyHelp("c create   enter restore   d delete   (CLI: sshfortress backup restore FILE)"))
	return b.String()
}

func (a *App) updateBackup(msg tea.KeyMsg) tea.Cmd {
	s := &a.bk
	switch msg.String() {
	case "up", "k":
		if s.Cursor > 0 {
			s.Cursor--
		}
	case "down", "j":
		if s.Cursor < len(s.Backups)-1 {
			s.Cursor++
		}
	case "c":
		return a.op(func() (string, error) {
			p, err := a.backupEng.Create()
			return "backup written: " + p, err
		})
	case "enter":
		if len(s.Backups) == 0 {
			return nil
		}
		x := s.Backups[s.Cursor]
		a.modal = confirmModal("Restore", fmt.Sprintf("Restore %d users from %s?", x.UserCount, x.CreatedAt.Format("2006-01-02 15:04")), func() tea.Cmd {
			return a.op(func() (string, error) {
				r, err := a.backupEng.Restore(x.FilePath)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("restored %d users (%d accounts created)", r.Users, len(r.Created)), nil
			})
		})
	case "d", "delete":
		if len(s.Backups) == 0 {
			return nil
		}
		x := s.Backups[s.Cursor]
		a.modal = confirmModal("Delete backup", "Delete "+x.FilePath+"?", func() tea.Cmd {
			return a.op(func() (string, error) { return "backup deleted", a.backupEng.DeleteBackup(x.ID) })
		})
	}
	return nil
}
