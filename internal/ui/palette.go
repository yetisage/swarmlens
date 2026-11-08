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
	// aliases are extra prefixes that select this command, in the spirit of
	// k9s' short resource aliases ("svc", "po", …).
	aliases []string
	run     func(a *app) (tea.Model, tea.Cmd)
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
// substring on title, description or alias).
func (p *palette) matches(it paletteItem, q string) bool {
	if q == "" {
		return true
	}
	lq := strings.ToLower(q)
	if strings.Contains(strings.ToLower(it.title), lq) ||
		strings.Contains(strings.ToLower(it.desc), lq) {
		return true
	}
	for _, alias := range it.aliases {
		if alias == lq {
			return true
		}
	}
	return false
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
//
// Like k9s, the palette is the way to switch between resource types; each
// resource also has short aliases so "svc" is enough to jump to services.
func (a *app) paletteCommands() []paletteItem {
	items := []paletteItem{
		{title: "services", desc: "switch to the services view", aliases: []string{"svc", "service", "s"},
			run: func(a *app) (tea.Model, tea.Cmd) { return a.goTo(screenServices) }},
		{title: "nodes", desc: "switch to the nodes view", aliases: []string{"node", "no", "nd"},
			run: func(a *app) (tea.Model, tea.Cmd) { return a.goTo(screenNodes) }},
		{title: "volumes", desc: "switch to the volumes view", aliases: []string{"vol", "volume", "v"},
			run: func(a *app) (tea.Model, tea.Cmd) { return a.goTo(screenVolumes) }},
		{title: "networks", desc: "switch to the networks view", aliases: []string{"net", "network", "nw"},
			run: func(a *app) (tea.Model, tea.Cmd) { return a.goTo(screenNetworks) }},
		{title: "secrets", desc: "switch to the secrets view", aliases: []string{"secret", "sec"},
			run: func(a *app) (tea.Model, tea.Cmd) { return a.goTo(screenSecrets) }},
		{title: "configs", desc: "switch to the configs view", aliases: []string{"config", "cfg"},
			run: func(a *app) (tea.Model, tea.Cmd) { return a.goTo(screenConfigs) }},
		{title: "refresh", desc: "reload all data", aliases: []string{"reload"},
			run: func(a *app) (tea.Model, tea.Cmd) { return a, a.refreshCmd() }},
		{title: "help", desc: "toggle the help view", aliases: []string{"keybindings"},
			run: func(a *app) (tea.Model, tea.Cmd) { a.showHelp = !a.showHelp; return a, nil }},
	}

	// The actions of the current view are also reachable from the palette,
	// so everything can be done without the keyboard shortcuts.
	for _, act := range a.viewActions() {
		if act.enabled != nil && !act.enabled(a) {
			continue
		}
		act := act
		items = append(items, paletteItem{
			title: act.label,
			desc:  act.desc,
			run:   func(a *app) (tea.Model, tea.Cmd) { return act.run(a) },
		})
	}

	switch a.screen {
	case screenServices:
		if s := a.currentService(); s != nil {
			name := s.Name
			items = append(items,
				paletteItem{title: "scale up " + name, desc: "add one replica", run: func(a *app) (tea.Model, tea.Cmd) { return a.scaleService(+1) }},
				paletteItem{title: "scale down " + name, desc: "remove one replica", run: func(a *app) (tea.Model, tea.Cmd) { return a.scaleService(-1) }},
				paletteItem{title: "update " + name, desc: "force a rolling update", run: func(a *app) (tea.Model, tea.Cmd) { return a.forceUpdateService() }},
				paletteItem{title: "rollback " + name, desc: "restore the previous spec", run: func(a *app) (tea.Model, tea.Cmd) { return a.rollbackService() }},
				paletteItem{title: "remove " + name, desc: "delete the service", run: func(a *app) (tea.Model, tea.Cmd) { return a.removeService() }},
				paletteItem{title: "details of " + name, desc: "open the task list", run: func(a *app) (tea.Model, tea.Cmd) { return a.openDetail() }},
			)
		}
	case screenNodes:
		if n := a.currentNode(); n != nil {
			host := n.Hostname
			items = append(items,
				paletteItem{title: "drain " + host, desc: "stop scheduling new tasks", run: func(a *app) (tea.Model, tea.Cmd) { return a.setNodeAvailability("drain") }},
				paletteItem{title: "activate " + host, desc: "resume scheduling", run: func(a *app) (tea.Model, tea.Cmd) { return a.setNodeAvailability("active") }},
				paletteItem{title: "promote " + host, desc: "make it a manager", run: func(a *app) (tea.Model, tea.Cmd) { return a.setNodeRole("manager") }},
				paletteItem{title: "demote " + host, desc: "make it a worker", run: func(a *app) (tea.Model, tea.Cmd) { return a.setNodeRole("worker") }},
				paletteItem{title: "remove " + host, desc: "remove the node from the swarm", run: func(a *app) (tea.Model, tea.Cmd) { return a.removeNode() }},
			)
		}
	case screenDetail:
		if t := a.currentTask(); t != nil {
			name := t.Name
			items = append(items,
				paletteItem{title: "logs of " + name, desc: "open the log viewer", run: func(a *app) (tea.Model, tea.Cmd) { return a.openLogs() }},
			)
		}
	}
	return items
}

// overlayHelp draws the help view on top of the base view. It lists the
// actions of the current view first, since those are the ones whose
// meaning follows the resource type.
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
