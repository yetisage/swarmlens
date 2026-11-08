package ui

import (
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// helpView lists every key binding, in k9s' four columns. The first column
// is generated from the view help was opened over, so it is always the
// truth about what the keys do right now.
type helpView struct {
	a     *app
	vid   int
	below view
}

func newHelpView(a *app) *helpView {
	h := &helpView{a: a, vid: newViewID()}
	if len(a.stack) > 0 {
		h.below = a.view()
	}
	return h
}

func (v *helpView) id() int                        { return v.vid }
func (v *helpView) crumb() string                  { return "help" }
func (v *helpView) start() tea.Cmd                 { return nil }
func (v *helpView) refresh() tea.Cmd               { return nil }
func (v *helpView) stop()                          {}
func (v *helpView) handle(tea.Msg) tea.Cmd         { return nil }
func (v *helpView) key(tea.KeyMsg) (tea.Cmd, bool) { return nil, false }
func (v *helpView) hints() []hint                  { return []hint{{key: "esc", desc: "Back", show: true}} }
func (v *helpView) title() []seg                   { return []seg{{"Help", v.a.th.s(v.a.th.frame.title.fg).bold()}} }

type helpItem struct{ label, desc string }

type helpSection struct {
	title string
	items []helpItem
}

func (v *helpView) sections() []helpSection {
	var resource []helpItem
	seen := map[string]bool{}
	if v.below != nil {
		hs := v.below.hints()
		sort.SliceStable(hs, func(i, j int) bool {
			return strings.ToLower(hs[i].desc) < strings.ToLower(hs[j].desc)
		})
		for _, h := range hs {
			k := h.key + "/" + h.desc
			if !seen[k] && h.desc != "" {
				seen[k] = true
				resource = append(resource, helpItem{h.label(), h.desc})
			}
		}
		if tv, ok := v.below.(*tableView); ok && tv.res.scoped {
			resource = append(resource,
				helpItem{"<0>", "All Stacks"},
				helpItem{"<1-9>", "Switch Stack"})
		}
	}
	general := []helpItem{
		{"<ctrl-a>", "Aliases"},
		{"<q>", "Back"},
		{"<esc>", "Back/Clear"},
		{"<ctrl-u>", "Command Clear"},
		{"<:cmd>", "Command mode"},
		{"<tab>", "Field Next"},
		{"<backtab>", "Field Previous"},
		{"</term>", "Filter mode"},
		{"<?>", "Help"},
		{"<:q>", "Quit"},
		{"<ctrl-r>", "Reload"},
		{"<ctrl-g>", "Toggle Crumbs"},
		{"<ctrl-e>", "Toggle Header"},
	}
	nav := []helpItem{
		{"<j>", "Down"},
		{"<k>", "Up"},
		{"<g>", "Goto Top"},
		{"<shift-g>", "Goto Bottom"},
		{"<ctrl-f>", "Page Down"},
		{"<ctrl-b>", "Page Up"},
		{"<[>", "History Back"},
		{"<]>", "History Forward"},
		{"<->", "Last Used Command"},
	}
	var hot []helpItem
	for _, p := range v.a.cfg.Plugins {
		hot = append(hot, helpItem{keyLabel(p.Key), p.Description})
	}
	return []helpSection{
		{"RESOURCE", resource}, {"GENERAL", general}, {"NAVIGATION", nav}, {"HOTKEYS", hot},
	}
}

func (v *helpView) draw(p painter, r rect, focused bool) {
	t := v.a.th
	border := t.s(t.frame.border).bold()
	if focused {
		border = t.s(t.frame.focus).bold()
	}
	p.frame(r, border, v.title())
	inner := r.inset(1)
	content := rect{inner.x + 1, inner.y, max(inner.w-2, 0), inner.h}
	cp := p.sub(content)

	secs := v.sections()
	// Natural widths first, the spare room shared out evenly.
	type dims struct{ key, desc int }
	ds := make([]dims, len(secs))
	widths := make([]int, len(secs))
	total := 0
	for i, s := range secs {
		d := dims{key: textWidth(s.title)}
		for _, it := range s.items {
			d.key = max(d.key, textWidth(it.label))
			d.desc = max(d.desc, textWidth(it.desc))
		}
		ds[i] = d
		widths[i] = d.key + 3 + d.desc + 3
		total += widths[i]
	}
	if spare := content.w - total; spare > 0 {
		for i := range widths {
			widths[i] += spare / len(widths)
		}
	}

	x := content.x
	for i, s := range secs {
		cp.text(x, content.y, s.title, t.s(t.help.section))
		for j, it := range s.items {
			y := content.y + 1 + j
			if y >= content.bottom() {
				break
			}
			cp.text(x, y, it.label, t.s(t.help.key).bold())
			cp.text(x+ds[i].key+3, y, it.desc, t.s(t.help.desc))
		}
		x += widths[i]
	}
}
