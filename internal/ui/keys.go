package ui

import "github.com/charmbracelet/bubbles/key"

// Global keys are available on every screen.
type globalKeyMap struct {
	Quit      key.Binding
	Services  key.Binding
	Nodes     key.Binding
	Volumes   key.Binding
	Networks  key.Binding
	Secrets   key.Binding
	Configs   key.Binding
	Search    key.Binding
	Palette   key.Binding
	Refresh   key.Binding
	Help      key.Binding
	Back      key.Binding
	Details   key.Binding
	Logs      key.Binding
	ScaleUp   key.Binding
	ScaleDown key.Binding
	Update    key.Binding
	Rollback  key.Binding
	Remove    key.Binding
	Drain     key.Binding
	Activate  key.Binding
	Promote   key.Binding
	Demote    key.Binding
	Follow    key.Binding
}

func defaultGlobalKeys() globalKeyMap {
	return globalKeyMap{
		Quit:      key.NewBinding(key.WithKeys("ctrl+c", "q"), key.WithHelp("q", "quit")),
		Services:  key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "services")),
		Nodes:     key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "nodes")),
		Volumes:   key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "volumes")),
		Networks:  key.NewBinding(key.WithKeys("w"), key.WithHelp("w", "networks")),
		Secrets:   key.NewBinding(key.WithKeys("k"), key.WithHelp("k", "secrets")),
		Configs:   key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "configs")),
		Search:    key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
		Palette:   key.NewBinding(key.WithKeys(":"), key.WithHelp(":", "commands")),
		Refresh:   key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		Help:      key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Back:      key.NewBinding(key.WithKeys("esc", "b"), key.WithHelp("esc", "back")),
		Details:   key.NewBinding(key.WithKeys("enter", "d"), key.WithHelp("d", "detail")),
		Logs:      key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "logs")),
		ScaleUp:   key.NewBinding(key.WithKeys("+", "="), key.WithHelp("+", "scale up")),
		ScaleDown: key.NewBinding(key.WithKeys("-"), key.WithHelp("-", "scale down")),
		Update:    key.NewBinding(key.WithKeys("u"), key.WithHelp("u", "update")),
		Rollback:  key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "rollback")),
		Remove:    key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "remove")),
		Drain:     key.NewBinding(key.WithKeys("D"), key.WithHelp("D", "drain")),
		Activate:  key.NewBinding(key.WithKeys("A"), key.WithHelp("A", "activate")),
		Promote:   key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "promote")),
		Demote:    key.NewBinding(key.WithKeys("w"), key.WithHelp("w", "demote")),
		Follow:    key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "follow")),
	}
}

// helpEntry is one row of the help view.
type helpEntry struct {
	key  string
	desc string
}

func (g globalKeyMap) help() []helpEntry {
	return []helpEntry{
		{"s / n / v / w / k / c", "services · nodes · volumes · networks · secrets · configs"},
		{"↑ ↓", "move cursor"},
		{"enter / d", "open detail"},
		{"l", "task logs"},
		{"f", "toggle log follow"},
		{"/", "search / filter"},
		{":", "command palette"},
		{"+ / -", "scale service up / down"},
		{"u", "force update service"},
		{"R", "rollback service"},
		{"x", "remove (with confirmation)"},
		{"D / A", "drain / activate node"},
		{"p / w", "promote / demote node"},
		{"r", "refresh"},
		{"esc / b", "back"},
		{"?", "toggle help"},
		{"q", "quit"},
	}
}
