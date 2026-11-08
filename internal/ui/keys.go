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
	Quit         key.Binding
	Help         key.Binding
	Palette      key.Binding
	Search       key.Binding
	Refresh      key.Binding
	Back         key.Binding
	AliasList    key.Binding
	BackView     key.Binding
	ToggleCrumbs key.Binding
	ForwardView  key.Binding
	LastView     key.Binding
	ClearFilter  key.Binding
}

func defaultGlobalKeys() globalKeyMap {
	return globalKeyMap{
		Quit:         key.NewBinding(key.WithKeys("ctrl+c", "q"), key.WithHelp("q", "quit")),
		Help:         key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Palette:      key.NewBinding(key.WithKeys(":"), key.WithHelp(":", "commands")),
		Search:       key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
		Refresh:      key.NewBinding(key.WithKeys("ctrl+r"), key.WithHelp("ctrl+r", "refresh")),
		Back:         key.NewBinding(key.WithKeys("esc", "b"), key.WithHelp("esc", "back")),
		AliasList:    key.NewBinding(key.WithKeys("ctrl+a"), key.WithHelp("ctrl+a", "aliases")),
		BackView:     key.NewBinding(key.WithKeys("["), key.WithHelp("[", "go back")),
		ToggleCrumbs: key.NewBinding(key.WithKeys("ctrl+g"), key.WithHelp("ctrl+g", "toggle crumbs")),
		ForwardView:  key.NewBinding(key.WithKeys("]"), key.WithHelp("]", "go forward")),
		LastView:     key.NewBinding(key.WithKeys("-"), key.WithHelp("-", "last view")),
		ClearFilter:  key.NewBinding(key.WithKeys("ctrl+u", "ctrl+q"), key.WithHelp("ctrl+u", "clear filter")),
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

// mnemonic returns the key label shown in the menu bar.
func (v viewAction) mnemonic() string {
	keys := v.binding.Keys()
	if len(keys) == 0 {
		return ""
	}
	if len(keys) > 1 {
		return keys[0] + "/" + keys[1]
	}
	return keys[0]
}

// helpEntry is one row of the help view.
type helpEntry struct {
	key  string
	desc string
}

// globalHelp describes the keys available everywhere.
func (g globalKeyMap) globalHelp() []helpEntry {
	return []helpEntry{
		{":", "switch resource (service, node, volume, …)"},
		{"/", "filter the current view"},
		{"ctrl+r", "refresh"},
		{"ctrl+a", "show the resource aliases"},
		{"?", "toggle help"},
		{"esc / b", "back"},
		{"q", "quit"},
	}
}
