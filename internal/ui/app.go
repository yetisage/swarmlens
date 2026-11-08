// Package ui implements the swarmlens terminal UI on Bubble Tea.
package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"swarmlens/internal/api"
)

type screen int

const (
	screenServices screen = iota
	screenNodes
	screenDetail
	screenLogs
	screenVolumes
	screenNetworks
	screenSecrets
	screenConfigs
)

const (
	refreshInterval = 5 * time.Second
	eventDebounce   = 700 * time.Millisecond
	maxLogLines     = 2000
)

// EventMsg is sent to the program when a relevant /events message arrives.
type EventMsg struct {
	Ev api.Event
}

type (
	servicesMsg struct {
		rows []api.ServiceRow
		err  error
	}
	nodesMsg struct {
		rows []api.NodeRow
		err  error
	}
	tasksMsg struct {
		rows []api.TaskRow
		err  error
	}
	volumesMsg struct {
		rows []api.VolumeRow
		err  error
	}
	networksMsg struct {
		rows []api.NetworkRow
		err  error
	}
	secretsMsg struct {
		rows []api.SecretRow
		err  error
	}
	configsMsg struct {
		rows []api.ConfigRow
		err  error
	}
	logsMsg struct {
		content string
		err     error
	}
	logLineMsg struct {
		line string
		err  error
	}
	actionMsg struct {
		message string
		err     error
	}
	tickMsg    struct{}
	refreshMsg struct{}
)

// app is the root Bubble Tea model.
type app struct {
	api           *api.API
	state         *api.SwarmState
	screen        screen
	keys          globalKeyMap
	width, height int

	// tables, one per screen
	services, nodes, tasks table.Model
	volumes, networks      table.Model
	secrets, configs       table.Model

	// data backing the tables
	serviceRows []api.ServiceRow
	nodeRows    []api.NodeRow
	taskRows    []api.TaskRow
	volumeRows  []api.VolumeRow
	networkRows []api.NetworkRow
	secretRows  []api.SecretRow
	configRows  []api.ConfigRow

	// selection is tracked by id so it survives live refreshes
	selectedServiceID string
	selectedTaskID    string
	selectedNodeID    string

	logs      viewport.Model
	logsTask  string // task id currently streamed
	follow    bool
	logBuffer []string

	program      *tea.Program
	followCancel context.CancelFunc

	// overlays
	search    textinput.Model
	searching bool
	filter    string
	palette   palette
	confirm   confirm
	showHelp  bool

	status string
	err    string
	notify bool // an event-triggered refresh is already queued
}

// NewApp builds the UI.
func NewApp(a *api.API, state *api.SwarmState) *app {
	app := &app{
		api:    a,
		state:  state,
		keys:   defaultGlobalKeys(),
		screen: screenServices,
	}
	app.services = newNavTable(serviceColumns()...)
	app.nodes = newNavTable(nodeColumns()...)
	app.tasks = newNavTable(taskColumns()...)
	app.volumes = newNavTable(volumeColumns()...)
	app.networks = newNavTable(networkColumns()...)
	app.secrets = newNavTable(secretColumns()...)
	app.configs = newNavTable(configColumns()...)
	app.logs = viewport.New(80, 20)

	app.search = textinput.New()
	app.search.Prompt = "/ "
	app.search.Placeholder = "filter…"
	app.search.CharLimit = 64

	app.palette = newPalette()
	return app
}

// RenderFrame renders one static frame synchronously (used by --render).
func RenderFrame(a *api.API, state *api.SwarmState, width, height int) string {
	app := NewApp(a, state)
	app.width, app.height = width, height
	app.resize()
	ctx := context.Background()
	if rows, err := a.Services(ctx); err == nil {
		app.applyServices(rows)
	}
	if rows, err := a.Nodes(ctx); err == nil {
		app.applyNodes(rows)
	}
	return app.View()
}

func (a *app) Init() tea.Cmd {
	return tea.Batch(
		loadServicesCmd(a),
		loadNodesCmd(a),
		tea.Tick(refreshInterval, func(time.Time) tea.Msg { return tickMsg{} }),
	)
}

func (a *app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		a.resize()
		return a, a.refreshCmd()

	case servicesMsg:
		a.applyServices(msg.rows)
		a.setError(msg.err)
		if msg.err == nil {
			a.setStatus(fmt.Sprintf("%d services", len(a.visibleServices())))
		}
		return a, nil

	case nodesMsg:
		a.applyNodes(msg.rows)
		a.setError(msg.err)
		if msg.err == nil {
			a.setStatus(fmt.Sprintf("%d nodes", len(a.nodeRows)))
		}
		return a, nil

	case tasksMsg:
		a.applyTasks(msg.rows)
		a.setError(msg.err)
		if msg.err == nil {
			a.setStatus(fmt.Sprintf("tasks for %s: %d", a.currentServiceName(), len(a.taskRows)))
		}
		return a, nil

	case volumesMsg:
		a.volumeRows = msg.rows
		a.volumes.SetRows(volumeTableRows(msg.rows))
		a.setError(msg.err)
		return a, nil

	case networksMsg:
		a.networkRows = msg.rows
		a.networks.SetRows(networkTableRows(msg.rows))
		a.setError(msg.err)
		return a, nil

	case secretsMsg:
		a.secretRows = msg.rows
		a.secrets.SetRows(secretTableRows(msg.rows))
		a.setError(msg.err)
		return a, nil

	case configsMsg:
		a.configRows = msg.rows
		a.configs.SetRows(configTableRows(msg.rows))
		a.setError(msg.err)
		return a, nil

	case logsMsg:
		a.logs.SetContent(msg.content)
		a.setError(msg.err)
		return a, nil

	case logLineMsg:
		if msg.err != nil {
			a.setError(msg.err)
			return a, nil
		}
		a.appendLogLine(msg.line)
		return a, nil

	case actionMsg:
		if msg.err != nil {
			a.setError(msg.err)
		} else {
			a.setStatus(msg.message)
			a.err = ""
		}
		return a, a.refreshCmd()

	case tickMsg:
		return a, tea.Batch(a.refreshCmd(), tea.Tick(refreshInterval, func(time.Time) tea.Msg { return tickMsg{} }))

	case EventMsg:
		if a.notify {
			return a, nil
		}
		a.notify = true
		name := msg.Ev.Actor.Attributes["name"]
		if name == "" {
			name = api.ShortID(msg.Ev.Actor.ID)
		}
		a.setStatus(fmt.Sprintf("event: %s/%s %s — refreshing", msg.Ev.Type, msg.Ev.Action, name))
		return a, tea.Tick(eventDebounce, func(time.Time) tea.Msg { return refreshMsg{} })

	case refreshMsg:
		a.notify = false
		return a, a.refreshCmd()

	case tea.KeyMsg:
		return a.handleKey(msg)
	}
	return a, nil
}

func (a *app) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()

	// Overlays capture all input while they are open.
	if a.confirm.active {
		return a.handleConfirmKey(msg)
	}
	if a.palette.active {
		return a.handlePaletteKey(msg)
	}
	if a.searching {
		switch k {
		case "esc":
			a.searching = false
			a.filter = ""
			a.search.SetValue("")
			a.search.Blur()
			a.applyFilter()
			return a, nil
		case "enter":
			a.searching = false
			a.search.Blur()
			a.applyFilter()
			return a, nil
		}
		var cmd tea.Cmd
		a.search, cmd = a.search.Update(msg)
		a.filter = a.search.Value()
		a.applyFilter()
		return a, cmd
	}

	// Global keys.
	switch {
	case key.Matches(msg, a.keys.Quit):
		return a, tea.Quit
	case key.Matches(msg, a.keys.Help):
		a.showHelp = !a.showHelp
		return a, nil
	case key.Matches(msg, a.keys.Palette):
		a.palette.open(a.paletteCommands())
		return a, nil
	case key.Matches(msg, a.keys.Search):
		a.searching = true
		a.search.SetValue(a.filter)
		return a, a.search.Focus()
	case key.Matches(msg, a.keys.Refresh):
		return a, a.refreshCmd()
	case key.Matches(msg, a.keys.Services):
		return a.goTo(screenServices)
	case key.Matches(msg, a.keys.Nodes):
		return a.goTo(screenNodes)
	case key.Matches(msg, a.keys.Volumes):
		return a.goTo(screenVolumes)
	case key.Matches(msg, a.keys.Networks):
		return a.goTo(screenNetworks)
	case key.Matches(msg, a.keys.Secrets):
		return a.goTo(screenSecrets)
	case key.Matches(msg, a.keys.Configs):
		return a.goTo(screenConfigs)
	}

	// Screen-specific keys.
	switch a.screen {
	case screenServices:
		switch {
		case key.Matches(msg, a.keys.Details):
			return a.openDetail()
		case key.Matches(msg, a.keys.ScaleUp):
			return a.scaleService(+1)
		case key.Matches(msg, a.keys.ScaleDown):
			return a.scaleService(-1)
		case key.Matches(msg, a.keys.Update):
			return a.forceUpdateService()
		case key.Matches(msg, a.keys.Rollback):
			return a.rollbackService()
		case key.Matches(msg, a.keys.Remove):
			return a.removeService()
		}
		a.services, _ = a.services.Update(msg)

	case screenNodes:
		switch {
		case key.Matches(msg, a.keys.Drain):
			return a.setNodeAvailability("drain")
		case key.Matches(msg, a.keys.Activate):
			return a.setNodeAvailability("active")
		case key.Matches(msg, a.keys.Promote):
			return a.setNodeRole("manager")
		case key.Matches(msg, a.keys.Demote):
			return a.setNodeRole("worker")
		case key.Matches(msg, a.keys.Remove):
			return a.removeNode()
		}
		a.nodes, _ = a.nodes.Update(msg)

	case screenDetail:
		switch {
		case key.Matches(msg, a.keys.Back):
			return a.goTo(screenServices)
		case key.Matches(msg, a.keys.Logs):
			return a.openLogs()
		}
		a.tasks, _ = a.tasks.Update(msg)

	case screenLogs:
		switch {
		case key.Matches(msg, a.keys.Back):
			a.stopFollow()
			return a.goTo(screenDetail)
		case key.Matches(msg, a.keys.Logs):
			a.stopFollow()
			return a.goTo(screenDetail)
		case key.Matches(msg, a.keys.Follow):
			a.follow = !a.follow
			if a.follow {
				a.setStatus("following logs (f to stop)")
				return a, a.startFollow()
			}
			a.stopFollow()
			a.setStatus("log follow stopped")
			return a, nil
		}
		a.logs, _ = a.logs.Update(msg)

	case screenVolumes:
		a.volumes, _ = a.volumes.Update(msg)
	case screenNetworks:
		a.networks, _ = a.networks.Update(msg)
	case screenSecrets:
		switch {
		case key.Matches(msg, a.keys.Remove):
			return a.removeSecret()
		}
		a.secrets, _ = a.secrets.Update(msg)
	case screenConfigs:
		switch {
		case key.Matches(msg, a.keys.Remove):
			return a.removeConfig()
		}
		a.configs, _ = a.configs.Update(msg)
	}
	return a, nil
}

// stopFollow cancels an active log follow.
func (a *app) stopFollow() {
	a.follow = false
	if a.followCancel != nil {
		a.followCancel()
		a.followCancel = nil
	}
}

// goTo switches screens and loads the data that screen needs.
func (a *app) goTo(s screen) (tea.Model, tea.Cmd) {
	a.screen = s
	a.err = ""
	a.resize()
	switch s {
	case screenServices:
		a.status = "services"
		return a, tea.Batch(loadServicesCmd(a), loadNodesCmd(a))
	case screenNodes:
		a.status = "nodes"
		return a, loadNodesCmd(a)
	case screenVolumes:
		a.status = "volumes"
		return a, loadVolumesCmd(a)
	case screenNetworks:
		a.status = "networks"
		return a, loadNetworksCmd(a)
	case screenSecrets:
		a.status = "secrets"
		return a, loadSecretsCmd(a)
	case screenConfigs:
		a.status = "configs"
		return a, loadConfigsCmd(a)
	}
	return a, nil
}

func (a *app) openDetail() (tea.Model, tea.Cmd) {
	svc := a.currentService()
	if svc == nil {
		a.setStatus("no service selected")
		return a, nil
	}
	a.selectedServiceID = svc.ID
	a.screen = screenDetail
	a.status = "service: " + svc.Name
	a.err = ""
	a.resize()
	return a, loadTasksCmd(a)
}

func (a *app) openLogs() (tea.Model, tea.Cmd) {
	t := a.currentTask()
	if t == nil {
		a.setStatus("no task selected")
		return a, nil
	}
	a.selectedTaskID = t.ID
	a.logsTask = t.ID
	a.screen = screenLogs
	a.status = fmt.Sprintf("logs: %s (container %s) — f follow, esc back", t.Name, api.ShortID(t.ContainerID))
	a.err = ""
	a.resize()
	return a, loadLogsCmd(a)
}

// currentService resolves the selected service from the current data,
// never from a stale snapshot.
func (a *app) currentService() *api.ServiceRow {
	for i := range a.serviceRows {
		if a.serviceRows[i].ID == a.selectedServiceID {
			return &a.serviceRows[i]
		}
	}
	return nil
}

// currentTask resolves the selected task from the current data.
func (a *app) currentTask() *api.TaskRow {
	for i := range a.taskRows {
		if a.taskRows[i].ID == a.selectedTaskID {
			return &a.taskRows[i]
		}
	}
	return nil
}

// currentNode resolves the selected node from the current data.
func (a *app) currentNode() *api.NodeRow {
	for i := range a.nodeRows {
		if a.nodeRows[i].ID == a.selectedNodeID {
			return &a.nodeRows[i]
		}
	}
	return nil
}

func (a *app) currentServiceName() string {
	if s := a.currentService(); s != nil {
		return s.Name
	}
	return ""
}

// refreshCmd reloads the data the current screen needs.
func (a *app) refreshCmd() tea.Cmd {
	cmds := []tea.Cmd{loadServicesCmd(a), loadNodesCmd(a)}
	switch a.screen {
	case screenDetail:
		cmds = append(cmds, loadTasksCmd(a))
	case screenLogs:
		cmds = append(cmds, loadLogsCmd(a))
	case screenVolumes:
		cmds = append(cmds, loadVolumesCmd(a))
	case screenNetworks:
		cmds = append(cmds, loadNetworksCmd(a))
	case screenSecrets:
		cmds = append(cmds, loadSecretsCmd(a))
	case screenConfigs:
		cmds = append(cmds, loadConfigsCmd(a))
	}
	return tea.Batch(cmds...)
}

func (a *app) setStatus(s string) { a.status = s }

func (a *app) setError(err error) {
	if err != nil {
		a.err = err.Error()
	} else {
		a.err = ""
	}
}

// applyServices stores the rows and restores the cursor by service id.
func (a *app) applyServices(rows []api.ServiceRow) {
	a.serviceRows = rows
	visible := a.filterServices(rows)
	a.services.SetRows(serviceTableRows(visible))
	restoreCursor(&a.services, visible, func(r api.ServiceRow) string { return r.ID }, a.selectedServiceID, &a.selectedServiceID)
}

func (a *app) applyNodes(rows []api.NodeRow) {
	a.nodeRows = rows
	a.nodes.SetRows(nodeTableRows(rows))
	restoreCursor(&a.nodes, rows, func(r api.NodeRow) string { return r.ID }, a.selectedNodeID, &a.selectedNodeID)
}

func (a *app) applyTasks(rows []api.TaskRow) {
	a.taskRows = rows
	a.tasks.SetRows(taskTableRows(rows))
	restoreCursor(&a.tasks, rows, func(r api.TaskRow) string { return r.ID }, a.selectedTaskID, &a.selectedTaskID)
}

// restoreCursor keeps the selection stable across refreshes by re-finding
// the previously selected id in the new row set.
func restoreCursor[T any](tbl *table.Model, rows []T, id func(T) string, selected string, sel *string) {
	if len(rows) == 0 {
		tbl.SetCursor(0)
		return
	}
	if selected != "" {
		for i, r := range rows {
			if id(r) == selected {
				tbl.SetCursor(i)
				return
			}
		}
	}
	tbl.SetCursor(0)
	*sel = id(rows[0])
}

func (a *app) resize() {
	w, h := a.width, a.height
	if w < 50 {
		w = 80
	}
	if h < 14 {
		h = 24
	}
	tw := w - 2
	if tw < 40 {
		tw = 40
	}
	th := h - a.chromeHeight()
	if th < 3 {
		th = 3
	}
	for _, t := range []*table.Model{&a.services, &a.nodes, &a.tasks, &a.volumes, &a.networks, &a.secrets, &a.configs} {
		t.SetWidth(tw)
		t.SetHeight(th)
	}
	a.logs.Width = tw
	a.logs.Height = th
}

// chromeHeight returns the number of rows the surrounding chrome (header,
// optional detail header, status bar) occupies for the current screen.
func (a *app) chromeHeight() int {
	n := 2 // header + status bar
	if a.screen == screenDetail {
		n += 3 // detail header
	}
	if a.searching || a.filter != "" {
		n++
	}
	return n
}

func (a *app) View() string {
	if a.width <= 0 {
		return " swarmlens — waiting for terminal size…"
	}
	var b strings.Builder
	b.WriteString(a.headerLine())
	if a.searching || a.filter != "" {
		b.WriteString(a.searchLine())
	}
	b.WriteString(a.bodyView())
	b.WriteString(a.statusLine())

	view := b.String()
	if a.showHelp {
		view = a.overlayHelp(view)
	}
	if a.palette.active {
		view = a.overlayPalette(view)
	}
	if a.confirm.active {
		view = a.overlayConfirm(view)
	}
	return view
}

func (a *app) bodyView() string {
	switch a.screen {
	case screenServices:
		out := a.services.View()
		if len(a.visibleServices()) == 0 && a.err == "" {
			out += hintLine("no services — create one with: docker service create --name web <image>", a.width)
		}
		return out
	case screenNodes:
		out := a.nodes.View()
		if len(a.nodeRows) == 0 && a.err == "" {
			out += hintLine("no nodes in this swarm", a.width)
		}
		return out
	case screenDetail:
		var b strings.Builder
		b.WriteString(a.detailHeader())
		b.WriteString(a.tasks.View())
		return b.String()
	case screenLogs:
		return a.logs.View()
	case screenVolumes:
		return a.volumes.View()
	case screenNetworks:
		return a.networks.View()
	case screenSecrets:
		return a.secrets.View()
	case screenConfigs:
		return a.configs.View()
	}
	return ""
}

func (a *app) headerLine() string {
	name := a.state.SwarmName
	if name == "" {
		name = "swarm"
	}
	left := lipgloss.JoinHorizontal(lipgloss.Left,
		titleStyle.Render(fmt.Sprintf(" swarmlens · %s ", name)),
		dimStyle.Render(fmt.Sprintf(" node: %s ", a.state.State)),
	)
	if a.state.Role != "" {
		left = lipgloss.JoinHorizontal(lipgloss.Left, left,
			dimStyle.Render(fmt.Sprintf(" role: %s ", a.state.Role)),
		)
	}
	if ep := a.api.Endpoint(); ep != "" {
		left = lipgloss.JoinHorizontal(lipgloss.Left, left,
			dimStyle.Render(fmt.Sprintf(" %s ", ep)),
		)
	}
	right := keyStyle.Render(" s svc  n nodes  / search  : cmd  ? help  q quit ")
	pad := a.width - lipgloss.Width(left) - lipgloss.Width(right) - 1
	if pad < 1 {
		pad = 1
	}
	return left + " " + strings.Repeat(" ", pad) + right + "\n"
}

func (a *app) searchLine() string {
	return " " + a.search.View() + "\n"
}

func (a *app) detailHeader() string {
	c := a.currentService()
	if c == nil {
		return ""
	}
	lines := []string{
		titleStyle.Render("  " + c.Name),
		dimStyle.Render(fmt.Sprintf("  image %s    mode %s    replicas %s", c.Image, c.Mode, c.Replicas)),
		dimStyle.Render(fmt.Sprintf("  update %s    id %s", c.Updated, c.ShortID)),
	}
	return strings.Join(lines, "\n") + "\n"
}

func (a *app) statusLine() string {
	s := a.status
	if s == "" {
		s = fmt.Sprintf("%d services · %d nodes · live: %v", len(a.visibleServices()), len(a.nodeRows), a.state.Role == "manager")
	}
	line := statusStyle.Render(" " + s)
	if a.err != "" {
		line = statusStyle.Render(" ") + errStyle.Render(" ⚠ "+a.err)
	}
	if lipgloss.Width(line) < a.width {
		line += strings.Repeat(" ", a.width-lipgloss.Width(line))
	}
	return line
}

func hintLine(s string, width int) string {
	if len(s) > width {
		return s[:width] + "\n"
	}
	return strings.Repeat(" ", (width-len(s))/2) + dimStyle.Render(s) + "\n"
}

// appendLogLine appends a streamed log line to the viewport.
func (a *app) appendLogLine(line string) {
	a.logBuffer = append(a.logBuffer, line)
	if len(a.logBuffer) > maxLogLines {
		a.logBuffer = a.logBuffer[len(a.logBuffer)-maxLogLines:]
	}
	a.logs.SetContent(strings.Join(a.logBuffer, "\n"))
	_ = a.logs.GotoBottom()
}

// SetProgram wires the Bubble Tea program so background goroutines can
// deliver messages to the UI.
func (a *app) SetProgram(p *tea.Program) { a.program = p }
