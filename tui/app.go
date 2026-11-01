// Package tui provides the Bubbletea terminal user interface for SSH-VPN.
package tui

import (
	"sshfortress/internal/config"
	"sshfortress/internal/store"
	"sshfortress/tui/views"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Run starts the TUI application.
func Run(cfg *config.Config, db *store.DB) error {
	// The theme is dark: say so, so styles never re-query the terminal for
	// its background colour while the program owns the input.
	lipgloss.SetHasDarkBackground(true)
	m := views.NewApp(cfg, db)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}
