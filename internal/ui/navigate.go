package ui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// goBack walks back through the visited resource views.
func (a *app) goBack() (tea.Model, tea.Cmd) {
	entry, ok := a.history.back()
	if !ok {
		a.setStatus("can't go back any further")
		return a, nil
	}
	return a.gotoEntry(entry)
}

// goForward walks forward through the visited resource views.
func (a *app) goForward() (tea.Model, tea.Cmd) {
	entry, ok := a.history.forward()
	if !ok {
		a.setStatus("can't go forward any further")
		return a, nil
	}
	return a.gotoEntry(entry)
}

// goLast jumps back to the previously visited view.
func (a *app) goLast() (tea.Model, tea.Cmd) {
	entry, ok := a.history.last()
	if !ok {
		a.setStatus("no previous view")
		return a, nil
	}
	return a.gotoEntry(entry)
}

// gotoEntry returns to a view recorded in the history. Like k9s, walking
// the history does not record a new entry.
func (a *app) gotoEntry(entry string) (tea.Model, tea.Cmd) {
	for _, al := range aliases {
		if entry == al.kind || entry == al.plural {
			return a.goToScreen(al.screen, false)
		}
	}
	a.setStatus(fmt.Sprintf("unknown view %q", entry))
	return a, nil
}

// recordView notes the current view in the navigation history.
func (a *app) recordView() {
	switch a.screen {
	case screenServices:
		a.history.push("services")
	case screenNodes:
		a.history.push("nodes")
	case screenVolumes:
		a.history.push("volumes")
	case screenNetworks:
		a.history.push("networks")
	case screenSecrets:
		a.history.push("secrets")
	case screenConfigs:
		a.history.push("configs")
	}
}
