package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"swarmlens/internal/api"
)

func svcRow(id, name, stack string, running, desired int, age string) *row {
	r := newRow(serviceRes, id, name)
	r.stack = stack
	r.cells[svcStack] = stack
	r.cells[svcName] = name
	r.cells[svcMode] = "replicated"
	r.cells[svcReplicas], r.nums[svcReplicas] = ratioCell(running, desired)
	r.cells[svcImage] = "nginx"
	r.cells[svcAge] = age
	return r
}

func plainTable(t *testing.T) *tableView {
	t.Helper()
	a := newTestApp(t, 140, 38)
	return newTableView(a, serviceRes, loadQuery{})
}

// The regression test for the stale selection bug of the first version: a
// live refresh that reorders rows must keep the same row selected.
func TestSelectionSurvivesReorder(t *testing.T) {
	v := plainTable(t)
	v.applyRows([]*row{svcRow("a", "alpha", "", 1, 1, "1d"), svcRow("b", "beta", "", 1, 1, "1d"), svcRow("c", "gamma", "", 1, 1, "1d")})
	v.move(1)
	if v.selected().name != "beta" {
		t.Fatalf("selected %s, want beta", v.selected().name)
	}
	// The refresh returns them in another order, and a new row sorts first.
	v.applyRows([]*row{svcRow("c", "gamma", "", 1, 1, "1d"), svcRow("b", "beta", "", 1, 1, "1d"), svcRow("z", "aardvark", "", 1, 1, "1d"), svcRow("a", "alpha", "", 1, 1, "1d")})
	if v.selected().name != "beta" {
		t.Errorf("selection moved to %s after the refresh, want beta", v.selected().name)
	}
	if v.cursor != 2 {
		t.Errorf("cursor = %d, want 2 (aardvark, alpha, beta)", v.cursor)
	}
}

func TestSelectionFallsBackWhenTheRowDisappears(t *testing.T) {
	v := plainTable(t)
	v.applyRows([]*row{svcRow("a", "alpha", "", 1, 1, ""), svcRow("b", "beta", "", 1, 1, "")})
	v.move(1)
	v.applyRows([]*row{svcRow("a", "alpha", "", 1, 1, "")})
	if v.selected() == nil || v.selected().name != "alpha" {
		t.Fatalf("selected %v, want a fallback to alpha", v.selected())
	}
	v.applyRows(nil)
	if v.selected() != nil || v.selID != "" {
		t.Error("an empty table has no selection")
	}
	v.move(1) // must not panic
	v.key(keyMsg("G"))
}

func TestDeltaMarksNewAndChangedRowsOnly(t *testing.T) {
	v := plainTable(t)
	first := []*row{svcRow("a", "alpha", "", 1, 1, "1d"), svcRow("b", "beta", "", 1, 2, "1d")}
	v.applyRows(first)
	for _, r := range first {
		if r.delta != roleStd {
			t.Errorf("%s was highlighted on the first load", r.name)
		}
	}
	second := []*row{
		svcRow("a", "alpha", "", 1, 1, "2d"), // only the age changed
		svcRow("b", "beta", "", 2, 2, "1d"),  // replicas changed
		svcRow("c", "gamma", "", 1, 1, "1d"), // new
	}
	v.applyRows(second)
	if got := [3]role{second[0].delta, second[1].delta, second[2].delta}; got != [3]role{roleStd, roleMod, roleAdd} {
		t.Errorf("deltas = %v, want [std mod add]: ages must not count as updates", got)
	}
	// One refresh later the highlight is gone.
	third := []*row{svcRow("a", "alpha", "", 1, 1, "2d"), svcRow("b", "beta", "", 2, 2, "1d"), svcRow("c", "gamma", "", 1, 1, "1d")}
	v.applyRows(third)
	for _, r := range third {
		if r.delta != roleStd {
			t.Errorf("%s is still highlighted", r.name)
		}
	}
}

func TestNumericColumnsSortByValue(t *testing.T) {
	v := plainTable(t)
	v.applyRows([]*row{svcRow("a", "a", "", 10, 10, ""), svcRow("b", "b", "", 1, 2, ""), svcRow("c", "c", "", 0, 4, "")})
	v.sortBy(svcReplicas)
	if names := rowNames(v); names != "c b a" {
		t.Errorf("ascending by replica ratio = %s, want c b a", names)
	}
	v.sortBy(svcReplicas)
	if names := rowNames(v); names != "a b c" {
		t.Errorf("descending = %s, want a b c", names)
	}
	// Text columns use natural order: web.2 before web.10.
	v.applyRows([]*row{svcRow("1", "web.10", "", 1, 1, ""), svcRow("2", "web.2", "", 1, 1, "")})
	v.sortBy(svcName)
	if names := rowNames(v); names != "web.2 web.10" {
		t.Errorf("natural order = %s", names)
	}
}

func rowNames(v *tableView) string {
	var out []string
	for _, r := range v.rows {
		out = append(out, r.name)
	}
	return strings.Join(out, " ")
}

func TestScopeFaultsAndFilterCombine(t *testing.T) {
	v := plainTable(t)
	bad := svcRow("b", "shop_worker", "shop", 1, 2, "")
	bad.fault = true
	v.applyRows([]*row{svcRow("a", "shop_web", "shop", 1, 1, ""), bad, svcRow("c", "ci_runner", "ci", 1, 1, "")})
	v.a.scope = "shop"
	v.rebuild()
	if got := rowNames(v); got != "shop_web shop_worker" {
		t.Errorf("scope shop → %s", got)
	}
	v.faults = true
	v.rebuild()
	if got := rowNames(v); got != "shop_worker" {
		t.Errorf("scope + faults → %s", got)
	}
	v.faults = false
	v.setFilter("web")
	if got := rowNames(v); got != "shop_web" {
		t.Errorf("scope + filter → %s", got)
	}
	v.setFilter("!web")
	if got := rowNames(v); got != "shop_worker" {
		t.Errorf("scope + inverse filter → %s", got)
	}
}

func TestMarkRangeAndToggle(t *testing.T) {
	v := plainTable(t)
	var rows []*row
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		rows = append(rows, svcRow(n, n, "", 1, 1, ""))
	}
	v.applyRows(rows)
	v.toggleMark() // marks a, moves to b
	v.cursor = 3
	v.syncSel()
	v.markRange()
	if len(v.marks) != 4 {
		t.Errorf("range marked %d rows, want 4 (a-d)", len(v.marks))
	}
	if got := len(v.targets()); got != 4 {
		t.Errorf("targets = %d, want the 4 marked rows", got)
	}
	v.marks = map[string]bool{}
	if got := v.targets(); len(got) != 1 || got[0].name != "d" {
		t.Errorf("with no marks the target is the selected row, got %v", got)
	}
	// Marks of rows that vanish are dropped.
	v.marks["z"] = true
	v.rebuild()
	if v.marks["z"] {
		t.Error("a mark for a row that no longer exists survived")
	}
}

func TestEmptyStackColumnIsHidden(t *testing.T) {
	v := plainTable(t)
	v.applyRows([]*row{svcRow("a", "alpha", "", 1, 1, "")})
	for _, i := range v.visibleCols() {
		if v.res.cols[i].name == "STACK" {
			t.Error("the STACK column must disappear when no service belongs to a stack")
		}
	}
	v.applyRows([]*row{svcRow("a", "alpha", "shop", 1, 1, "")})
	found := false
	for _, i := range v.visibleCols() {
		found = found || v.res.cols[i].name == "STACK"
	}
	if !found {
		t.Error("the STACK column must show once a stack exists")
	}
}

func TestColumnLayoutInvariants(t *testing.T) {
	a := newTestApp(t, 140, 38)
	for _, res := range resources {
		if res.key == "events" || res.key == "contexts" {
			continue
		}
		v := newTableView(a, res, loadQuery{})
		rows, err := res.load(nil, a.be, loadQuery{}) //nolint:staticcheck // the demo ignores the context
		if err != nil {
			t.Fatalf("%s: %v", res.key, err)
		}
		v.applyRows(rows)
		for _, wide := range []bool{false, true} {
			v.wide = wide
			for avail := 10; avail <= 220; avail += 7 {
				plans := v.layout(avail)
				if len(plans) == 0 {
					t.Errorf("%s: no columns fit %d cells", res.key, avail)
					continue
				}
				end := 0
				for i, pl := range plans {
					if pl.w <= 0 {
						t.Errorf("%s avail=%d: column %d has width %d", res.key, avail, i, pl.w)
					}
					if pl.x < end {
						t.Errorf("%s avail=%d: column %d overlaps its neighbour (x=%d, previous end %d)", res.key, avail, i, pl.x, end)
					}
					end = pl.x + pl.w
				}
				if end > avail {
					t.Errorf("%s avail=%d wide=%v: layout is %d cells wide", res.key, avail, wide, end)
				}
			}
		}
	}
}

func TestLayoutSharesSpareRoomLikeK9s(t *testing.T) {
	a := newTestApp(t, 140, 38)
	v := newTableView(a, contextRes, loadQuery{})
	v.applyRows([]*row{testRow(contextRes, "x", "x", "x", "d", "e")})
	plans := v.layout(136)
	if len(plans) != 3 {
		t.Fatalf("plans = %+v", plans)
	}
	last := plans[len(plans)-1]
	if last.x+last.w != 136 {
		t.Errorf("columns end at %d, want the table filled to 136", last.x+last.w)
	}
	widths := []int{plans[0].w, plans[1].w, plans[2].w}
	for _, w := range widths {
		if w < 40 {
			t.Errorf("widths = %v: with few columns they should spread across the screen", widths)
		}
	}
}

func TestRowRoles(t *testing.T) {
	svc := func(mode, update string, running, desired int) api.Service {
		return api.Service{Mode: mode, UpdateState: update, Running: running, Desired: desired}
	}
	for name, c := range map[string]struct {
		got, want role
	}{
		"healthy":            {serviceRole(svc("replicated", "", 3, 3)), roleStd},
		"degraded":           {serviceRole(svc("replicated", "", 1, 3)), roleErr},
		"degraded+updating":  {serviceRole(svc("replicated", "updating", 1, 3)), rolePending},
		"updating":           {serviceRole(svc("replicated", "updating", 3, 3)), rolePending},
		"paused update":      {serviceRole(svc("replicated", "paused", 3, 3)), roleErr},
		"scaled to zero":     {serviceRole(svc("replicated", "", 0, 0)), roleCompleted},
		"global healthy":     {serviceRole(svc("global", "", 5, 5)), roleStd},
		"task running":       {taskRole(api.Task{State: "running", Desired: "running"}), roleStd},
		"task starting":      {taskRole(api.Task{State: "starting", Desired: "running"}), rolePending},
		"task failed":        {taskRole(api.Task{State: "failed", Desired: "running"}), roleErr},
		"task failed (old)":  {taskRole(api.Task{State: "failed", Desired: "shutdown"}), roleCompleted},
		"task shutdown":      {taskRole(api.Task{State: "shutdown", Desired: "shutdown"}), roleCompleted},
		"task being stopped": {taskRole(api.Task{State: "running", Desired: "shutdown"}), roleKill},
		"node ready":         {nodeRole(api.Node{Status: "ready", Availability: "active"}), roleStd},
		"node down":          {nodeRole(api.Node{Status: "down", Availability: "active"}), roleErr},
		"node drained":       {nodeRole(api.Node{Status: "ready", Availability: "drain"}), rolePending},
		"manager unreach.":   {nodeRole(api.Node{Status: "ready", Availability: "active", Manager: "unreachable"}), roleErr},
	} {
		if c.got != c.want {
			t.Errorf("%s: role %d, want %d", name, c.got, c.want)
		}
	}
}

func TestSaveWritesCSV(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	a := newTestApp(t, 140, 38)
	a.press("/")
	a.typeText("shop_w")
	a.press("enter")
	a.press("ctrl+s")
	matches, _ := filepath.Glob(filepath.Join(dir, "swarmlens", "screen-dumps", "demo", "services-*.csv"))
	if len(matches) != 1 {
		t.Fatalf("expected one csv under %s, found %v", dir, matches)
	}
	raw, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.HasPrefix(text, "NAME,MODE,REPLICAS,IMAGE,PORTS,UPDATE,AGE") && !strings.HasPrefix(text, "STACK,NAME,MODE") {
		t.Errorf("csv header: %q", strings.SplitN(text, "\n", 2)[0])
	}
	if !strings.Contains(text, "shop_web") || !strings.Contains(text, "shop_worker") || strings.Contains(text, "monitoring") {
		t.Errorf("csv must hold exactly the filtered rows:\n%s", text)
	}
	mustContain(t, a.screenText(), "File saved to")
}
