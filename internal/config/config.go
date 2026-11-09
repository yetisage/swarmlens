// Package config loads swarmlens' optional configuration. The files mirror
// the ones k9s reads, so a k9s user's habits carry over:
//
//	config.yaml    settings (refresh rate, read-only, header/logo/crumbs, logs)
//	aliases.yaml   extra command aliases for the ":" prompt
//	plugins.yaml   external commands bound to keys
//
// All of them are optional.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the effective configuration.
type Config struct {
	RefreshRate float64 // seconds between refreshes; 0 means the default
	ReadOnly    bool
	UI          UI
	Logger      Logger
	Aliases     map[string]string // alias -> resource name
	Plugins     []Plugin
}

// UI holds the look-and-feel switches.
type UI struct {
	Headless   bool
	Logoless   bool
	Crumbsless bool
	NoIcons    bool
}

// Logger holds the defaults of the log viewer.
type Logger struct {
	Tail         int
	Buffer       int
	TextWrap     bool
	ShowTime     bool
	SinceSeconds int
}

// Plugin binds an external command to a key in some views.
type Plugin struct {
	Name        string
	ShortCut    string // as written: "Shift-L", "Ctrl-X", "a"
	Key         string // as bubbletea names it: "L", "ctrl+x", "a"
	Description string
	Scopes      []string // resource names, or "all"
	Command     string
	Args        []string
	Background  bool // run detached instead of taking over the terminal
	Confirm     bool // ask first
	Dangerous   bool // hidden in read-only mode
}

// Defaults returns the configuration used when no file says otherwise.
func Defaults() *Config {
	c := &Config{Aliases: map[string]string{}}
	c.RefreshRate = 2
	c.Logger = Logger{Tail: 100, Buffer: 5000, SinceSeconds: -1}
	return c
}

// Dir returns the configuration directory: $SWARMLENS_CONFIG_DIR, else
// $XDG_CONFIG_HOME/swarmlens, else ~/.config/swarmlens.
func Dir() string {
	if d := os.Getenv("SWARMLENS_CONFIG_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "swarmlens")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "swarmlens")
}

// rawConfig is the on-disk shape of config.yaml.
type rawConfig struct {
	Swarmlens struct {
		RefreshRate float64 `yaml:"refreshRate"`
		ReadOnly    bool    `yaml:"readOnly"`
		UI          struct {
			Headless   bool `yaml:"headless"`
			Logoless   bool `yaml:"logoless"`
			Crumbsless bool `yaml:"crumbsless"`
			NoIcons    bool `yaml:"noIcons"`
		} `yaml:"ui"`
		Logger struct {
			Tail         *int `yaml:"tail"`
			Buffer       *int `yaml:"buffer"`
			TextWrap     bool `yaml:"textWrap"`
			ShowTime     bool `yaml:"showTime"`
			SinceSeconds *int `yaml:"sinceSeconds"`
		} `yaml:"logger"`
	} `yaml:"swarmlens"`
}

type rawPlugins struct {
	Plugins map[string]struct {
		ShortCut    string   `yaml:"shortCut"`
		Description string   `yaml:"description"`
		Scopes      []string `yaml:"scopes"`
		Command     string   `yaml:"command"`
		Args        []string `yaml:"args"`
		Background  bool     `yaml:"background"`
		Confirm     bool     `yaml:"confirm"`
		Dangerous   bool     `yaml:"dangerous"`

		// What k9s knows and has no meaning here: accepted, so that a k9s
		// plugins file loads, and ignored.
		Override        any `yaml:"override"`
		Pipes           any `yaml:"pipes"`
		OverwriteOutput any `yaml:"overwriteOutput"`
	} `yaml:"plugins"`
}

// Load reads the configuration directory. Missing files are not an error;
// unreadable or malformed ones are, and name the file. So are keys that
// mean nothing: a typo in a setting such as readOnly must not leave the
// setting off without a word.
func Load(dir string) (*Config, error) {
	c := Defaults()
	if dir == "" {
		return c, nil
	}

	var raw rawConfig
	if err := readYAML(filepath.Join(dir, "config.yaml"), &raw); err != nil {
		return c, err
	}
	s := raw.Swarmlens
	if s.RefreshRate > 0 {
		c.RefreshRate = s.RefreshRate
	}
	c.ReadOnly = s.ReadOnly
	c.UI = UI{Headless: s.UI.Headless, Logoless: s.UI.Logoless, Crumbsless: s.UI.Crumbsless, NoIcons: s.UI.NoIcons}
	if s.Logger.Tail != nil {
		c.Logger.Tail = *s.Logger.Tail
	}
	if s.Logger.Buffer != nil && *s.Logger.Buffer > 0 {
		c.Logger.Buffer = *s.Logger.Buffer
	}
	if s.Logger.SinceSeconds != nil {
		c.Logger.SinceSeconds = *s.Logger.SinceSeconds
	}
	c.Logger.TextWrap, c.Logger.ShowTime = s.Logger.TextWrap, s.Logger.ShowTime

	var al struct {
		Aliases map[string]string `yaml:"aliases"`
	}
	if err := readYAML(filepath.Join(dir, "aliases.yaml"), &al); err != nil {
		return c, err
	}
	for k, v := range al.Aliases {
		c.Aliases[strings.ToLower(k)] = strings.ToLower(v)
	}

	var pl rawPlugins
	if err := readYAML(filepath.Join(dir, "plugins.yaml"), &pl); err != nil {
		return c, err
	}
	names := make([]string, 0, len(pl.Plugins))
	for n := range pl.Plugins {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		p := pl.Plugins[n]
		key, err := ParseShortcut(p.ShortCut)
		if err != nil {
			return c, fmt.Errorf("plugin %q: %w", n, err)
		}
		if p.Command == "" {
			return c, fmt.Errorf("plugin %q: no command", n)
		}
		c.Plugins = append(c.Plugins, Plugin{
			Name: n, ShortCut: p.ShortCut, Key: key, Description: p.Description, Scopes: p.Scopes,
			Command: p.Command, Args: p.Args, Background: p.Background, Confirm: p.Confirm, Dangerous: p.Dangerous,
		})
	}
	return c, nil
}

// readYAML decodes a file into out, refusing keys out does not have. A missing
// or empty file leaves out untouched.
func readYAML(path string, out any) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s: %w", path, friendly(err, reflect.TypeOf(out)))
	}
	return nil
}

var unknownField = regexp.MustCompile(`^(line \d+): field (\S+) not found in type .*$`)

// friendly rewrites the decoder's complaints about unknown keys, which name a
// Go type, into what the person who wrote the file needs: where, which key,
// and the key that exists and is nearly the same, if there is one.
func friendly(err error, target reflect.Type) error {
	var te *yaml.TypeError
	if !errors.As(err, &te) {
		return err
	}
	known := map[string]string{} // lower case -> as written
	collectKeys(target, known)
	msgs := make([]string, 0, len(te.Errors))
	for _, e := range te.Errors {
		m := unknownField.FindStringSubmatch(e)
		if m == nil {
			msgs = append(msgs, e)
			continue
		}
		msg := fmt.Sprintf("%s: unknown key %q", m[1], m[2])
		if want, ok := closest(m[2], known); ok {
			msg += fmt.Sprintf(" (did you mean %q?)", want)
		}
		msgs = append(msgs, msg)
	}
	return errors.New(strings.Join(msgs, "; "))
}

// closest finds the known key a misspelt one most likely meant: the same
// letters in another case, or a couple of edits away (a short key gets one).
func closest(key string, known map[string]string) (string, bool) {
	lower := strings.ToLower(key)
	limit := 2
	if len(key) <= 4 {
		limit = 1
	}
	best, bestDist := "", limit+1
	for k, written := range known {
		d := editDistance(lower, k)
		if d < bestDist || (d == bestDist && written < best) {
			best, bestDist = written, d
		}
	}
	return best, bestDist <= limit
}

// editDistance is the number of insertions, deletions, substitutions and
// swaps of two neighbouring letters (the commonest typo) between two strings.
func editDistance(a, b string) int {
	prev2 := make([]int, len(b)+1)
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				cur[j] = min(cur[j], prev2[j-2]+1)
			}
		}
		prev2, prev = prev, cur
	}
	return prev[len(b)]
}

// collectKeys gathers every key a type reads, nested ones included.
func collectKeys(t reflect.Type, into map[string]string) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Map {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := strings.Split(f.Tag.Get("yaml"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		into[strings.ToLower(name)] = name
		collectKeys(f.Type, into)
	}
}

// ParseShortcut turns a k9s style shortcut ("Shift-L", "Ctrl-X", "Alt-a",
// "Enter", "a") into the key name the UI matches on ("L", "ctrl+x",
// "alt+a", "enter", "a").
func ParseShortcut(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("no shortcut")
	}
	var ctrl, alt, shift bool
	parts := strings.Split(s, "-")
	key := parts[len(parts)-1]
	if s == "-" {
		key, parts = "-", nil
	}
	for _, m := range parts[:max(len(parts)-1, 0)] {
		switch strings.ToLower(m) {
		case "ctrl":
			ctrl = true
		case "alt":
			alt = true
		case "shift":
			shift = true
		case "":
		default:
			return "", fmt.Errorf("unknown modifier %q in shortcut %q", m, s)
		}
	}
	lower := strings.ToLower(key)
	named := map[string]bool{"enter": true, "esc": true, "tab": true, "space": true, "up": true, "down": true, "left": true, "right": true, "home": true, "end": true, "pgup": true, "pgdown": true, "backspace": true, "delete": true}
	switch {
	case lower == "space":
		key = " "
	case named[lower]:
		key = lower
	case len(key) == 1 && shift:
		key = strings.ToUpper(key)
		shift = false
	case len(key) == 1:
		// A bare capital letter already means shift.
	default:
		return "", fmt.Errorf("unknown key %q in shortcut %q", key, s)
	}
	if shift { // shift with a named key, e.g. Shift-Left
		key = "shift+" + key
	}
	if ctrl {
		key = "ctrl+" + strings.ToLower(key)
	}
	if alt {
		key = "alt+" + key
	}
	return key, nil
}
