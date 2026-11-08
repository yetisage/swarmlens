package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultsWhenNothingIsConfigured(t *testing.T) {
	for _, dir := range []string{"", t.TempDir(), filepath.Join(t.TempDir(), "missing")} {
		c, err := Load(dir)
		if err != nil {
			t.Fatalf("Load(%q): %v", dir, err)
		}
		if c.RefreshRate != 2 || c.ReadOnly || c.Logger.Tail != 100 || c.Logger.Buffer != 5000 || len(c.Plugins) != 0 {
			t.Errorf("defaults = %+v", c)
		}
	}
}

func TestLoadAllFiles(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "config.yaml", `
swarmlens:
  refreshRate: 5
  readOnly: true
  ui: {headless: true, logoless: true, crumbsless: true, noIcons: true}
  logger: {tail: 250, buffer: 100, textWrap: true, showTime: true, sinceSeconds: 60}
`)
	write(t, dir, "aliases.yaml", "aliases:\n  DP: Services\n  sec: secrets\n")
	// The very same file k9s reads.
	write(t, dir, "plugins.yaml", `
plugins:
  podlogs:
    shortCut: Shift-L
    description: "Pod logs in Zellij"
    scopes: [tasks]
    command: zellij-run
    args: ["-n", "Task: $NAME", "-c", "docker service logs -f $NAME"]
  open:
    shortCut: Ctrl-O
    description: open
    scopes: [all]
    command: xdg-open
    background: true
    confirm: true
    dangerous: true
`)
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.RefreshRate != 5 || !c.ReadOnly || !c.UI.Headless || !c.UI.Logoless || !c.UI.Crumbsless || !c.UI.NoIcons {
		t.Errorf("config = %+v", c)
	}
	if c.Logger.Tail != 250 || c.Logger.Buffer != 100 || !c.Logger.TextWrap || !c.Logger.ShowTime || c.Logger.SinceSeconds != 60 {
		t.Errorf("logger = %+v", c.Logger)
	}
	if c.Aliases["dp"] != "services" || c.Aliases["sec"] != "secrets" {
		t.Errorf("aliases are lower-cased: %v", c.Aliases)
	}
	if len(c.Plugins) != 2 {
		t.Fatalf("plugins = %+v", c.Plugins)
	}
	// Sorted by name for a stable menu.
	open, logs := c.Plugins[0], c.Plugins[1]
	if open.Name != "open" || open.Key != "ctrl+o" || !open.Background || !open.Confirm || !open.Dangerous {
		t.Errorf("open = %+v", open)
	}
	if logs.Key != "L" || logs.Command != "zellij-run" || len(logs.Args) != 4 || logs.Scopes[0] != "tasks" {
		t.Errorf("logs = %+v", logs)
	}
}

func TestMalformedFilesAreReportedWithTheirName(t *testing.T) {
	for file, content := range map[string]string{
		"config.yaml":  "swarmlens: [unclosed",
		"aliases.yaml": "aliases: {a: [",
		"plugins.yaml": "plugins:\n  x:\n    shortCut: Hyper-L\n    command: ls\n",
	} {
		dir := t.TempDir()
		write(t, dir, file, content)
		_, err := Load(dir)
		if err == nil {
			t.Errorf("%s: expected an error", file)
			continue
		}
		if file != "plugins.yaml" && !strings.Contains(err.Error(), file) {
			t.Errorf("%s: error %q does not name the file", file, err)
		}
	}
	dir := t.TempDir()
	write(t, dir, "plugins.yaml", "plugins:\n  x:\n    shortCut: a\n")
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "no command") {
		t.Errorf("a plugin without a command: %v", err)
	}
}

func TestParseShortcut(t *testing.T) {
	for in, want := range map[string]string{
		"a": "a", "Shift-L": "L", "shift-b": "B", "Ctrl-X": "ctrl+x", "Alt-a": "alt+a",
		"Enter": "enter", "Shift-Left": "shift+left", "Ctrl-Space": "ctrl+ ", "Space": " ", "L": "L",
		"Ctrl-Shift-D": "ctrl+d", "-": "-", "F": "F",
	} {
		got, err := ParseShortcut(in)
		if err != nil {
			t.Errorf("ParseShortcut(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseShortcut(%q) = %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"", "Hyper-X", "Ctrl-"} {
		if _, err := ParseShortcut(bad); err == nil {
			t.Errorf("ParseShortcut(%q) should fail", bad)
		}
	}
}

func TestDirPrecedence(t *testing.T) {
	t.Setenv("SWARMLENS_CONFIG_DIR", "/a")
	if Dir() != "/a" {
		t.Errorf("Dir() = %q", Dir())
	}
	t.Setenv("SWARMLENS_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if Dir() != "/xdg/swarmlens" {
		t.Errorf("Dir() = %q", Dir())
	}
}
