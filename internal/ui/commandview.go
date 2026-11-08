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
	case "enter":
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
	case "tab":
		// Complete the first word with the first matching alias.
		if len(a.command.matches) > 0 {
			a.command.acceptCompletion(a.command.matches[0])
		}
		return a, nil
	case "up", "ctrl+p":
		a.command.historyPrev()
		return a, nil
	case "down", "ctrl+n":
		a.command.historyNext()
		return a, nil
	}
	var cmd tea.Cmd
	a.command.input, cmd = a.command.input.Update(msg)
	a.command.err = ""
	a.command.updateMatches()
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

// overlayCommand draws the command prompt over the base view.
func (a *app) overlayCommand(base string) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(" switch resource"))
	b.WriteString("\n")
	b.WriteString(a.command.input.View())
	b.WriteString("\n")

	if a.command.err != "" {
		b.WriteString(errStyle.Render("  " + a.command.err))
		b.WriteString("\n")
	} else {
		for _, m := range a.command.matches {
			names := strings.Join(m.aliases, ", ")
			b.WriteString("  ")
			b.WriteString(nameStyle.Render(m.kind))
			b.WriteString(dimStyle.Render("  (" + names + ")"))
			b.WriteString(dimStyle.Render("   " + m.plural))
			b.WriteString("\n")
		}
		if len(a.command.matches) == 0 {
			b.WriteString(dimStyle.Render("  no matching resource — try service, node, volume, network, secret or config"))
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  enter run   tab complete   ↑↓ history   esc cancel"))
	b.WriteString("\n")

	box := overlayBox.Width(minInt(a.width-8, 68)).Render(strings.TrimRight(b.String(), "\n"))
	return placeOverlay(base, box, a.width, a.height)
}
