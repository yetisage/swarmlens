package ui

import (
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type promptMode uint8

const (
	promptOff promptMode = iota
	promptCommand
	promptFilter
)

// prompt is the bordered input box that opens under the header for ":"
// commands and "/" filters. Like k9s' it completes inline: the suggestion
// is shown as dimmed text after the cursor, tab accepts it and the up and
// down keys cycle through the candidates.
type prompt struct {
	mode promptMode
	in   input
	sugg []string // full candidate texts
	si   int      // current candidate, -1 for none
}

func (a *app) openPrompt(m promptMode) {
	a.pr = prompt{mode: m, si: -1}
	a.suggest()
}

func (a *app) closePrompt() { a.pr = prompt{} }

func (a *app) promptKey(msg tea.KeyMsg) tea.Cmd {
	pr := &a.pr
	k := msg.String()
	atEnd := pr.in.pos == len(pr.in.r)
	switch k {
	case "esc":
		if pr.mode == promptFilter {
			if f, ok := a.view().(filterer); ok {
				f.setFilter("")
			}
		}
		a.closePrompt()
		return nil

	case "enter":
		text, mode := strings.TrimSpace(pr.in.String()), pr.mode
		a.closePrompt()
		if mode == promptFilter || text == "" {
			return nil // a filter is already live in the view
		}
		return a.runCommand(text, true)

	case "tab", "right", "ctrl+f":
		if pr.mode == promptCommand && (k == "tab" || atEnd) && a.ghost() != "" {
			pr.in.set(pr.in.String() + a.ghost())
			a.suggest()
			return nil
		}
		if k == "tab" {
			return nil
		}

	case "up", "ctrl+p":
		a.cycle(-1)
		return nil
	case "down", "ctrl+n":
		a.cycle(1)
		return nil
	}

	if pr.in.handle(msg) {
		a.promptChanged()
	}
	return nil
}

// promptChanged reacts to an edit: filters apply live, commands re-suggest.
func (a *app) promptChanged() {
	if a.pr.mode == promptFilter {
		if f, ok := a.view().(filterer); ok {
			f.setFilter(a.pr.in.String())
		}
		return
	}
	a.suggest()
}

// suggest recomputes the completion candidates for the command prompt.
func (a *app) suggest() {
	a.pr.sugg, a.pr.si = nil, -1
	if a.pr.mode != promptCommand {
		return
	}
	text := a.pr.in.String()
	words := strings.Fields(text)
	trailing := strings.HasSuffix(text, " ")

	switch {
	case text == "":
		// Nothing typed: offer the command history, newest first.
		seen := map[string]bool{}
		for i := len(a.hist) - 1; i >= 0 && len(a.pr.sugg) < 10; i-- {
			if !seen[a.hist[i]] {
				seen[a.hist[i]] = true
				a.pr.sugg = append(a.pr.sugg, a.hist[i])
			}
		}

	case len(words) == 1 && !trailing:
		w := strings.ToLower(words[0])
		for _, c := range a.commandWords() {
			if c != w && strings.HasPrefix(c, w) {
				a.pr.sugg = append(a.pr.sugg, c)
			}
		}
		sort.SliceStable(a.pr.sugg, func(i, j int) bool {
			if len(a.pr.sugg[i]) != len(a.pr.sugg[j]) {
				return len(a.pr.sugg[i]) < len(a.pr.sugg[j])
			}
			return a.pr.sugg[i] < a.pr.sugg[j]
		})

	case len(words) >= 1:
		// After a resource: offer stack names.
		res, ok := a.resolve(strings.ToLower(words[0]))
		if !ok || !res.scoped {
			break
		}
		head, prefix := strings.Join(words, " "), ""
		if !trailing {
			head = strings.Join(words[:len(words)-1], " ")
			prefix = words[len(words)-1]
		}
		if len(words) > 1 || trailing {
			for _, s := range a.stackNames() {
				if strings.HasPrefix(s, prefix) && s != prefix {
					a.pr.sugg = append(a.pr.sugg, head+" "+s)
				}
			}
		}
	}
	if len(a.pr.sugg) > 0 {
		a.pr.si = 0
	}
}

// ghost is the part of the current candidate that extends the typed text.
func (a *app) ghost() string {
	if a.pr.si < 0 || a.pr.si >= len(a.pr.sugg) {
		return ""
	}
	cand, text := a.pr.sugg[a.pr.si], a.pr.in.String()
	if len(cand) >= len(text) && strings.EqualFold(cand[:len(text)], text) {
		return cand[len(text):]
	}
	return ""
}

func (a *app) cycle(delta int) {
	n := len(a.pr.sugg)
	if n == 0 {
		return
	}
	a.pr.si = (a.pr.si + delta + n) % n
}

func (a *app) promptIcon() string {
	if a.noIcons {
		return ""
	}
	if a.pr.mode == promptFilter {
		return "🔍"
	}
	return "🐳"
}

func (a *app) drawPrompt(p painter, r rect) {
	t := a.th
	border, mark := t.prompt.borderCmd, ">"
	if a.pr.mode == promptFilter {
		border, mark = t.prompt.borderFilter, "/"
	}
	p.fill(r, t.base())
	p.frame(r, t.s(border), nil)

	y := r.y + 1
	x := r.x + 2
	fg := t.s(t.prompt.fg)
	x += p.text(x, y, a.promptIcon()+mark+" ", fg)
	ghost := ""
	if a.pr.mode == promptCommand && a.pr.in.pos == len(a.pr.in.r) {
		ghost = a.ghost()
	}
	a.pr.in.draw(p.sub(r.inset(1)), x, y, fg.bold(), fg.bold().reverse(), t.s(t.prompt.suggest), ghost)
}
