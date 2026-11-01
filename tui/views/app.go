// Package views contains the SSH-VPN terminal UI.
package views

import (
	"fmt"
	"strings"
	"time"

	"sshfortress/internal/backup"
	"sshfortress/internal/buildinfo"
	"sshfortress/internal/config"
	"sshfortress/internal/store"
	"sshfortress/internal/user"
	apptheme "sshfortress/tui/theme"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// View identifiers.
const (
	ViewDashboard = iota
	ViewUsers
	ViewOnline
	ViewNetwork
	ViewOptimizer
	ViewDoctor
	ViewBackup
	ViewSettings
)

var menuItems = []struct{ icon, label string }{
	{"📊", "Dashboard"},
	{"👤", "Users"},
	{"📡", "Online"},
	{"🌐", "Gateway & UDP"},
	{"⚡", "Optimizer"},
	{"🩺", "Doctor"},
	{"💾", "Backup"},
	{"⚙️", "Settings"},
}

// App is the root Bubbletea model: sidebar + routed content + modal.
type App struct {
	cfg       *config.Config
	db        *store.DB
	users     *user.Manager
	backupEng *backup.Engine

	width, height int

	activeView   int
	menuCursor   int
	focusSidebar bool

	dash   DashboardState
	us     UsersState
	online OnlineState
	net    NetworkState
	opt    OptimizerState
	doc    DoctorState
	bk     BackupState

	modal *modal

	notification string
	notifyStyle  lipgloss.Style
	notifyAt     time.Time
}

// NewApp creates the root model.
func NewApp(cfg *config.Config, db *store.DB) *App {
	return &App{
		cfg:          cfg,
		db:           db,
		users:        user.NewManager(cfg, db),
		backupEng:    backup.NewEngine(cfg, db),
		focusSidebar: true,
		notifyStyle:  apptheme.StyleSuccess,
	}
}

// ─── messages ──────────────────────────────────────────────────────────────

type tickMsg time.Time

type viewDataMsg struct {
	view int
	data any
}

// opDoneMsg reports the result of an action.
type opDoneMsg struct {
	text   string
	err    error
	reload bool
	card   string // shown in a modal (e.g. a new user's connection card)
}

func (a *App) tick() tea.Cmd {
	d := time.Duration(a.cfg.Monitor.RefreshIntervalSecs) * time.Second
	if d < time.Second {
		d = 2 * time.Second
	}
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (a *App) notify(text string, err error) {
	if err != nil {
		a.notification = "✗ " + err.Error()
		a.notifyStyle = apptheme.StyleDanger
	} else {
		a.notification = "✓ " + text
		a.notifyStyle = apptheme.StyleSuccess
	}
	a.notifyAt = time.Now()
}

// Init implements tea.Model.
func (a *App) Init() tea.Cmd {
	return tea.Batch(tea.SetWindowTitle("SSH-VPN"), a.load(ViewDashboard), a.tick())
}

// load fetches a view's data in the background.
func (a *App) load(view int) tea.Cmd {
	return func() tea.Msg {
		var data any
		switch view {
		case ViewDashboard:
			data = loadDashboard(a)
		case ViewUsers:
			data = loadUsers(a)
		case ViewOnline:
			data = loadOnline(a)
		case ViewNetwork:
			data = loadNetwork(a)
		case ViewOptimizer:
			data = loadOptimizer(a)
		case ViewDoctor:
			data = loadDoctor(a)
		case ViewBackup:
			data = loadBackups(a)
		default:
			return nil
		}
		return viewDataMsg{view: view, data: data}
	}
}

// Update implements tea.Model.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		return a, nil

	case tickMsg:
		if !a.notifyAt.IsZero() && time.Since(a.notifyAt) > 8*time.Second {
			a.notification = ""
		}
		var cmd tea.Cmd
		switch a.activeView { // live views
		case ViewDashboard, ViewUsers, ViewOnline, ViewNetwork:
			if a.modal == nil {
				cmd = a.load(a.activeView)
			}
		}
		return a, tea.Batch(cmd, a.tick())

	case viewDataMsg:
		a.applyData(msg)
		return a, nil

	case opDoneMsg:
		if msg.err != nil || msg.text != "" {
			a.notify(msg.text, msg.err)
		}
		if msg.card != "" {
			a.modal = textModal("Connection card — copy and send to the user", msg.card)
		}
		if msg.reload {
			return a, a.load(a.activeView)
		}
		return a, nil

	case tea.KeyMsg:
		if a.modal != nil {
			return a, a.modal.update(a, msg)
		}
		switch msg.String() {
		case "ctrl+c":
			return a, tea.Quit
		case "q":
			if a.focusSidebar {
				return a, tea.Quit
			}
			a.focusSidebar = true
			return a, nil
		case "esc":
			a.focusSidebar = true
			return a, nil
		case "tab":
			a.focusSidebar = !a.focusSidebar
			return a, nil
		case "r", "f5":
			return a, a.load(a.activeView)
		}
		if a.focusSidebar {
			switch msg.String() {
			case "up", "k":
				if a.menuCursor > 0 {
					a.menuCursor--
				}
			case "down", "j":
				if a.menuCursor < len(menuItems)-1 {
					a.menuCursor++
				}
			case "enter", "right", "l":
				a.activeView = a.menuCursor
				a.focusSidebar = false
				return a, a.load(a.activeView)
			default:
				// number shortcuts 1-8
				if s := msg.String(); len(s) == 1 && s[0] >= '1' && int(s[0]-'1') < len(menuItems) {
					a.menuCursor = int(s[0] - '1')
					a.activeView = a.menuCursor
					a.focusSidebar = false
					return a, a.load(a.activeView)
				}
			}
			return a, nil
		}
		return a, a.updateActiveView(msg)
	}
	return a, nil
}

func (a *App) applyData(m viewDataMsg) {
	switch d := m.data.(type) {
	case DashboardState:
		a.dash = d
	case usersData:
		a.us.apply(d)
	case OnlineState:
		cur := a.online.Cursor
		a.online = d
		a.online.Cursor = clamp(cur, 0, len(d.Sessions)-1)
	case NetworkState:
		a.net = d
	case OptimizerState:
		msg := a.opt.Message
		a.opt = d
		a.opt.Message = msg
	case DoctorState:
		a.doc = d
	case BackupState:
		cur, msg := a.bk.Cursor, a.bk.Message
		a.bk = d
		a.bk.Cursor, a.bk.Message = clamp(cur, 0, len(d.Backups)-1), msg
	}
}

func (a *App) updateActiveView(msg tea.KeyMsg) tea.Cmd {
	switch a.activeView {
	case ViewUsers:
		return a.updateUsers(msg)
	case ViewOnline:
		return a.updateOnline(msg)
	case ViewOptimizer:
		return a.updateOptimizer(msg)
	case ViewDoctor:
		return a.updateDoctor(msg)
	case ViewBackup:
		return a.updateBackup(msg)
	}
	return nil
}

// View implements tea.Model.
func (a *App) View() string {
	if a.width == 0 {
		return "Loading..."
	}
	sidebarWidth := 26
	contentWidth := a.width - sidebarWidth - 3
	if contentWidth < 40 {
		contentWidth = 40
	}
	sidebar := lipgloss.NewStyle().Width(sidebarWidth).Height(a.height - 2).Render(a.renderSidebar())
	content := a.renderContent(contentWidth)
	if a.modal != nil {
		content = a.modal.view(contentWidth)
	}
	contentBox := lipgloss.NewStyle().Width(contentWidth).Height(a.height - 2).MaxHeight(a.height - 2).Render(content)
	main := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, " ", contentBox)
	return lipgloss.JoinVertical(lipgloss.Left, main, a.renderStatusBar())
}

func (a *App) renderSidebar() string {
	var b strings.Builder
	b.WriteString(apptheme.StyleLogo.Render("  ╔══════════════════╗\n  ║     SSH-VPN      ║\n  ╚══════════════════╝"))
	b.WriteString("\n")
	b.WriteString(apptheme.StyleDim.Render("  v" + buildinfo.Version))
	b.WriteString("\n\n")
	for i, it := range menuItems {
		label := fmt.Sprintf(" %d %s  %s", i+1, it.icon, it.label)
		switch {
		case i == a.menuCursor && a.focusSidebar:
			b.WriteString(apptheme.StyleMenuItemActive.Width(24).Render(label))
		case i == a.activeView:
			b.WriteString(lipgloss.NewStyle().Foreground(apptheme.ColorPrimary).Bold(true).PaddingLeft(1).Render(apptheme.ArrowRight + label))
		default:
			b.WriteString(apptheme.StyleMenuItem.Render(label))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(apptheme.StyleDim.Render("  ──────────────────────\n  tab  switch focus\n  1-8  jump to view\n  r    refresh\n  q    back / quit"))
	return b.String()
}

func (a *App) renderContent(w int) string {
	switch a.activeView {
	case ViewDashboard:
		return a.renderDashboard(w)
	case ViewUsers:
		return a.renderUsers(w)
	case ViewOnline:
		return a.renderOnline(w)
	case ViewNetwork:
		return a.renderNetwork(w)
	case ViewOptimizer:
		return a.renderOptimizer(w)
	case ViewDoctor:
		return a.renderDoctor(w)
	case ViewBackup:
		return a.renderBackup(w)
	case ViewSettings:
		return a.renderSettings(w)
	}
	return ""
}

func (a *App) renderStatusBar() string {
	left := apptheme.StyleKeyBind.Render(" SSH-VPN ")
	help := apptheme.StyleKeyHelp.Render(" tab:focus  ↑↓:move  enter:select  r:refresh  q:quit ")
	right := ""
	if a.notification != "" {
		right = a.notifyStyle.Render(" " + a.notification)
	}
	return lipgloss.NewStyle().Width(a.width).Background(apptheme.ColorBgAlt).Render(left + help + right)
}

// ─── shared helpers ────────────────────────────────────────────────────────

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// pad pads/truncates s (which may contain ANSI styles) to width w.
func pad(s string, w int) string {
	sw := lipgloss.Width(s)
	if sw > w {
		r := []rune(stripANSI(s))
		if len(r) > w {
			if w > 1 {
				return string(r[:w-1]) + "…"
			}
			return string(r[:w])
		}
		return string(r)
	}
	return s + strings.Repeat(" ", w-sw)
}

func stripANSI(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			in = true
		case in && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'):
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// row joins cells padded to the given widths.
func row(widths []int, cells ...string) string {
	var b strings.Builder
	b.WriteString("  ")
	for i, c := range cells {
		if i < len(widths) {
			b.WriteString(pad(c, widths[i]))
			b.WriteString(" ")
		} else {
			b.WriteString(c)
		}
	}
	return b.String()
}

func header(widths []int, cells ...string) string {
	st := lipgloss.NewStyle().Bold(true).Foreground(apptheme.ColorPrimary)
	styled := make([]string, len(cells))
	for i, c := range cells {
		styled[i] = st.Render(c)
	}
	total := 0
	for _, w := range widths {
		total += w + 1
	}
	return row(widths, styled...) + "\n" + apptheme.StyleDim.Render("  "+strings.Repeat("─", total)) + "\n"
}

func title(s string) string { return apptheme.StyleTitle.Render(" "+s+" ") + "\n\n" }

func keyHelp(s string) string { return "\n" + apptheme.StyleKeyHelp.Render("  "+s) }

type cardRow struct{ Label, Value string }

// cardWidth is the width to pass to renderCard so that two cards plus their
// borders and the 2-column gap fit exactly in w.
func cardWidth(w int) int {
	cw := (w-2)/2 - 2
	if cw < 36 {
		cw = 36
	}
	return cw
}

// twoColumns reports whether two cards fit side by side.
func twoColumns(w int) bool { return w >= 2*(36+2)+2 }

func (a *App) renderCard(cardTitle string, rows []cardRow, width int) string {
	var c strings.Builder
	c.WriteString(apptheme.StyleSubtitle.Render(cardTitle))
	c.WriteString("\n")
	for _, r := range rows {
		c.WriteString(apptheme.StyleLabel.Render(r.Label) + " " + apptheme.StyleValue.Render(r.Value) + "\n")
	}
	return apptheme.StylePanel.Width(width).Render(strings.TrimRight(c.String(), "\n"))
}

func renderProgressBar(percent float64, width int) string {
	if percent > 100 {
		percent = 100
	}
	if percent < 0 {
		percent = 0
	}
	filled := int(percent / 100 * float64(width))
	col := apptheme.ColorSuccess
	switch {
	case percent >= 90:
		col = apptheme.ColorDanger
	case percent >= 70:
		col = apptheme.ColorWarning
	}
	return lipgloss.NewStyle().Foreground(col).Render(strings.Repeat("█", filled)) +
		lipgloss.NewStyle().Foreground(apptheme.ColorFgMuted).Render(strings.Repeat("░", width-filled))
}

func ok(b bool, yes, no string) string {
	if b {
		return apptheme.StyleSuccess.Render(apptheme.CheckMark + " " + yes)
	}
	return apptheme.StyleDanger.Render(apptheme.CrossMark + " " + no)
}
