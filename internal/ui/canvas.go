package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/rivo/uniseg"
)

// The UI is drawn on a canvas: a grid of styled cells the size of the
// terminal. Every frame is composed on it and serialised once. That makes
// framed boxes with titles in their borders, full-width cursor bars, modal
// overlays and a fully painted background exact, instead of the string
// concatenation that breaks as soon as an overlay or a wide character
// shows up.

// color is a 24-bit RGB value, noColor for "terminal default", or a palette
// index (paletteBase+n) that follows the user's terminal theme.
type color int32

const (
	noColor     color = -1
	paletteBase color = 0x1000000
)

func palette(n int) color { return paletteBase + color(n) }

type attr uint8

const (
	attrBold attr = 1 << iota
	attrDim
	attrItalic
	attrUnderline
	attrReverse
)

// style is the look of one cell.
type style struct {
	fg, bg color
	attrs  attr
}

func (s style) withFg(c color) style { s.fg = c; return s }
func (s style) withBg(c color) style { s.bg = c; return s }
func (s style) bold() style          { s.attrs |= attrBold; return s }
func (s style) dim() style           { s.attrs |= attrDim; return s }
func (s style) underline() style     { s.attrs |= attrUnderline; return s }
func (s style) reverse() style       { s.attrs |= attrReverse; return s }

// fgStyle is a convenience for "this colour on the default background".
func fgStyle(c color) style { return style{fg: c, bg: noColor} }

// cell is one terminal cell. A wide character occupies a left cell (w=2)
// and a right cell (w=0, empty text).
type cell struct {
	g  string
	st style
	w  uint8
}

type rect struct{ x, y, w, h int }

func (r rect) right() int  { return r.x + r.w }
func (r rect) bottom() int { return r.y + r.h }

func (r rect) inset(n int) rect {
	r.x += n
	r.y += n
	r.w -= 2 * n
	r.h -= 2 * n
	if r.w < 0 {
		r.w = 0
	}
	if r.h < 0 {
		r.h = 0
	}
	return r
}

func (r rect) intersect(o rect) rect {
	x, y := max(r.x, o.x), max(r.y, o.y)
	right, bottom := min(r.right(), o.right()), min(r.bottom(), o.bottom())
	if right < x || bottom < y {
		return rect{x: x, y: y}
	}
	return rect{x, y, right - x, bottom - y}
}

func (r rect) contains(x, y int) bool {
	return x >= r.x && x < r.right() && y >= r.y && y < r.bottom()
}

type canvas struct {
	w, h    int
	cells   []cell
	base    style
	profile termenv.Profile
	sgrs    map[style]string
}

func newCanvas(w, h int, base style, profile termenv.Profile) *canvas {
	c := &canvas{w: max(w, 0), h: max(h, 0), base: base, profile: profile, sgrs: map[style]string{}}
	c.cells = make([]cell, c.w*c.h)
	blank := cell{g: " ", st: base, w: 1}
	for i := range c.cells {
		c.cells[i] = blank
	}
	return c
}

func (c *canvas) painter() painter { return painter{c: c, clip: rect{0, 0, c.w, c.h}} }

// set writes one grapheme, repairing any wide character it overlaps.
func (c *canvas) set(x, y int, g string, w int, st style) {
	i := y*c.w + x
	old := c.cells[i]
	if old.w == 0 && x > 0 { // overwriting the right half of a wide char
		c.cells[i-1] = cell{g: " ", st: c.cells[i-1].st, w: 1}
	}
	if old.w == 2 && x+1 < c.w { // overwriting the left half
		c.cells[i+1] = cell{g: " ", st: c.cells[i+1].st, w: 1}
	}
	c.cells[i] = cell{g: g, st: st, w: uint8(w)}
	if w == 2 && x+1 < c.w {
		if c.cells[i+1].w == 2 && x+2 < c.w {
			c.cells[i+2] = cell{g: " ", st: c.cells[i+2].st, w: 1}
		}
		c.cells[i+1] = cell{st: st, w: 0}
	}
}

// painter draws on a canvas, clipped to a rectangle.
type painter struct {
	c    *canvas
	clip rect
}

func (p painter) sub(r rect) painter { return painter{c: p.c, clip: p.clip.intersect(r)} }

func (p painter) put(x, y int, g string, w int, st style) {
	if !p.clip.contains(x, y) || x+w > p.clip.right() {
		return
	}
	p.c.set(x, y, g, w, st)
}

// text draws plain text without wrapping and returns its display width,
// whether or not all of it was visible, so callers can lay out neighbours.
func (p painter) text(x, y int, s string, st style) int {
	cx := x
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		str := g.Str()
		switch r := []rune(str)[0]; {
		case r == '\t':
			for i := 0; i < 4; i++ {
				p.put(cx, y, " ", 1, st)
				cx++
			}
			continue
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
			continue
		}
		w := g.Width()
		if w == 0 {
			continue
		}
		p.put(cx, y, str, w, st)
		cx += w
	}
	return cx - x
}

// seg is a run of text in one style.
type seg struct {
	s  string
	st style
}

func segsWidth(ss []seg) int {
	n := 0
	for _, s := range ss {
		n += textWidth(s.s)
	}
	return n
}

func (p painter) segs(x, y int, ss []seg) int {
	cx := x
	for _, s := range ss {
		cx += p.text(cx, y, s.s, s.st)
	}
	return cx - x
}

func (p painter) fill(r rect, st style) {
	r = p.clip.intersect(r)
	for y := r.y; y < r.bottom(); y++ {
		for x := r.x; x < r.right(); x++ {
			p.c.set(x, y, " ", 1, st)
		}
	}
}

// restyle changes the style of existing cells without touching their text,
// which is how cursor bars and marks are painted over a row.
func (p painter) restyle(r rect, f func(style) style) {
	r = p.clip.intersect(r)
	for y := r.y; y < r.bottom(); y++ {
		for x := r.x; x < r.right(); x++ {
			ce := &p.c.cells[y*p.c.w+x]
			ce.st = f(ce.st)
		}
	}
}

func (p painter) hline(x, y, n int, g string, st style) {
	for i := 0; i < n; i++ {
		p.put(x+i, y, g, 1, st)
	}
}

func (p painter) vline(x, y, n int, g string, st style) {
	for i := 0; i < n; i++ {
		p.put(x, y+i, g, 1, st)
	}
}

// frame draws a thin border around r. If title is not empty it is centred
// in the top border with a space of padding on each side, like k9s.
func (p painter) frame(r rect, border style, title []seg) {
	if r.w < 2 || r.h < 2 {
		return
	}
	p.hline(r.x+1, r.y, r.w-2, "─", border)
	p.hline(r.x+1, r.bottom()-1, r.w-2, "─", border)
	p.vline(r.x, r.y+1, r.h-2, "│", border)
	p.vline(r.right()-1, r.y+1, r.h-2, "│", border)
	p.put(r.x, r.y, "┌", 1, border)
	p.put(r.right()-1, r.y, "┐", 1, border)
	p.put(r.x, r.bottom()-1, "└", 1, border)
	p.put(r.right()-1, r.bottom()-1, "┘", 1, border)

	if len(title) == 0 {
		return
	}
	room := r.w - 4
	if room < 1 {
		return
	}
	title = clipSegs(title, room)
	tw := segsWidth(title)
	x := r.x + 1 + (r.w-2-(tw+2))/2
	p.text(x, r.y, " ", border)
	p.segs(x+1, r.y, title)
	p.text(x+1+tw, r.y, " ", border)
}

// clipSegs shortens a run of segments to fit a width, marking the cut with
// an ellipsis.
func clipSegs(ss []seg, w int) []seg {
	if segsWidth(ss) <= w {
		return ss
	}
	var out []seg
	used := 0
	for _, s := range ss {
		sw := textWidth(s.s)
		if used+sw <= w-1 {
			out = append(out, s)
			used += sw
			continue
		}
		out = append(out, seg{ansi.Truncate(s.s, max(w-used, 1), "…"), s.st})
		break
	}
	return out
}

func textWidth(s string) int { return uniseg.StringWidth(s) }

// ellipsize shortens plain text to a display width.
func ellipsize(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if textWidth(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

func padRight(s string, w int) string {
	if n := w - textWidth(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// String serialises the canvas, one line per row, every row fully styled.
func (c *canvas) String() string {
	var b strings.Builder
	b.Grow(c.w * c.h * 3)
	for y := 0; y < c.h; y++ {
		if y > 0 {
			b.WriteByte('\n')
		}
		var cur style
		first := true
		for x := 0; x < c.w; x++ {
			ce := c.cells[y*c.w+x]
			if ce.w == 0 {
				continue
			}
			if first || ce.st != cur {
				b.WriteString(c.sgr(ce.st))
				cur, first = ce.st, false
			}
			b.WriteString(ce.g)
		}
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// Text returns the canvas as plain text, for tests and --render.
func (c *canvas) Text() string {
	lines := make([]string, c.h)
	for y := 0; y < c.h; y++ {
		var b strings.Builder
		for x := 0; x < c.w; x++ {
			if ce := c.cells[y*c.w+x]; ce.w != 0 {
				b.WriteString(ce.g)
			}
		}
		lines[y] = strings.TrimRight(b.String(), " ")
	}
	return strings.Join(lines, "\n")
}

// sgr builds (and caches) the escape sequence that selects a style.
func (c *canvas) sgr(st style) string {
	if s, ok := c.sgrs[st]; ok {
		return s
	}
	parts := []string{"0"}
	if st.attrs&attrBold != 0 {
		parts = append(parts, "1")
	}
	if st.attrs&attrDim != 0 {
		parts = append(parts, "2")
	}
	if st.attrs&attrItalic != 0 {
		parts = append(parts, "3")
	}
	if st.attrs&attrUnderline != 0 {
		parts = append(parts, "4")
	}
	if st.attrs&attrReverse != 0 {
		parts = append(parts, "7")
	}
	if p := c.colorParam(st.fg, false); p != "" {
		parts = append(parts, p)
	}
	if p := c.colorParam(st.bg, true); p != "" {
		parts = append(parts, p)
	}
	s := "\x1b[" + strings.Join(parts, ";") + "m"
	c.sgrs[st] = s
	return s
}

func (c *canvas) colorParam(col color, bg bool) string {
	switch {
	case col == noColor:
		return ""
	case col >= paletteBase:
		n := int(col - paletteBase)
		base := 30
		if bg {
			base = 40
		}
		switch {
		case n < 8:
			return strconv.Itoa(base + n)
		case n < 16:
			return strconv.Itoa(base + 60 + n - 8)
		}
		if bg {
			return "48;5;" + strconv.Itoa(n)
		}
		return "38;5;" + strconv.Itoa(n)
	}
	// termenv downgrades to the 256 or 16 colour palette as needed. It answers
	// nil for a value it cannot parse, which must not take the UI down.
	tc := c.profile.Color(fmt.Sprintf("#%06x", int(col)&0xffffff))
	if tc == nil {
		return ""
	}
	return tc.Sequence(bg)
}
