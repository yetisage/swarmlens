package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"swarmlens/internal/api"
)

func svc(id, name string, running, desired int) api.ServiceRow {
	return api.ServiceRow{
		ID: id, ShortID: api.ShortID(id), Name: name,
		Image: "nginx", Mode: "replicated",
		Running: running, Desired: desired,
		Replicas: itoa(running) + "/" + itoa(desired),
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func newTestApp() *app {
	a := NewApp(nil, &api.SwarmState{State: "active", Role: "manager"})
	a.width, a.height = 100, 30
	a.resize()
	return a
}

// TestSelectionSurvivesReorder is the regression test for the stale
// selection bug: a live refresh that reorders rows must keep the same
// service selected.
func TestSelectionSurvivesReorder(t *testing.T) {
	a := newTestApp()
	a.applyServices([]api.ServiceRow{svc("id-a", "alpha", 1, 1), svc("id-b", "beta", 1, 1)})

	// Select the second row.
	a.services.SetCursor(1)
	a.selectedServiceID = "id-b"
	if got := a.currentService(); got == nil || got.Name != "beta" {
		t.Fatalf("expected beta selected, got %v", got)
	}

	// The refresh returns the same services in the opposite order.
	a.applyServices([]api.ServiceRow{svc("id-b", "beta", 1, 1), svc("id-a", "alpha", 1, 1)})

	if got := a.currentService(); got == nil || got.Name != "beta" {
		t.Fatalf("selection lost across reorder, got %v", got)
	}
	if a.services.Cursor() != 0 {
		t.Errorf("cursor = %d, want 0 (beta moved to the first row)", a.services.Cursor())
	}
}

// TestSelectionFallsBackWhenServiceDisappears covers a service that is
// removed while it is selected.
func TestSelectionFallsBackWhenServiceDisappears(t *testing.T) {
	a := newTestApp()
	a.applyServices([]api.ServiceRow{svc("id-a", "alpha", 1, 1), svc("id-b", "beta", 1, 1)})
	a.services.SetCursor(1)
	a.selectedServiceID = "id-b"

	a.applyServices([]api.ServiceRow{svc("id-a", "alpha", 1, 1)})

	if got := a.currentService(); got == nil || got.Name != "alpha" {
		t.Fatalf("expected fallback to alpha, got %v", got)
	}
	if a.selectedServiceID != "id-a" {
		t.Errorf("selected id = %q, want id-a", a.selectedServiceID)
	}
}

// TestCurrentServiceNeverReturnsStaleData verifies the pointer fix: the
// resolved service must reflect the latest data, not an old snapshot.
func TestCurrentServiceNeverReturnsStaleData(t *testing.T) {
	a := newTestApp()
	a.applyServices([]api.ServiceRow{svc("id-a", "alpha", 1, 1)})
	a.selectedServiceID = "id-a"

	// The service is updated (new replica count) by a refresh.
	a.applyServices([]api.ServiceRow{svc("id-a", "alpha", 2, 3)})

	got := a.currentService()
	if got == nil {
		t.Fatal("currentService returned nil")
	}
	if got.Running != 2 || got.Desired != 3 {
		t.Errorf("stale data: running=%d desired=%d, want 2/3", got.Running, got.Desired)
	}
}

func TestFilterServices(t *testing.T) {
	a := newTestApp()
	rows := []api.ServiceRow{
		{ID: "1", Name: "web", Image: "nginx"},
		{ID: "2", Name: "db", Image: "postgres"},
		{ID: "3", Name: "web-cache", Image: "redis"},
	}

	a.filter = "web"
	got := a.filterServices(rows)
	if len(got) != 2 {
		t.Fatalf("filtered to %d rows, want 2", len(got))
	}

	a.filter = "redis"
	got = a.filterServices(rows)
	if len(got) != 1 || got[0].Name != "web-cache" {
		t.Fatalf("filter by image failed: %v", got)
	}

	a.filter = ""
	if len(a.filterServices(rows)) != 3 {
		t.Fatal("empty filter should match everything")
	}
}

func TestApplyFilterUpdatesTable(t *testing.T) {
	a := newTestApp()
	a.applyServices([]api.ServiceRow{
		{ID: "1", Name: "web", Image: "nginx"},
		{ID: "2", Name: "db", Image: "postgres"},
	})
	a.filter = "db"
	a.applyFilter()

	if len(a.services.Rows()) != 1 {
		t.Fatalf("table has %d rows, want 1", len(a.services.Rows()))
	}
}

// TestCommandSwitchesResourceViews checks that resource types are reached
// through the ":" command, as in k9s, rather than via dedicated keys.
func TestCommandSwitchesResourceViews(t *testing.T) {
	cases := []struct {
		word string
		want screen
	}{
		{"svc", screenServices},
		{"service", screenServices},
		{"services", screenServices},
		{"no", screenNodes},
		{"node", screenNodes},
		{"vol", screenVolumes},
		{"net", screenNetworks},
		{"sec", screenSecrets},
		{"cfg", screenConfigs},
	}
	for _, c := range cases {
		a := newTestApp()
		a.command.open()

		// Type the command word.
		for _, r := range c.word {
			a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
		a.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
		if a.screen != c.want {
			t.Errorf("command %q switched to %v, want %v", c.word, a.screen, c.want)
		}
	}
}

// TestCommandCarriesFilter checks the k9s-style ":resource /filter"
// argument.
func TestCommandCarriesFilter(t *testing.T) {
	a := newTestApp()
	a.applyServices([]api.ServiceRow{
		{ID: "1", Name: "web", Image: "nginx"},
		{ID: "2", Name: "db", Image: "postgres"},
	})
	a.command.open()
	a.command.input.SetValue("service /web")
	a.handleKey(tea.KeyMsg{Type: tea.KeyEnter})

	if a.screen != screenServices {
		t.Fatalf("screen = %v, want services", a.screen)
	}
	if a.filter != "web" {
		t.Errorf("filter = %q, want web", a.filter)
	}
	if len(a.visibleServices()) != 1 {
		t.Errorf("filtered to %d services, want 1", len(a.visibleServices()))
	}
}

// TestCommandDoesNotOfferActions checks that the command mode is for
// navigation only — no actions live there.
func TestCommandDoesNotOfferActions(t *testing.T) {
	if len(aliases) == 0 {
		t.Fatal("expected a resource alias table")
	}
	for _, al := range aliases {
		for _, n := range al.names() {
			if strings.Contains(n, "scale") || strings.Contains(n, "delete") {
				t.Errorf("the command mode must not offer actions, found %q", n)
			}
		}
	}
}

// TestCommandRejectsUnknownResource checks the error path.
func TestCommandRejectsUnknownResource(t *testing.T) {
	a := newTestApp()
	a.command.open()
	a.command.input.SetValue("bogus")
	a.handleKey(tea.KeyMsg{Type: tea.KeyEnter})

	if a.command.err == "" {
		t.Error("expected an error for an unknown command")
	}
	if a.screen != screenServices {
		t.Errorf("screen = %v, want to stay on services", a.screen)
	}
}

// TestCommandHistory checks the command history, like k9s' command buffer.
func TestCommandHistory(t *testing.T) {
	a := newTestApp()
	for _, word := range []string{"service", "node"} {
		a.command.open()
		a.command.input.SetValue(word)
		a.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	}
	if len(a.command.history) != 2 {
		t.Fatalf("history has %d entries, want 2", len(a.command.history))
	}
	a.command.historyPrev()
	if a.command.input.Value() != "node" {
		t.Errorf("history back = %q, want node", a.command.input.Value())
	}
	a.command.historyNext()
	if a.command.input.Value() != "" {
		t.Errorf("history forward = %q, want empty", a.command.input.Value())
	}
}

// TestSingleLetterKeysDoNotSwitchViews makes sure the letter keys are free
// for context-sensitive actions.
func TestSingleLetterKeysDoNotSwitchViews(t *testing.T) {
	a := newTestApp()
	for _, r := range []rune{'s', 'n', 'v', 'w', 'k', 'c'} {
		a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		if a.screen != screenServices {
			t.Errorf("pressing %q switched to %v, want to stay on services", r, a.screen)
		}
	}
}

// TestSameKeyMeansDifferentThingsPerView checks the k9s-style behaviour:
// "x" removes a service in the services view and a node in the nodes view.
func TestSameKeyMeansDifferentThingsPerView(t *testing.T) {
	a := newTestApp()
	a.applyServices([]api.ServiceRow{svc("id-a", "web", 1, 1)})
	a.services.SetCursor(0)
	a.selectedServiceID = "id-a"

	// In the services view, ctrl-d asks to delete the service.
	a.handleKey(tea.KeyMsg{Type: tea.KeyCtrlD})
	if !a.confirm.active {
		t.Fatal("expected a confirmation prompt in the services view")
	}
	if !strings.Contains(a.confirm.title, "web") {
		t.Errorf("confirmation mentions %q, want the service name", a.confirm.title)
	}
	a.confirm.active = false

	// In the nodes view, the same key asks to delete the node.
	a.screen = screenNodes
	a.nodeRows = []api.NodeRow{{ID: "node-1", Hostname: "worker-1", Role: "worker", Availability: "active", Status: "ready"}}
	a.applyNodes(a.nodeRows)
	a.nodes.SetCursor(0)
	a.selectedNodeID = "node-1"

	a.handleKey(tea.KeyMsg{Type: tea.KeyCtrlD})
	if !a.confirm.active {
		t.Fatal("expected a confirmation prompt in the nodes view")
	}
	if !strings.Contains(a.confirm.title, "worker-1") {
		t.Errorf("confirmation mentions %q, want the node name", a.confirm.title)
	}
}

// TestActionBarShowsCurrentViewKeys checks that the footer lists the keys
// that apply to the current resource view.
func TestActionBarShowsCurrentViewKeys(t *testing.T) {
	a := newTestApp()
	a.applyServices([]api.ServiceRow{svc("id-a", "web", 1, 1)})
	a.services.SetCursor(0)
	a.selectedServiceID = "id-a"

	bar := a.actionBar()
	for _, want := range []string{"d describe", "s scale", "u update", "ctrl+d delete"} {
		if !strings.Contains(bar, want) {
			t.Errorf("action bar %q is missing %q", bar, want)
		}
	}

	a.screen = screenNodes
	bar = a.actionBar()
	for _, want := range []string{"D drain", "A activate", "p promote", "w demote"} {
		if !strings.Contains(bar, want) {
			t.Errorf("node action bar %q is missing %q", bar, want)
		}
	}
	if strings.Contains(bar, "scale") {
		t.Errorf("node action bar should not offer scaling: %q", bar)
	}
}

func TestSearchModeCapturesKeys(t *testing.T) {
	a := newTestApp()
	a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if !a.searching {
		t.Fatal("expected search mode to be active")
	}

	// Typing 'n' in search mode must not switch screens.
	a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if a.screen != screenServices {
		t.Errorf("screen changed while searching: %v", a.screen)
	}
	if a.filter != "n" {
		t.Errorf("filter = %q, want n", a.filter)
	}

	// Escape exits search mode and clears the filter.
	a.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if a.searching {
		t.Error("search mode should be closed")
	}
	if a.filter != "" {
		t.Errorf("filter = %q, want empty", a.filter)
	}
}

func TestRemoveServiceAsksForConfirmation(t *testing.T) {
	a := newTestApp()
	a.applyServices([]api.ServiceRow{svc("id-a", "web", 1, 1)})
	a.services.SetCursor(0)
	a.selectedServiceID = "id-a"

	a.handleKey(tea.KeyMsg{Type: tea.KeyCtrlD})
	if !a.confirm.active {
		t.Fatal("expected a confirmation prompt before deleting")
	}

	// Cancelling must close the prompt without acting.
	a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if a.confirm.active {
		t.Error("confirmation should be closed after cancelling")
	}
}

func TestCommandCompletion(t *testing.T) {
	a := newTestApp()
	a.command.open()

	if len(a.command.matches) != len(aliases) {
		t.Fatalf("an empty command should list every resource, got %d", len(a.command.matches))
	}

	// Typing a prefix narrows the list.
	a.command.input.SetValue("vol")
	a.command.updateMatches()
	if len(a.command.matches) != 1 || a.command.matches[0].screen != screenVolumes {
		t.Errorf("matches = %v, want the volumes view", a.command.matches)
	}

	// A word that matches nothing yields no completions.
	a.command.input.SetValue("zzzznotacommand")
	a.command.updateMatches()
	if len(a.command.matches) != 0 {
		t.Errorf("expected no matches, got %d", len(a.command.matches))
	}
}

func TestLayoutFitsTerminal(t *testing.T) {
	a := newTestApp()
	a.width, a.height = 100, 30
	a.applyServices([]api.ServiceRow{svc("id-a", "web", 1, 1)})
	a.screen = screenDetail
	a.selectedServiceID = "id-a"
	a.resize()

	view := a.View()
	lines := 0
	for _, l := range splitLines(view) {
		_ = l
		lines++
	}
	if lines > a.height+2 {
		t.Errorf("view is %d lines tall, terminal is %d", lines, a.height)
	}
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	out = append(out, cur)
	return out
}

// TestAppDispatchesNavigationToTheTable checks that arrow keys reach the
// table through the app's key handling, not just the table in isolation.
func TestAppDispatchesNavigationToTheTable(t *testing.T) {
	a := newTestApp()
	a.screen = screenVolumes
	var rows []api.VolumeRow
	for i := 0; i < 10; i++ {
		rows = append(rows, api.VolumeRow{Name: "vol", Driver: "local", Scope: "local", Created: "2025-06-01"})
	}
	a.volumes.SetRows(volumeTableRows(rows))
	a.resize()

	a.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	a.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	if got := a.volumes.Cursor(); got != 2 {
		t.Errorf("cursor = %d, want 2 after two arrow-down presses", got)
	}
	a.handleKey(tea.KeyMsg{Type: tea.KeyUp})
	if got := a.volumes.Cursor(); got != 1 {
		t.Errorf("cursor = %d, want 1 after arrow-up", got)
	}
}
