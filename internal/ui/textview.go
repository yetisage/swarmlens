package ui

import (
	"context"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// textView shows a block of text: the describe and YAML views. It scrolls,
// searches ("/", n, N), wraps and can take the whole screen.
type textView struct {
	a    *app
	vid  int
	kind string // "describe" or "yaml": the crumb and the title
	name string // what is being shown, for the title
	load func(ctx context.Context) (string, error)

	lines   []string
	loading bool

	top, left    int
	wrap         bool
	pageH, pageW int

	query   string
	re      *regexp.Regexp
	matches []int // line numbers
	mi      int
}

func newTextView(a *app, kind, name string, load func(ctx context.Context) (string, error)) *textView {
	return &textView{a: a, vid: newViewID(), kind: kind, name: name, load: load, loading: true}
}

func (v *textView) id() int       { return v.vid }
func (v *textView) crumb() string { return v.kind }

func (v *textView) start() tea.Cmd {
	vid, load := v.vid, v.load
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		text, err := load(ctx)
		return textMsg{vid: vid, text: text, err: err}
	}
}

// Text views show a snapshot, like k9s with liveViewAutoRefresh off.
func (v *textView) refresh() tea.Cmd { return nil }
func (v *textView) stop()            {}

func (v *textView) handle(msg tea.Msg) tea.Cmd {
	m, ok := msg.(textMsg)
	if !ok {
		return nil
	}
	v.loading = false
	if m.err != nil {
		v.a.flash(flashErr, m.err.Error())
		return v.a.pop() // nothing to show: the object is probably gone
	}
	v.lines = strings.Split(strings.TrimRight(m.text, "\n"), "\n")
	v.research()
	return nil
}

func (v *textView) title() []seg {
	t := v.a.th
	plain := t.s(t.frame.title.fg)
	segs := []seg{
		{v.kind + "(", plain.bold()},
		{v.name, t.s(t.frame.title.highlight).bold()},
		{")", plain},
	}
	if v.query != "" {
		segs = append(segs,
			seg{" <", plain},
			seg{"/" + v.query, style{fg: t.frame.title.filter, bg: t.body.bg, attrs: attrReverse}},
			seg{">", plain})
	}
	return segs
}

func (v *textView) hints() []hint {
	return []hint{
		{key: "c", desc: "Copy", show: true},
		{key: "n", desc: "Next Match", show: true},
		{key: "N", desc: "Prev Match", show: true},
		{key: "f", desc: "Toggle FullScreen", show: true},
		{key: "w", desc: "Toggle Wrap", show: true},
		{key: "j", desc: "Down"}, {key: "k", desc: "Up"},
		{key: "g", desc: "Top"}, {key: "G", desc: "Bottom"},
		{key: "h", desc: "Left"}, {key: "l", desc: "Right"},
	}
}

// ---- search ----

func (v *textView) setFilter(q string) {
	v.query = q
	v.re = compileFilter(q)
	v.research()
	if len(v.matches) > 0 {
		v.jump(v.firstMatchFrom(v.top))
	}
}

func (v *textView) filterText() string { return v.query }

func (v *textView) research() {
	v.matches = nil
	if v.re == nil {
		return
	}
	for i, l := range v.lines {
		if v.re.MatchString(l) {
			v.matches = append(v.matches, i)
		}
	}
	v.mi = min(v.mi, max(len(v.matches)-1, 0))
}

func (v *textView) firstMatchFrom(line int) int {
	for i, m := range v.matches {
		if m >= line {
			return i
		}
	}
	return 0
}

func (v *textView) jump(i int) {
	if len(v.matches) == 0 {
		return
	}
	v.mi = (i + len(v.matches)) % len(v.matches)
	v.top = v.matches[v.mi]
}

// ---- keys ----

func (v *textView) key(msg tea.KeyMsg) (tea.Cmd, bool) {
	maxTop := max(len(v.lines)-v.pageH, 0)
	switch msg.String() {
	case "j", "down":
		v.top++
	case "k", "up":
		v.top--
	case "ctrl+f", "pgdown":
		v.top += max(v.pageH-1, 1)
	case "ctrl+b", "pgup":
		v.top -= max(v.pageH-1, 1)
	case "g", "home":
		v.top = 0
	case "G", "end":
		v.top = maxTop
	case "h", "left":
		v.left = max(v.left-4, 0)
	case "l", "right":
		if !v.wrap {
			v.left += 4
		}
	case "w":
		v.wrap = !v.wrap
		v.left = 0
	case "f":
		v.a.fullscreen = !v.a.fullscreen
	case "n":
		if len(v.matches) > 0 {
			v.jump(v.mi + 1)
		}
	case "N":
		if len(v.matches) > 0 {
			v.jump(v.mi - 1)
		}
	case "c":
		v.a.copy(strings.Join(v.lines, "\n"))
		v.a.flash(flashInfo, "Content copied to clipboard...")
	case "esc":
		if v.query == "" {
			return nil, false
		}
		v.setFilter("")
	default:
		return nil, false
	}
	v.top = max(0, min(v.top, maxTop))
	return nil, true
}

// ---- drawing ----

func (v *textView) draw(p painter, r rect, focused bool) {
	t := v.a.th
	border := t.s(t.frame.border).bold()
	if focused {
		border = t.s(t.frame.focus).bold()
	}
	p.frame(r, border, v.title())
	inner := r.inset(1)
	content := rect{inner.x + 1, inner.y, max(inner.w-2, 0), inner.h}
	if content.w <= 0 || content.h <= 0 {
		return
	}
	cp := p.sub(content)
	v.pageH, v.pageW = content.h, content.w
	if v.loading {
		cp.text(content.x, content.y, "Loading...", t.s(t.body.fg))
		return
	}

	hl := style{fg: cBlack, bg: t.table.cursorBg}
	current := style{fg: cBlack, bg: cFuchsia}
	maxTop := max(len(v.lines)-v.pageH, 0)
	v.top = max(0, min(v.top, maxTop))

	y := content.y
	for i := v.top; i < len(v.lines) && y < content.bottom(); i++ {
		segs := yamlSegs(v.lines[i], t)
		segs = highlight(segs, v.re, hl)
		if v.wrap {
			for _, row := range wrapSegs(segs, content.w) {
				if y >= content.bottom() {
					break
				}
				cp.segs(content.x, y, row)
				y++
			}
			continue
		}
		cp.segs(content.x, y, cutSegs(segs, v.left, content.w))
		if len(v.matches) > 0 && v.matches[v.mi] == i {
			cp.restyle(rect{content.x, y, content.w, 1}, func(s style) style {
				if s.bg == hl.bg {
					s.bg = current.bg
				}
				return s
			})
		}
		y++
	}
}

// cutSegs keeps the cells [from, from+w) of a run of segments.
func cutSegs(ss []seg, from, w int) []seg {
	var out []seg
	x := 0
	for _, s := range ss {
		sw := textWidth(s.s)
		start, end := x, x+sw
		x = end
		if end <= from || start >= from+w {
			continue
		}
		lo, hi := max(from-start, 0), min(from+w-start, sw)
		if lo == 0 && hi == sw {
			out = append(out, s)
			continue
		}
		out = append(out, seg{ansi.Cut(s.s, lo, hi), s.st})
	}
	return out
}

// wrapSegs breaks a styled line into rows of at most w cells.
func wrapSegs(ss []seg, w int) [][]seg {
	if w <= 0 || segsWidth(ss) <= w {
		return [][]seg{ss}
	}
	total := segsWidth(ss)
	var rows [][]seg
	for from := 0; from < total; from += w {
		rows = append(rows, cutSegs(ss, from, w))
	}
	return rows
}
