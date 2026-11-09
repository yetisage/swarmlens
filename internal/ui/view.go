package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// view is one page of the UI stack: a resource table, a describe/YAML
// viewer, the log viewer, help. Like k9s pages, views are pushed on drill
// down and popped with esc.
type view interface {
	id() int
	// crumb is the name shown in the breadcrumb bar.
	crumb() string
	// title is drawn in the top border of the view's frame.
	title() []seg
	// hints are the key bindings listed in the header menu and in help.
	hints() []hint

	// start runs when the view is pushed; refresh on every refresh tick;
	// stop when it is popped.
	start() tea.Cmd
	refresh() tea.Cmd
	stop()

	// key offers a key to the view; handled tells whether it was used.
	key(msg tea.KeyMsg) (cmd tea.Cmd, handled bool)
	// handle receives the data messages addressed to the view.
	handle(msg tea.Msg) tea.Cmd

	draw(p painter, r rect, focused bool)
}

// filterer is implemented by views that support the "/" prompt.
type filterer interface {
	setFilter(q string)
	filterText() string
}

// hint is a documented key binding.
type hint struct {
	key  string // the key as bubbletea names it, e.g. "ctrl+d"
	desc string
	show bool // listed in the header menu (otherwise only in help)
}

// label formats the key like k9s does: <ctrl-d>, <shift-n>, <enter>.
func (h hint) label() string { return keyLabel(h.key) }

func keyLabel(k string) string {
	switch k {
	case " ":
		return "<space>"
	case "ctrl+@":
		return "<ctrl-space>"
	case "pgdown":
		return "<pgdn>"
	}
	if len(k) == 1 && k[0] >= 'A' && k[0] <= 'Z' {
		return "<shift-" + strings.ToLower(k) + ">"
	}
	return "<" + strings.ReplaceAll(k, "+", "-") + ">"
}

// action is a hint that also does something.
type action struct {
	hint
	mutates bool // changes the swarm or what runs in it: refused in read-only mode
	run     func() tea.Cmd
}

func act(key, desc string, show bool, run func() tea.Cmd) action {
	return action{hint: hint{key: key, desc: desc, show: show}, run: run}
}

func mut(key, desc string, run func() tea.Cmd) action {
	a := act(key, desc, true, run)
	a.mutates = true
	return a
}

// view ids keep asynchronous results from reaching a view that was popped.
var nextViewID = 0

func newViewID() int {
	nextViewID++
	return nextViewID
}
