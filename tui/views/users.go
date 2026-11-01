package views

import (
	"fmt"
	"strconv"
	"strings"

	"sshfortress/internal/card"
	"sshfortress/internal/config"
	"sshfortress/internal/store"
	"sshfortress/internal/user"
	apptheme "sshfortress/tui/theme"

	tea "github.com/charmbracelet/bubbletea"
)

// UsersState holds data for the users view.
type UsersState struct {
	Users  []user.UserInfo
	Cursor int
	Err    error
}

type usersData struct {
	users []user.UserInfo
	err   error
}

func loadUsers(a *App) usersData {
	us, err := a.users.List()
	return usersData{us, err}
}

func (s *UsersState) apply(d usersData) {
	s.Users, s.Err = d.users, d.err
	s.Cursor = clamp(s.Cursor, 0, len(s.Users)-1)
}

func (s *UsersState) selected() *user.UserInfo {
	if len(s.Users) == 0 {
		return nil
	}
	return &s.Users[clamp(s.Cursor, 0, len(s.Users)-1)]
}

func statusStyled(st string) string {
	switch st {
	case user.StatusActive:
		return apptheme.StyleSuccess.Render("● active")
	case user.StatusExpired:
		return apptheme.StyleDanger.Render("● expired")
	case user.StatusQuota:
		return apptheme.StyleDanger.Render("● quota")
	default:
		return apptheme.StyleWarning.Render("● locked")
	}
}

func (a *App) renderUsers(w int) string {
	var b strings.Builder
	b.WriteString(title("👤 USERS"))
	s := a.us
	if s.Err != nil {
		b.WriteString(apptheme.StyleDanger.Render("  " + s.Err.Error()))
		return b.String()
	}
	widths := []int{14, 10, 7, 16, 26, 12}
	b.WriteString(header(widths, "USER", "STATUS", "ONLINE", "EXPIRES", "TRAFFIC", "NOTE"))
	if len(s.Users) == 0 {
		b.WriteString(apptheme.StyleDim.Render("\n  No users yet — press n to create one."))
	}
	for i, u := range s.Users {
		online := fmt.Sprintf("%d/%d", u.ActiveSessions, u.MaxConnections)
		if u.ActiveSessions > 0 {
			online = apptheme.StyleInfo.Render(online)
		}
		exp := fmt.Sprintf("%s %3dd", u.ExpiresAt.Format("2006-01-02"), u.DaysLeft)
		if u.DaysLeft <= 3 && u.Status == user.StatusActive {
			exp = apptheme.StyleWarning.Render(exp)
		}
		traffic := card.Traffic(&u.UserRecord)
		line := row(widths, u.Username, statusStyled(u.Status), online, exp, traffic, u.Notes)
		if i == s.Cursor && !a.focusSidebar {
			line = apptheme.StyleTableRowSelected.Width(w - 2).Render(stripANSI(line))
		}
		b.WriteString(line + "\n")
	}
	b.WriteString(fmt.Sprintf("\n  %d users", len(s.Users)))
	b.WriteString(keyHelp("n new  enter card  e renew  p password  c devices  t traffic  l lock/unlock  x kick  d delete"))
	return b.String()
}

func (a *App) op(f func() (string, error)) tea.Cmd {
	return func() tea.Msg {
		text, err := f()
		return opDoneMsg{text: text, err: err, reload: true}
	}
}

func (a *App) updateUsers(msg tea.KeyMsg) tea.Cmd {
	s := &a.us
	switch msg.String() {
	case "up", "k":
		if s.Cursor > 0 {
			s.Cursor--
		}
		return nil
	case "down", "j":
		if s.Cursor < len(s.Users)-1 {
			s.Cursor++
		}
		return nil
	case "n", "a":
		d := a.cfg.UserDefaults
		a.modal = formModal("New user", []field{
			{label: "Username", hint: "letters, digits, - _"},
			{label: "Password", hint: "empty = random"},
			{label: "Days", value: strconv.Itoa(d.DefaultExpirationDays)},
			{label: "Devices", value: strconv.Itoa(d.DefaultMaxConnections), hint: "simultaneous connections"},
			{label: "Traffic (GB)", value: strconv.FormatFloat(d.DefaultTrafficGB, 'f', -1, 64), hint: "0 = unlimited"},
			{label: "Note"},
		}, func(v []string) tea.Cmd {
			return func() tea.Msg {
				days, _ := strconv.Atoi(v[2])
				conns, _ := strconv.Atoi(v[3])
				gb, _ := strconv.ParseFloat(v[4], 64)
				if gb == 0 {
					gb = -1
				}
				rec, pw, err := a.users.Create(user.CreateUserOpts{Username: v[0], Password: v[1],
					ExpirationDays: days, MaxConnections: conns, TrafficGB: gb, Notes: v[5]})
				if err != nil {
					return opDoneMsg{err: err}
				}
				return opDoneMsg{text: "created " + rec.Username, reload: true, card: card.Render(a.cfg, rec, pw)}
			}
		})
		return nil
	}

	u := s.selected()
	if u == nil {
		return nil
	}
	name := u.Username
	switch msg.String() {
	case "enter", "i":
		a.modal = textModal("Connection card — "+name, card.Render(a.cfg, &u.UserRecord, ""))
	case "d", "delete":
		a.modal = confirmModal("Delete user", fmt.Sprintf("Delete %s and disconnect them?", name), func() tea.Cmd {
			return a.op(func() (string, error) { return "deleted " + name, a.users.Delete(name) })
		})
	case "e":
		a.modal = formModal("Renew "+name, []field{{label: "Add days", value: "30"}, {label: "Reset traffic", value: "no", hint: "yes/no"}},
			func(v []string) tea.Cmd {
				return a.op(func() (string, error) {
					days, err := strconv.Atoi(v[0])
					if err != nil {
						return "", fmt.Errorf("days must be a number")
					}
					rec, err := a.users.Renew(name, days)
					if err != nil {
						return "", err
					}
					if strings.HasPrefix(strings.ToLower(v[1]), "y") {
						if _, err := a.users.ResetTraffic(name); err != nil {
							return "", err
						}
					}
					return fmt.Sprintf("%s expires %s", name, rec.ExpiresAt.Format("2006-01-02")), nil
				})
			})
	case "p":
		a.modal = formModal("New password for "+name, []field{{label: "Password", hint: "empty = random"}}, func(v []string) tea.Cmd {
			return func() tea.Msg {
				pw, err := a.users.SetPassword(name, v[0])
				if err != nil {
					return opDoneMsg{err: err}
				}
				rec, _ := a.db.GetUser(name)
				return opDoneMsg{text: "password changed", reload: true, card: card.Render(a.cfg, rec, pw)}
			}
		})
	case "c":
		a.modal = formModal("Devices for "+name, []field{{label: "Max devices", value: strconv.Itoa(u.MaxConnections)}}, func(v []string) tea.Cmd {
			return a.op(func() (string, error) {
				n, err := strconv.Atoi(v[0])
				if err != nil {
					return "", fmt.Errorf("enter a number")
				}
				return fmt.Sprintf("%s: %d devices", name, n), a.users.SetMaxConnections(name, n)
			})
		})
	case "t":
		cur := "0"
		if u.TrafficLimit > 0 {
			cur = strconv.FormatFloat(float64(u.TrafficLimit)/(1<<30), 'f', -1, 64)
		}
		a.modal = formModal("Traffic for "+name+" — used "+card.FormatBytes(u.TrafficUsed), []field{
			{label: "Quota (GB)", value: cur, hint: "0 = unlimited"},
			{label: "Reset usage", value: "no", hint: "yes/no"},
		}, func(v []string) tea.Cmd {
			return a.op(func() (string, error) {
				gb, err := strconv.ParseFloat(v[0], 64)
				if err != nil {
					return "", fmt.Errorf("quota must be a number")
				}
				if _, err := a.users.SetTrafficLimit(name, config.GB(gb)); err != nil {
					return "", err
				}
				if strings.HasPrefix(strings.ToLower(v[1]), "y") {
					if _, err := a.users.ResetTraffic(name); err != nil {
						return "", err
					}
				}
				return "traffic updated for " + name, nil
			})
		})
	case "l":
		if u.Disabled {
			return a.op(func() (string, error) { return "unlocked " + name, a.users.Unlock(name) })
		}
		a.modal = confirmModal("Lock user", fmt.Sprintf("Lock %s and disconnect them?", name), func() tea.Cmd {
			return a.op(func() (string, error) { return "locked " + name, a.users.Lock(name, store.LockManual) })
		})
	case "x":
		return a.op(func() (string, error) {
			n, err := a.users.Kick(name)
			return fmt.Sprintf("disconnected %d session(s) of %s", n, name), err
		})
	}
	return nil
}
