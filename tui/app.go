// Package tui provides the Bubbletea terminal user interface for SSH Fortress.
package tui

import (
	"sshfortress/internal/config"
	"sshfortress/internal/store"
	"sshfortress/tui/views"

	tea "github.com/charmbracelet/bubbletea"
)

// Run starts the TUI application.
func Run(cfg *config.Config, db *store.DB) error {
	m := views.NewApp(cfg, db)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}
