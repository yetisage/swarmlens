package ui

import (
	"fmt"
	"strings"
)

// aliasRows renders the alias table, the swarmlens equivalent of k9s'
// "show all available resource alias" screen (ctrl-a).
func (a *app) aliasRows() [][]string {
	rows := make([][]string, 0, len(aliases))
	for _, al := range aliases {
		marker := " "
		if al.screen == a.screen {
			marker = "▌"
		}
		rows = append(rows, []string{
			marker + al.kind,
			strings.Join(al.aliases, ", "),
			al.plural,
			a.viewNameFor(al.screen),
		})
	}
	return rows
}

// viewNameFor is the display name of a resource view.
func (a *app) viewNameFor(s screen) string {
	switch s {
	case screenServices:
		return "services"
	case screenNodes:
		return "nodes"
	case screenVolumes:
		return "volumes"
	case screenNetworks:
		return "networks"
	case screenSecrets:
		return "secrets"
	case screenConfigs:
		return "configs"
	}
	return "swarmlens"
}

// overlayAliases draws the alias table over the base view.
func (a *app) overlayAliases(base string) string {
	rows := a.aliasRows()
	width := 62
	if a.width-8 < width {
		width = max(20, a.width-8)
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render(" resource aliases"))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render(" NAME            ALIASES     PLURAL        VIEW"))
	b.WriteString("\n")
	for _, r := range rows {
		name := r[0]
		if strings.HasPrefix(name, "▌") {
			name = selectedStyle.Render(name)
		} else {
			name = nameStyle.Render(name)
		}
		b.WriteString(fit(name, 16))
		b.WriteString(fit(dimStyle.Render(r[1]), 13))
		b.WriteString(fit(dimStyle.Render(r[2]), 15))
		b.WriteString(fit(dimStyle.Render(r[3]), 15))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(dimStyle.Render(fmt.Sprintf(" Type a command like \":%s\" to switch views.", aliases[0].kind)))
	b.WriteString("\n")

	box := overlayBox.Width(width - 4).Render(strings.TrimRight(b.String(), "\n"))
	return placeOverlay(base, box, a.width, a.height)
}
