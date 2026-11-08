package ui

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"swarmlens/internal/config"
)

// pluginActions are the user's plugins that apply to a table, in k9s'
// plugins.yaml format: a key bound to an external command that may use the
// selected row's values.
func (a *app) pluginActions(v *tableView) []action {
	var acts []action
	for _, p := range a.cfg.Plugins {
		if !scopeMatches(p.Scopes, v.res) {
			continue
		}
		p := p
		ac := act(p.Key, p.Description, true, func() tea.Cmd { return a.runPlugin(p, v) })
		ac.mutates = p.Dangerous
		acts = append(acts, ac)
	}
	return acts
}

func scopeMatches(scopes []string, res *resource) bool {
	for _, s := range scopes {
		s = strings.ToLower(s)
		if s == "all" || s == "*" || s == res.key || s == res.singular {
			return true
		}
		for _, al := range res.aliases {
			if s == al {
				return true
			}
		}
	}
	return false
}

// pluginVars are the substitutions available to a plugin's arguments.
func (a *app) pluginVars(v *tableView, r *row) map[string]string {
	vars := map[string]string{
		"NAME":     r.name,
		"ID":       r.id,
		"STACK":    r.stack,
		"CONTEXT":  a.be.ContextName(),
		"ENDPOINT": a.be.Endpoint(),
		"FILTER":   v.filter,
	}
	for i, c := range v.res.cols {
		vars["COL-"+strings.ReplaceAll(c.name, " ", "-")] = r.cells[i]
	}
	return vars
}

// expand replaces $VAR and ${VAR}, longest names first so $COL-NAME is not
// mistaken for $COL.
func expand(s string, vars map[string]string) string {
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, k := range keys {
		s = strings.ReplaceAll(s, "${"+k+"}", vars[k])
		s = strings.ReplaceAll(s, "$"+k, vars[k])
	}
	return s
}

func (a *app) runPlugin(p config.Plugin, v *tableView) tea.Cmd {
	r := v.selected()
	if r == nil {
		return nil
	}
	vars := a.pluginVars(v, r)
	args := make([]string, len(p.Args))
	for i, arg := range p.Args {
		args[i] = expand(arg, vars)
	}
	run := func() tea.Cmd {
		cmd := exec.Command(p.Command, args...)
		if p.Background {
			if err := cmd.Start(); err != nil {
				a.flash(flashErr, fmt.Sprintf("%s: %v", p.Command, err))
				return nil
			}
			go func() { _ = cmd.Wait() }()
			return a.flashFor(flashInfo, "Running "+p.Description+"...")
		}
		return tea.ExecProcess(cmd, func(err error) tea.Msg { return execDoneMsg{what: p.Command, err: err} })
	}
	if p.Confirm {
		return a.confirm("Plugin", fmt.Sprintf("Run %q?\n%s %s", p.Description, p.Command, strings.Join(args, " ")), run)
	}
	return run()
}
