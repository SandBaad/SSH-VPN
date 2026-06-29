// Package views contains all TUI views for SSH Fortress.
package views

import (
	"fmt"
	"strings"

	"sshfortress/internal/backup"
	"sshfortress/internal/config"
	"sshfortress/internal/store"
	"sshfortress/internal/system"
	"sshfortress/internal/tunnel"
	"sshfortress/internal/user"
	apptheme "sshfortress/tui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// View identifiers.
const (
	ViewDashboard = iota
	ViewUsers
	ViewTunnels
	ViewBadVPN
	ViewMonitor
	ViewOptimizer
	ViewBackup
	ViewSettings
)

var menuItems = []string{
	"  Dashboard",
	"  Users",
	"  SSH Tunnels",
	"  BadVPN",
	"  Monitor",
	"  Optimizer",
	"  Backup",
	"  Settings",
}

var menuIcons = []string{
	"📊", "👤", "🔐", "🌐", "📡", "⚡", "💾", "⚙️",
}

// App is the root Bubbletea model with sidebar + content routing.
type App struct {
	cfg    *config.Config
	db     *store.DB
	width  int
	height int

	// Navigation
	activeView  int
	menuCursor  int
	focusSidebar bool

	// Sub-managers
	userMgr   *user.Manager
	sshMgr    *tunnel.SSHManager
	badvpnMgr *tunnel.BadVPNManager
	optimizer *system.Optimizer
	backupEng *backup.Engine

	// View states
	dashState    DashboardState
	usersState   UsersState
	tunnelState  TunnelState
	badvpnState  BadVPNState
	monitorState MonitorState
	optState     OptimizerState
	backupState  BackupState

	// Notifications
	notification string
	notifyStyle  lipgloss.Style
}

// NewApp creates the root application model.
func NewApp(cfg *config.Config, db *store.DB) *App {
	return &App{
		cfg:          cfg,
		db:           db,
		activeView:   ViewDashboard,
		menuCursor:   0,
		focusSidebar: true,
		userMgr:      user.NewManager(cfg, db),
		sshMgr:       tunnel.NewSSHManager(cfg),
		badvpnMgr:    tunnel.NewBadVPNManager(cfg),
		optimizer:    system.NewOptimizer(cfg),
		backupEng:    backup.NewEngine(cfg, db),
		notifyStyle:  apptheme.StyleSuccess,
	}
}

// Init implements tea.Model.
func (a *App) Init() tea.Cmd {
	return tea.Batch(tea.SetWindowTitle("SSH-VPN"), a.refreshData())
}

// Update implements tea.Model.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		return a, nil

	case tea.KeyMsg:
		// Global keys.
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
		case "r":
			return a, a.refreshData()
		}

		// Sidebar navigation.
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
			case "enter":
				a.activeView = a.menuCursor
				a.focusSidebar = false
				return a, a.refreshData()
			}
			return a, nil
		}

		// Delegate to active view.
		return a.updateActiveView(msg)

	case refreshMsg:
		a.loadViewData()
		return a, nil

	case notifyMsg:
		a.notification = msg.text
		a.notifyStyle = msg.style
		return a, nil
	}
	return a, nil
}

// View implements tea.Model.
func (a *App) View() string {
	if a.width == 0 {
		return "Loading..."
	}

	sidebar := a.renderSidebar()
	content := a.renderContent()

	// Layout: sidebar (24 cols) | content (rest)
	sidebarWidth := 26
	contentWidth := a.width - sidebarWidth - 3

	sidebarBox := lipgloss.NewStyle().
		Width(sidebarWidth).
		Height(a.height - 2).
		Render(sidebar)

	contentBox := lipgloss.NewStyle().
		Width(contentWidth).
		Height(a.height - 2).
		Render(content)

	main := lipgloss.JoinHorizontal(lipgloss.Top, sidebarBox, " ", contentBox)

	// Status bar.
	statusBar := a.renderStatusBar()

	return lipgloss.JoinVertical(lipgloss.Left, main, statusBar)
}

func (a *App) renderSidebar() string {
	var b strings.Builder

	// Logo.
	logo := apptheme.StyleLogo.Render("  ╔═══════════════════╗\n  ║     SSH-VPN        ║\n  ╚═══════════════════╝")
	b.WriteString(logo)
	b.WriteString("\n\n")

	// Version.
	b.WriteString(apptheme.StyleDim.Render("  v1.0.0"))
	b.WriteString("\n")
	b.WriteString(apptheme.StyleDim.Render("  ─────────────────────"))
	b.WriteString("\n\n")

	// Menu items.
	for i, item := range menuItems {
		icon := menuIcons[i]
		label := fmt.Sprintf(" %s %s", icon, item)

		if i == a.menuCursor {
			if a.focusSidebar {
				b.WriteString(apptheme.StyleMenuItemActive.Render(label))
			} else {
				// Selected but sidebar not focused — dimmed highlight.
				style := lipgloss.NewStyle().
					Foreground(apptheme.ColorPrimary).
					Bold(true).
					PaddingLeft(1)
				b.WriteString(style.Render(apptheme.ArrowRight + label))
			}
		} else {
			b.WriteString(apptheme.StyleMenuItem.Render(label))
		}
		b.WriteString("\n")
	}

	// Footer in sidebar.
	b.WriteString("\n")
	b.WriteString(apptheme.StyleDim.Render("  ─────────────────────"))
	b.WriteString("\n")
	b.WriteString(apptheme.StyleDim.Render("  tab: switch focus"))
	b.WriteString("\n")
	b.WriteString(apptheme.StyleDim.Render("  q: quit"))

	return b.String()
}

func (a *App) renderContent() string {
	switch a.activeView {
	case ViewDashboard:
		return a.renderDashboard()
	case ViewUsers:
		return a.renderUsers()
	case ViewTunnels:
		return a.renderTunnels()
	case ViewBadVPN:
		return a.renderBadVPN()
	case ViewMonitor:
		return a.renderMonitor()
	case ViewOptimizer:
		return a.renderOptimizer()
	case ViewBackup:
		return a.renderBackup()
	case ViewSettings:
		return a.renderSettings()
	default:
		return "Unknown view"
	}
}

func (a *App) renderStatusBar() string {
	left := apptheme.StyleKeyBind.Render(" SSH-VPN")
	right := ""
	if a.notification != "" {
		right = a.notifyStyle.Render(a.notification)
	}

	help := apptheme.StyleKeyHelp.Render("  tab:focus  ↑↓:nav  enter:select  r:refresh  q:quit")

	bar := lipgloss.NewStyle().
		Width(a.width).
		Background(apptheme.ColorBgAlt).
		Padding(0, 1).
		Render(left + "  " + help + "  " + right)

	return bar
}

// ─── Messages ───────────────────────────────────────────────────────────────

type refreshMsg struct{}
type notifyMsg struct {
	text  string
	style lipgloss.Style
}

func (a *App) refreshData() tea.Cmd {
	return func() tea.Msg {
		return refreshMsg{}
	}
}

func (a *App) loadViewData() {
	switch a.activeView {
	case ViewDashboard:
		a.dashState.SysInfo = system.GetInfo()
		a.dashState.SSHStatus = a.sshMgr.GetStatus()
		a.dashState.BadVPNStatus = a.badvpnMgr.GetStatus()
		users, _ := a.db.ListUsers()
		a.dashState.UserCount = len(users)
		sessions := a.userMgr.GetActiveSessions()
		a.dashState.ActiveSessions = len(sessions)

	case ViewUsers:
		a.usersState.Users, _ = a.userMgr.ListUsers()

	case ViewTunnels:
		a.tunnelState.Status = a.sshMgr.GetStatus()
		a.tunnelState.ConfiguredPorts, _ = a.sshMgr.GetConfiguredPorts()

	case ViewBadVPN:
		a.badvpnState.Status = a.badvpnMgr.GetStatus()

	case ViewMonitor:
		a.monitorState.Sessions = a.userMgr.GetActiveSessions()

	case ViewOptimizer:
		a.optState.Params = a.optimizer.GetRecommendedParams()

	case ViewBackup:
		a.backupState.Backups, _ = a.backupEng.ListBackups()
	}
}

func (a *App) updateActiveView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch a.activeView {
	case ViewUsers:
		return a.updateUsers(msg)
	case ViewOptimizer:
		return a.updateOptimizer(msg)
	case ViewBadVPN:
		return a.updateBadVPN(msg)
	case ViewBackup:
		return a.updateBackup(msg)
	}
	return a, nil
}
