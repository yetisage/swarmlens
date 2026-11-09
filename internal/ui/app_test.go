package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"

	"github.com/yetisage/swarmlens/internal/api"
	"github.com/yetisage/swarmlens/internal/config"
)

func TestInitialViewIsServicesWithHeader(t *testing.T) {
	a := newTestApp(t, 140, 38)
	s := a.screenText()
	mustContain(t, s,
		"Context:  demo [RW]", "Swarm:    acme-prod (6/7 ready)", "Node:     manager-1 (leader)",
		"services(all)[14]", "NAME↑", "shop_worker", "<services>",
		"<0>", "<?>", "swarm", // menu and logo
	)
}

func TestCommandPromptSwitchesResource(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.command("nodes")
	s := a.screenText()
	mustContain(t, s, "nodes(all)[7]", "worker-4", "<nodes>")
	mustNotContain(t, s, "<services>")

	a.command("ps") // an alias
	mustContain(t, a.screenText(), "tasks(all)[")
}

func TestCommandWithStackAndFilter(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.command("svc shop /api")
	s := a.screenText()
	mustContain(t, s, "services(shop)[1]", "</api>", "shop_api")
	mustNotContain(t, s, "shop_web", "monitoring_grafana")
	if a.scope != "shop" {
		t.Errorf("scope = %q, want shop", a.scope)
	}
	a.command("svc all")
	if a.scope != "" {
		t.Errorf("scope = %q after 'all', want empty", a.scope)
	}
}

func TestUnknownCommandShowsTheWhaleAndDismisses(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.command("zzz")
	mustContain(t, a.screenText(), "<error>", "`zzz` command not found", "Dismiss")
	a.press("enter")
	mustNotContain(t, a.screenText(), "<error>")
	mustContain(t, a.screenText(), "services(all)")
}

func TestPromptSuggestionsAndTabCompletion(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.press(":")
	a.typeText("sec")
	if g := a.ghost(); g != "ret" {
		t.Errorf("ghost after %q = %q, want the shortest candidate to extend it", "sec", g)
	}
	a.press("tab")
	if got := a.pr.in.String(); got != "secret" {
		t.Errorf("after tab the prompt holds %q, want secret", got)
	}
	a.press("enter")
	mustContain(t, a.screenText(), "secrets(all)")

	// With nothing typed the history is offered.
	a.press(":")
	if g := a.ghost(); g != "secret" {
		t.Errorf("empty prompt ghost = %q, want the last command %q", g, "secret")
	}
	a.press("esc")
	if a.pr.mode != promptOff {
		t.Error("esc must close the prompt")
	}
}

func TestStackNameCompletionAfterResource(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.press(":")
	a.typeText("svc mon")
	if g := a.ghost(); g != "itoring" {
		t.Errorf("ghost = %q, want itoring", g)
	}
}

func TestStackKeysSwitchScope(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.press("1") // favourites are alphabetical until used: ci
	mustContain(t, a.screenText(), "services(ci)[3]")
	mustNotContain(t, a.screenText(), "shop_web")
	a.press("0")
	mustContain(t, a.screenText(), "services(all)[14]")

	// The most recently used stack moves to the front.
	a.press("3") // shop
	a.press("0")
	if got := a.favStacks(); got[0] != "shop" {
		t.Errorf("favourites = %v, want shop first", got)
	}
}

func TestEnterDrillsIntoTasksAndEscGoesBack(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.command("svc /shop_web")
	a.press("enter")
	s := a.screenText()
	mustContain(t, s, "tasks(shop_web)[", "shop_web.1", "<services>", "<tasks>")
	mustNotContain(t, s, "SERVICE ", "STACK ", "<0>") // drilled views drop the scope columns and keys

	a.press("esc")
	mustContain(t, a.screenText(), "services(all)")
	mustNotContain(t, a.screenText(), "<tasks>")

	// q goes back too, and does nothing at the root, like k9s.
	a.press("q")
	mustContain(t, a.screenText(), "services(all)")
	if len(a.stack) != 1 {
		t.Errorf("stack depth %d after q at the root", len(a.stack))
	}
}

func TestNodeDrillDownShowsItsTasks(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.command("nodes")
	a.press("down", "down", "down") // worker-1
	a.press("enter")
	mustContain(t, a.screenText(), "tasks(node:worker-1)[")
}

func TestFilterIsLiveAndEscClearsIt(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.press("/")
	a.typeText("shop")
	if a.pr.mode != promptFilter {
		t.Fatal("/ must open the filter prompt")
	}
	mustContain(t, a.screenText(), "services(all)[5]", "</shop>") // already filtering while typing
	a.press("enter")
	if a.pr.mode != promptOff {
		t.Error("enter must close the prompt")
	}
	mustContain(t, a.screenText(), "</shop>")
	a.press("esc")
	mustContain(t, a.screenText(), "services(all)[14]")
	mustNotContain(t, a.screenText(), "</shop>")
}

func TestInverseFilter(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.press("/")
	a.typeText("!shop")
	a.press("enter")
	s := a.screenText()
	mustContain(t, s, "services(all)[9]", "monitoring_grafana")
	mustNotContain(t, s, "shop_web")
}

func TestFilterEscWhileTypingCancelsIt(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.press("/")
	a.typeText("shop")
	a.press("esc")
	mustContain(t, a.screenText(), "services(all)[14]")
	if a.pr.mode != promptOff {
		t.Error("prompt still open")
	}
}

func TestSortKeysToggleDirection(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.press("R")
	mustContain(t, a.screenText(), "REPLICAS↑")
	tv := a.view().(*tableView)
	if first := tv.rows[0].name; first != "ci_backup" && first != "ci_legacy" {
		t.Errorf("ascending replicas ratio should list 0/x first, got %s", first)
	}
	a.press("R")
	mustContain(t, a.screenText(), "REPLICAS↓")
	a.press("N")
	mustContain(t, a.screenText(), "NAME↑")
}

func TestSelectedColumnSort(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.press("shift+right") // STACK
	a.press("shift+right") // NAME
	a.press("shift+right") // MODE
	a.press("O")
	mustContain(t, a.screenText(), "MODE↑")
}

func TestFaultsToggle(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.press("ctrl+z")
	s := a.screenText()
	mustContain(t, s, "services(all)[1]", "shop_worker")
	mustNotContain(t, s, "shop_web")
	a.press("ctrl+z")
	mustContain(t, a.screenText(), "services(all)[14]")
}

func TestWideToggleAddsColumns(t *testing.T) {
	a := newTestApp(t, 200, 38)
	mustNotContain(t, a.screenText(), "CONSTRAINTS")
	a.press("ctrl+w")
	mustContain(t, a.screenText(), "CONSTRAINTS", "UPDATED")
	a.press("ctrl+w")
	mustNotContain(t, a.screenText(), "CONSTRAINTS")
}

func TestMarksAndBulkDelete(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.command("svc /shop_(cache|db)")
	a.press("space", "space")
	tv := a.view().(*tableView)
	if len(tv.marks) != 2 {
		t.Fatalf("marked %d rows, want 2", len(tv.marks))
	}
	a.press("ctrl+d")
	mustContain(t, a.screenText(), "<Delete>", "Delete 2 services?", "Cancel", "OK")
	a.press("tab", "enter") // move the focus to OK
	if a.modal != nil {
		t.Fatal("dialog still open")
	}
	a.refreshNow()
	a.command("svc all")
	s := a.screenText()
	mustNotContain(t, s, "shop_cache", "shop_db")
	mustContain(t, s, "shop_web", "services(all)[12]")
}

func TestDeleteDialogDefaultsToCancel(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.press("ctrl+d")
	d, ok := a.modal.(*dialog)
	if !ok {
		t.Fatal("no dialog")
	}
	if d.focus != 0 || d.buttons[0].label != "Cancel" {
		t.Errorf("focus on %d (%s), want Cancel first so a stray enter cannot delete", d.focus, d.buttons[d.focus].label)
	}
	a.press("enter")
	a.refreshNow()
	mustContain(t, a.screenText(), "services(all)[14]")
}

func TestScaleDialogSetsReplicas(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.command("svc /shop_web")
	a.press("s")
	mustContain(t, a.screenText(), "<Scale>", `Scale service "shop_web"?`, "Replicas: 4")
	for i := 0; i < 3; i++ {
		a.press("backspace")
	}
	a.typeText("6")
	a.typeText("x") // not a digit: ignored
	a.press("enter")
	if a.modal != nil {
		t.Fatal("dialog still open")
	}
	svcs, _ := a.be.Services(context.Background())
	for _, s := range svcs {
		if s.Name == "shop_web" && s.Desired != 6 {
			t.Errorf("shop_web desired = %d, want 6", s.Desired)
		}
	}
	mustContain(t, a.screenText(), "Scaled shop_web to 6")
}

func TestScaleRefusedForGlobalServices(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.command("svc /traefik")
	a.press("s")
	if a.modal != nil {
		t.Error("no dialog for a global service")
	}
	mustContain(t, a.screenText(), "global mode and cannot be scaled")
}

func TestRestartRollsServiceAndShowsUpdate(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.command("svc /shop_web")
	a.press("r")
	a.refreshNow()
	mustContain(t, a.screenText(), "Restarted shop_web", "updating")
}

func TestRollbackNeedsPreviousSpec(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.command("svc /shop_db")
	a.press("b")
	mustContain(t, a.screenText(), "no previous spec")

	a.command("svc /shop_api")
	a.press("b")
	mustContain(t, a.screenText(), "<Rollback>", "Roll back service")
	a.press("tab", "enter")
	a.refreshNow()
	mustContain(t, a.screenText(), "Rolled back shop_api", "rollback started")
}

func TestNodeActionsDrainAndConfirm(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.command("nodes")
	a.press("down", "down", "down") // worker-1
	a.press("r")
	mustContain(t, a.screenText(), "<Drain>", `Drain node "worker-1"?`)
	a.press("tab", "enter")
	a.refreshNow()
	tv := a.view().(*tableView)
	for _, r := range tv.rows {
		if r.name == "worker-1" && r.cells[ndAvail] != "drain" {
			t.Errorf("worker-1 availability = %q, want drain", r.cells[ndAvail])
		}
	}

	a.press("c") // cordon is immediate (no confirmation)
	a.refreshNow()
	for _, r := range a.view().(*tableView).rows {
		if r.name == "worker-1" && r.cells[ndAvail] != "pause" {
			t.Errorf("after cordon: %q", r.cells[ndAvail])
		}
	}
}

func TestPromoteAlreadyManagerIsRefused(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.command("nodes")
	a.press("p") // manager-1 is a manager already
	if a.modal != nil {
		t.Error("promoting a manager must not open a dialog")
	}
	mustContain(t, a.screenText(), "already a manager")
}

func TestReadOnlyModeBlocksMutations(t *testing.T) {
	opt := testOpts()
	opt.ReadOnly = true
	a := newTestAppOpts(t, opt, 140, 38)
	s := a.screenText()
	mustContain(t, s, "demo [RO]")
	mustNotContain(t, s, "<ctrl-d>", "<s>", "<r>", "<b>") // mutating actions leave the menu
	mustContain(t, s, "<d>", "<l>")                       // reading ones stay
	a.press("ctrl+d")
	if a.modal != nil {
		t.Error("a delete dialog opened in read-only mode")
	}
	mustContain(t, a.screenText(), "read-only mode")
}

func TestDescribeAndYAMLViews(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.command("svc /shop_web")
	a.press("d")
	s := a.screenText()
	mustContain(t, s, "describe(shop_web)", "<describe>", "Name:", "shop_web", "<f>", "Toggle FullScreen")
	a.press("esc")
	a.press("y")
	mustContain(t, a.screenText(), "yaml(shop_web)", "<yaml>", "Name: shop_web")
	a.press("esc")
	mustContain(t, a.screenText(), "services(all)")
}

func TestTextViewSearchAndFullscreen(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.command("svc /shop_web")
	a.press("y")
	a.press("/")
	a.typeText("Image")
	mustContain(t, a.screenText(), "</Image>")
	tv := a.view().(*textView)
	if len(tv.matches) == 0 {
		t.Error("no matches found for Image")
	}
	a.press("enter")
	a.press("n")
	a.press("esc") // clears the search first
	mustNotContain(t, a.screenText(), "</Image>")
	mustContain(t, a.screenText(), "yaml(shop_web)")

	a.press("f")
	s := a.screenText()
	mustNotContain(t, s, "Context:") // fullscreen hides the header...
	mustNotContain(t, s, "<yaml>")   // ...and the crumbs
	a.press("f")
	mustContain(t, a.screenText(), "Context:")
}

func TestLogsViewTogglesAndStops(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.command("svc /shop_web")
	a.press("l")
	s := a.screenText()
	mustContain(t, s, "logs(shop_web)[tail 100]", "<logs>", "Autoscroll:On", "FullScreen:Off", "Timestamps:Off", "Wrap:Off", "shop_web.")
	lv := a.view().(*logView)
	if len(lv.lines) == 0 {
		t.Fatal("no log lines loaded")
	}

	a.press("t")
	mustContain(t, a.screenText(), "Timestamps:On")
	a.press("w")
	mustContain(t, a.screenText(), "Wrap:On")
	a.press("s")
	mustContain(t, a.screenText(), "Autoscroll:Off")
	a.press("s")
	mustContain(t, a.screenText(), "Autoscroll:On")

	a.press("2") // last 5 minutes
	mustContain(t, a.screenText(), "logs(shop_web)[last 5m]")
	a.press("0")
	mustContain(t, a.screenText(), "[tail 100]")

	a.press("esc")
	if lv.cancel != nil {
		t.Error("leaving the logs must cancel the stream")
	}
	mustContain(t, a.screenText(), "services(all)")
}

func TestLogsFilterAndANSIColours(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.command("svc /shop_worker")
	a.press("l")
	lv := a.view().(*logView)
	s := a.screenText()
	mustContain(t, s, "INFO") // the ANSI-coloured level, with the escape sequences gone
	mustNotContain(t, s, "\x1b", "[32m")

	a.press("/")
	a.typeText("ERROR")
	a.press("enter")
	mustContain(t, a.screenText(), "</ERROR>")
	for _, l := range lv.shown() {
		if !strings.Contains(l.Text, "ERROR") {
			t.Fatalf("line %q passed the ERROR filter", l.Text)
		}
	}
	mustNotContain(t, a.screenText(), "INFO")
}

func TestTaskLogsAndJumpOwner(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.command("svc /shop_web")
	a.press("enter") // tasks of the service
	a.press("enter") // enter on a task opens its logs
	mustContain(t, a.screenText(), "logs(shop_web.")
	a.press("esc")
	a.press("J")
	tv := a.view().(*tableView)
	if tv.res.key != "services" || tv.selected() == nil || tv.selected().name != "shop_web" {
		t.Fatalf("jump owner: view %s, selected %v, want services with shop_web selected", tv.res.key, tv.selected())
	}
	mustContain(t, a.screenText(), "services(all)[14]")
}

func TestHelpListsTheKeysOfTheCurrentView(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.press("?")
	s := a.screenText()
	mustContain(t, s, "Help", "RESOURCE", "GENERAL", "NAVIGATION", "HOTKEYS", "<ctrl-d>", "Delete", "<esc>", "Toggle Wide", "<:cmd>", "<shift-g>", "<help>")
	a.press("?") // toggles
	mustContain(t, a.screenText(), "services(all)")
	a.press("?")
	a.press("esc")
	mustContain(t, a.screenText(), "services(all)")
}

func TestAliasView(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.press("ctrl+a")
	mustContain(t, a.screenText(), "aliases(all)[", "RESOURCE", "service services srv svc", "<aliases>")
	// Enter goes to the resource under the cursor.
	a.press("down") // nodes? first sorted row is "aliases" itself; step to a real one
	a.press("enter")
	if _, ok := a.view().(*tableView); !ok || a.view().crumb() == "aliases" {
		t.Errorf("enter on an alias did not navigate (crumb %q)", a.view().crumb())
	}
}

func TestUserAliasesFromConfig(t *testing.T) {
	opt := testOpts()
	opt.Config = nil
	a := newTestAppOpts(t, opt, 140, 38)
	a.cfg.Aliases["dp"] = "services"
	a.cfg.Aliases["sec"] = "secrets"
	a.command("nodes")
	a.command("dp")
	mustContain(t, a.screenText(), "services(all)")
	a.press("ctrl+a")
	mustContain(t, a.screenText(), "dp")
}

func TestHistoryKeys(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.command("nodes")
	a.command("secrets")
	a.press("[")
	mustContain(t, a.screenText(), "nodes(all)")
	a.press("[")
	mustContain(t, a.screenText(), "services(all)")
	a.press("]")
	mustContain(t, a.screenText(), "nodes(all)")
	a.press("]", "]")
	mustContain(t, a.screenText(), "secrets(all)")
	mustContain(t, a.screenText(), "forward any further")
	a.press("-") // last used command: swaps with the one before
	mustContain(t, a.screenText(), "nodes(all)")
	a.press("-")
	mustContain(t, a.screenText(), "secrets(all)")
}

func TestHeaderAndCrumbToggles(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.press("ctrl+e")
	mustNotContain(t, a.screenText(), "Context:")
	a.press("ctrl+e")
	mustContain(t, a.screenText(), "Context:")
	a.press("ctrl+g")
	mustNotContain(t, a.screenText(), "<services>")
	a.press("ctrl+g")
	mustContain(t, a.screenText(), "<services>")
}

func TestCrumbsStackAndFlashExpiry(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.command("svc /shop_web")
	a.press("enter")
	a.press("d")
	mustContain(t, a.screenText(), "<services>", "<tasks>", "<describe>")

	a.flash(flashErr, "boom")
	mustContain(t, a.screenText(), "boom")
	a.flashMsg.until = a.flashMsg.until.Add(-flashDuration * 2)
	mustNotContain(t, a.screenText(), "boom")
}

func TestMultiRuneKeyMessagesAreSplit(t *testing.T) {
	a := newTestApp(t, 140, 38)
	// Characters read in one go arrive as a single message.
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(":no")})
	a.drain(cmd)
	if a.pr.mode != promptCommand || a.pr.in.String() != "no" {
		t.Fatalf("prompt mode %v text %q, want a command prompt holding \"no\"", a.pr.mode, a.pr.in.String())
	}
	a.press("enter")
	mustContain(t, a.screenText(), "nodes(all)")
}

func TestPasteGoesIntoThePromptOnly(t *testing.T) {
	a := newTestApp(t, 140, 38)
	_, _ = a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("nodes"), Paste: true})
	mustContain(t, a.screenText(), "services(all)") // ignored outside inputs

	a.press("/")
	_, _ = a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("shop_web"), Paste: true})
	if a.pr.in.String() != "shop_web" {
		t.Errorf("pasted text %q", a.pr.in.String())
	}
	mustContain(t, a.screenText(), "services(all)[1]")
}

func TestTooSmallTerminal(t *testing.T) {
	a := newTestApp(t, 30, 8)
	mustContain(t, a.screenText(), "Terminal too small")
}

func TestSmallTerminalsStayWellFormed(t *testing.T) {
	for _, size := range [][2]int{{40, 10}, {60, 20}, {80, 24}, {100, 30}, {200, 60}} {
		a := newTestApp(t, size[0], size[1])
		for _, cmd := range []string{"svc", "tasks", "nodes", "stacks", "events", "ctx"} {
			a.command(cmd)
			c := a.screen()
			lines := strings.Split(c.Text(), "\n")
			if len(lines) != size[1] {
				t.Errorf("%dx%d %s: %d lines, want %d", size[0], size[1], cmd, len(lines), size[1])
			}
			for i, l := range lines {
				if w := textWidth(l); w > size[0] {
					t.Errorf("%dx%d %s: line %d is %d cells wide", size[0], size[1], cmd, i, w)
				}
			}
		}
		a.press("?")
		a.press("esc")
	}
}

func TestEventsViewCollectsEvents(t *testing.T) {
	a := newTestApp(t, 140, 38)
	for i := 0; i < 3; i++ {
		a.Update(eventMsg{ev: api.Event{Type: "service", Action: "update", Name: "shop_web", Stack: "shop", Attrs: map[string]string{"updatestate.new": "updating"}}})
	}
	a.command("events")
	s := a.screenText()
	mustContain(t, s, "events(all)[3]", "service", "update", "shop_web", "updatestate.new=updating")
}

func TestEventTriggersDebouncedRefresh(t *testing.T) {
	a := newTestApp(t, 140, 38)
	_, _ = a.Update(eventMsg{ev: api.Event{Type: "service", Action: "update", Name: "x"}})
	if !a.eventPending {
		t.Error("an event must schedule a refresh")
	}
	_, _ = a.Update(refreshMsg{})
	if a.eventPending {
		t.Error("the refresh must clear the pending flag")
	}
}

func TestContextSwitch(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.command("ctx")
	mustContain(t, a.screenText(), "contexts(all)[2]", "demo(*)", "lab")
	a.press("down", "enter")
	if a.be.ContextName() != "lab" {
		t.Fatalf("context = %q, want lab", a.be.ContextName())
	}
	s := a.screenText()
	mustContain(t, s, "Context:  lab", "services(all)")

	a.command("ctx demo")
	if a.be.ContextName() != "demo" {
		t.Errorf("context = %q after :ctx demo", a.be.ContextName())
	}
}

func TestStackDeleteRemovesEverything(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.command("stacks")
	a.press("down", "down") // shop
	a.press("ctrl+d")
	mustContain(t, a.screenText(), `Delete stack "shop"?`, "Volumes are kept")
	a.press("tab", "enter")
	a.refreshNow()
	mustContain(t, a.screenText(), "stacks(all)[2]", "Deleted stack shop")
	for _, r := range a.view().(*tableView).rows {
		if r.name == "shop" {
			t.Error("stack shop is still listed")
		}
	}
}

func TestPluginsAppearInMenuAndHelp(t *testing.T) {
	opt := testOpts()
	a := newTestAppOpts(t, opt, 140, 38)
	a.cfg.Plugins = append(a.cfg.Plugins, pluginForTest("L", "Logs in a pane", []string{"services"}))
	s := a.screenText()
	mustContain(t, s, "<shift-l>", "Logs in a pane")
	a.press("?")
	mustContain(t, a.screenText(), "<shift-l>")
	a.press("esc")
	a.command("nodes") // scope: services only
	mustNotContain(t, a.screenText(), "Logs in a pane")
}

func TestPaletteSurvivesLowColourTerminals(t *testing.T) {
	opt := testOpts()
	opt.Profile = termenv.ANSI256
	a := newTestAppOpts(t, opt, 100, 30)
	out := a.View()
	if !strings.Contains(out, "\x1b[") || strings.Contains(out, "38;2;") {
		t.Error("a 256-colour terminal must not receive 24-bit colour sequences")
	}
}

func TestViewIsExactlyTheTerminalSize(t *testing.T) {
	a := newTestApp(t, 120, 36)
	out := a.View()
	if n := strings.Count(out, "\n") + 1; n != 36 {
		t.Errorf("View has %d lines, want 36", n)
	}
}

func TestQuitCommands(t *testing.T) {
	for _, word := range []string{"q", "quit", "q!", "exit"} {
		a := newTestApp(t, 140, 38)
		a.press(":")
		a.typeText(word)
		_, cmd := a.Update(keyMsg("enter"))
		if cmd == nil {
			t.Fatalf(":%s returned no command", word)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf(":%s did not quit", word)
		}
	}
	a := newTestApp(t, 140, 38)
	_, cmd := a.Update(keyMsg("ctrl+c"))
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("ctrl+c must quit")
	}
}

// failingBackend makes every summary fail, like an unreachable daemon.
type failingBackend struct{ api.Backend }

func (failingBackend) Summary(context.Context) (*api.Summary, error) {
	return nil, errString("dial unix /var/run/docker.sock: connect: no such file or directory")
}

type errString string

func (e errString) Error() string { return string(e) }

func TestUnreachableDaemonExplainsItselfOnce(t *testing.T) {
	opt := testOpts()
	opt.Backend = failingBackend{opt.Backend}
	a := New(opt)
	a.w, a.h = 140, 38
	a.drain(a.runCommand("services", true))
	a.drain(a.loadSummary())
	mustContain(t, a.screenText(), "<error>", "Cannot reach the Docker daemon", "no such file or directory", "Dismiss")
	mustContain(t, a.screenText(), "Context:  demo [RW]", "Swarm:    n/a") // the header still draws, with n/a

	a.press("enter") // dismiss
	a.drain(a.loadSummary())
	if a.modal != nil {
		t.Error("the dialog must not reappear on every retry")
	}
	mustContain(t, a.screenText(), "no such file or directory") // but the flash line keeps saying so

	// Once the daemon is back the failure state clears.
	a.Update(summaryMsg{s: &api.Summary{SwarmState: "active", Role: "manager"}})
	if a.unreachable {
		t.Error("a good summary must clear the failure state")
	}
}

func TestNotInASwarmExplainsWhy(t *testing.T) {
	for state, want := range map[string]struct {
		s    api.Summary
		text string
	}{
		"inactive": {api.Summary{SwarmState: "inactive"}, "not part of a swarm"},
		"worker":   {api.Summary{SwarmState: "active", Role: "worker"}, "worker node"},
	} {
		a := newTestApp(t, 140, 38)
		a.summary = nil
		a.Update(summaryMsg{s: &want.s})
		mustContain(t, a.screenText(), "<error>", want.text)
		a.press("esc")
		if a.modal != nil {
			t.Errorf("%s: dialog not dismissed", state)
		}
	}
}

func TestCtrlCQuitsEvenOverADialogOrPrompt(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.press("ctrl+d") // delete dialog
	if _, cmd := a.Update(keyMsg("ctrl+c")); cmd == nil || fmt.Sprintf("%T", cmd()) != "tea.QuitMsg" {
		t.Error("ctrl+c must quit while a dialog is open")
	}
	b := newTestApp(t, 140, 38)
	b.press(":")
	if _, cmd := b.Update(keyMsg("ctrl+c")); cmd == nil || fmt.Sprintf("%T", cmd()) != "tea.QuitMsg" {
		t.Error("ctrl+c must quit while the prompt is open")
	}
}

func TestStartupCommandAndStackOptions(t *testing.T) {
	opt := testOpts()
	opt.Command = "nodes"
	a := newTestAppOpts(t, opt, 140, 38)
	mustContain(t, a.screenText(), "nodes(all)[7]")

	opt = testOpts()
	opt.Stack = "shop"
	a = newTestAppOpts(t, opt, 140, 38)
	mustContain(t, a.screenText(), "services(shop)[5]")
	if got := a.favStacks(); got[0] != "shop" {
		t.Errorf("the starting stack should be a favourite, got %v", got)
	}

	opt = testOpts()
	opt.Command, opt.Stack = "tasks", "ci"
	a = newTestAppOpts(t, opt, 140, 38)
	mustContain(t, a.screenText(), "tasks(ci)[")
}

func TestCopyNamesFlashes(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.press("c")
	mustContain(t, a.screenText(), "Copied ci_backup to the clipboard")
	a.press("space", "space")
	a.press("c")
	mustContain(t, a.screenText(), "Copied 2 names to the clipboard")
}

// No two keys of a view may mean different things: a sort hotkey must not
// shadow an action, and nothing may steal the keys the app itself uses.
func TestKeyBindingsDoNotCollide(t *testing.T) {
	reserved := map[string]string{
		":": "command prompt", "/": "filter", "?": "help", "q": "back", "[": "history", "]": "history", "-": "history",
		"esc": "back", "ctrl+a": "aliases", "ctrl+e": "header", "ctrl+g": "crumbs", "ctrl+r": "reload", "ctrl+c": "quit",
		"j": "down", "k": "up", "g": "top", "G": "bottom", "ctrl+f": "page down", "ctrl+b": "page up",
		"up": "up", "down": "down", "pgup": "page", "pgdown": "page", "home": "top", "end": "bottom",
	}
	a := newTestApp(t, 140, 38)
	for _, res := range resources {
		v := newTableView(a, res, loadQuery{})
		seen := map[string]string{}
		for _, ac := range v.actions() {
			if why, bad := reserved[ac.key]; bad && ac.key != "esc" {
				t.Errorf("%s: %q (%s) is taken by the app (%s)", res.key, ac.key, ac.desc, why)
			}
			if len(ac.key) == 1 && ac.key[0] >= '0' && ac.key[0] <= '9' && res.scoped {
				t.Errorf("%s: digits switch stacks, %q cannot be bound", res.key, ac.key)
			}
			if prev, dup := seen[ac.key]; dup && prev != ac.desc {
				// enter and l may both open logs: only differing meanings are a bug
				t.Errorf("%s: key %q is both %q and %q", res.key, ac.key, prev, ac.desc)
			}
			seen[ac.key] = ac.desc
		}
	}
}

func TestAliasesDoesNotStack(t *testing.T) {
	a := newTestApp(t, 120, 30)
	a.command("aliases")
	n := len(a.stack)
	a.command("aliases")
	a.command("a")
	if len(a.stack) != n {
		t.Fatalf("aliases stacked: %d -> %d", n, len(a.stack))
	}
	a.command("help")
	m := len(a.stack)
	a.command("help")
	if len(a.stack) != m {
		t.Fatalf("help stacked")
	}
}

// Whatever -c asks for, the program must come up with a view to stand on.
// help, aliases, a typo and a context switch cannot be the root themselves:
// they run on top of the default view, as k9s falls back to its default.
func TestEveryStartCommandLeavesAView(t *testing.T) {
	cases := []struct {
		cmd       string
		root, top string // crumbs
		modal     bool   // an error dialog is open
	}{
		{"", "services", "services", false},
		{"services", "services", "services", false},
		{"svc shop", "services", "services", false},
		{"nodes", "nodes", "nodes", false},
		{"events", "events", "events", false},
		{"help", "services", "help", false},
		{"?", "services", "help", false},
		{"aliases", "services", "aliases", false},
		{"a", "services", "aliases", false},
		{"nosuchcommand", "services", "services", true},
		{"nosuchcommand shop", "services", "services", true},
		{"ctx lab", "services", "services", false},
	}
	for _, c := range cases {
		t.Run(c.cmd, func(t *testing.T) {
			opt := testOpts()
			opt.Command = c.cmd
			a := New(opt)
			a.w, a.h = 140, 38
			a.drain(a.Init())
			if len(a.stack) == 0 {
				t.Fatal("no view after Init")
			}
			if got := a.stack[0].crumb(); got != c.root {
				t.Errorf("root view = %q, want %q", got, c.root)
			}
			if got := a.view().crumb(); got != c.top {
				t.Errorf("top view = %q, want %q", got, c.top)
			}
			if (a.modal != nil) != c.modal {
				t.Errorf("error dialog open = %v, want %v", a.modal != nil, c.modal)
			}
			// what arrives after the start must be harmless
			for _, m := range []tea.Msg{tickMsg{}, summaryTick{}, refreshMsg{}, actionMsg{msg: "x"}, execDoneMsg{}} {
				a.Update(m)
			}
			_ = a.screenText()
		})
	}

	t.Run("ctx lab switches the context", func(t *testing.T) {
		opt := testOpts()
		opt.Command = "ctx lab"
		a := New(opt)
		a.w, a.h = 140, 38
		a.drain(a.Init())
		if a.be.ContextName() != "lab" {
			t.Errorf("context = %q, want lab", a.be.ContextName())
		}
	})
}

// The messages that keep arriving (ticks, results of actions) must survive an
// app that has no view yet.
func TestMessagesAreHarmlessWithoutAView(t *testing.T) {
	a := New(testOpts())
	a.w, a.h = 140, 38
	for _, m := range []tea.Msg{tickMsg{}, summaryTick{}, refreshMsg{}, actionMsg{msg: "x"}, actionMsg{err: fmt.Errorf("boom")}, execDoneMsg{}, execDoneMsg{err: fmt.Errorf("boom")}} {
		a.Update(m)
	}
	a.press("esc", ":", "?", "ctrl+r")
	if s := a.screenText(); !strings.Contains(s, "Revision") {
		t.Errorf("expected the splash screen, got:\n%s", s)
	}
}

// Marking nodes and draining them asks once and drains every marked node. It
// used to ask twice: the second dialog was opened by the first one's button
// and closed again by the key handler, so nothing happened at all.
func TestBulkDrainAsksOnceAndDrainsEveryMarkedNode(t *testing.T) {
	opt := testOpts()
	rec := &recorder{Backend: opt.Backend}
	opt.Backend = rec
	a := newTestAppOpts(t, opt, 140, 38)
	a.command("nodes")
	a.press("down", "down", "down") // worker-1
	a.press("space", "space")       // mark worker-1 and worker-2
	a.press("r")
	mustContain(t, a.screenText(), "<Drain>", "Drain 2 nodes?")
	a.press("tab", "enter")
	if a.modal != nil {
		t.Fatalf("a second dialog was left open: %T", a.modal)
	}
	if len(rec.calls) != 2 {
		t.Fatalf("backend calls = %v, want a drain for each of the 2 marked nodes", rec.calls)
	}
	a.refreshNow()
	drained := map[string]bool{}
	for _, r := range a.view().(*tableView).rows {
		if r.cells[ndAvail] == "drain" {
			drained[r.name] = true
		}
	}
	for _, want := range []string{"worker-1", "worker-2", "worker-3"} { // worker-3 was drained in the seed
		if !drained[want] {
			t.Errorf("%s is not drained: %v", want, drained)
		}
	}
	mustContain(t, a.screenText(), "Drained 2 nodes")
}

func TestADialogOpenedByAButtonIsNotClosedWithIt(t *testing.T) {
	a := newTestApp(t, 100, 30)
	a.confirm("First", "first?", func() tea.Cmd { return a.confirm("Second", "second?", nil) })
	a.press("tab", "enter")
	d, ok := a.modal.(*dialog)
	if !ok || d.title != "Second" {
		t.Fatalf("modal = %#v, want the second dialog", a.modal)
	}
	a.press("esc")
	if a.modal != nil {
		t.Error("esc must close the dialog that is open")
	}
}

// docker service rm a b c goes on after a failure; so do bulk actions here,
// and they say what failed instead of stopping at the first error.
func TestBulkDeleteGoesOnAfterAFailure(t *testing.T) {
	opt := testOpts()
	rec := &recorder{Backend: opt.Backend, fail: map[string]error{}}
	opt.Backend = rec
	a := newTestAppOpts(t, opt, 140, 38)
	a.command("svc /shop_(api|cache|db)")
	tv := a.view().(*tableView)
	if len(tv.rows) != 3 {
		t.Fatalf("%d rows, want 3", len(tv.rows))
	}
	rec.fail[tv.rows[0].id] = fmt.Errorf("update out of sequence")
	a.press("space", "space", "space")
	a.press("ctrl+d")
	mustContain(t, a.screenText(), "Delete 3 services?")
	a.press("tab", "enter")
	if len(rec.calls) != 3 {
		t.Fatalf("backend calls = %v, want an attempt for all 3", rec.calls)
	}
	if msg := a.flashMsg.text; !strings.Contains(msg, "1 of 3 failed") || !strings.Contains(msg, "update out of sequence") {
		t.Errorf("flash = %q, want the failure reported", msg)
	}
	a.refreshNow()
	left := map[string]bool{}
	for _, r := range a.view().(*tableView).rows {
		left[r.name] = true
	}
	if len(left) != 1 {
		t.Errorf("rows left = %v, want only the one that failed to go", left)
	}
}

func TestTextViewErrorRemovesOnlyItself(t *testing.T) {
	load := func(context.Context) (string, error) { return "", fmt.Errorf("gone") }

	// the usual case: it is on top and goes back to the table
	a := newTestApp(t, 120, 30)
	root := a.view()
	tv := newTextView(a, "describe", "ghost", load)
	a.push(tv)
	a.Update(textMsg{vid: tv.vid, err: fmt.Errorf("gone")})
	if a.view() != root || len(a.stack) != 1 {
		t.Errorf("stack after the error = %d views, top %T", len(a.stack), a.view())
	}

	// help was opened over it while it loaded: help must stay
	tv = newTextView(a, "describe", "ghost", load)
	a.push(tv)
	a.push(newHelpView(a))
	a.Update(textMsg{vid: tv.vid, err: fmt.Errorf("gone")})
	if _, ok := a.view().(*helpView); !ok {
		t.Fatalf("top = %T, want the help view to stay", a.view())
	}
	for _, v := range a.stack {
		if v == view(tv) {
			t.Error("the failed text view is still on the stack")
		}
	}
}

// Rows of the events view are identified by the number an event arrived
// under. Once the buffer is full, one new event used to rename every row.
func TestEventRowsKeepTheirIdentityWhenOldEventsRollOff(t *testing.T) {
	a := newTestApp(t, 140, 38)
	base := fixedNow.Add(-time.Hour)
	add := func(i int) {
		a.Update(eventMsg{ev: api.Event{Time: base.Add(time.Duration(i) * time.Second), Type: "service", Action: "update", Name: fmt.Sprintf("svc-%d", i)}})
	}
	for i := 0; i < maxEvents; i++ {
		add(i)
	}
	a.command("events")
	tv := a.view().(*tableView)
	tv.cursor = 100 // somewhere in the middle: the oldest row is about to roll off
	tv.syncSel()
	selected := tv.selected().name

	add(maxEvents) // the oldest event rolls off
	a.refreshNow()
	changed := 0
	for _, r := range tv.rows {
		if r.delta != roleStd {
			changed++
		}
	}
	if changed != 1 {
		t.Errorf("%d rows flagged as new or changed after a single event, want 1", changed)
	}
	if got := tv.selected(); got == nil || got.name != selected {
		t.Errorf("the selection moved off %s", selected)
	}
}

func TestLogRestartIgnoresMessagesFromTheOldStream(t *testing.T) {
	a := newTestApp(t, 140, 30)
	a.command("svc /shop_web")
	a.press("l")
	lv := a.view().(*logView)
	old := lv.gen
	a.press("1") // another time range restarts the stream
	if lv.gen == old {
		t.Fatal("a restart must start a new run of the stream")
	}
	n := len(lv.lines)
	a.Update(logMsg{vid: lv.vid, gen: old, lines: []api.LogLine{{Text: "stale"}}})
	a.Update(logEndMsg{vid: lv.vid, gen: old, err: fmt.Errorf("the old stream failed")})
	if len(lv.lines) != n || lv.ended || lv.err != nil {
		t.Errorf("a message from the old stream got through: %d lines (was %d), ended=%v, err=%v", len(lv.lines), n, lv.ended, lv.err)
	}
	a.Update(logMsg{vid: lv.vid, gen: lv.gen, lines: []api.LogLine{{Text: "fresh"}}})
	if len(lv.lines) != n+1 {
		t.Error("a message from the current stream must be kept")
	}
}

// A service that writes huge lines must not be able to exhaust memory
// through the viewer, whatever the line budget says.
func TestLogBufferIsBoundedInBytes(t *testing.T) {
	a := newTestApp(t, 140, 30)
	a.command("svc /shop_web")
	a.press("l")
	lv := a.view().(*logView)
	lv.bufMax = 1_000_000 // the line budget is not what limits here
	big := strings.Repeat("x", 1<<20)
	for i := 0; i < 100; i++ {
		a.Update(logMsg{vid: lv.vid, gen: lv.gen, lines: []api.LogLine{{Text: big}}})
	}
	if lv.bytes > maxLogBytes {
		t.Errorf("the view holds %d MiB of text, limit %d MiB", lv.bytes>>20, maxLogBytes>>20)
	}
	if len(lv.lines) == 0 || lv.lines[len(lv.lines)-1].Text != big {
		t.Error("the newest line must be kept")
	}
	sum := 0
	for _, l := range lv.lines {
		sum += len(l.Text)
	}
	if sum != lv.bytes {
		t.Errorf("byte accounting drifted: counted %d, summed %d", lv.bytes, sum)
	}
}

// int(hash) goes negative for about half the names where int is 32 bits wide
// (GOARCH=386, linux/arm); the CI runs this on 386 too.
func TestLogPrefixColourIsAlwaysAPaletteEntry(t *testing.T) {
	a := newTestApp(t, 100, 20)
	a.command("svc /shop_web")
	a.press("l")
	lv := a.view().(*logView)
	pal := map[color]bool{}
	for _, c := range a.th.logs.prefix {
		pal[c] = true
	}
	for i := 0; i < 5000; i++ {
		if c := lv.prefixColor(fmt.Sprintf("shop_web.%d", i)); !pal[c] {
			t.Fatalf("prefixColor(%d) = %#x, not in the palette", i, int(c))
		}
	}
}

// Read-only is not only a matter of hiding keys: the backend refuses changes,
// whatever asks, and still does after the context is switched.
func TestReadOnlyIsEnforcedByTheBackend(t *testing.T) {
	opt := testOpts()
	opt.ReadOnly = true
	a := newTestAppOpts(t, opt, 140, 38)
	refused := func() {
		t.Helper()
		ctx := context.Background()
		for name, err := range map[string]error{
			"Remove":       a.be.Remove(ctx, api.KindService, "x"),
			"ScaleService": a.be.ScaleService(ctx, "x", 1),
			"RemoveStack":  a.be.RemoveStack(ctx, "shop"),
		} {
			if !errors.Is(err, api.ErrReadOnly) {
				t.Errorf("%s on %s: %v, want ErrReadOnly", name, a.be.ContextName(), err)
			}
		}
	}
	refused()
	a.command("ctx lab")
	if a.be.ContextName() != "lab" {
		t.Fatalf("context = %q", a.be.ContextName())
	}
	refused()
	a.command("nodes")
	mustContain(t, a.screenText(), "nodes(all)[7]", "lab [RO]") // reading still works
}

func TestReadOnlyFromTheConfigFileWrapsTheBackendToo(t *testing.T) {
	cfg := config.Defaults()
	cfg.ReadOnly = true
	opt := testOpts()
	opt.Config = cfg
	a := newTestAppOpts(t, opt, 140, 38)
	if err := a.be.Remove(context.Background(), api.KindService, "x"); !errors.Is(err, api.ErrReadOnly) {
		t.Errorf("Remove: %v, want ErrReadOnly", err)
	}
}

// A shell changes whatever it is used for: read-only mode offers none, and
// says why if it is asked for anyway.
func TestReadOnlyModeHasNoShell(t *testing.T) {
	opt := testOpts()
	opt.ReadOnly = true
	ro := newTestAppOpts(t, opt, 140, 38)
	ro.command("tasks")
	mustNotContain(t, ro.screenText(), "Shell")
	ro.press("s")
	if got := ro.flashMsg.text; got != "Command disabled in read-only mode" {
		t.Errorf("the shell key flashed %q, want it refused", got)
	}

	rw := newTestApp(t, 140, 38)
	rw.command("tasks")
	mustContain(t, rw.screenText(), "Shell")
}
