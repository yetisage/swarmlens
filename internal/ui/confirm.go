package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// confirm is a modal confirmation prompt for destructive actions.
type confirm struct {
	active bool
	title  string
	detail string
	onYes  func(a *app) (tea.Model, tea.Cmd)
}

// ask opens the confirmation modal.
func (c *confirm) ask(title, detail string, onYes func(a *app) (tea.Model, tea.Cmd)) {
	c.active = true
	c.title = title
	c.detail = detail
	c.onYes = onYes
}

// handleConfirmKey processes input while the confirmation modal is open.
func (a *app) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		cb := a.confirm.onYes
		a.confirm.active = false
		if cb != nil {
			return cb(a)
		}
		return a, nil
	case "n", "N", "esc", "q":
		a.confirm.active = false
		a.setStatus("cancelled")
		return a, nil
	}
	return a, nil
}

// overlayConfirm draws the confirmation modal on top of the base view.
func (a *app) overlayConfirm(base string) string {
	body := titleStyle.Render(a.confirm.title) + "\n" +
		dimStyle.Render(a.confirm.detail) + "\n\n" +
		warnStyle.Render("y") + dimStyle.Render(" confirm   ") +
		keyStyle.Render("n") + dimStyle.Render(" cancel")
	box := overlayBox.Width(minInt(a.width-8, 70)).Render(body)
	return placeOverlay(base, box, a.width, a.height)
}

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
