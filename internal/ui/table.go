package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// column describes one table column.
type column struct {
	Title string
	Width int
	// Style is applied to every cell in this column (may be the zero value).
	Style lipgloss.Style
}

// dataTable renders a simple, ANSI-aware table.
//
// It replaces bubbles' table because that implementation truncates cell
// values byte-wise, which cuts ANSI escape sequences in half. A broken
// escape leaves the terminal's colour state set, and because plain cells
// carry no escape of their own they inherit it from the previous row —
// which made most rows render dim while the first row and the row below
// the cursor rendered in the default colour. Truncating and padding here
// are ANSI-aware, so every cell ends with a reset and nothing leaks.
type dataTable struct {
	cols   []column
	rows   [][]string
	cursor int
	height int
	width  int
}

func newDataTable(cols ...column) dataTable {
	return dataTable{cols: cols, height: 20, width: 80}
}

// SetColumns replaces the column definitions.
func (t *dataTable) SetColumns(cols []column) { t.cols = cols }

// SetRows replaces the table content.
func (t *dataTable) SetRows(rows [][]string) {
	t.rows = rows
	if t.cursor >= len(rows) {
		t.cursor = max(0, len(rows)-1)
	}
}

// Rows returns the current rows.
func (t dataTable) Rows() [][]string { return t.rows }

// Cursor returns the index of the selected row.
func (t dataTable) Cursor() int { return t.cursor }

// SetCursor moves the selection.
func (t *dataTable) SetCursor(n int) {
	if n < 0 {
		n = 0
	}
	if n >= len(t.rows) {
		n = max(0, len(t.rows)-1)
	}
	t.cursor = n
}

// SetWidth sets the total render width.
func (t *dataTable) SetWidth(w int) { t.width = w }

// SetHeight sets the total height of the table, including the header and
// the separator line.
func (t *dataTable) SetHeight(h int) { t.height = h }

// chrome is the number of lines the table spends on its header and
// separator.
const tableChrome = 2

// rowsHeight is the height available for data rows.
func (t dataTable) rowsHeight() int {
	h := t.height - tableChrome
	if h < 1 {
		h = 1
	}
	return h
}

// Update handles cursor movement keys.
func (t dataTable) Update(msg tea.KeyMsg) (dataTable, tea.Cmd) {
	page := max(1, t.rowsHeight())
	switch msg.String() {
	case "up":
		t.SetCursor(t.cursor - 1)
	case "down":
		t.SetCursor(t.cursor + 1)
	case "pgup":
		t.SetCursor(t.cursor - page)
	case "pgdown":
		t.SetCursor(t.cursor + page)
	case "home":
		t.SetCursor(0)
	case "end":
		t.SetCursor(len(t.rows) - 1)
	}
	return t, nil
}

// totalWidth is the sum of the column widths plus the gaps between them.
func (t dataTable) totalWidth() int {
	w := 0
	for _, c := range t.cols {
		w += c.Width
	}
	if len(t.cols) > 1 {
		w += 2 * (len(t.cols) - 1)
	}
	return w
}

// View renders the table.
func (t dataTable) View() string {
	var b strings.Builder

	// Header.
	hdr := make([]string, 0, len(t.cols))
	for _, c := range t.cols {
		hdr = append(hdr, headerStyle.Render(fit(c.Title, c.Width)))
	}
	b.WriteString(strings.Join(hdr, "  "))
	b.WriteString("\n")

	sep := t.totalWidth()
	if t.width > 0 && sep > t.width {
		sep = t.width
	}
	b.WriteString(dimStyle.Render(strings.Repeat("─", max(0, sep))))
	b.WriteString("\n")

	// Rows, windowed so the cursor stays visible.
	start, end := t.window()
	for i := start; i < end; i++ {
		b.WriteString(t.renderRow(t.rows[i], i == t.cursor))
		b.WriteString("\n")
	}
	return b.String()
}

// window returns the visible row range.
func (t dataTable) window() (int, int) {
	avail := t.rowsHeight()
	if len(t.rows) <= avail {
		return 0, len(t.rows)
	}
	start := 0
	if t.cursor >= avail {
		start = t.cursor - avail + 1
	}
	if start < 0 {
		start = 0
	}
	end := start + avail
	if end > len(t.rows) {
		end = len(t.rows)
	}
	return start, end
}

// renderRow renders one row, highlighting the selected one.
func (t dataTable) renderRow(row []string, selected bool) string {
	cells := make([]string, 0, len(t.cols))
	for i, c := range t.cols {
		text := ""
		if i < len(row) {
			text = row[i]
		}
		style := c.Style
		if selected {
			style = style.Background(colSelBG)
		}
		cells = append(cells, fitStyled(style, text, c.Width))
	}
	return strings.Join(cells, "  ")
}

// fit truncates plain text to width and pads it to exactly width.
func fit(text string, width int) string {
	if width <= 0 {
		return ""
	}
	return lipgloss.NewStyle().Width(width).Inline(true).Render(truncateANSI(text, width))
}

// fitStyled applies a style to text, then truncates and pads the result
// using ANSI-aware operations so escape sequences survive intact.
func fitStyled(style lipgloss.Style, text string, width int) string {
	if width <= 0 {
		return ""
	}
	styled := style.Render(text)
	styled = truncateANSI(styled, width)
	return lipgloss.NewStyle().Width(width).Inline(true).Render(styled)
}

// truncateANSI shortens a string to a visible width without cutting ANSI
// escape sequences, marking the cut with an ellipsis.
func truncateANSI(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, "…")
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
