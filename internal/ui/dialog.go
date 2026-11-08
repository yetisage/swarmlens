package ui

import (
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

// modal is an overlay that takes all input until it is dismissed.
type modal interface {
	// key handles a key; done reports that the modal should close.
	key(msg tea.KeyMsg) (cmd tea.Cmd, done bool)
	draw(p painter, screen rect, t *theme)
}

type dlgButton struct {
	label string
	run   func() tea.Cmd // nil: just close
}

type dlgField struct {
	label  string
	in     input
	digits bool // accept digits only
}

// dialog is k9s' modal box: a message, optional input fields and a row of
// buttons. Confirmations, forms and errors are all this one widget.
type dialog struct {
	title   string
	lines   []string // message, one entry per line
	art     []string // pre-formatted lines drawn as a block below the message
	isError bool
	fields  []*dlgField
	buttons []dlgButton
	focus   int // fields first, then buttons
}

func (d *dialog) focusable() int { return len(d.fields) + len(d.buttons) }

func (d *dialog) key(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "esc":
		return nil, true
	case "tab", "right", "down":
		if d.inField() && msg.String() != "tab" && msg.String() != "down" {
			break // arrows edit the text while a field has focus
		}
		d.focus = (d.focus + 1) % d.focusable()
		return nil, false
	case "shift+tab", "left", "up":
		if d.inField() && msg.String() != "shift+tab" && msg.String() != "up" {
			break
		}
		d.focus = (d.focus - 1 + d.focusable()) % d.focusable()
		return nil, false
	case "enter":
		if d.inField() { // enter in a field submits, like pressing the first button
			return d.press(0)
		}
		return d.press(d.focus - len(d.fields))
	}
	if d.inField() {
		f := d.fields[d.focus]
		if f.digits && msg.Type == tea.KeyRunes {
			var keep []rune
			for _, r := range msg.Runes {
				if unicode.IsDigit(r) {
					keep = append(keep, r)
				}
			}
			msg.Runes = keep
		}
		f.in.handle(msg)
	}
	return nil, false
}

func (d *dialog) inField() bool { return d.focus < len(d.fields) }

func (d *dialog) press(i int) (tea.Cmd, bool) {
	if i < 0 || i >= len(d.buttons) {
		return nil, true
	}
	if run := d.buttons[i].run; run != nil {
		return run(), true
	}
	return nil, true
}

// value is the text of the first field.
func (d *dialog) value() string {
	if len(d.fields) == 0 {
		return ""
	}
	return strings.TrimSpace(d.fields[0].in.String())
}

func wrapWords(s string, w int) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := words[0]
		for _, wd := range words[1:] {
			if textWidth(line)+1+textWidth(wd) > w {
				out = append(out, line)
				line = wd
			} else {
				line += " " + wd
			}
		}
		out = append(out, line)
	}
	return out
}

func (d *dialog) draw(p painter, screen rect, t *theme) {
	// Width: at least 50, wide enough for the content, within the screen.
	want := 50
	for _, l := range d.lines {
		want = max(want, textWidth(l)+8)
	}
	for _, l := range d.art {
		want = max(want, textWidth(l)+8)
	}
	width := min(want, max(screen.w-4, 20))

	var msg []string
	for _, l := range d.lines {
		msg = append(msg, wrapWords(l, width-8)...)
	}
	art := d.art
	if len(msg)+len(art)+8 > screen.h { // no room for the picture
		art = nil
	}

	// Rows: blank, message, art, blank, fields (or a spare blank), buttons, blank.
	rows := 1 + len(msg) + len(art) + 1
	if len(d.fields) > 0 {
		rows += len(d.fields) + 1
	} else if !d.isError {
		rows++
	}
	rows += 1 + 1
	height := min(rows+2, screen.h)

	box := rect{screen.x + (screen.w-width)/2, screen.y + (screen.h-height)/2, width, height}
	p.fill(box, t.base())
	border := t.s(t.dialog.fg).withFg(t.frame.border)
	title := []seg{{"<" + d.title + ">", t.s(t.frame.title.fg)}}
	p.frame(box, border, title)

	in := rect{box.x + 1, box.y + 1, box.w - 2, box.h - 2}
	ip := p.sub(in)
	y := in.y + 1
	msgStyle := t.s(t.dialog.fg)
	if d.isError {
		msgStyle = t.s(t.dialog.errorFg)
	}
	center := func(s string, st style) {
		ip.text(in.x+(in.w-textWidth(s))/2, y, s, st)
		y++
	}
	for _, l := range msg {
		center(l, msgStyle)
	}
	if len(art) > 0 {
		aw := 0
		for _, l := range art {
			aw = max(aw, textWidth(l))
		}
		for _, l := range art {
			ip.text(in.x+(in.w-aw)/2, y, l, msgStyle)
			y++
		}
	}
	y++

	for i, f := range d.fields {
		label := f.label + " "
		x := in.x + 2
		x += ip.text(x, y, label, t.s(t.dialog.labelFg))
		st := t.s(t.dialog.fieldFg)
		if d.focus == i {
			f.in.draw(ip, x, y, st, st.reverse(), st, "")
		} else {
			ip.text(x, y, f.in.String(), st)
		}
		y++
	}
	if len(d.fields) > 0 || !d.isError {
		y++
	}

	// Buttons, centred as a group.
	const gap = 5
	total := gap * (len(d.buttons) - 1)
	for _, b := range d.buttons {
		total += textWidth(b.label)
	}
	x := in.x + (in.w-total)/2
	for i, b := range d.buttons {
		st := t.s(t.dialog.buttonFg)
		if d.focus == len(d.fields)+i {
			st = style{fg: t.dialog.buttonFocusFg, bg: t.dialog.buttonFocusBg}
		}
		x += ip.text(x, y, b.label, st) + gap
	}
}

// whale is what delivers bad news, in place of k9s' cow: the classic
// docker whalesay.
var whale = []string{
	`      \`,
	`       \`,
	`        \`,
	`                    ##        .`,
	`              ## ## ##       ==`,
	`           ## ## ## ##      ===`,
	`       /""""""""""""""""\___/ ===`,
	`  ~~~ {~~ ~~~~ ~~~ ~~~~ ~~ ~ /  ===- ~~~`,
	`       \______ o          __/`,
	`        \    \        __/`,
	`          \____\______/`,
}

func newConfirm(title, message string, onOK func() tea.Cmd) *dialog {
	return &dialog{
		title: title,
		lines: []string{message},
		buttons: []dlgButton{
			{label: "Cancel"},
			{label: "OK", run: onOK},
		},
	}
}

func newErrorDialog(message string) *dialog {
	return &dialog{
		title:   "error",
		lines:   []string{"< " + message + " >"},
		art:     whale,
		isError: true,
		buttons: []dlgButton{{label: "Dismiss"}},
	}
}
