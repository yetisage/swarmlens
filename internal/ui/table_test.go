package ui

import (
	"strings"
	"testing"

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
