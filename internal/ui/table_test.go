package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"swarmlens/internal/api"
)

// withColors enables truecolor rendering for the duration of a test.
func withColors(t *testing.T) {
	t.Helper()
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(termenv.Ascii)
		lipgloss.SetHasDarkBackground(false)
	})
}

// TestTableRowsRenderIdentically is the regression test for the colour
// leak that made some rows render in the terminal's default colour while
// others inherited the dim colour of the previous row.
//
// The cause was bubbles' table truncating cell values byte-wise, which cut
// ANSI escape sequences in half. A broken escape leaves the terminal's
// colour state set, and plain cells inherit it from the previous row.
func TestTableRowsRenderIdentically(t *testing.T) {
	withColors(t)

	a := newTestApp()
	a.screen = screenVolumes

	var rows []api.VolumeRow
	for i := 0; i < 25; i++ {
		rows = append(rows, api.VolumeRow{
			Name:    "volume-" + strings.Repeat("x", 50),
			Driver:  "local",
			Scope:   "local",
			Created: "2025-06-01",
		})
	}
	a.volumes.SetRows(volumeTableRows(rows))
	a.volumes.SetCursor(12)
	a.resize()

	var seen []string
	for _, line := range strings.Split(a.View(), "\n") {
		if !strings.Contains(line, "volume-") {
			continue
		}
		seen = append(seen, line)
	}
	if len(seen) < 20 {
		t.Fatalf("expected the rows to be rendered, got %d", len(seen))
	}

	// Every row except the selected one must be byte-identical.
	for i, line := range seen {
		if i == 12 {
			continue // selected row carries the highlight background
		}
		if line != seen[0] {
			t.Errorf("row %d renders differently from row 0:\n row 0: %q\n row %d: %q", i, seen[0], i, line)
		}
	}

	// The selected row must be distinguishable.
	if seen[12] == seen[0] {
		t.Error("the selected row is not visually distinguishable")
	}
}

// TestTableDoesNotTruncateStyledCells makes sure a styled cell keeps its
// escape sequences and its full text.
func TestTableDoesNotTruncateStyledCells(t *testing.T) {
	withColors(t)

	styled := dimStyle.Render("2025-06-01")
	got := fitStyled(dimStyle, "2025-06-01", 26)
	if !strings.Contains(got, "2025-06-01") {
		t.Errorf("styled cell lost text: %q", got)
	}
	if !strings.Contains(got, "\x1b[") {
		t.Errorf("styled cell lost its escape sequences: %q", got)
	}
	if lipgloss.Width(got) != 26 {
		t.Errorf("styled cell width = %d, want 26", lipgloss.Width(got))
	}
	_ = styled
}

// TestTableTruncatesLongTextWithEllipsis checks the visible behaviour for
// content that does not fit.
func TestTableTruncatesLongTextWithEllipsis(t *testing.T) {
	withColors(t)

	long := strings.Repeat("a", 100)
	got := fit(long, 10)
	if lipgloss.Width(got) != 10 {
		t.Errorf("width = %d, want 10", lipgloss.Width(got))
	}
	if !strings.HasSuffix(strings.TrimSpace(got), "…") {
		t.Errorf("expected an ellipsis, got %q", got)
	}
}

// TestTableEndsCellsWithReset guards against colour state leaking into the
// next row: every cell must terminate its own styling.
func TestTableEndsCellsWithReset(t *testing.T) {
	withColors(t)

	a := newTestApp()
	a.screen = screenVolumes
	rows := []api.VolumeRow{
		{Name: "aaa", Driver: "local", Scope: "local", Created: "2025-06-01"},
		{Name: "bbb", Driver: "local", Scope: "local", Created: "2025-06-01"},
	}
	a.volumes.SetRows(volumeTableRows(rows))
	a.resize()

	for _, line := range strings.Split(a.View(), "\n") {
		if !strings.Contains(line, "aaa") && !strings.Contains(line, "bbb") {
			continue
		}
		if !strings.Contains(line, "\x1b[0m") {
			t.Errorf("row does not end with a reset: %q", line)
		}
	}
}

// TestTableSelectedRowBackground checks that the selected row is
// highlighted with a background.
func TestTableSelectedRowBackground(t *testing.T) {
	withColors(t)

	a := newTestApp()
	a.screen = screenVolumes
	rows := []api.VolumeRow{
		{Name: "aaa", Driver: "local", Scope: "local", Created: "2025-06-01"},
		{Name: "bbb", Driver: "local", Scope: "local", Created: "2025-06-01"},
	}
	a.volumes.SetRows(volumeTableRows(rows))
	a.volumes.SetCursor(0)
	a.resize()

	lines := strings.Split(a.View(), "\n")
	var selected string
	for _, line := range lines {
		if strings.Contains(line, "aaa") {
			selected = line
		}
	}
	if !strings.Contains(selected, "48;2;") {
		t.Errorf("selected row has no background highlight: %q", selected)
	}
}

// TestViewFitsTerminalHeight is the regression test for the layout
// regression: the table renders a header and a separator in addition to
// its rows, so the total must still fit the terminal height — otherwise
// the view scrolls, the status bar disappears and overlays land in the
// wrong place.
func TestViewFitsTerminalHeight(t *testing.T) {
	for _, h := range []int{14, 20, 24, 40} {
		a := newTestApp()
		a.width, a.height = 100, h
		a.screen = screenVolumes

		var rows []api.VolumeRow
		for i := 0; i < 50; i++ {
			rows = append(rows, api.VolumeRow{
				Name:    "volume-" + strings.Repeat("y", 40),
				Driver:  "local",
				Scope:   "local",
				Created: "2025-06-01",
			})
		}
		a.volumes.SetRows(volumeTableRows(rows))
		a.resize()

		lines := strings.Split(a.View(), "\n")
		// The view may end with a trailing newline, which renders as an
		// extra empty line.
		n := len(lines)
		if n > 0 && lines[n-1] == "" {
			n--
		}
		if n > h {
			t.Errorf("view is %d lines tall but the terminal is %d lines", n, h)
		}
	}
}

// TestOverlayIsNotClipped checks that an overlay taller than the base view
// is rendered in full rather than cut off.
func TestOverlayIsNotClipped(t *testing.T) {
	base := strings.Repeat("base line\n", 3)
	box := overlayBox.Width(20).Render("one\ntwo\nthree\nfour")

	out := placeOverlay(base, box, 60, 12)
	lines := strings.Split(out, "\n")
	if len(lines) < 6 {
		t.Fatalf("overlay was clipped: only %d lines", len(lines))
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"one", "two", "three", "four"} {
		if !strings.Contains(joined, want) {
			t.Errorf("overlay lost the line %q", want)
		}
	}
}

// TestOverlayFitsShortTerminal checks that an overlay never makes the view
// taller than the terminal.
func TestOverlayFitsShortTerminal(t *testing.T) {
	base := strings.Repeat("base line\n", 4)
	box := overlayBox.Width(20).Render(strings.Repeat("x\n", 40))

	out := placeOverlay(base, box, 60, 10)
	lines := strings.Split(out, "\n")
	if len(lines) > 10 {
		t.Errorf("overlay made the view %d lines tall in a 10 line terminal", len(lines))
	}
}

// TestTruncateVisibleIsExact checks the helper the overlay relies on.
func TestTruncateVisibleIsExact(t *testing.T) {
	for _, w := range []int{0, 1, 5, 15, 40} {
		got := truncateVisible(" 1736c7a1259e3faaa31345c08c877387e", w)
		if w == 0 {
			if got != "" {
				t.Errorf("width 0 should be empty, got %q", got)
			}
			continue
		}
		if lipgloss.Width(got) != w {
			t.Errorf("width = %d, want %d", lipgloss.Width(got), w)
		}
	}
	// Styled input keeps its escape sequences.
	got := truncateVisible("\x1b[38;2;242;242;242m 1736c7a\x1b[0m", 10)
	if !strings.Contains(got, "\x1b[38;2;242;242;242m") {
		t.Errorf("escape sequence lost: %q", got)
	}
	if lipgloss.Width(got) != 10 {
		t.Errorf("width = %d, want 10", lipgloss.Width(got))
	}
}

// TestArrowKeysMoveTheCursor checks the navigation keys reach the table.
func TestArrowKeysMoveTheCursor(t *testing.T) {
	a := newTestApp()
	a.screen = screenVolumes
	var rows []api.VolumeRow
	for i := 0; i < 10; i++ {
		rows = append(rows, api.VolumeRow{Name: fmt.Sprintf("volume-%d", i), Driver: "local", Scope: "local", Created: "2025-06-01"})
	}
	a.volumes.SetRows(volumeTableRows(rows))
	a.resize()

	if got := a.volumes.Cursor(); got != 0 {
		t.Fatalf("initial cursor = %d, want 0", got)
	}

	down := tea.KeyMsg{Type: tea.KeyDown}
	up := tea.KeyMsg{Type: tea.KeyUp}

	a.volumes, _ = a.volumes.Update(down)
	if got := a.volumes.Cursor(); got != 1 {
		t.Errorf("after down, cursor = %d, want 1", got)
	}
	a.volumes, _ = a.volumes.Update(down)
	a.volumes, _ = a.volumes.Update(down)
	if got := a.volumes.Cursor(); got != 3 {
		t.Errorf("after 3 downs, cursor = %d, want 3", got)
	}
	a.volumes, _ = a.volumes.Update(up)
	if got := a.volumes.Cursor(); got != 2 {
		t.Errorf("after up, cursor = %d, want 2", got)
	}

	// The cursor must not run past the ends.
	for i := 0; i < 20; i++ {
		a.volumes, _ = a.volumes.Update(down)
	}
	if got := a.volumes.Cursor(); got != len(rows)-1 {
		t.Errorf("cursor = %d, want %d (last row)", got, len(rows)-1)
	}
	for i := 0; i < 20; i++ {
		a.volumes, _ = a.volumes.Update(up)
	}
	if got := a.volumes.Cursor(); got != 0 {
		t.Errorf("cursor = %d, want 0 (first row)", got)
	}
}
