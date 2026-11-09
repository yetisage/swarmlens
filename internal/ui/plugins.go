package ui

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/yetisage/swarmlens/internal/config"
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

// expand replaces $VAR and ${VAR} in one pass: a value that itself contains
// "$NAME" is inserted as it is, not expanded again. The longest name wins, so
// $COL-NAME is not read as $COL followed by "-NAME". Unknown variables are
// left alone.
func expand(s string, vars map[string]string) string {
	names := make([]string, 0, len(vars))
	for k := range vars {
		names = append(names, k)
	}
	sort.Slice(names, func(i, j int) bool {
		if len(names[i]) != len(names[j]) {
			return len(names[i]) > len(names[j])
		}
		return names[i] < names[j]
	})

	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '$' {
			b.WriteByte(s[i])
			i++
			continue
		}
		rest := s[i+1:]
		if strings.HasPrefix(rest, "{") {
			if end := strings.IndexByte(rest, '}'); end > 0 {
				if v, ok := vars[rest[1:end]]; ok {
					b.WriteString(v)
					i += end + 2
					continue
				}
			}
		} else {
			matched := false
			for _, k := range names {
				if strings.HasPrefix(rest, k) {
					b.WriteString(vars[k])
					i += 1 + len(k)
					matched = true
					break
				}
			}
			if matched {
				continue
			}
		}
		b.WriteByte('$')
		i++
	}
	return b.String()
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
