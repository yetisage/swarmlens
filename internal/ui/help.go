package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// overlayHelp draws the help view over the base view. It lists the
// actions of the current view first, since those are the ones whose
// meaning follows the resource type — the same "active keyboard
// mnemonics" k9s shows.
func (a *app) overlayHelp(base string) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(" " + a.viewName() + " — keybindings"))
	b.WriteString("\n")

	for _, act := range a.viewActions() {
		keys := act.binding.Keys()
		if len(keys) == 0 {
			continue
		}
		label := keys[0]
		if len(keys) > 1 {
			label = keys[0] + "/" + keys[1]
		}
		b.WriteString(keyStyle.Render(fmt.Sprintf("%-12s", label)))
		b.WriteString(dimStyle.Render(act.desc))
		b.WriteString("\n")
	}

	b.WriteString(dimStyle.Render("── global ──"))
	b.WriteString("\n")
	for _, h := range a.keys.globalHelp() {
		b.WriteString(keyStyle.Render(fmt.Sprintf("%-12s", h.key)))
		b.WriteString(dimStyle.Render(h.desc))
		b.WriteString("\n")
	}

	box := helpBoxStyle.Width(minInt(a.width-8, 72)).Render(strings.TrimRight(b.String(), "\n"))
	return placeOverlay(base, box, a.width, a.height)
}

// ensure lipgloss stays referenced for style helpers.
var _ = lipgloss.Width
