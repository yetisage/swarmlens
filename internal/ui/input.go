package ui

import (
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

// input is a single-line text editor, used by the prompt and by dialogs.
type input struct {
	r   []rune
	pos int
}

func (in *input) String() string { return string(in.r) }

func (in *input) set(s string) {
	in.r = []rune(s)
	in.pos = len(in.r)
}

func (in *input) clear() { in.r, in.pos = nil, 0 }

func (in *input) insert(s string) {
	add := []rune(s)
	in.r = append(in.r[:in.pos], append(add, in.r[in.pos:]...)...)
	in.pos += len(add)
}

// handle applies an editing key and reports whether it was one.
func (in *input) handle(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyRunes:
		if msg.Alt {
			return false
		}
		// Control characters (e.g. from a paste) never belong in the field.
		var b strings.Builder
		for _, r := range msg.Runes {
			if unicode.IsPrint(r) {
				b.WriteRune(r)
			}
		}
		in.insert(b.String())
		return true
	case tea.KeySpace:
		in.insert(" ")
		return true
	}
	switch msg.String() {
	case "backspace", "ctrl+h":
		if in.pos > 0 {
			in.r = append(in.r[:in.pos-1], in.r[in.pos:]...)
			in.pos--
		}
	case "delete", "ctrl+d":
		if in.pos < len(in.r) {
			in.r = append(in.r[:in.pos], in.r[in.pos+1:]...)
		}
	case "left", "ctrl+b":
		in.pos = max(in.pos-1, 0)
	case "right", "ctrl+f":
		in.pos = min(in.pos+1, len(in.r))
	case "home", "ctrl+a":
		in.pos = 0
	case "end", "ctrl+e":
		in.pos = len(in.r)
	case "ctrl+w":
		from := in.pos
		for from > 0 && in.r[from-1] == ' ' {
			from--
		}
		for from > 0 && in.r[from-1] != ' ' {
			from--
		}
		in.r = append(in.r[:from], in.r[in.pos:]...)
		in.pos = from
	case "ctrl+k":
		in.r = in.r[:in.pos]
	case "ctrl+u":
		in.clear()
	default:
		return false
	}
	return true
}

// draw paints the text at (x, y) with a block cursor, followed by an
// optional ghost completion when the cursor is at the end.
func (in *input) draw(p painter, x, y int, st, cursor, ghostSt style, ghost string) {
	for i, r := range in.r {
		if i == in.pos {
			break
		}
		x += p.text(x, y, string(r), st)
	}
	// Characters before the cursor were drawn above; the rest follow it.
	if in.pos < len(in.r) {
		x += p.text(x, y, string(in.r[in.pos]), cursor)
		for _, r := range in.r[in.pos+1:] {
			x += p.text(x, y, string(r), st)
		}
		return
	}
	if g := []rune(ghost); len(g) > 0 {
		// The cursor sits on the first character of the suggestion, which
		// keeps the completion flush against what was typed.
		x += p.text(x, y, string(g[0]), cursor)
		p.text(x, y, string(g[1:]), ghostSt)
		return
	}
	p.text(x, y, " ", cursor)
}
