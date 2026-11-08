package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	headerHeight = 7
	infoWidthMax = 50
)

// logoArt is the wordmark in the top right corner, the counterpart of the
// k9s dog-tag logo: a honeycomb for the swarm and the name.
var logoArt = []string{
	` __    __`,
	`/  \__/  \  swarm`,
	`\__/  \__/  lens`,
	`/  \__/  \`,
	`\__/  \__/`,
}

func logoWidth() int {
	w := 0
	for _, l := range logoArt {
		w = max(w, textWidth(l))
	}
	return w
}

// infoRow is one "Label: value" line of the header.
type infoRow struct {
	label, value string
	warn         bool
}

// infoRows describes the swarm for the header, in the spirit of k9s'
// Context / Cluster / User / Rev block: what am I connected to, and is it
// healthy.
func (a *app) infoRows() []infoRow {
	rw := "RW"
	if a.readOnly {
		rw = "RO"
	}
	rows := []infoRow{
		{label: "Context:", value: fmt.Sprintf("%s [%s]", a.be.ContextName(), rw)},
	}
	s := a.summary
	if s == nil {
		return append(rows,
			infoRow{label: "Swarm:", value: "n/a"},
			infoRow{label: "Node:", value: "n/a"},
			infoRow{label: "SL Rev:", value: a.version},
			infoRow{label: "Docker:", value: "n/a"},
			infoRow{label: "Services:", value: "n/a", warn: true},
			infoRow{label: "Tasks:", value: "n/a", warn: true},
		)
	}

	swarm := "n/a"
	switch {
	case s.SwarmState != "active":
		swarm = "not in a swarm (" + s.SwarmState + ")"
	default:
		nodes := fmt.Sprintf("%d nodes", s.Nodes)
		if s.NodesReady < s.Nodes {
			nodes = fmt.Sprintf("%d/%d ready", s.NodesReady, s.Nodes)
		}
		swarm = nodes
		if s.Cluster != "" {
			swarm = fmt.Sprintf("%s (%s)", s.Cluster, nodes)
		}
	}
	node := orNA(s.NodeName)
	switch {
	case s.Leader:
		node += " (leader)"
	case s.Role != "":
		node += " (" + s.Role + ")"
	}
	rows = append(rows,
		infoRow{label: "Swarm:", value: swarm, warn: s.SwarmState == "active" && s.NodesReady < s.Nodes},
		infoRow{label: "Node:", value: node},
		infoRow{label: "SL Rev:", value: a.version},
		infoRow{label: "Docker:", value: orNA(s.ServerVersion)},
	)
	if s.InSwarm() {
		rows = append(rows,
			infoRow{label: "Services:", value: fmt.Sprintf("%d/%d", s.Services-s.ServicesDegraded, s.Services), warn: s.ServicesDegraded > 0},
			infoRow{label: "Tasks:", value: fmt.Sprintf("%d/%d", s.TasksRunning, s.TasksDesired), warn: s.TasksRunning < s.TasksDesired},
		)
	} else {
		rows = append(rows,
			infoRow{label: "Services:", value: "n/a", warn: true},
			infoRow{label: "Tasks:", value: "n/a", warn: true},
		)
	}
	return rows
}

func orNA(s string) string {
	if s == "" {
		return "n/a"
	}
	return s
}

// headerLayout splits the header width between info, menu and logo. The
// info block keeps its natural width (k9s uses a fixed 50), the logo goes
// first when space runs short, then the menu.
func (a *app) headerLayout(w int) (info, menu, logo rect) {
	rows := a.infoRows()
	labelW, valueW := 0, 0
	for _, r := range rows {
		labelW = max(labelW, textWidth(r.label))
		valueW = max(valueW, textWidth(r.value))
	}
	infoW := min(max(labelW+1+valueW+2, 28), infoWidthMax)
	logoW := 0
	if !a.logoless {
		logoW = logoWidth() + 2
	}
	// Keep at least ~24 cells for the menu, or give up the logo.
	if logoW > 0 && w-infoW-logoW < 24 {
		logoW = 0
	}
	info = rect{0, 0, infoW, headerHeight}
	logo = rect{w - logoW, 0, logoW, headerHeight}
	menu = rect{infoW, 0, max(w-infoW-logoW, 0), headerHeight}
	return
}

func (a *app) drawHeader(p painter, r rect) {
	t := a.th
	info, menu, logo := a.headerLayout(r.w)

	rows := a.infoRows()
	labelW := 0
	for _, row := range rows {
		labelW = max(labelW, textWidth(row.label))
	}
	for i, row := range rows {
		if i >= headerHeight {
			break
		}
		x := info.x
		x += p.text(x, r.y+i, padRight(row.label, labelW)+" ", t.s(t.info.label))
		vs := t.s(t.info.value).bold()
		if row.warn {
			vs = t.s(t.info.warn).bold()
		}
		p.sub(info).text(x, r.y+i, ellipsize(row.value, max(info.right()-x, 1)), vs)
	}

	a.drawMenu(p, menu)

	if logo.w > 0 {
		st := t.s(a.logoColor()).bold()
		for i, line := range logoArt {
			lp := p.sub(logo)
			x := logo.right() - logoWidth() - 1
			// Wordmark in white, honeycomb in the logo colour.
			if i == 1 || i == 2 {
				cut := strings.Index(line, "  ") + 2
				hc := line[:cut]
				lp.text(x, r.y+i, hc, st)
				lp.text(x+textWidth(hc), r.y+i, line[cut:], t.s(cWhite).bold())
				continue
			}
			lp.text(x, r.y+i, line, st)
		}
	}
}

// logoColor flashes the logo with the level of the current flash message,
// as k9s does.
func (a *app) logoColor() color {
	t := a.th
	if a.flashMsg.text != "" && time.Now().Before(a.flashMsg.until) {
		switch a.flashMsg.level {
		case flashErr:
			return t.flash.err
		case flashWarn:
			return t.flash.warn
		}
	}
	return t.body.logo
}

// menuHint is a hint as shown in the menu.
type menuHint struct {
	label, desc string
	num         bool
}

// menuHints gathers the header menu: numbered keys first (the stack
// quick-switch keys, the log ranges), then the view's visible bindings
// sorted by description, like k9s.
func (a *app) menuHints() []menuHint {
	var out []menuHint
	if tv, ok := a.view().(*tableView); ok && tv.scopeActive() {
		out = append(out, menuHint{label: "<0>", desc: "all", num: true})
		for i, s := range a.favStacks() {
			if i >= 9 {
				break
			}
			out = append(out, menuHint{label: fmt.Sprintf("<%d>", i+1), desc: s, num: true})
		}
	}
	var hs, nums []hint
	seen := map[string]bool{}
	for _, h := range a.view().hints() {
		if !h.show || seen[h.key] {
			continue
		}
		seen[h.key] = true
		if len(h.key) == 1 && h.key[0] >= '0' && h.key[0] <= '9' {
			nums = append(nums, h)
		} else {
			hs = append(hs, h)
		}
	}
	if !seen["?"] {
		hs = append(hs, hint{key: "?", desc: "Help", show: true})
	}
	sort.SliceStable(nums, func(i, j int) bool { return nums[i].key < nums[j].key })
	for _, h := range nums {
		out = append(out, menuHint{label: h.label(), desc: h.desc, num: true})
	}
	sort.SliceStable(hs, func(i, j int) bool {
		return strings.ToLower(hs[i].desc) < strings.ToLower(hs[j].desc)
	})
	for _, h := range hs {
		out = append(out, menuHint{label: h.label(), desc: h.desc})
	}
	return out
}

// drawMenu lays the hints out in columns of up to six, filling a column
// before starting the next, and drops what does not fit.
func (a *app) drawMenu(p painter, r rect) {
	if r.w < 8 {
		return
	}
	t := a.th
	hs := a.menuHints()
	rows := min(headerHeight-1, r.h)
	x := r.x + 1
	for start := 0; start < len(hs); start += rows {
		col := hs[start:min(start+rows, len(hs))]
		keyW, descW := 0, 0
		for _, h := range col {
			keyW = max(keyW, textWidth(h.label))
			descW = max(descW, textWidth(h.desc))
		}
		colW := keyW + 1 + descW
		if x+colW > r.right() {
			if x == r.x+1 { // even the first column does not fit: clip it
				colW = r.right() - x
			} else {
				return
			}
		}
		cp := p.sub(rect{x, r.y, colW, r.h})
		for i, h := range col {
			ks := t.s(t.frame.menu.key).bold()
			if h.num {
				ks = t.s(t.frame.menu.numKey).bold()
			}
			cp.text(x, r.y+i, h.label, ks)
			cp.text(x+keyW+1, r.y+i, h.desc, t.s(t.frame.menu.fg))
		}
		x += colW + 3
	}
}

// drawCrumbs paints the view stack as <crumb> boxes, the last one active.
func (a *app) drawCrumbs(p painter, r rect) {
	t := a.th
	x := r.x + 1
	for i, v := range a.stack {
		bg := t.frame.crumbs.bg
		if i == len(a.stack)-1 {
			bg = t.frame.crumbs.active
		}
		text := " <" + v.crumb() + "> "
		st := style{fg: t.frame.crumbs.fg, bg: bg, attrs: attrBold}
		x += p.text(x, r.y, text, st) + 1
	}
}

// ---- flash ----

type flashState struct {
	text  string
	level flashLevel
	until time.Time
	seq   int
}

const flashDuration = 3 * time.Second

func (a *app) flashIcon(l flashLevel) string {
	if a.noIcons {
		return ""
	}
	switch l {
	case flashWarn:
		return "😗  "
	case flashErr:
		return "😡  "
	}
	return "😎  "
}

func (a *app) drawFlash(p painter, r rect) {
	if a.flashMsg.text == "" || !time.Now().Before(a.flashMsg.until) {
		return
	}
	t := a.th
	col := t.flash.info
	switch a.flashMsg.level {
	case flashWarn:
		col = t.flash.warn
	case flashErr:
		col = t.flash.err
	}
	msg := strings.ReplaceAll(a.flashMsg.text, "\n", " ")
	text := a.flashIcon(a.flashMsg.level) + msg
	text = ellipsize(text, max(r.w-2, 1))
	x := r.x + (r.w-textWidth(text))/2
	p.text(x, r.y, text, t.s(col))
}

// ---- helpers ----

// scopeLabel names the active stack for messages.
func scopeLabel(scope string) string {
	if scope == "" {
		return "all"
	}
	return scope
}
