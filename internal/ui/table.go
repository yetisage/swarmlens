package ui

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"swarmlens/internal/api"
)

type sortKind uint8

const (
	sortText   sortKind = iota // natural, case-insensitive
	sortNumber                 // by row.nums
)

type align uint8

const (
	alignLeft align = iota
	alignRight
)

// column describes one table column.
type column struct {
	name     string
	align    align
	kind     sortKind
	hotkey   string // key that sorts by this column, e.g. "N" for shift-n
	wide     bool   // only in wide mode
	scoped   bool   // only when viewing all stacks (the STACK column)
	volatile bool   // changes by itself (ages): ignored when detecting updates
	min, max int
	// drop ranks the column for removal on narrow screens: when the columns
	// do not fit even squeezed, the highest rank goes first. Zero never drops.
	drop int
	skip func(q *loadQuery) bool // hide the column for some drill-downs
}

// row is one table row. cells has one entry per column of the resource,
// wide ones included.
type row struct {
	id    string
	name  string // what the row is called in messages and when copied
	stack string // for stack scoping
	cells []string
	nums  []float64
	role  role
	fault bool
	obj   any

	delta role // roleAdd or roleMod while a change is being highlighted
}

// loadQuery carries what a resource needs to know to load its rows.
type loadQuery struct {
	scope     string // active stack ("" = all)
	serviceID string // drill-down: tasks of a service
	nodeID    string // drill-down: tasks on a node
	events    []seenEvent
	now       time.Time // the clock ages are measured against
}

// clock is the time ages are measured against.
func (q loadQuery) clock() time.Time {
	if q.now.IsZero() {
		return time.Now()
	}
	return q.now
}

// resource is a kind of table: which columns it has and how rows come to be.
type resource struct {
	key      string // plural command name, also the title
	singular string
	aliases  []string
	kind     api.Kind
	cols     []column
	scoped   bool // honours the active stack
	defSort  int
	defAsc   bool
	// load fetches rows from the backend.
	load func(ctx context.Context, be api.Backend, q loadQuery) ([]*row, error)
	// actions are the bindings specific to this resource.
	actions func(v *tableView) []action
}

func (r *resource) names() []string {
	return append([]string{r.key, r.singular}, r.aliases...)
}

// tableView shows a resource as a table.
type tableView struct {
	a    *app
	vid  int
	res  *resource
	q    loadQuery
	name string // crumb
	sub  string // what the title shows instead of the stack, e.g. a service name

	all  []*row
	rows []*row // after scope, filter and sort
	prev map[string]string

	cursor, offset int
	pageH          int
	selID          string

	sortCol int
	asc     bool
	selCol  int // column picked with shift-left/right, -1 for none

	alt          bool // the lighter palette k9s uses for its alias view
	wide, faults bool
	filter       string
	re           *regexp.Regexp
	inverse      bool
	marks        map[string]bool
	anchor       string // last marked row, for range marking

	loading bool
	loaded  bool
	err     error
}

func newTableView(a *app, res *resource, q loadQuery) *tableView {
	v := &tableView{
		a: a, vid: newViewID(), res: res, q: q,
		sortCol: res.defSort, asc: res.defAsc, selCol: -1,
		marks: map[string]bool{}, prev: map[string]string{},
		name: res.key,
	}
	return v
}

func (v *tableView) id() int       { return v.vid }
func (v *tableView) crumb() string { return v.name }

func (v *tableView) start() tea.Cmd { return v.refresh() }

func (v *tableView) stop() {}

// refresh reloads the rows unless a load is already running.
func (v *tableView) refresh() tea.Cmd {
	if v.loading {
		return nil
	}
	v.loading = true
	be, res, q, vid := v.a.be, v.res, v.q, v.vid
	q.scope = v.a.scope
	q.now = v.a.now()
	if v.res.key == "events" {
		q.events = v.a.eventsSnapshot()
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		rows, err := res.load(ctx, be, q)
		return rowsMsg{vid: vid, rows: rows, err: err}
	}
}

func (v *tableView) handle(msg tea.Msg) tea.Cmd {
	m, ok := msg.(rowsMsg)
	if !ok {
		return nil
	}
	v.loading = false
	if m.err != nil {
		v.err = m.err
		v.a.flash(flashErr, m.err.Error())
		return nil
	}
	v.err = nil
	v.applyRows(m.rows)
	return nil
}

// signature identifies the content of a row, ignoring columns that change
// on their own (ages), to detect real updates.
func (v *tableView) signature(r *row) string {
	var b strings.Builder
	for i, c := range r.cells {
		if i < len(v.res.cols) && v.res.cols[i].volatile {
			continue
		}
		b.WriteString(c)
		b.WriteByte(0)
	}
	return b.String()
}

// applyRows installs freshly loaded rows and works out which ones are new
// or changed since the previous refresh, which k9s highlights.
func (v *tableView) applyRows(rows []*row) {
	next := make(map[string]string, len(rows))
	for _, r := range rows {
		sig := v.signature(r)
		if v.loaded {
			switch old, seen := v.prev[r.id]; {
			case !seen:
				r.delta = roleAdd
			case old != sig:
				r.delta = roleMod
			}
		}
		next[r.id] = sig
	}
	v.prev = next
	v.all = rows
	v.loaded = true
	v.rebuild()
}

// scopeActive reports whether the active stack filters this table. Views
// reached by drilling down (the tasks of a service or of a node) already say
// what they show, so the stack scope does not apply to them.
func (v *tableView) scopeActive() bool {
	return v.res.scoped && v.q.serviceID == "" && v.q.nodeID == ""
}

// stackFilter is the stack rows are restricted to, or "".
func (v *tableView) stackFilter() string {
	if v.scopeActive() {
		return v.a.scope
	}
	return ""
}

// visibleCols lists the indexes of the columns currently shown.
func (v *tableView) visibleCols() []int {
	var out []int
	for i := range v.res.cols {
		c := &v.res.cols[i]
		switch {
		case c.wide && !v.wide:
		case c.scoped && (v.stackFilter() != "" || v.q.serviceID != "" || v.columnEmpty(i)):
		case c.skip != nil && c.skip(&v.q):
		default:
			out = append(out, i)
		}
	}
	return out
}

// columnEmpty reports whether no row has a value in a column, so a STACK
// column does not waste space in a swarm that has no stacks.
func (v *tableView) columnEmpty(col int) bool {
	if !v.loaded {
		return false
	}
	for _, r := range v.all {
		if r.cells[col] != "" {
			return false
		}
	}
	return true
}

// rebuild recomputes the visible rows: scope, faults, filter, sort. The
// selection follows the row it was on, not the position.
func (v *tableView) rebuild() {
	rows := make([]*row, 0, len(v.all))
	for _, r := range v.all {
		if f := v.stackFilter(); f != "" && r.stack != f {
			continue
		}
		if v.faults && !r.fault {
			continue
		}
		if v.re != nil && v.re.MatchString(strings.Join(r.cells, " ")) == v.inverse {
			continue
		}
		rows = append(rows, r)
	}
	col := v.res.cols[v.sortCol]
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		var less, eq bool
		if col.kind == sortNumber && v.sortCol < len(a.nums) && v.sortCol < len(b.nums) {
			less, eq = a.nums[v.sortCol] < b.nums[v.sortCol], a.nums[v.sortCol] == b.nums[v.sortCol]
		} else {
			x, y := a.cells[v.sortCol], b.cells[v.sortCol]
			less, eq = naturalLess(x, y), x == y
		}
		if eq {
			return naturalLess(a.name, b.name) // stable, readable tie-break
		}
		return less == v.asc
	})
	v.rows = rows

	live := make(map[string]bool, len(rows))
	for _, r := range rows {
		live[r.id] = true
	}
	for id := range v.marks {
		if !live[id] {
			delete(v.marks, id)
		}
	}

	v.cursor = 0
	for i, r := range rows {
		if r.id == v.selID {
			v.cursor = i
			break
		}
	}
	v.syncSel()
}

func (v *tableView) syncSel() {
	if v.cursor >= len(v.rows) {
		v.cursor = max(len(v.rows)-1, 0)
	}
	if len(v.rows) == 0 {
		v.selID = ""
		return
	}
	v.selID = v.rows[v.cursor].id
}

// selected is the row under the cursor, or nil.
func (v *tableView) selected() *row {
	if v.cursor < 0 || v.cursor >= len(v.rows) {
		return nil
	}
	return v.rows[v.cursor]
}

// targets are the marked rows, or the selected row if none is marked.
func (v *tableView) targets() []*row {
	var out []*row
	for _, r := range v.rows {
		if v.marks[r.id] {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		if r := v.selected(); r != nil {
			out = append(out, r)
		}
	}
	return out
}

func (v *tableView) move(delta int) {
	v.cursor = max(0, min(v.cursor+delta, len(v.rows)-1))
	v.syncSel()
}

func (v *tableView) page() int { return max(v.pageH-1, 1) }

func (v *tableView) setFilter(q string) {
	v.filter = q
	v.inverse = false
	if strings.HasPrefix(q, "!") {
		v.inverse = true
		q = q[1:]
	}
	v.re = compileFilter(q)
	v.rebuild()
}

func (v *tableView) filterText() string { return v.filter }

func (v *tableView) sortBy(col int) {
	if col < 0 || col >= len(v.res.cols) {
		return
	}
	if v.sortCol == col {
		v.asc = !v.asc
	} else {
		v.sortCol, v.asc = col, true
	}
	v.rebuild()
}

// ---- keys ----

// sharedActions are the bindings every table has. They are documented in
// help but kept out of the header menu, as in k9s.
func (v *tableView) sharedActions() []action {
	acts := []action{
		act(" ", "Mark", false, func() tea.Cmd { v.toggleMark(); return nil }),
		act("ctrl+@", "Mark Range", false, func() tea.Cmd { v.markRange(); return nil }),
		act("ctrl+\\", "Mark Clear", false, func() tea.Cmd { v.marks = map[string]bool{}; return nil }),
		act("c", "Copy", false, func() tea.Cmd { v.copyNames(); return nil }),
		act("ctrl+w", "Toggle Wide", false, func() tea.Cmd { v.wide = !v.wide; v.selCol = -1; return nil }),
		act("ctrl+z", "Toggle Faults", false, func() tea.Cmd { v.faults = !v.faults; v.rebuild(); return nil }),
		act("ctrl+s", "Save", false, func() tea.Cmd { v.save(); return nil }),
		act("shift+right", "Select Next Column", false, func() tea.Cmd { v.pickCol(1); return nil }),
		act("shift+left", "Select Previous Column", false, func() tea.Cmd { v.pickCol(-1); return nil }),
		act("O", "Sort Selected Column", false, func() tea.Cmd {
			if v.selCol >= 0 {
				v.sortBy(v.selCol)
			}
			return nil
		}),
	}
	for i, c := range v.res.cols {
		if c.hotkey == "" {
			continue
		}
		i := i
		acts = append(acts, act(c.hotkey, "Sort "+titleCase(c.name), false, func() tea.Cmd { v.sortBy(i); return nil }))
	}
	return acts
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + strings.ToLower(s[1:])
}

func (v *tableView) actions() []action {
	var acts []action
	if v.res.actions != nil {
		acts = append(acts, v.res.actions(v)...)
	}
	acts = append(acts, v.a.pluginActions(v)...)
	// A key the resource (or a plugin) uses itself wins over the shared one.
	taken := map[string]bool{}
	for _, ac := range acts {
		taken[ac.key] = true
	}
	for _, ac := range v.sharedActions() {
		if !taken[ac.key] {
			acts = append(acts, ac)
		}
	}
	return acts
}

func (v *tableView) hints() []hint {
	var hs []hint
	for _, ac := range v.actions() {
		if ac.mutates && v.a.readOnly {
			continue
		}
		hs = append(hs, ac.hint)
	}
	return hs
}

func (v *tableView) key(msg tea.KeyMsg) (tea.Cmd, bool) {
	k := msg.String()
	for _, ac := range v.actions() {
		if ac.key != k {
			continue
		}
		if ac.mutates && v.a.readOnly {
			v.a.flash(flashWarn, "Command disabled in read-only mode")
			return nil, true
		}
		return ac.run(), true
	}
	switch k {
	case "up", "k":
		v.move(-1)
	case "down", "j":
		v.move(1)
	case "pgup", "ctrl+b":
		v.move(-v.page())
	case "pgdown", "ctrl+f":
		v.move(v.page())
	case "home", "g":
		v.cursor = 0
		v.syncSel()
	case "end", "G":
		v.cursor = len(v.rows) - 1
		v.syncSel()
	case "esc":
		switch {
		case v.filter != "":
			v.setFilter("")
		case len(v.marks) > 0:
			v.marks = map[string]bool{}
		case v.faults:
			v.faults = false
			v.rebuild()
		default:
			return nil, false
		}
	default:
		return nil, false
	}
	return nil, true
}

func (v *tableView) toggleMark() {
	r := v.selected()
	if r == nil {
		return
	}
	if v.marks[r.id] {
		delete(v.marks, r.id)
	} else {
		v.marks[r.id] = true
		v.anchor = r.id
	}
	v.move(1)
}

// markRange marks everything between the last marked row and the cursor.
func (v *tableView) markRange() {
	from := -1
	for i, r := range v.rows {
		if r.id == v.anchor {
			from = i
		}
	}
	if from < 0 {
		v.toggleMark()
		return
	}
	lo, hi := min(from, v.cursor), max(from, v.cursor)
	for _, r := range v.rows[lo : hi+1] {
		v.marks[r.id] = true
	}
}

// copyNames puts the names of the marked (or selected) rows on the clipboard.
func (v *tableView) copyNames() {
	var names []string
	for _, r := range v.targets() {
		names = append(names, r.name)
	}
	if len(names) == 0 {
		return
	}
	v.a.copy(strings.Join(names, "\n"))
	if len(names) == 1 {
		v.a.flash(flashInfo, "Copied "+names[0]+" to the clipboard")
	} else {
		v.a.flash(flashInfo, fmt.Sprintf("Copied %d names to the clipboard", len(names)))
	}
}

func (v *tableView) pickCol(delta int) {
	cols := v.visibleCols()
	if len(cols) == 0 {
		return
	}
	pos := -1
	for i, c := range cols {
		if c == v.selCol {
			pos = i
		}
	}
	switch {
	case pos < 0 && delta > 0:
		pos = 0
	case pos < 0:
		pos = len(cols) - 1
	default:
		pos = (pos + delta + len(cols)) % len(cols)
	}
	v.selCol = cols[pos]
}

// save writes the visible table to a CSV file, like k9s' ctrl-s.
func (v *tableView) save() {
	dir := stateDir("screen-dumps", sanitize(v.a.be.ContextName()))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		v.a.flash(flashErr, err.Error())
		return
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%d.csv", v.res.key, time.Now().Unix()))
	f, err := os.Create(path)
	if err != nil {
		v.a.flash(flashErr, err.Error())
		return
	}
	defer f.Close()
	w := csv.NewWriter(f)
	cols := v.visibleCols()
	head := make([]string, len(cols))
	for i, c := range cols {
		head[i] = v.res.cols[c].name
	}
	_ = w.Write(head)
	for _, r := range v.rows {
		rec := make([]string, len(cols))
		for i, c := range cols {
			rec[i] = r.cells[c]
		}
		_ = w.Write(rec)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		v.a.flash(flashErr, err.Error())
		return
	}
	v.a.flash(flashInfo, "File saved to "+path)
}

func sanitize(s string) string {
	return regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(s, "_")
}

// stateDir is where swarmlens keeps dumps: $XDG_STATE_HOME/swarmlens/….
func stateDir(parts ...string) string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(append([]string{base, "swarmlens"}, parts...)...)
}

// ---- drawing ----

// scopeName is what the title shows in parentheses.
func (v *tableView) scopeName() string {
	switch {
	case v.sub != "":
		return v.sub
	case v.stackFilter() != "":
		return v.stackFilter()
	}
	return "all"
}

func (v *tableView) title() []seg {
	t := v.a.th
	plain := t.s(t.frame.title.fg)
	segs := []seg{
		{v.res.key + "(", plain.bold()},
		{v.scopeName(), t.s(t.frame.title.highlight).bold()},
		{")[", plain},
		{fmt.Sprint(len(v.rows)), t.s(t.frame.title.counter).bold()},
		{"]", plain},
	}
	if v.faults {
		segs = append(segs, seg{" ⚠", t.s(t.frame.status.err).bold()})
	}
	if v.filter != "" {
		segs = append(segs,
			seg{" <", plain},
			seg{"/" + v.filter, style{fg: t.frame.title.filter, bg: t.body.bg, attrs: attrReverse}},
			seg{">", plain})
	}
	return segs
}

type colPlan struct{ idx, x, w int }

// layout places the visible columns across avail cells. Like k9s, columns
// get their natural width and the spare room is shared out evenly, so a
// table with few columns spreads across the screen.
func (v *tableView) layout(avail int) []colPlan {
	cols := v.visibleCols()
	if len(cols) == 0 || avail <= 0 {
		return nil
	}
	const gap = 2
	nat := make([]int, len(cols))
	lo := make([]int, len(cols))
	for i, ci := range cols {
		c := v.res.cols[ci]
		w := textWidth(c.name) + 1 // room for the sort arrow
		hdr := w
		for _, r := range v.rows {
			if cw := textWidth(r.cells[ci]); cw > w {
				w = cw
			}
		}
		if c.max > 0 && w > c.max {
			w = c.max
		}
		// min is how far the column may be squeezed on a narrow screen: the
		// identifying columns hold out longest. It never widens a column.
		lo[i] = min(max(hdr, c.min), w)
		nat[i] = w
	}
	total := func() int {
		s := gap * (len(nat) - 1)
		for _, w := range nat {
			s += w
		}
		return s
	}

	// Too wide: squeeze the columns with the most slack towards their
	// floors, then drop the least valuable ones, and only as a last resort
	// squeeze below the floors.
	for total() > avail {
		best, room := -1, 0
		for i := range nat {
			if r := nat[i] - lo[i]; r > room {
				best, room = i, r
			}
		}
		if best < 0 {
			break
		}
		nat[best]--
	}
	for total() > avail {
		idx, rank := -1, 0
		for i := range cols {
			if r := v.res.cols[cols[i]].drop; r > 0 && r >= rank {
				idx, rank = i, r // rightmost among equals
			}
		}
		if idx < 0 {
			break
		}
		nat = append(nat[:idx], nat[idx+1:]...)
		lo = append(lo[:idx], lo[idx+1:]...)
		cols = append(cols[:idx], cols[idx+1:]...)
	}
	for total() > avail {
		widest := 0
		for i := range nat {
			if nat[i] > nat[widest] {
				widest = i
			}
		}
		if nat[widest] <= 3 {
			break
		}
		nat[widest]--
	}
	for total() > avail && len(nat) > 1 {
		nat, lo, cols = nat[:len(nat)-1], lo[:len(lo)-1], cols[:len(cols)-1]
	}
	if total() > avail {
		nat[0] = avail
	}

	// Spare room is shared evenly.
	if spare := avail - total(); spare > 0 {
		each, extra := spare/len(nat), spare%len(nat)
		for i := range nat {
			nat[i] += each
			if i < extra {
				nat[i]++
			}
		}
	}

	plans := make([]colPlan, len(cols))
	x := 0
	for i, ci := range cols {
		plans[i] = colPlan{idx: ci, x: x, w: nat[i]}
		x += nat[i] + gap
		if i == len(cols)-1 {
			plans[i].w = min(plans[i].w, avail-plans[i].x)
		}
	}
	return plans
}

func (v *tableView) draw(p painter, r rect, focused bool) {
	t := v.a.th
	border := t.s(t.frame.border).bold()
	if focused {
		border = t.s(t.frame.focus).bold()
	}
	p.frame(r, border, v.title())

	inner := r.inset(1)
	content := rect{inner.x + 1, inner.y, max(inner.w-2, 0), inner.h}
	if content.w <= 0 || content.h <= 0 {
		return
	}
	cp := p.sub(content)
	plans := v.layout(content.w)

	// Header.
	hdr := t.s(t.table.header.fg)
	for _, pl := range plans {
		c := v.res.cols[pl.idx]
		x := content.x + pl.x
		name := c.name
		sorted := pl.idx == v.sortCol
		st := hdr
		if sorted {
			st = t.s(t.table.header.sorted)
		}
		if pl.idx == v.selCol {
			st = st.underline()
		}
		arrow := ""
		if sorted {
			arrow = "↓"
			if v.asc {
				arrow = "↑"
			}
		}
		room := pl.w - textWidth(arrow)
		name = ellipsize(name, max(room, 1))
		if c.align == alignRight {
			x += pl.w - textWidth(name) - textWidth(arrow)
		}
		cp.text(x, content.y, name, st)
		if arrow != "" {
			cp.text(x+textWidth(name), content.y, arrow, t.s(t.table.header.sorter).bold())
		}
	}

	// Rows, windowed so the cursor stays visible.
	v.pageH = content.h - 1
	if v.pageH < 1 {
		return
	}
	if v.cursor < v.offset {
		v.offset = v.cursor
	}
	if v.cursor >= v.offset+v.pageH {
		v.offset = v.cursor - v.pageH + 1
	}
	if v.offset > max(len(v.rows)-v.pageH, 0) {
		v.offset = max(len(v.rows)-v.pageH, 0)
	}
	for i := 0; i < v.pageH && v.offset+i < len(v.rows); i++ {
		ri := v.offset + i
		rw := v.rows[ri]
		y := content.y + 1 + i
		line := rect{content.x, y, content.w, 1}

		col := rw.role
		if rw.delta != roleStd && col != roleErr {
			col = rw.delta
		}
		st := t.s(t.roleColor(col))
		cursorBg := t.table.cursorBg
		if v.alt {
			st, cursorBg = t.s(t.table.alias.fg), t.table.alias.cursorBg
		}
		marked := v.marks[rw.id]
		cursor := ri == v.cursor
		switch {
		case cursor:
			st = style{fg: t.table.cursorFg, bg: cursorBg, attrs: attrBold}
		case marked:
			st = style{fg: cBlack, bg: t.table.mark}
		}
		if cursor || marked {
			cp.fill(line, st)
		}
		for _, pl := range plans {
			text := ellipsize(rw.cells[pl.idx], pl.w)
			x := content.x + pl.x
			if v.res.cols[pl.idx].align == alignRight {
				x += pl.w - textWidth(text)
			}
			cp.text(x, y, text, st)
		}
	}
}
