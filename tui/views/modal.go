package views

import (
	"strings"

	apptheme "sshfortress/tui/theme"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type field struct {
	label, value, hint string
	secret             bool
}

type modalKind int

const (
	modalConfirm modalKind = iota
	modalForm
	modalText
)

// modal is a confirm dialog, an input form or a text box.
type modal struct {
	kind     modalKind
	title    string
	text     string
	fields   []field
	idx      int
	onOK     func(values []string) tea.Cmd
	errorMsg string
}

func confirmModal(title, text string, onYes func() tea.Cmd) *modal {
	return &modal{kind: modalConfirm, title: title, text: text, onOK: func([]string) tea.Cmd { return onYes() }}
}

func formModal(title string, fields []field, onOK func(values []string) tea.Cmd) *modal {
	return &modal{kind: modalForm, title: title, fields: fields, onOK: onOK}
}

func textModal(title, text string) *modal {
	return &modal{kind: modalText, title: title, text: text}
}

func (m *modal) update(a *App, msg tea.KeyMsg) tea.Cmd {
	switch m.kind {
	case modalText:
		a.modal = nil
		return nil
	case modalConfirm:
		switch strings.ToLower(msg.String()) {
		case "y", "enter":
			a.modal = nil
			return m.onOK(nil)
		case "n", "esc", "q":
			a.modal = nil
		}
		return nil
	}
	// form
	switch msg.String() {
	case "esc":
		a.modal = nil
	case "tab", "down":
		m.idx = (m.idx + 1) % len(m.fields)
	case "shift+tab", "up":
		m.idx = (m.idx + len(m.fields) - 1) % len(m.fields)
	case "enter":
		vals := make([]string, len(m.fields))
		for i, f := range m.fields {
			vals[i] = strings.TrimSpace(f.value)
		}
		a.modal = nil
		return m.onOK(vals)
	case "backspace", "ctrl+h":
		v := []rune(m.fields[m.idx].value)
		if len(v) > 0 {
			m.fields[m.idx].value = string(v[:len(v)-1])
		}
	case "ctrl+u":
		m.fields[m.idx].value = ""
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			m.fields[m.idx].value += string(msg.Runes)
		}
	}
	return nil
}

func (m *modal) view(width int) string {
	var b strings.Builder
	b.WriteString(apptheme.StyleTitle.Render(" " + m.title + " "))
	b.WriteString("\n\n")
	switch m.kind {
	case modalConfirm:
		b.WriteString("  " + m.text + "\n\n")
		b.WriteString(apptheme.StyleKeyHelp.Render("  y / enter: yes    n / esc: no"))
	case modalText:
		b.WriteString(m.text)
		b.WriteString("\n")
		b.WriteString(apptheme.StyleKeyHelp.Render("  press any key to close"))
	case modalForm:
		for i, f := range m.fields {
			prefix := "  "
			if i == m.idx {
				prefix = apptheme.StyleKeyBind.Render("▸ ")
			}
			v := f.value
			if f.secret && v != "" {
				v = strings.Repeat("•", len([]rune(v)))
			}
			cursor := ""
			if i == m.idx {
				cursor = apptheme.StyleKeyBind.Render("█")
			}
			hint := ""
			if f.hint != "" {
				hint = "  " + apptheme.StyleDim.Render(f.hint)
			}
			b.WriteString(prefix + apptheme.StyleLabel.Render(f.label) + " " + v + cursor + hint + "\n")
		}
		b.WriteString("\n")
		b.WriteString(apptheme.StyleKeyHelp.Render("  enter: save    tab/↑↓: next field    ctrl+u: clear    esc: cancel"))
	}
	return lipgloss.NewStyle().Width(width).Render(b.String())
}
