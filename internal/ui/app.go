// Package ui is the swarmlens terminal UI. It is built the way k9s is: a
// stack of views (tables, viewers, logs) under a header with the swarm's
// vitals and the key menu, a ":" command prompt, a "/" filter, crumbs and a
// flash line. Bubble Tea runs the event loop; everything is drawn on a
// cell canvas (see canvas.go).
package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"swarmlens/internal/api"
	"swarmlens/internal/config"
)

// Options configures the UI.
type Options struct {
	Backend api.Backend
	// Connect opens another docker context; nil disables switching.
	Connect func(name string) (api.Backend, error)
	Config  *config.Config
	Version string

	Command string // command to run at startup, e.g. "nodes"
	Stack   string // initial stack scope

	ReadOnly   bool
	Headless   bool
	Logoless   bool
	Crumbsless bool
	NoIcons    bool
	Refresh    time.Duration
	// Now is the clock ages are measured against; nil means time.Now.
	Now func() time.Time

	// Profile fixes the colour profile; the zero value (TrueColor) is used
	// by tests, Run detects the terminal's.
	Profile termenv.Profile
	// Static disables timers and background streams (tests, --render).
	Static bool
}

const (
	minWidth, minHeight = 40, 10
	maxEvents           = 500
	summaryEvery        = 5 * time.Second
	eventDebounce       = 300 * time.Millisecond
)

type app struct {
	be  api.Backend
	opt Options
	cfg *config.Config
	th  *theme

	profile termenv.Profile
	w, h    int

	version                                           string
	readOnly, headless, logoless, crumbsless, noIcons bool
	refreshEvery                                      time.Duration

	stack      []view
	modal      modal
	pr         prompt
	flashMsg   flashState
	fullscreen bool

	summary     *api.Summary
	unreachable bool     // the last summary failed
	scope       string   // active stack; "" means all
	recent      []string // stacks used, most recent first

	events       []api.Event
	eventPending bool

	hist    []string
	histPos int

	program      *tea.Program
	cancelEvents context.CancelFunc
	static       bool
}

// New builds the UI model.
func New(opt Options) *app {
	cfg := opt.Config
	if cfg == nil {
		cfg = config.Defaults()
	}
	a := &app{
		be: opt.Backend, opt: opt, cfg: cfg, th: stockTheme(),
		profile:      opt.Profile,
		version:      opt.Version,
		readOnly:     opt.ReadOnly || cfg.ReadOnly,
		headless:     opt.Headless || cfg.UI.Headless,
		logoless:     opt.Logoless || cfg.UI.Logoless,
		crumbsless:   opt.Crumbsless || cfg.UI.Crumbsless,
		noIcons:      opt.NoIcons || cfg.UI.NoIcons,
		refreshEvery: opt.Refresh,
		scope:        opt.Stack,
		static:       opt.Static,
	}
	if a.refreshEvery <= 0 {
		a.refreshEvery = time.Duration(cfg.RefreshRate * float64(time.Second))
	}
	if a.refreshEvery <= 0 {
		a.refreshEvery = 2 * time.Second
	}
	if a.version == "" {
		a.version = "dev"
	}
	if opt.Stack != "" {
		a.recent = []string{opt.Stack}
	}
	return a
}

// now is the current time as far as displayed ages are concerned.
func (a *app) now() time.Time {
	if a.opt.Now != nil {
		return a.opt.Now()
	}
	return time.Now()
}

// SetProgram gives background goroutines a way to message the UI.
func (a *app) SetProgram(p *tea.Program) { a.program = p }

// Run starts the UI and blocks until it quits.
func Run(opt Options) error {
	if opt.Profile == 0 && !opt.Static {
		opt.Profile = lipgloss.ColorProfile()
	}
	a := New(opt)
	p := tea.NewProgram(a, tea.WithAltScreen())
	a.SetProgram(p)
	_, err := p.Run()
	a.stopEvents()
	return err
}

// Render draws one frame synchronously and returns it as plain text, for
// --render and for documentation.
func Render(opt Options, w, h int) string {
	opt.Static = true
	a := New(opt)
	a.w, a.h = w, h
	a.loadSync()
	return a.screen().Text()
}

// ---- messages ----

type (
	summaryMsg struct {
		s   *api.Summary
		err error
	}
	eventMsg    struct{ ev api.Event }
	tickMsg     struct{}
	summaryTick struct{}
	refreshMsg  struct{}
	flashEnd    struct{ seq int }
	actionMsg   struct {
		msg string
		err error
	}
	execDoneMsg struct {
		what string
		err  error
	}
	switchedMsg struct {
		be   api.Backend
		name string
		err  error
	}
	// addressed messages are routed to the view with that id.
	rowsMsg struct {
		vid  int
		rows []*row
		err  error
	}
	textMsg struct {
		vid  int
		text string
		err  error
	}
	logMsg struct {
		vid   int
		lines []api.LogLine
	}
	logEndMsg struct {
		vid int
		err error
	}
)

func (m rowsMsg) viewID() int   { return m.vid }
func (m textMsg) viewID() int   { return m.vid }
func (m logMsg) viewID() int    { return m.vid }
func (m logEndMsg) viewID() int { return m.vid }

type addressed interface{ viewID() int }

// after schedules msg, except in static mode where nothing may sleep.
func (a *app) after(d time.Duration, msg tea.Msg) tea.Cmd {
	if a.static {
		return nil
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return msg })
}

// ---- tea.Model ----

func (a *app) Init() tea.Cmd {
	cmds := []tea.Cmd{
		a.loadSummary(),
		a.startView(),
		a.after(a.refreshEvery, tickMsg{}),
		a.after(summaryEvery, summaryTick{}),
		a.startEvents(),
	}
	if a.opt.Static {
		cmds = cmds[:3]
	}
	return tea.Batch(cmds...)
}

func (a *app) startCommand() string {
	cmd := a.opt.Command
	if cmd == "" {
		cmd = a.defaultCommand()
	}
	if a.opt.Stack != "" && !strings.Contains(cmd, " ") {
		cmd += " " + a.opt.Stack
	}
	return cmd
}

// defaultCommand is the view the program opens when nothing else is asked
// for, and the one it falls back to.
func (a *app) defaultCommand() string {
	if a.opt.Stack != "" {
		return "services " + a.opt.Stack
	}
	return "services"
}

// startView opens the view the program starts in and guarantees that the
// stack is not empty afterwards. A start command that cannot be the root of
// the stack (help, aliases, a context switch, a typo) runs on top of the
// default view, which is how k9s falls back when -c names something it
// cannot show.
func (a *app) startView() tea.Cmd {
	line := a.startCommand()
	if a.commandOpensView(line) {
		return a.runCommand(line, true)
	}
	root := a.runCommand(a.defaultCommand(), false)
	return tea.Batch(root, a.runCommand(line, true))
}

func (a *app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.w, a.h = m.Width, m.Height
		return a, nil

	case tea.KeyMsg:
		return a, a.onKeyMsg(m)

	case addressed:
		for i := len(a.stack) - 1; i >= 0; i-- {
			if a.stack[i].id() == m.viewID() {
				return a, a.stack[i].handle(msg)
			}
		}
		return a, nil

	case summaryMsg:
		if m.err != nil {
			first := !a.unreachable
			a.summary, a.unreachable = nil, true
			a.flash(flashErr, m.err.Error())
			if first && a.modal == nil {
				a.modal = newErrorDialog(fmt.Sprintf("Cannot reach the Docker daemon at %s: %v", a.be.Endpoint(), m.err))
			}
			return a, nil
		}
		a.unreachable = false
		first := a.summary == nil
		a.summary = m.s
		if first && !m.s.InSwarm() {
			a.notInSwarm(m.s)
		}
		return a, nil

	case eventMsg:
		return a, a.onEvent(m.ev)

	case tickMsg:
		return a, tea.Batch(a.refreshTop(), a.after(a.refreshEvery, tickMsg{}))

	case summaryTick:
		return a, tea.Batch(a.loadSummary(), a.after(summaryEvery, summaryTick{}))

	case refreshMsg:
		a.eventPending = false
		return a, tea.Batch(a.refreshTop(), a.loadSummary())

	case flashEnd:
		return a, nil // View checks the clock; this just forces a repaint

	case actionMsg:
		if m.err != nil {
			a.flash(flashErr, m.err.Error())
		} else if m.msg != "" {
			a.flash(flashInfo, m.msg)
		}
		return a, tea.Batch(a.refreshTop(), a.loadSummary())

	case execDoneMsg:
		if m.err != nil {
			a.flash(flashErr, fmt.Sprintf("%s: %v", m.what, m.err))
		}
		return a, a.refreshTop()

	case switchedMsg:
		return a, a.onSwitched(m)

	case editMsg:
		return a, a.onEdit(m)

	case editDoneMsg:
		return a, a.onEditDone(m)
	}
	return a, nil
}

// view is the top of the stack. Once the program has started the stack is
// never empty (see startView); code that can run before that, or in answer to
// a message that arrives regardless, uses top instead.
func (a *app) view() view { return a.stack[len(a.stack)-1] }

// top is the top of the stack, or nil when there is none yet.
func (a *app) top() view {
	if len(a.stack) == 0 {
		return nil
	}
	return a.view()
}

// refreshTop reloads the top view, if there is one.
func (a *app) refreshTop() tea.Cmd {
	if v := a.top(); v != nil {
		return v.refresh()
	}
	return nil
}

func (a *app) push(v view) tea.Cmd {
	a.stack = append(a.stack, v)
	a.fullscreen = false
	return v.start()
}

func (a *app) pop() tea.Cmd {
	if len(a.stack) <= 1 {
		return nil
	}
	a.view().stop()
	a.stack = a.stack[:len(a.stack)-1]
	a.fullscreen = false
	return a.view().refresh()
}

// setRoot replaces the whole stack, which is what ":" commands do.
func (a *app) setRoot(v view) tea.Cmd {
	for _, old := range a.stack {
		old.stop()
	}
	a.stack = []view{v}
	a.fullscreen = false
	return v.start()
}

// onKeyMsg splits what Bubble Tea delivers as one message. Characters that
// arrive in a single read (fast typing, key repeat, a macro) come as one
// KeyRunes message holding several runes, which would be matched as a
// meaningless string such as ":no". A bracketed paste is text, not keys.
func (a *app) onKeyMsg(msg tea.KeyMsg) tea.Cmd {
	if msg.Type != tea.KeyRunes || len(msg.Runes) < 2 {
		return a.onKey(msg)
	}
	if msg.Paste {
		a.paste(string(msg.Runes))
		return nil
	}
	var cmds []tea.Cmd
	for _, r := range msg.Runes {
		k := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: msg.Alt}
		if r == ' ' {
			k = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
		}
		cmds = append(cmds, a.onKey(k))
	}
	return tea.Batch(cmds...)
}

// paste inserts text into whatever is taking input, and is ignored otherwise.
func (a *app) paste(text string) {
	switch {
	case a.modal != nil:
		if d, ok := a.modal.(*dialog); ok && d.inField() {
			d.fields[d.focus].in.insert(text)
		}
	case a.pr.mode != promptOff:
		a.pr.in.insert(strings.ReplaceAll(text, "\n", " "))
		a.promptChanged()
	}
}

func (a *app) onKey(msg tea.KeyMsg) tea.Cmd {
	if msg.String() == "ctrl+c" {
		return a.quit() // even over a dialog or the prompt
	}
	if a.modal != nil {
		cmd, done := a.modal.key(msg)
		if done {
			a.modal = nil
		}
		return cmd
	}
	if a.pr.mode != promptOff {
		return a.promptKey(msg)
	}
	k := msg.String()
	if len(a.stack) == 0 {
		return nil
	}
	if cmd, ok := a.view().key(msg); ok {
		return cmd
	}
	if len(k) == 1 && k[0] >= '0' && k[0] <= '9' {
		if tv, ok := a.view().(*tableView); ok && tv.scopeActive() {
			return a.pickStack(int(k[0] - '0'))
		}
	}
	switch k {
	case ":":
		a.openPrompt(promptCommand)
	case "/":
		if f, ok := a.view().(filterer); ok {
			a.openPrompt(promptFilter)
			a.pr.in.set(f.filterText())
		}
	case "?":
		if _, ok := a.view().(*helpView); ok {
			return a.pop()
		}
		return a.push(newHelpView(a))
	case "ctrl+a":
		if _, ok := a.view().(*tableView); ok && a.view().crumb() == "aliases" {
			return a.pop()
		}
		return a.push(a.aliasView())
	case "esc", "q":
		return a.pop()
	case "[":
		return a.historyStep(-1)
	case "]":
		return a.historyStep(1)
	case "-":
		return a.historyLast()
	case "ctrl+e":
		a.headless = !a.headless
	case "ctrl+g":
		a.crumbsless = !a.crumbsless
	case "ctrl+r":
		return tea.Batch(a.view().refresh(), a.loadSummary())
	}
	return nil
}

func (a *app) quit() tea.Cmd {
	a.stopEvents()
	return tea.Quit
}

// ---- data ----

func (a *app) loadSummary() tea.Cmd {
	be := a.be
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		s, err := be.Summary(ctx)
		return summaryMsg{s: s, err: err}
	}
}

// loadSync loads the first view and the summary without a program, for
// --render and tests.
func (a *app) loadSync() {
	ctx := context.Background()
	if s, err := a.be.Summary(ctx); err == nil {
		a.summary = s
	}
	a.drain(a.startView())
}

// drain runs a command and feeds its messages back, recursively, ignoring
// anything that would wait. It lets tests and --render drive the model
// without a running program.
func (a *app) drain(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(5 * time.Second):
		return
	}
	switch m := msg.(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range m {
			a.drain(c)
		}
	default:
		_, next := a.Update(m)
		a.drain(next)
	}
}

// startEvents subscribes to the daemon's event stream. The cancel function
// is created here, on the update loop, so stopEvents never races with it.
func (a *app) startEvents() tea.Cmd {
	if a.program == nil || a.static {
		return nil
	}
	a.stopEvents()
	ctx, cancel := context.WithCancel(context.Background())
	a.cancelEvents = cancel
	p, be := a.program, a.be
	return func() tea.Msg {
		go func() {
			_ = be.Events(ctx, func(e api.Event) { p.Send(eventMsg{e}) })
		}()
		return nil
	}
}

func (a *app) stopEvents() {
	if a.cancelEvents != nil {
		a.cancelEvents()
		a.cancelEvents = nil
	}
}

func (a *app) onEvent(e api.Event) tea.Cmd {
	if e.Type == "error" {
		a.flash(flashWarn, "event stream: "+e.Attrs["message"])
		return nil
	}
	a.events = append(a.events, e)
	if len(a.events) > maxEvents {
		a.events = a.events[len(a.events)-maxEvents:]
	}
	if a.eventPending {
		return nil
	}
	a.eventPending = true
	return a.after(eventDebounce, refreshMsg{})
}

// eventsSnapshot copies the event buffer for the events view.
func (a *app) eventsSnapshot() []api.Event {
	return append([]api.Event(nil), a.events...)
}

// notInSwarm explains why nothing will load.
func (a *app) notInSwarm(s *api.Summary) {
	switch {
	case s.SwarmState != "active":
		a.modal = newErrorDialog(fmt.Sprintf("This daemon is not part of a swarm (state: %s). Use `docker swarm init`, or switch with :ctx.", s.SwarmState))
	case s.Role != string(api.RoleManager):
		a.modal = newErrorDialog("Connected to a worker node. Only managers serve the swarm API; point DOCKER_HOST or :ctx at a manager.")
	}
}

// ---- stacks (the namespaces of swarm) ----

// stackNames are the stacks seen so far.
func (a *app) stackNames() []string {
	set := map[string]bool{}
	if a.summary != nil {
		for _, s := range a.summary.Stacks {
			set[s] = true
		}
	}
	for _, s := range a.recent {
		set[s] = true
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// favStacks lists the stacks behind the 1-9 keys: the ones used recently
// first, then the rest alphabetically.
func (a *app) favStacks() []string {
	known := map[string]bool{}
	for _, s := range a.stackNames() {
		known[s] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, s := range a.recent {
		if known[s] && !seen[s] {
			out = append(out, s)
			seen[s] = true
		}
	}
	for _, s := range a.stackNames() {
		if !seen[s] {
			out = append(out, s)
		}
	}
	return out
}

func (a *app) pickStack(n int) tea.Cmd {
	if n == 0 {
		return a.setScope("")
	}
	favs := a.favStacks()
	if n > len(favs) {
		return nil
	}
	return a.setScope(favs[n-1])
}

// applyScope changes the active stack. Tables apply it to the rows they
// already hold, so the switch is instant.
func (a *app) applyScope(stack string) {
	a.scope = stack
	if stack != "" {
		out := []string{stack}
		for _, s := range a.recent {
			if s != stack && len(out) < 9 {
				out = append(out, s)
			}
		}
		a.recent = out
	}
	for _, v := range a.stack {
		if tv, ok := v.(*tableView); ok && tv.res.scoped {
			tv.rebuild()
		}
	}
}

// setScope is applyScope for the 0-9 keys: it also tells the user.
func (a *app) setScope(stack string) tea.Cmd {
	a.applyScope(stack)
	if tv, ok := a.view().(*tableView); ok {
		a.flash(flashInfo, fmt.Sprintf("Viewing %s in stack %s...", tv.res.key, scopeLabel(stack)))
		return tv.refresh()
	}
	return nil
}

// ---- backend switching ----

func (a *app) switchContext(name string) tea.Cmd {
	if a.opt.Connect == nil {
		a.flash(flashErr, "Context switching is not available")
		return nil
	}
	if name == a.be.ContextName() {
		a.flash(flashInfo, "Already using context "+name)
		return nil
	}
	connect := a.opt.Connect
	return func() tea.Msg {
		be, err := connect(name)
		return switchedMsg{be: be, name: name, err: err}
	}
}

func (a *app) onSwitched(m switchedMsg) tea.Cmd {
	if m.err != nil {
		a.flash(flashErr, m.err.Error())
		return nil
	}
	a.stopEvents()
	a.be = m.be
	a.summary = nil
	a.events = nil
	a.scope = ""
	a.recent = nil
	a.flash(flashInfo, "Switched to context "+m.name)
	return tea.Batch(a.runCommand("services", true), a.loadSummary(), a.startEvents())
}

// ---- command history: [ ] - ----

func (a *app) record(cmd string) {
	if n := len(a.hist); n > 0 && a.hist[n-1] == cmd {
		a.histPos = n - 1
		return
	}
	a.hist = append(a.hist, cmd)
	if len(a.hist) > 50 {
		a.hist = a.hist[len(a.hist)-50:]
	}
	a.histPos = len(a.hist) - 1
}

func (a *app) historyStep(delta int) tea.Cmd {
	next := a.histPos + delta
	if next < 0 || next >= len(a.hist) {
		if delta < 0 {
			a.flash(flashWarn, "Can't go back any further")
		} else {
			a.flash(flashWarn, "Can't go forward any further")
		}
		return nil
	}
	a.histPos = next
	return a.runCommand(a.hist[next], false)
}

// historyLast swaps between the last two commands, like k9s' "-".
func (a *app) historyLast() tea.Cmd {
	n := len(a.hist)
	if n < 2 {
		a.flash(flashWarn, "No previous command")
		return nil
	}
	a.hist[n-1], a.hist[n-2] = a.hist[n-2], a.hist[n-1]
	a.histPos = n - 1
	return a.runCommand(a.hist[n-1], false)
}

// ---- flash ----

func (a *app) flash(level flashLevel, msg string) {
	a.flashMsg.seq++
	a.flashMsg.text, a.flashMsg.level = msg, level
	a.flashMsg.until = time.Now().Add(flashDuration)
}

// flashFor is a flash that also schedules the repaint when it expires.
func (a *app) flashFor(level flashLevel, msg string) tea.Cmd {
	a.flash(level, msg)
	return a.after(flashDuration+50*time.Millisecond, flashEnd{a.flashMsg.seq})
}

// ---- rendering ----

func (a *app) View() string {
	if a.w <= 0 || a.h <= 0 {
		return ""
	}
	return a.screen().String()
}

// screen composes one frame.
func (a *app) screen() *canvas {
	c := newCanvas(a.w, a.h, a.th.base(), a.profile)
	p := c.painter()
	if a.w < minWidth || a.h < minHeight {
		msg := fmt.Sprintf("Terminal too small: %dx%d (need %dx%d)", a.w, a.h, minWidth, minHeight)
		p.text(max((a.w-textWidth(msg))/2, 0), a.h/2, ellipsize(msg, a.w), a.th.s(a.th.flash.err))
		return c
	}
	if len(a.stack) == 0 {
		a.drawSplash(p)
		return c
	}

	y := 0
	showHeader := !a.headless && !a.fullscreen
	if showHeader {
		a.drawHeader(p, rect{0, 0, a.w, headerHeight})
		y = headerHeight
	}
	if a.pr.mode != promptOff {
		a.drawPrompt(p, rect{0, y, a.w, 3})
		y += 3
	}
	bottom := 1 // flash
	if !a.crumbsless && !a.fullscreen {
		bottom++
	}
	content := rect{0, y, a.w, max(a.h-y-bottom, 3)}
	focused := a.pr.mode == promptOff && a.modal == nil
	a.view().draw(p, content, focused)

	if !a.crumbsless && !a.fullscreen {
		a.drawCrumbs(p, rect{0, a.h - 2, a.w, 1})
	}
	a.drawFlash(p, rect{0, a.h - 1, a.w, 1})

	if a.modal != nil {
		a.modal.draw(p, rect{0, 0, a.w, a.h}, a.th)
	}
	return c
}

// drawSplash is shown until the first view exists.
func (a *app) drawSplash(p painter) {
	st := a.th.s(a.th.body.logo).bold()
	y := a.h/2 - len(logoArt)/2
	for i, l := range logoArt {
		p.text((a.w-logoWidth())/2, y+i, l, st)
	}
	v := "Revision " + a.version
	p.text((a.w-textWidth(v))/2, y+len(logoArt)+1, v, a.th.s(cWhite))
}
