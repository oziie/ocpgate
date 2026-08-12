// Package keys defines every keybinding the TUI recognizes. Keeping them
// in one place means the help text and the actual behavior cannot drift
// apart, since both read the same bindings.
package keys

import "github.com/charmbracelet/bubbles/key"

// Global bindings are available in every view.
type Global struct {
	Quit key.Binding
	Back key.Binding
	Help key.Binding
}

// Form bindings apply to the credential and manual-namespace views.
type Form struct {
	Next    key.Binding
	Prev    key.Binding
	Submit  key.Binding
	Confirm key.Binding
}

// Session bindings apply to the active-session view.
type Session struct {
	Shell key.Binding
	End   key.Binding
}

// KeyMap is the full set, constructed once and passed to the views.
type KeyMap struct {
	Global  Global
	Form    Form
	Session Session
}

// Default returns the standard ocpgate keymap.
func Default() KeyMap {
	return KeyMap{
		Global: Global{
			Quit: key.NewBinding(
				key.WithKeys("ctrl+c"),
				key.WithHelp("ctrl+c", "quit"),
			),
			Back: key.NewBinding(
				key.WithKeys("esc"),
				key.WithHelp("esc", "back"),
			),
			Help: key.NewBinding(
				key.WithKeys("?"),
				key.WithHelp("?", "help"),
			),
		},
		Form: Form{
			Next: key.NewBinding(
				key.WithKeys("tab", "down"),
				key.WithHelp("tab", "next field"),
			),
			Prev: key.NewBinding(
				key.WithKeys("shift+tab", "up"),
				key.WithHelp("shift+tab", "previous field"),
			),
			Submit: key.NewBinding(
				key.WithKeys("enter"),
				key.WithHelp("enter", "submit"),
			),
			Confirm: key.NewBinding(
				key.WithKeys("enter"),
				key.WithHelp("enter", "confirm"),
			),
		},
		Session: Session{
			Shell: key.NewBinding(
				key.WithKeys("enter"),
				key.WithHelp("enter", "open shell"),
			),
			End: key.NewBinding(
				key.WithKeys("q", "esc"),
				key.WithHelp("q", "end session"),
			),
		},
	}
}
