package ui

import (
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

func TestGlobalKeySwitchesScreens(t *testing.T) {
	a := newTestApp()

	if _, _ = a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}}); a.screen != screenNodes {
		t.Errorf("screen = %v, want nodes", a.screen)
	}
	if _, _ = a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}}); a.screen != screenVolumes {
		t.Errorf("screen = %v, want volumes", a.screen)
	}
	if _, _ = a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}}); a.screen != screenSecrets {
		t.Errorf("screen = %v, want secrets", a.screen)
	}
	if _, _ = a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}}); a.screen != screenConfigs {
		t.Errorf("screen = %v, want configs", a.screen)
	}
	if _, _ = a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}); a.screen != screenServices {
		t.Errorf("screen = %v, want services", a.screen)
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

	a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if !a.confirm.active {
		t.Fatal("expected a confirmation prompt before removing")
	}

	// Cancelling must close the prompt without acting.
	a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if a.confirm.active {
		t.Error("confirmation should be closed after cancelling")
	}
}

func TestPaletteFiltersCommands(t *testing.T) {
	a := newTestApp()
	a.applyServices([]api.ServiceRow{svc("id-a", "web", 1, 1)})
	a.services.SetCursor(0)
	a.selectedServiceID = "id-a"

	a.palette.open(a.paletteCommands())

	if len(a.palette.filtered) == 0 {
		t.Fatal("palette should start with all commands")
	}

	// Service-specific commands only exist when a service is selected.
	a.palette.input.SetValue("scale")
	a.palette.applyFilter()
	if len(a.palette.filtered) == 0 {
		t.Fatal("filtering by 'scale' should match the scale commands")
	}
	for _, it := range a.palette.filtered {
		if !a.palette.matches(it, "scale") {
			t.Errorf("filtered item %q does not match the query", it.title)
		}
	}

	// A query that matches nothing yields no commands.
	a.palette.input.SetValue("zzzznotacommand")
	a.palette.applyFilter()
	if len(a.palette.filtered) != 0 {
		t.Errorf("expected no matches, got %d", len(a.palette.filtered))
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
