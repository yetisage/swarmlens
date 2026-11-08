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
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
	} `yaml:"plugins"`
}

// Load reads the configuration directory. Missing files are not an error;
// unreadable or malformed ones are, and name the file.
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

// readYAML decodes a file into out. A missing file leaves out untouched.
func readYAML(path string, out any) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
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
