package ui

import (
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/yetisage/swarmlens/internal/api"
)

// logView follows the logs of a service (all its tasks, each line tagged
// with the task that wrote it) or of a single task. The toggles and keys
// are k9s' log viewer's: autoscroll, timestamps, wrap, fullscreen, a regexp
// filter and time ranges.
type logView struct {
	a      *app
	vid    int
	target api.LogTarget
	stack  string

	lines  []api.LogLine
	bufMax int
	bytes  int // text held in lines
	gen    int // which run of the stream is current
	err    error
	ended  bool

	autoscroll, wrap, timestamps bool
	tail                         int
	since                        time.Duration
	sinceLabel                   string

	top          int // first visible line when not autoscrolling
	lastTop      int // first visible line of the last frame
	pageH, pageW int

	query string
	re    *regexp.Regexp

	cancel context.CancelFunc
}

func (a *app) newLogView(t api.LogTarget, stack string) *logView {
	l := a.cfg.Logger
	v := &logView{
		a: a, vid: newViewID(), target: t, stack: stack,
		autoscroll: true, wrap: l.TextWrap, timestamps: l.ShowTime,
		tail: l.Tail, bufMax: l.Buffer,
	}
	if v.bufMax <= 0 {
		v.bufMax = 5000
	}
	if l.SinceSeconds > 0 {
		v.since = time.Duration(l.SinceSeconds) * time.Second
		v.sinceLabel = v.since.String()
	}
	return v
}

func (v *logView) id() int       { return v.vid }
func (v *logView) crumb() string { return "logs" }

func (v *logView) start() tea.Cmd { return v.begin() }

// refresh does nothing: the stream is live.
func (v *logView) refresh() tea.Cmd { return nil }

func (v *logView) stop() {
	if v.cancel != nil {
		v.cancel()
		v.cancel = nil
	}
}

// begin (re)starts the stream.
func (v *logView) begin() tea.Cmd {
	v.stop()
	v.lines, v.bytes, v.err, v.ended = nil, 0, nil, false
	v.gen++
	ctx, cancel := context.WithCancel(context.Background())
	v.cancel = cancel
	be, target, vid, gen, p := v.a.be, v.target, v.vid, v.gen, v.a.program
	opts := api.LogOptions{Tail: v.tail, Since: v.since, Follow: true}
	if v.since > 0 {
		opts.Tail = 0
	}

	if p == nil { // no program (tests, --render): read what is there and stop
		return func() tea.Msg {
			var all []api.LogLine
			opts.Follow = false
			err := be.Logs(ctx, target, opts, func(b []api.LogLine) { all = append(all, b...) })
			if err != nil {
				return logEndMsg{vid: vid, gen: gen, err: err}
			}
			return logMsg{vid: vid, gen: gen, lines: all}
		}
	}
	return func() tea.Msg {
		go func() {
			err := be.Logs(ctx, target, opts, func(b []api.LogLine) { p.Send(logMsg{vid: vid, gen: gen, lines: b}) })
			if ctx.Err() == nil {
				p.Send(logEndMsg{vid: vid, gen: gen, err: err})
			}
		}()
		return nil
	}
}

func (v *logView) handle(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case logMsg:
		if m.gen != v.gen {
			return nil // from a stream that has been restarted since
		}
		for _, l := range m.lines {
			v.bytes += len(l.Text)
		}
		v.lines = append(v.lines, m.lines...)
		v.trim()
	case logEndMsg:
		if m.gen != v.gen {
			return nil
		}
		v.ended = true
		if m.err != nil {
			v.err = m.err
			v.a.flash(flashErr, m.err.Error())
		}
	}
	return nil
}

// maxLogBytes bounds the text a log view keeps, whatever the line budget is:
// a service that writes huge lines must not be able to exhaust memory.
const maxLogBytes = 32 << 20

// trim drops the oldest lines beyond the line budget and the byte budget.
func (v *logView) trim() {
	drop := max(len(v.lines)-v.bufMax, 0)
	freed := 0
	for i := 0; i < drop; i++ {
		freed += len(v.lines[i].Text)
	}
	for drop < len(v.lines)-1 && v.bytes-freed > maxLogBytes {
		freed += len(v.lines[drop].Text)
		drop++
	}
	if drop == 0 {
		return
	}
	v.lines = v.lines[drop:]
	v.bytes -= freed
	v.top = max(v.top-drop, 0)
}

func (v *logView) title() []seg {
	t := v.a.th
	plain := t.s(t.frame.title.fg)
	rng := fmt.Sprintf("tail %d", v.tail)
	switch {
	case v.since > 0:
		rng = "last " + v.sinceLabel
	case v.tail <= 0:
		rng = "all"
	}
	segs := []seg{
		{"logs(", plain.bold()},
		{v.target.Name, t.s(t.frame.title.highlight).bold()},
		{")[", plain},
		{rng, t.s(t.frame.title.counter).bold()},
		{"]", plain},
	}
	if v.query != "" {
		segs = append(segs,
			seg{" <", plain},
			seg{"/" + v.query, style{fg: t.frame.title.filter, bg: t.body.bg, attrs: attrReverse}},
			seg{">", plain})
	}
	return segs
}

func (v *logView) hints() []hint {
	return []hint{
		{key: "c", desc: "Copy", show: true},
		{key: "ctrl+s", desc: "Save", show: true},
		{key: "f", desc: "FullScreen", show: true},
		{key: "s", desc: "Toggle AutoScroll", show: true},
		{key: "t", desc: "Toggle Timestamp", show: true},
		{key: "w", desc: "Toggle Wrap", show: true},
		{key: "0", desc: "Tail", show: true},
		{key: "1", desc: "1m", show: true},
		{key: "2", desc: "5m", show: true},
		{key: "3", desc: "15m", show: true},
		{key: "4", desc: "30m", show: true},
		{key: "5", desc: "1h", show: true},
		{key: "j", desc: "Down"}, {key: "k", desc: "Up"},
		{key: "g", desc: "Top"}, {key: "G", desc: "Bottom"},
	}
}

func (v *logView) setFilter(q string) {
	v.query = q
	v.re = compileFilter(q)
}

func (v *logView) filterText() string { return v.query }

// shown are the lines that pass the filter.
func (v *logView) shown() []api.LogLine {
	if v.re == nil {
		return v.lines
	}
	out := make([]api.LogLine, 0, len(v.lines))
	for _, l := range v.lines {
		if v.re.MatchString(l.Text) {
			out = append(out, l)
		}
	}
	return out
}

func (v *logView) key(msg tea.KeyMsg) (tea.Cmd, bool) {
	scroll := func(delta int) {
		if v.autoscroll {
			v.top, v.autoscroll = v.lastTop, false
		}
		v.top += delta
	}
	switch k := msg.String(); k {
	case "j", "down":
		scroll(1)
	case "k", "up":
		scroll(-1)
	case "ctrl+f", "pgdown":
		scroll(max(v.pageH-2, 1))
	case "ctrl+b", "pgup":
		scroll(-max(v.pageH-2, 1))
	case "g", "home":
		v.autoscroll, v.top = false, 0
	case "G", "end":
		v.autoscroll = true
	case "s":
		v.autoscroll = !v.autoscroll
		if !v.autoscroll {
			v.top = v.lastTop
		}
	case "t":
		v.timestamps = !v.timestamps
	case "w":
		v.wrap = !v.wrap
	case "f":
		v.a.fullscreen = !v.a.fullscreen
	case "c":
		v.a.copy(v.plainText())
		v.a.flash(flashInfo, "Logs copied to clipboard...")
	case "ctrl+s":
		v.save()
	case "0", "1", "2", "3", "4", "5":
		return v.setRange(k), true
	case "esc":
		if v.query == "" {
			return nil, false
		}
		v.setFilter("")
	default:
		return nil, false
	}
	return nil, true
}

// setRange applies one of the time range keys and reloads.
func (v *logView) setRange(k string) tea.Cmd {
	ranges := map[string]time.Duration{"1": time.Minute, "2": 5 * time.Minute, "3": 15 * time.Minute, "4": 30 * time.Minute, "5": time.Hour}
	labels := map[string]string{"1": "1m", "2": "5m", "3": "15m", "4": "30m", "5": "1h"}
	if k == "0" {
		v.since, v.sinceLabel, v.tail = 0, "", v.a.cfg.Logger.Tail
	} else {
		v.since, v.sinceLabel = ranges[k], labels[k]
	}
	v.autoscroll = true
	return v.begin()
}

func (v *logView) plainText() string {
	var b strings.Builder
	for _, l := range v.shown() {
		b.WriteString(v.plainLine(l))
		b.WriteByte('\n')
	}
	return b.String()
}

func (v *logView) plainLine(l api.LogLine) string {
	var b strings.Builder
	if v.timestamps && !l.Time.IsZero() {
		b.WriteString(l.Time.Local().Format("01-02 15:04:05") + " ")
	}
	if v.target.Kind == api.KindService && l.Source != "" {
		b.WriteString(l.Source + " ")
	}
	b.WriteString(stripANSI(l.Text))
	return b.String()
}

func (v *logView) save() {
	dir, err := stateDir("logs", sanitize(v.a.be.ContextName()))
	if err != nil {
		v.a.flash(flashErr, err.Error())
		return
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%d.log", sanitize(v.target.Name), time.Now().Unix()))
	if err := os.WriteFile(path, []byte(v.plainText()), 0o600); err != nil {
		v.a.flash(flashErr, err.Error())
		return
	}
	v.a.flash(flashInfo, "Log saved to "+path)
}

// ---- drawing ----

// prefixColor picks a stable colour per task so interleaved lines can be
// told apart.
func (v *logView) prefixColor(source string) color {
	pal := v.a.th.logs.prefix
	h := fnv.New32a()
	_, _ = h.Write([]byte(source))
	// uint32 arithmetic: converting the sum to int first goes negative for
	// about half the names where int is 32 bits wide
	return pal[h.Sum32()%uint32(len(pal))]
}

func (v *logView) lineSegs(l api.LogLine) []seg {
	t := v.a.th
	base := t.s(t.logs.fg)
	var segs []seg
	if v.timestamps && !l.Time.IsZero() {
		segs = append(segs, seg{l.Time.Local().Format("01-02 15:04:05") + " ", t.s(cGray)})
	}
	if v.target.Kind == api.KindService && l.Source != "" {
		segs = append(segs, seg{l.Source + " ", t.s(v.prefixColor(l.Source)).bold()})
	}
	text := ansiSegs(l.Text, base)
	if l.Stderr && len(text) == 0 {
		text = []seg{{"", base}}
	}
	return append(segs, text...)
}

func (v *logView) draw(p painter, r rect, focused bool) {
	t := v.a.th
	border := t.s(t.frame.border).bold()
	if focused {
		border = t.s(t.frame.focus).bold()
	}
	p.frame(r, border, v.title())
	inner := r.inset(1)
	content := rect{inner.x + 1, inner.y, max(inner.w-2, 0), inner.h}
	if content.w <= 0 || content.h < 2 {
		return
	}
	cp := p.sub(content)

	// The indicator row: the state of the toggles.
	ind := t.s(t.logs.indicator)
	on, off := t.s(t.logs.on).bold(), t.s(t.logs.off).bold()
	x := content.x
	for i, f := range []struct {
		label string
		on    bool
	}{{"Autoscroll", v.autoscroll}, {"FullScreen", v.a.fullscreen}, {"Timestamps", v.timestamps}, {"Wrap", v.wrap}} {
		if i > 0 {
			x += 5
		}
		x += cp.text(x, content.y, f.label+":", ind)
		if f.on {
			x += cp.text(x, content.y, "On", on)
		} else {
			x += cp.text(x, content.y, "Off", off)
		}
	}

	body := rect{content.x, content.y + 1, content.w, content.h - 1}
	bp := p.sub(body)
	v.pageH, v.pageW = body.h, body.w
	lines := v.shown()
	if len(lines) == 0 {
		msg := "Waiting for logs..."
		switch {
		case v.err != nil:
			msg = "Cannot read logs: " + v.err.Error()
		case v.ended && v.query != "":
			msg = "No log lines match the filter"
		case v.ended:
			msg = "No logs"
		}
		bp.text(body.x, body.y, ellipsize(msg, body.w), t.s(t.body.fg))
		return
	}

	hl := style{fg: cBlack, bg: t.table.cursorBg}
	render := func(l api.LogLine) [][]seg {
		segs := highlight(v.lineSegs(l), v.re, hl)
		if v.wrap {
			return wrapSegs(segs, body.w)
		}
		return [][]seg{cutSegs(segs, 0, body.w)}
	}

	// Choose the first visible line.
	first := 0
	if v.autoscroll {
		rows := 0
		first = len(lines)
		for first > 0 && rows < body.h {
			first--
			rows += len(render(lines[first]))
		}
		if rows > body.h && first < len(lines)-1 { // the oldest line is cut off: skip it
			first++
		}
	} else {
		v.top = max(0, min(v.top, len(lines)-1))
		first = v.top
	}
	v.lastTop = first

	y := body.y
	for i := first; i < len(lines) && y < body.bottom(); i++ {
		for _, row := range render(lines[i]) {
			if y >= body.bottom() {
				break
			}
			bp.segs(body.x, y, row)
			y++
		}
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for _, sg := range ansiSegs(s, style{}) {
		b.WriteString(sg.s)
	}
	return b.String()
}
