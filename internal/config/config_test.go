package config

import (
	"os"
	"path/filepath"
	"reflect"
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

// A key that means nothing must be an error that says where and what: a
// mistyped readOnly leaving the session read-write, without a word, is the
// kind of failure a config file must not have.
func TestUnknownKeysAreErrorsThatSayWhereAndWhat(t *testing.T) {
	for name, c := range map[string]struct {
		file, content string
		want          []string
	}{
		"wrong case": {"config.yaml", "swarmlens:\n  readonly: true\n",
			[]string{"config.yaml", `line 2: unknown key "readonly"`, `did you mean "readOnly"?`}},
		"a k9s config": {"config.yaml", "k9s:\n  refreshRate: 2\n",
			[]string{`line 1: unknown key "k9s"`}},
		"a plugin flag typo": {"plugins.yaml", "plugins:\n  x:\n    shortCut: a\n    command: ls\n    dangrous: true\n",
			[]string{`did you mean "dangerous"?`}},
		"nested": {"config.yaml", "swarmlens:\n  ui:\n    headles: true\n    logoless: true\n",
			[]string{`line 3: unknown key "headles"`, `did you mean "headless"?`}},
		"two at once": {"config.yaml", "swarmlens:\n  readonly: true\n  refreshrate: 3\n",
			[]string{`unknown key "readonly"`, `unknown key "refreshrate"`, `did you mean "refreshRate"?`}},
		"aliases": {"aliases.yaml", "alias:\n  dp: services\n",
			[]string{"aliases.yaml", `unknown key "alias"`, `did you mean "aliases"?`}},
		"a plugin flag": {"plugins.yaml", "plugins:\n  x:\n    shortCut: a\n    command: ls\n    dangrous: true\n",
			[]string{"plugins.yaml", `line 5: unknown key "dangrous"`}},
		"a plugin key": {"plugins.yaml", "plugins:\n  x:\n    shortcut: a\n    command: ls\n",
			[]string{`unknown key "shortcut"`, `did you mean "shortCut"?`}},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, c.file, c.content)
			_, err := Load(dir)
			if err == nil {
				t.Fatal("want an error")
			}
			for _, want := range c.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%q is missing from %q", want, err)
				}
			}
			if strings.Contains(err.Error(), "struct {") || strings.Contains(err.Error(), "rawConfig") {
				t.Errorf("the message leaks Go types: %q", err)
			}
		})
	}
}

// Empty files, comment-only files and k9s' own plugin fields are fine.
func TestFilesThatAreEmptyOrFromK9sStillLoad(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "config.yaml", "")
	write(t, dir, "aliases.yaml", "# nothing yet\n")
	write(t, dir, "plugins.yaml", `
plugins:
  stern:
    shortCut: Shift-S
    description: Logs
    scopes: [services]
    command: stern
    args: ["$NAME"]
    # k9s fields without a meaning here: accepted and ignored
    override: true
    overwriteOutput: true
    pipes: [a, b]
`)
	c, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Plugins) != 1 || c.Plugins[0].Name != "stern" || c.RefreshRate != 2 {
		t.Errorf("config = %+v", c)
	}
}

func TestClosestKnownKey(t *testing.T) {
	known := map[string]string{}
	collectKeys(reflect.TypeOf(&rawConfig{}), known)
	for in, want := range map[string]string{
		"readonly": "readOnly", "READONLY": "readOnly", "refreshrate": "refreshRate", "headles": "headless",
		"textwrap": "textWrap", "sinceSecond": "sinceSeconds", "tial": "tail",
	} {
		if got, ok := closest(in, known); !ok || got != want {
			t.Errorf("closest(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	// nothing is near these: no suggestion beats a wrong one
	for _, in := range []string{"k9s", "colour", "x", "completelydifferent"} {
		if got, ok := closest(in, known); ok {
			t.Errorf("closest(%q) = %q, want no suggestion", in, got)
		}
	}
}
