package theme

import tea "github.com/charmbracelet/bubbletea"

// Key constants for navigation.
const (
	KeyQuit       = "q"
	KeyBack       = "esc"
	KeyEnter      = "enter"
	KeyUp         = "up"
	KeyDown       = "down"
	KeyLeft       = "left"
	KeyRight      = "right"
	KeyTab        = "tab"
	KeyShiftTab   = "shift+tab"
	KeyHelp       = "?"
	KeyRefresh    = "r"
	KeyNew        = "n"
	KeyDelete     = "d"
	KeyEdit       = "e"
	KeyToggle     = "t"
	KeySpace      = " "
)

// KeyMap defines the global help text shown in the status bar.
type KeyMap struct {
	Quit    string
	Back    string
	Nav     string
	Select  string
	Help    string
	Refresh string
}

// DefaultKeyMap returns the default key mappings.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Quit:    "q: quit",
		Back:    "esc: back",
		Nav:     "↑↓: navigate",
		Select:  "enter: select",
		Help:    "?: help",
		Refresh: "r: refresh",
	}
}

// IsQuit returns true if the message is a quit key.
func IsQuit(msg tea.KeyMsg) bool {
	return msg.String() == KeyQuit
}

