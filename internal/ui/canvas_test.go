package ui

import (
	"strings"
	"testing"

	"github.com/muesli/termenv"
)

func tc(w, h int) *canvas {
	return newCanvas(w, h, style{fg: noColor, bg: noColor}, termenv.TrueColor)
}

func TestCanvasTextAndClipping(t *testing.T) {
	c := tc(10, 2)
	p := c.painter()
	if n := p.text(7, 0, "hello", style{}); n != 5 {
		t.Errorf("text reports width %d, want the full 5 even when clipped", n)
	}
	p.text(0, 1, "abc", style{})
	got := c.Text()
	if got != "       hel\nabc" {
		t.Errorf("canvas = %q", got)
	}
	// Drawing outside the canvas or the clip must be harmless.
	p.text(-3, -1, "zzz", style{})
	p.sub(rect{0, 1, 2, 1}).text(0, 1, "XYZ", style{})
	if got := c.Text(); got != "       hel\nXYc" {
		t.Errorf("clipped draw = %q", got)
	}
}

func TestCanvasWideCharacters(t *testing.T) {
	c := tc(8, 1)
	p := c.painter()
	p.text(0, 0, "a🐳b", style{})
	if got := c.Text(); got != "a🐳b" {
		t.Fatalf("got %q", got)
	}
	// A wide character takes two cells: b is at column 3.
	if c.cells[3].g != "b" || c.cells[2].w != 0 || c.cells[1].w != 2 {
		t.Errorf("cell layout wrong: %+v", c.cells[:4])
	}
	// Overwriting its right half must blank the left half instead of
	// leaving half an emoji behind.
	p.text(2, 0, "X", style{})
	if got := c.Text(); got != "a Xb" {
		t.Errorf("after overwriting the right half: %q, want %q", got, "a Xb")
	}
	// A wide character that does not fit is not drawn at all.
	c2 := tc(3, 1)
	c2.painter().text(2, 0, "🐳", style{})
	if got := c2.Text(); strings.Contains(got, "🐳") {
		t.Errorf("a wide character was drawn across the edge: %q", got)
	}
}

func TestCanvasFrameTitleIsCentred(t *testing.T) {
	c := tc(30, 3)
	c.painter().frame(rect{0, 0, 30, 3}, style{}, []seg{{"title", style{}}})
	lines := strings.Split(c.Text(), "\n")
	// 28 border cells, title plus two spaces is 7 wide: 10 dashes, then 11.
	want := "┌" + strings.Repeat("─", 10) + " title " + strings.Repeat("─", 11) + "┐"
	if lines[0] != want {
		t.Errorf("top border = %q, want %q", lines[0], want)
	}
	if lines[1] != "│"+strings.Repeat(" ", 28)+"│" || lines[2] != "└"+strings.Repeat("─", 28)+"┘" {
		t.Errorf("frame body: %q / %q", lines[1], lines[2])
	}
}

func TestCanvasFrameTitleIsTruncated(t *testing.T) {
	c := tc(16, 3)
	c.painter().frame(rect{0, 0, 16, 3}, style{}, []seg{{"a-very-long-title-indeed", style{}}})
	top := strings.Split(c.Text(), "\n")[0]
	if textWidth(top) != 16 || !strings.Contains(top, "…") || !strings.HasPrefix(top, "┌") || !strings.HasSuffix(top, "┐") {
		t.Errorf("top border = %q", top)
	}
}

func TestCanvasStringStylesEveryRow(t *testing.T) {
	c := newCanvas(4, 2, style{fg: cWhite, bg: cBlack}, termenv.TrueColor)
	c.painter().text(0, 0, "ab", style{fg: cAqua, bg: cBlack, attrs: attrBold})
	out := c.String()
	rows := strings.Split(out, "\n")
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	for i, r := range rows {
		if !strings.HasPrefix(r, "\x1b[0;") || !strings.HasSuffix(r, "\x1b[0m") {
			t.Errorf("row %d is not fully styled/reset: %q", i, r)
		}
		if !strings.Contains(r, "48;2;0;0;0") {
			t.Errorf("row %d does not paint the background: %q", i, r)
		}
	}
	if !strings.Contains(rows[0], "1;38;2;0;255;255") { // bold + aqua
		t.Errorf("styled run missing: %q", rows[0])
	}
}

func TestCanvasPaletteColoursFollowTheTerminalTheme(t *testing.T) {
	c := tc(3, 1)
	c.painter().text(0, 0, "a", style{fg: palette(2), bg: palette(9)})
	c.painter().text(1, 0, "b", style{fg: palette(200), bg: noColor})
	out := c.String()
	for _, want := range []string{"32;", "101", "38;5;200"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

func TestEllipsizeAndPadding(t *testing.T) {
	if got := ellipsize("monitoring_node-exporter", 10); got != "monitorin…" {
		t.Errorf("ellipsize = %q", got)
	}
	if got := ellipsize("short", 10); got != "short" {
		t.Errorf("ellipsize short = %q", got)
	}
	if got := padRight("ab", 5); got != "ab   " {
		t.Errorf("padRight = %q", got)
	}
}

func TestRectIntersect(t *testing.T) {
	r := rect{2, 2, 6, 4}.intersect(rect{5, 0, 10, 4})
	if r != (rect{5, 2, 3, 2}) {
		t.Errorf("intersect = %+v", r)
	}
	if got := (rect{0, 0, 2, 2}).intersect(rect{5, 5, 2, 2}); got.w != 0 || got.h != 0 {
		t.Errorf("disjoint rects must intersect to nothing, got %+v", got)
	}
}
