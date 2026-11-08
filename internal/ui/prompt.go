package ui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// prompt is a small single-value input, the swarmlens equivalent of the
// dialogs k9s opens for actions such as scaling a deployment.
type prompt struct {
	active bool
	title  string
	input  textinput.Model
	onDone func(a *app, value string) (tea.Model, tea.Cmd)
}

func newPrompt() prompt {
	in := textinput.New()
	in.Prompt = "> "
	in.CharLimit = 16
	return prompt{input: in}
}

// ask opens the prompt with an initial value.
func (p *prompt) ask(title, initial string, onDone func(a *app, value string) (tea.Model, tea.Cmd)) {
	p.active = true
	p.title = title
	p.onDone = onDone
	p.input.SetValue(initial)
	p.input.CursorEnd()
	p.input.Focus()
}

func (p *prompt) close() {
	p.active = false
	p.input.Blur()
	p.input.SetValue("")
}

// handlePromptKey processes input while the prompt is open.
func (a *app) handlePromptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		a.prompt.close()
		a.setStatus("cancelled")
		return a, nil
	case "enter":
		value := strings.TrimSpace(a.prompt.input.Value())
		cb := a.prompt.onDone
		a.prompt.close()
		if cb != nil {
			return cb(a, value)
		}
		return a, nil
	}
	var cmd tea.Cmd
	a.prompt.input, cmd = a.prompt.input.Update(msg)
	return a, cmd
}

// overlayPrompt draws the prompt over the base view.
func (a *app) overlayPrompt(base string) string {
	body := titleStyle.Render(a.prompt.title) + "\n" + a.prompt.input.View()
	box := overlayBox.Width(minInt(a.width-8, 60)).Render(body)
	return placeOverlay(base, box, a.width, a.height)
}

// parseCount turns the prompt input into a non-negative number.
func parseCount(s string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// confirm is a modal confirmation for destructive actions.
//
// Like k9s, the focus starts on "Cancel" and TAB moves it, so a stray
// ENTER cannot delete anything. The y/n shortcuts are also available.
type confirm struct {
	active bool
	title  string
	detail string
	focus  int // 0 = cancel, 1 = confirm
	onYes  func(a *app) (tea.Model, tea.Cmd)
	onNo   func(a *app) (tea.Model, tea.Cmd)
}

// ask opens the confirmation modal.
func (c *confirm) ask(title, detail string, onYes func(a *app) (tea.Model, tea.Cmd)) {
	c.active = true
	c.title = title
	c.detail = detail
	c.focus = 0
	c.onYes = onYes
	c.onNo = nil
}

// handleConfirmKey processes input while the confirmation modal is open.
func (a *app) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "tab", "right":
		a.confirm.focus = 1
		return a, nil
	case "shift+tab", "left":
		a.confirm.focus = 0
		return a, nil
	case "y", "Y":
		return a.confirmYes()
	case "n", "N":
		return a.confirmNo()
	case "enter":
		if a.confirm.focus == 1 {
			return a.confirmYes()
		}
		return a.confirmNo()
	case "esc":
		return a.confirmNo()
	}
	return a, nil
}

func (a *app) confirmYes() (tea.Model, tea.Cmd) {
	cb := a.confirm.onYes
	a.confirm.active = false
	if cb != nil {
		return cb(a)
	}
	return a, nil
}

func (a *app) confirmNo() (tea.Model, tea.Cmd) {
	cb := a.confirm.onNo
	a.confirm.active = false
	a.setStatus("cancelled")
	if cb != nil {
		return cb(a)
	}
	return a, nil
}

// overlayConfirm draws the confirmation modal on top of the base view.
func (a *app) overlayConfirm(base string) string {
	cancelBtn := keyStyle.Render("  Cancel  ")
	confirmBtn := keyStyle.Render("  Delete  ")
	if a.confirm.focus == 0 {
		cancelBtn = selectedStyle.Render("▌Cancel ")
		confirmBtn = dimStyle.Render("  Delete  ")
	} else {
		cancelBtn = dimStyle.Render("  Cancel  ")
		confirmBtn = selectedStyle.Render("▌Delete ")
	}
	body := titleStyle.Render(a.confirm.title) + "\n" +
		dimStyle.Render(a.confirm.detail) + "\n\n" +
		cancelBtn + "   " + confirmBtn
	box := overlayBox.Width(minInt(a.width-8, 70)).Render(body)
	return placeOverlay(base, box, a.width, a.height)
}

// ensure lipgloss stays referenced for style helpers.
var _ = lipgloss.Width
