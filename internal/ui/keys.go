package ui

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

// globalKeyMap holds the keys that behave the same in every view.
//
// Like k9s, switching between resource types is done through the command
// palette (":") rather than a dedicated key per resource, so the letter
// keys stay free for actions whose meaning follows the current view.
type globalKeyMap struct {
	Quit    key.Binding
	Help    key.Binding
	Palette key.Binding
	Search  key.Binding
	Refresh key.Binding
	Back    key.Binding
}

func defaultGlobalKeys() globalKeyMap {
	return globalKeyMap{
		Quit:    key.NewBinding(key.WithKeys("ctrl+c", "q"), key.WithHelp("q", "quit")),
		Help:    key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Palette: key.NewBinding(key.WithKeys(":"), key.WithHelp(":", "commands")),
		Search:  key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
		Refresh: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		Back:    key.NewBinding(key.WithKeys("esc", "b"), key.WithHelp("esc", "back")),
	}
}

// viewAction is a key binding whose meaning depends on the current view.
// The same key can do different things in different views, which is how
// k9s keeps a single-letter vocabulary.
type viewAction struct {
	binding key.Binding
	label   string // short hint shown in the action bar
	desc    string // long description shown in the help view
	enabled func(a *app) bool
	run     func(a *app) (tea.Model, tea.Cmd)
}

// helpEntry is one row of the help view.
type helpEntry struct {
	key  string
	desc string
}

// globalHelp describes the keys available everywhere.
func (g globalKeyMap) globalHelp() []helpEntry {
	return []helpEntry{
		{":", "command palette (switch resource, run actions)"},
		{"/", "search / filter"},
		{"r", "refresh"},
		{"?", "toggle help"},
		{"esc / b", "back"},
		{"q", "quit"},
	}
}
