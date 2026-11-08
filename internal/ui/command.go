package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"swarmlens/internal/api"
)

var (
	quitWords    = []string{"q", "q!", "qa", "quit", "exit"}
	helpWords    = []string{"help", "h", "?"}
	aliasWords   = []string{"alias", "aliases", "a"}
	contextWords = []string{"ctx", "context", "contexts"}
)

func in(word string, set []string) bool {
	for _, s := range set {
		if s == word {
			return true
		}
	}
	return false
}

// resolve maps a command word to a resource, honouring the user's aliases.
func (a *app) resolve(word string) (*resource, bool) {
	if target, ok := a.cfg.Aliases[word]; ok {
		word = target
	}
	for _, r := range resources {
		for _, n := range r.names() {
			if n == word {
				return r, true
			}
		}
	}
	return nil, false
}

// commandWords are all the names the prompt completes.
func (a *app) commandWords() []string {
	var out []string
	for _, r := range resources {
		out = append(out, r.names()...)
	}
	for alias := range a.cfg.Aliases {
		out = append(out, alias)
	}
	out = append(out, quitWords...)
	out = append(out, "help", "alias", "aliases")
	sort.Strings(out)
	return out
}

// runCommand interprets a ":" command line. Like k9s' prompt it navigates:
// it never changes the swarm.
//
//	services                  switch to a resource
//	services shop             … in one stack ("all" for every stack)
//	services shop /web        … filtered
//	ctx prod                  switch docker context
func (a *app) runCommand(line string, record bool) tea.Cmd {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return nil
	}
	word := strings.ToLower(fields[0])
	switch {
	case in(word, quitWords):
		return a.quit()
	case in(word, helpWords):
		if _, ok := a.view().(*helpView); ok {
			return nil
		}
		return a.push(newHelpView(a))
	case in(word, aliasWords):
		if a.view().crumb() == "aliases" {
			return nil
		}
		return a.push(a.aliasView())
	case in(word, contextWords) && len(fields) > 1:
		return a.switchContext(fields[1])
	}

	res, ok := a.resolve(word)
	if !ok {
		a.modal = newErrorDialog(fmt.Sprintf("`%s` command not found", fields[0]))
		return nil
	}

	filter, scope, scopeSet := "", "", false
	for i, f := range fields[1:] {
		if strings.HasPrefix(f, "/") {
			filter = strings.TrimPrefix(strings.Join(fields[1+i:], " "), "/")
			break
		}
		scope, scopeSet = f, true
		if f == "all" || f == "*" || f == "-" {
			scope = ""
		}
	}
	if scopeSet && res.scoped {
		a.applyScope(scope)
	}

	v := newTableView(a, res, loadQuery{})
	if filter != "" {
		v.setFilter(filter)
	}
	msg := fmt.Sprintf("Viewing %s", res.key)
	if res.scoped && a.scope != "" {
		msg += " in stack " + a.scope
	}
	a.flash(flashInfo, msg+"...")
	if record {
		a.record(strings.Join(strings.Fields(line), " "))
	}
	return a.setRoot(v)
}

// aliasView lists every command the prompt understands (ctrl-a).
func (a *app) aliasView() view {
	res := &resource{
		key: "aliases", singular: "alias", defSort: 0, defAsc: true,
		cols: []column{
			{name: "RESOURCE", hotkey: "R", min: 14},
			{name: "COMMAND", max: 80},
		},
		load: func(context.Context, api.Backend, loadQuery) ([]*row, error) {
			return a.aliasRows(), nil
		},
	}
	res.actions = func(v *tableView) []action {
		return []action{act("enter", "Goto", true, func() tea.Cmd {
			if r := v.selected(); r != nil {
				return a.runCommand(r.id, true)
			}
			return nil
		})}
	}
	v := newTableView(a, res, loadQuery{})
	v.alt = true
	return v
}

func (a *app) aliasRows() []*row {
	extra := map[string][]string{}
	for alias, target := range a.cfg.Aliases {
		extra[target] = append(extra[target], alias)
	}
	var rows []*row
	add := func(name string, words []string) {
		sort.Strings(words)
		r := &row{id: words[0], name: name, cells: []string{name, strings.Join(words, " ")}, nums: make([]float64, 2)}
		rows = append(rows, r)
	}
	for _, r := range resources {
		words := append(r.names(), extra[r.key]...)
		add(r.key, words)
	}
	add("aliases", append([]string(nil), aliasWords...))
	add("help", append([]string(nil), helpWords...))
	add("quit", append([]string(nil), quitWords...))
	return rows
}
