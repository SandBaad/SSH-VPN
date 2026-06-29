package tui

import "github.com/charmbracelet/lipgloss"

// ─── Color Palette ──────────────────────────────────────────────────────────
// A cohesive dark-theme palette with cyan/magenta/green accents.

var (
	// Primary colors
	ColorPrimary   = lipgloss.Color("#00D4AA") // Cyan-green accent
	ColorSecondary = lipgloss.Color("#BD93F9") // Soft purple
	ColorAccent    = lipgloss.Color("#FF79C6") // Magenta-pink
	ColorSuccess   = lipgloss.Color("#50FA7B") // Green
	ColorWarning   = lipgloss.Color("#F1FA8C") // Yellow
	ColorDanger    = lipgloss.Color("#FF5555") // Red
	ColorInfo      = lipgloss.Color("#8BE9FD") // Light cyan

	// Neutral colors
	ColorBg        = lipgloss.Color("#0D1117") // Deep dark background
	ColorBgAlt     = lipgloss.Color("#161B22") // Slightly lighter background
	ColorBgPanel   = lipgloss.Color("#1C2333") // Panel background
	ColorBorder    = lipgloss.Color("#30363D") // Border gray
	ColorFg        = lipgloss.Color("#E6EDF3") // Light text
	ColorFgDim     = lipgloss.Color("#8B949E") // Dimmed text
	ColorFgMuted   = lipgloss.Color("#484F58") // Very muted text
)

// ─── Reusable Styles ────────────────────────────────────────────────────────

var (
	// Title bar at the top of the application.
	StyleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(ColorPrimary).
			Padding(0, 2).
			MarginBottom(1)

	// Subtitle / section headers.
	StyleSubtitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorPrimary).
			MarginBottom(1)

	// Panel with border — used for dashboard cards.
	StylePanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorBorder).
			Padding(1, 2)

	// Active/selected panel (highlighted border).
	StylePanelActive = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(ColorPrimary).
				Padding(1, 2)

	// Status bar at the bottom.
	StyleStatusBar = lipgloss.NewStyle().
			Foreground(ColorFgDim).
			Background(ColorBgAlt).
			Padding(0, 1)

	// Menu item (normal).
	StyleMenuItem = lipgloss.NewStyle().
			Foreground(ColorFg).
			PaddingLeft(2)

	// Menu item (selected / highlighted).
	StyleMenuItemActive = lipgloss.NewStyle().
				Foreground(ColorBg).
				Background(ColorPrimary).
				Bold(true).
				PaddingLeft(2)

	// Key help text.
	StyleKeyHelp = lipgloss.NewStyle().
			Foreground(ColorFgMuted)

	// Key binding highlight.
	StyleKeyBind = lipgloss.NewStyle().
			Foreground(ColorPrimary).
			Bold(true)

	// Success text.
	StyleSuccess = lipgloss.NewStyle().
			Foreground(ColorSuccess)

	// Warning text.
	StyleWarning = lipgloss.NewStyle().
			Foreground(ColorWarning)

	// Error/danger text.
	StyleDanger = lipgloss.NewStyle().
			Foreground(ColorDanger)

	// Info text.
	StyleInfo = lipgloss.NewStyle().
			Foreground(ColorInfo)

	// Dimmed text.
	StyleDim = lipgloss.NewStyle().
			Foreground(ColorFgDim)

	// Label (left side of a key-value pair).
	StyleLabel = lipgloss.NewStyle().
			Foreground(ColorFgDim).
			Width(20)

	// Value (right side of a key-value pair).
	StyleValue = lipgloss.NewStyle().
			Foreground(ColorFg).
			Bold(true)

	// Logo / banner style.
	StyleLogo = lipgloss.NewStyle().
			Foreground(ColorPrimary).
			Bold(true)

	// Table header.
	StyleTableHeader = lipgloss.NewStyle().
				Bold(true).
				Foreground(ColorPrimary).
				BorderBottom(true).
				BorderStyle(lipgloss.NormalBorder()).
				BorderForeground(ColorBorder)

	// Table row.
	StyleTableRow = lipgloss.NewStyle().
			Foreground(ColorFg)

	// Table row (selected).
	StyleTableRowSelected = lipgloss.NewStyle().
				Foreground(ColorBg).
				Background(ColorPrimary).
				Bold(true)
)

// ─── Box Drawing Characters ─────────────────────────────────────────────────

const (
	BoxHorizontal = "─"
	BoxVertical   = "│"
	BoxTopLeft    = "╭"
	BoxTopRight   = "╮"
	BoxBottomLeft = "╰"
	BoxBottomRight = "╯"
	BoxCross      = "┼"
	BoxTeeRight   = "├"
	BoxTeeLeft    = "┤"

	BulletActive   = "●"
	BulletInactive = "○"
	ArrowRight     = "▸"
	CheckMark      = "✓"
	CrossMark      = "✗"
	Star           = "★"
)
