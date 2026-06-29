package views

import (
	"fmt"
	"strings"
	"time"

	"sshfortress/internal/user"
	apptheme "sshfortress/tui/theme"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// UsersState holds data for the users view.
type UsersState struct {
	Users    []user.UserInfo
	Cursor   int
	Mode     string // "list", "create", "delete-confirm"
	InputBuf [4]string // username, password, maxconn, expdays
	InputIdx int
	Message  string
}

func (a *App) renderUsers() string {
	var b strings.Builder

	title := apptheme.StyleTitle.Render(" 👤 USER MANAGEMENT ")
	b.WriteString(title)
	b.WriteString("\n\n")

	if a.usersState.Mode == "create" {
		return b.String() + a.renderUserCreateForm()
	}

	if a.usersState.Message != "" {
		b.WriteString(apptheme.StyleSuccess.Render("  " + a.usersState.Message))
		b.WriteString("\n\n")
	}

	// Table header.
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(apptheme.ColorPrimary)
	header := fmt.Sprintf("  %-18s %-8s %-6s %-14s %-10s %-8s",
		headerStyle.Render("USERNAME"),
		headerStyle.Render("CONNS"),
		headerStyle.Render("MAX"),
		headerStyle.Render("EXPIRES"),
		headerStyle.Render("STATUS"),
		headerStyle.Render("NOTES"),
	)
	b.WriteString(header)
	b.WriteString("\n")
	b.WriteString(apptheme.StyleDim.Render("  " + strings.Repeat("─", 70)))
	b.WriteString("\n")

	if len(a.usersState.Users) == 0 {
		b.WriteString(apptheme.StyleDim.Render("\n  No users found. Press 'n' to create a new user."))
	}

	for i, u := range a.usersState.Users {
		statusIcon := apptheme.StyleSuccess.Render(apptheme.CheckMark)
		statusText := apptheme.StyleSuccess.Render("Active")
		if u.IsExpired {
			statusIcon = apptheme.StyleDanger.Render(apptheme.CrossMark)
			statusText = apptheme.StyleDanger.Render("Expired")
		} else if u.Disabled {
			statusIcon = apptheme.StyleWarning.Render("○")
			statusText = apptheme.StyleWarning.Render("Disabled")
		}

		daysLeft := int(time.Until(u.ExpiresAt).Hours() / 24)
		expiresStr := u.ExpiresAt.Format("2006-01-02")
		if daysLeft <= 3 && !u.IsExpired {
			expiresStr = apptheme.StyleWarning.Render(expiresStr)
		}

		connColor := apptheme.ColorSuccess
		if u.ActiveSessions >= u.MaxConnections {
			connColor = apptheme.ColorDanger
		}

		row := fmt.Sprintf("  %-18s %s%-5d  %-6d %-14s %s %-8s %-8s",
			u.Username,
			lipgloss.NewStyle().Foreground(connColor).Render(""),
			u.ActiveSessions,
			u.MaxConnections,
			expiresStr,
			statusIcon,
			statusText,
			u.Notes,
		)

		if i == a.usersState.Cursor {
			b.WriteString(apptheme.StyleMenuItemActive.Width(74).Render(row))
		} else {
			b.WriteString(row)
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(apptheme.StyleDim.Render(fmt.Sprintf("  Total: %d users", len(a.usersState.Users))))
	b.WriteString("\n\n")
	b.WriteString(apptheme.StyleKeyHelp.Render("  n:new  d:delete  e:edit  ↑↓:select  esc:back"))

	return b.String()
}

func (a *App) renderUserCreateForm() string {
	var b strings.Builder

	b.WriteString(apptheme.StyleSubtitle.Render("  Create New User"))
	b.WriteString("\n\n")

	labels := []string{"Username:", "Password:", "Max Connections:", "Expiration (days):"}
	for i, label := range labels {
		prefix := "  "
		if i == a.usersState.InputIdx {
			prefix = apptheme.StyleKeyBind.Render("▸ ")
		}

		val := a.usersState.InputBuf[i]
		if i == 1 && val != "" {
			val = strings.Repeat("•", len(val))
		}

		b.WriteString(fmt.Sprintf("%s%-20s %s█\n", prefix, apptheme.StyleLabel.Render(label), val))
	}

	b.WriteString("\n")
	b.WriteString(apptheme.StyleKeyHelp.Render("  enter:confirm  tab:next field  esc:cancel"))
	return b.String()
}

func (a *App) updateUsers(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch a.usersState.Mode {
	case "create":
		return a.updateUserCreate(msg)
	default:
		return a.updateUserList(msg)
	}
}

func (a *App) updateUserList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if a.usersState.Cursor > 0 {
			a.usersState.Cursor--
		}
	case "down", "j":
		if a.usersState.Cursor < len(a.usersState.Users)-1 {
			a.usersState.Cursor++
		}
	case "n":
		a.usersState.Mode = "create"
		a.usersState.InputIdx = 0
		a.usersState.InputBuf = [4]string{"", "", "2", "30"}
	case "d":
		if len(a.usersState.Users) > 0 {
			u := a.usersState.Users[a.usersState.Cursor]
			if err := a.userMgr.DeleteUser(u.Username); err == nil {
				a.usersState.Message = fmt.Sprintf("User '%s' deleted", u.Username)
				a.usersState.Users, _ = a.userMgr.ListUsers()
				if a.usersState.Cursor >= len(a.usersState.Users) {
					a.usersState.Cursor = len(a.usersState.Users) - 1
				}
			}
		}
	}
	return a, nil
}

func (a *App) updateUserCreate(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		a.usersState.Mode = "list"
	case "tab":
		a.usersState.InputIdx = (a.usersState.InputIdx + 1) % 4
	case "shift+tab":
		a.usersState.InputIdx = (a.usersState.InputIdx + 3) % 4
	case "backspace":
		idx := a.usersState.InputIdx
		if len(a.usersState.InputBuf[idx]) > 0 {
			a.usersState.InputBuf[idx] = a.usersState.InputBuf[idx][:len(a.usersState.InputBuf[idx])-1]
		}
	case "enter":
		// Submit form.
		opts := user.CreateUserOpts{
			Username: a.usersState.InputBuf[0],
			Password: a.usersState.InputBuf[1],
		}
		fmt.Sscanf(a.usersState.InputBuf[2], "%d", &opts.MaxConnections)
		fmt.Sscanf(a.usersState.InputBuf[3], "%d", &opts.ExpirationDays)

		if err := a.userMgr.CreateUser(opts); err != nil {
			a.usersState.Message = "Error: " + err.Error()
		} else {
			a.usersState.Message = fmt.Sprintf("User '%s' created successfully!", opts.Username)
		}
		a.usersState.Mode = "list"
		a.usersState.Users, _ = a.userMgr.ListUsers()
	default:
		// Type character into current field.
		if len(msg.String()) == 1 {
			idx := a.usersState.InputIdx
			a.usersState.InputBuf[idx] += msg.String()
		}
	}
	return a, nil
}
