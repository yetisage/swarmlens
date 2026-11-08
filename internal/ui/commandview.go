package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// handleCommandKey processes input while the ":" prompt is open.
//
// Like k9s, the prompt is a command interpreter for navigation: it switches
// resource views and can carry a filter. It never runs actions.
func (a *app) handleCommandKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		a.command.close()
		a.setStatus("cancelled")
		return a, nil
	case "enter", "ctrl+e":
		line := strings.TrimSpace(a.command.input.Value())
		if line == "" {
			a.command.close()
			return a, nil
		}
		cmd, err := parseCommand(line)
		if err != nil {
			a.command.err = err.Error()
			return a, nil
		}
		a.command.pushHistory(line)
		a.command.close()
		return a.runCommand(cmd)
	case "tab", "right", "ctrl+f":
		a.command.acceptSuggestion()
		return a, nil
	case "up", "ctrl+p":
		a.command.cycleSuggestion(1)
		return a, nil
	case "down", "ctrl+n":
		a.command.cycleSuggestion(-1)
		return a, nil
	case "ctrl+w", "ctrl+u":
		a.command.clear()
		return a, nil
	}
	var cmd tea.Cmd
	a.command.input, cmd = a.command.input.Update(msg)
	a.command.err = ""
	a.command.suggest()
	return a, cmd
}

// runCommand switches to the resource the command names, applying the
// filter it carries.
func (a *app) runCommand(cmd command) (tea.Model, tea.Cmd) {
	if cmd.filter != "" {
		a.filter = cmd.filter
		a.search.SetValue(cmd.filter)
	} else {
		a.filter = ""
		a.search.SetValue("")
	}
	a.inverse = cmd.inverse
	m, c := a.goTo(cmd.resource.screen)
	if cmd.filter != "" {
		a.applyFilter()
	}
	a.setStatus(fmt.Sprintf("%s view", cmd.resource.plural))
	return m, c
}

// promptLine renders the command prompt with k9s' inline suggestion: the
// completion is shown as ghost text right after the cursor.
func (a *app) promptLine() string {
	line := a.command.input.View()
	// The ghost text only makes sense when the cursor sits at the end.
	if a.command.input.Position() == len(a.command.input.Value()) {
		if g := a.command.ghost(); g != "" {
			line += dimStyle.Render(g)
		}
	}
	if a.command.err != "" {
		line += "  " + errStyle.Render(a.command.err)
	}
	return line + "\n"
}
