package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// paletteItem is one command in the command palette.
type paletteItem struct {
	title string
	desc  string
	run   func(a *app) (tea.Model, tea.Cmd)
}

// palette is the k9s-style command palette (opened with ':').
type palette struct {
	active   bool
	input    textinput.Model
	items    []paletteItem
	filtered []paletteItem
	cursor   int
}

func newPalette() palette {
	in := textinput.New()
	in.Prompt = ": "
	in.Placeholder = "command…"
	in.CharLimit = 64
	return palette{input: in}
}

func (p *palette) open(items []paletteItem) {
	p.active = true
	p.items = items
	p.filtered = items
	p.cursor = 0
	p.input.SetValue("")
	p.input.Focus()
}

// matches reports whether the query matches the item (case-insensitive
// substring on title or description).
func (p *palette) matches(it paletteItem, q string) bool {
	if q == "" {
		return true
	}
	return strings.Contains(strings.ToLower(it.title), strings.ToLower(q)) ||
		strings.Contains(strings.ToLower(it.desc), strings.ToLower(q))
}

func (p *palette) applyFilter() {
	q := p.input.Value()
	p.filtered = p.filtered[:0]
	for _, it := range p.items {
		if p.matches(it, q) {
			p.filtered = append(p.filtered, it)
		}
	}
	if p.cursor >= len(p.filtered) {
		p.cursor = 0
	}
}

// handlePaletteKey processes input while the palette is open.
func (a *app) handlePaletteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		a.palette.active = false
		a.palette.input.Blur()
		return a, nil
	case "enter":
		if a.palette.cursor >= 0 && a.palette.cursor < len(a.palette.filtered) {
			it := a.palette.filtered[a.palette.cursor]
			a.palette.active = false
			a.palette.input.Blur()
			if it.run != nil {
				return it.run(a)
			}
		}
		return a, nil
	case "up", "ctrl+p":
		if a.palette.cursor > 0 {
			a.palette.cursor--
		}
		return a, nil
	case "down", "ctrl+n":
		if a.palette.cursor < len(a.palette.filtered)-1 {
			a.palette.cursor++
		}
		return a, nil
	}
	var cmd tea.Cmd
	a.palette.input, cmd = a.palette.input.Update(msg)
	a.palette.applyFilter()
	return a, cmd
}

// overlayPalette draws the palette on top of the base view.
func (a *app) overlayPalette(base string) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(" commands"))
	b.WriteString("\n")
	b.WriteString(a.palette.input.View())
	b.WriteString("\n")
	visible := a.palette.filtered
	start := 0
	if len(visible) > 10 {
		start = a.palette.cursor - 5
		if start < 0 {
			start = 0
		}
		if start+10 > len(visible) {
			start = len(visible) - 10
		}
		visible = visible[start : start+10]
	}
	for i, it := range visible {
		idx := start + i
		marker := "  "
		style := dimStyle
		if idx == a.palette.cursor {
			marker = "> "
			style = selectedStyle
		}
		b.WriteString(marker)
		b.WriteString(style.Render(it.title))
		b.WriteString("  ")
		b.WriteString(dimStyle.Render(it.desc))
		b.WriteString("\n")
	}
	if len(a.palette.filtered) == 0 {
		b.WriteString(dimStyle.Render("  no matching command"))
		b.WriteString("\n")
	}
	box := overlayBox.Width(minInt(a.width-8, 76)).Render(strings.TrimRight(b.String(), "\n"))
	return placeOverlay(base, box, a.width, a.height)
}

// paletteCommands builds the command list for the current screen.
func (a *app) paletteCommands() []paletteItem {
	items := []paletteItem{
		{"services", "switch to the services view", func(a *app) (tea.Model, tea.Cmd) { return a.goTo(screenServices) }},
		{"nodes", "switch to the nodes view", func(a *app) (tea.Model, tea.Cmd) { return a.goTo(screenNodes) }},
		{"volumes", "switch to the volumes view", func(a *app) (tea.Model, tea.Cmd) { return a.goTo(screenVolumes) }},
		{"networks", "switch to the networks view", func(a *app) (tea.Model, tea.Cmd) { return a.goTo(screenNetworks) }},
		{"secrets", "switch to the secrets view", func(a *app) (tea.Model, tea.Cmd) { return a.goTo(screenSecrets) }},
		{"configs", "switch to the configs view", func(a *app) (tea.Model, tea.Cmd) { return a.goTo(screenConfigs) }},
		{"refresh", "reload all data", func(a *app) (tea.Model, tea.Cmd) { return a, a.refreshCmd() }},
		{"help", "toggle the help view", func(a *app) (tea.Model, tea.Cmd) { a.showHelp = !a.showHelp; return a, nil }},
	}

	switch a.screen {
	case screenServices:
		if s := a.currentService(); s != nil {
			name := s.Name
			items = append(items,
				paletteItem{"scale up " + name, "add one replica", func(a *app) (tea.Model, tea.Cmd) { return a.scaleService(+1) }},
				paletteItem{"scale down " + name, "remove one replica", func(a *app) (tea.Model, tea.Cmd) { return a.scaleService(-1) }},
				paletteItem{"update " + name, "force a rolling update", func(a *app) (tea.Model, tea.Cmd) { return a.forceUpdateService() }},
				paletteItem{"rollback " + name, "restore the previous spec", func(a *app) (tea.Model, tea.Cmd) { return a.rollbackService() }},
				paletteItem{"remove " + name, "delete the service", func(a *app) (tea.Model, tea.Cmd) { return a.removeService() }},
				paletteItem{"details of " + name, "open the task list", func(a *app) (tea.Model, tea.Cmd) { return a.openDetail() }},
			)
		}
	case screenNodes:
		if n := a.currentNode(); n != nil {
			host := n.Hostname
			items = append(items,
				paletteItem{"drain " + host, "stop scheduling new tasks", func(a *app) (tea.Model, tea.Cmd) { return a.setNodeAvailability("drain") }},
				paletteItem{"activate " + host, "resume scheduling", func(a *app) (tea.Model, tea.Cmd) { return a.setNodeAvailability("active") }},
				paletteItem{"promote " + host, "make it a manager", func(a *app) (tea.Model, tea.Cmd) { return a.setNodeRole("manager") }},
				paletteItem{"demote " + host, "make it a worker", func(a *app) (tea.Model, tea.Cmd) { return a.setNodeRole("worker") }},
				paletteItem{"remove " + host, "remove the node from the swarm", func(a *app) (tea.Model, tea.Cmd) { return a.removeNode() }},
			)
		}
	case screenDetail:
		if t := a.currentTask(); t != nil {
			name := t.Name
			items = append(items,
				paletteItem{"logs of " + name, "open the log viewer", func(a *app) (tea.Model, tea.Cmd) { return a.openLogs() }},
			)
		}
	}
	return items
}

// overlayHelp draws the help view on top of the base view.
func (a *app) overlayHelp(base string) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(" keybindings"))
	b.WriteString("\n")
	for _, h := range a.keys.help() {
		b.WriteString(keyStyle.Render(fmt.Sprintf("%-22s", h.key)))
		b.WriteString(dimStyle.Render(h.desc))
		b.WriteString("\n")
	}
	box := helpBoxStyle.Width(minInt(a.width-8, 76)).Render(strings.TrimRight(b.String(), "\n"))
	return placeOverlay(base, box, a.width, a.height)
}
