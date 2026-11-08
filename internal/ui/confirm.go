package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// placeOverlay centres a box over a base view, truncating the base lines
// where the box is drawn so the overlay stays on screen.
func placeOverlay(base, box string, width, height int) string {
	baseLines := strings.Split(base, "\n")
	boxLines := strings.Split(box, "\n")

	// A box taller than the terminal is clipped rather than pushing the
	// view past the bottom of the screen.
	if height > 0 && len(boxLines) > height {
		boxLines = boxLines[:height]
	}
	boxWidth := lipgloss.Width(box)

	top := (height - len(boxLines)) / 2
	if top < 0 {
		top = 0
	}
	left := (width - boxWidth) / 2
	if left < 0 {
		left = 0
	}

	// The result must be tall enough to hold the whole box.
	total := len(baseLines)
	if need := top + len(boxLines); need > total {
		total = need
	}

	out := make([]string, 0, total)
	for i := 0; i < total; i++ {
		line := ""
		if i < len(baseLines) {
			line = baseLines[i]
		}
		if i >= top && i < top+len(boxLines) {
			out = append(out, truncateVisible(line, left)+boxLines[i-top])
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// truncateVisible cuts a string to a visible width, keeping ANSI escape
// sequences intact so styling is not broken.
func truncateVisible(s string, width int) string {
	if width <= 0 {
		return ""
	}
	var b strings.Builder
	visible := 0
	inEscape := false
	for _, r := range s {
		if inEscape {
			b.WriteRune(r)
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEscape = false
			}
			continue
		}
		if r == 0x1b {
			inEscape = true
			b.WriteRune(r)
			continue
		}
		if visible >= width {
			break
		}
		b.WriteRune(r)
		visible++
	}
	if pad := width - visible; pad > 0 {
		b.WriteString(strings.Repeat(" ", pad))
	}
	return b.String()
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
