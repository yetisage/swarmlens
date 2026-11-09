package ui

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"

	"github.com/yetisage/swarmlens/internal/api"
	"github.com/yetisage/swarmlens/internal/config"
	"github.com/yetisage/swarmlens/internal/demo"
)

var update = flag.Bool("update", false, "rewrite the golden screens in testdata/")

// fixedNow freezes the demo's clock so ages in the golden screens are stable.
var fixedNow = time.Date(2026, 11, 8, 15, 0, 0, 0, time.UTC)

// testOpts returns options for a static app over the deterministic demo.
func testOpts() Options {
	be := demo.NewStatic(fixedNow)
	return Options{
		Backend: be, Static: true, Profile: termenv.TrueColor, Version: "test",
		Now:     func() time.Time { return fixedNow },
		Connect: func(name string) (api.Backend, error) { return be.WithName(name), nil },
	}
}

func newTestApp(t *testing.T, w, h int) *app {
	t.Helper()
	return newTestAppOpts(t, testOpts(), w, h)
}

func newTestAppOpts(t *testing.T, opt Options, w, h int) *app {
	t.Helper()
	a := New(opt)
	a.w, a.h = w, h
	a.loadSync()
	return a
}

// keyMsg builds the message bubbletea would deliver for a key name.
func keyMsg(k string) tea.KeyMsg {
	named := map[string]tea.KeyType{
		"enter": tea.KeyEnter, "esc": tea.KeyEsc, "tab": tea.KeyTab, "shift+tab": tea.KeyShiftTab,
		"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
		"pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown, "home": tea.KeyHome, "end": tea.KeyEnd,
		"backspace": tea.KeyBackspace, "delete": tea.KeyDelete,
		"shift+left": tea.KeyShiftLeft, "shift+right": tea.KeyShiftRight,
		"ctrl+@": tea.KeyCtrlAt, "ctrl+\\": tea.KeyCtrlBackslash,
	}
	if t, ok := named[k]; ok {
		return tea.KeyMsg{Type: t}
	}
	if k == "space" || k == " " {
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	}
	if strings.HasPrefix(k, "ctrl+") && len(k) == 6 && k[5] >= 'a' && k[5] <= 'z' {
		return tea.KeyMsg{Type: tea.KeyType(k[5] - 'a' + 1)}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

// press sends keys one at a time and lets the commands they produce run.
func (a *app) press(keys ...string) {
	for _, k := range keys {
		_, cmd := a.Update(keyMsg(k))
		a.drain(cmd)
	}
}

// typeText types a string one character at a time.
func (a *app) typeText(s string) {
	for _, r := range s {
		a.press(string(r))
	}
}

// command types a ":" command and runs it.
func (a *app) command(line string) {
	a.press(":")
	a.typeText(line)
	a.press("enter")
}

// screenText is the current frame as plain text.
func (a *app) screenText() string { return a.screen().Text() }

// refreshNow reloads the top view and the summary synchronously.
func (a *app) refreshNow() {
	a.drain(a.view().refresh())
	a.drain(a.loadSummary())
}

func mustContain(t *testing.T, screen string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(screen, w) {
			t.Errorf("screen does not contain %q:\n%s", w, screen)
		}
	}
}

func mustNotContain(t *testing.T, screen string, banned ...string) {
	t.Helper()
	for _, w := range banned {
		if strings.Contains(screen, w) {
			t.Errorf("screen unexpectedly contains %q:\n%s", w, screen)
		}
	}
}

// golden compares a screen with testdata/<name>.golden; -update rewrites it.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	got += "\n"
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden screen %s (run `go test ./internal/ui -update`): %v", path, err)
	}
	if got != string(want) {
		t.Errorf("screen differs from %s\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

// row builders for the table tests.
func testRow(res *resource, id, name string, cells ...string) *row {
	r := newRow(res, id, name)
	copy(r.cells, cells)
	return r
}

func pluginForTest(key, desc string, scopes []string) config.Plugin {
	return config.Plugin{Name: "test", Key: key, ShortCut: "Shift-" + key, Description: desc, Scopes: scopes, Command: "true"}
}
