package ui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
)

// resourceAlias maps a resource view to the names the command mode answers
// to. It is the swarmlens equivalent of k9s' alias table.
type resourceAlias struct {
	screen  screen
	kind    string   // singular, e.g. "service"
	plural  string   // plural, e.g. "services"
	aliases []string // extra short names
}

// aliases is the resource registry.
var aliases = []resourceAlias{
	{screen: screenServices, kind: "service", plural: "services", aliases: []string{"svc"}},
	{screen: screenNodes, kind: "node", plural: "nodes", aliases: []string{"no"}},
	{screen: screenVolumes, kind: "volume", plural: "volumes", aliases: []string{"vol"}},
	{screen: screenNetworks, kind: "network", plural: "networks", aliases: []string{"net"}},
	{screen: screenSecrets, kind: "secret", plural: "secrets", aliases: []string{"sec"}},
	{screen: screenConfigs, kind: "config", plural: "configs", aliases: []string{"cfg"}},
}

// names returns every name this alias answers to.
func (a resourceAlias) names() []string {
	return append([]string{a.kind, a.plural}, a.aliases...)
}

// resolveAlias maps a command word to a resource view. Exact matches win;
// a unique prefix is also accepted, like k9s' short names.
func resolveAlias(word string) (resourceAlias, bool) {
	w := strings.ToLower(strings.TrimSpace(word))
	if w == "" {
		return resourceAlias{}, false
	}
	for _, a := range aliases {
		for _, n := range a.names() {
			if w == n {
				return a, true
			}
		}
	}
	var match *resourceAlias
	for i := range aliases {
		a := &aliases[i]
		for _, n := range a.names() {
			if strings.HasPrefix(n, w) {
				if match != nil {
					return resourceAlias{}, false // ambiguous
				}
				match = a
				break
			}
		}
	}
	if match != nil {
		return *match, true
	}
	return resourceAlias{}, false
}

// command is a parsed command line.
type command struct {
	resource resourceAlias
	filter   string
	inverse  bool
}

// parseCommand parses a command line of the form "<resource> [/filter]".
//
// Like k9s, the command mode is for navigation only: it switches resource
// views and can carry a filter. Actions are bound to keys instead.
func parseCommand(line string) (command, error) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return command{}, errors.New("no command")
	}
	res, ok := resolveAlias(fields[0])
	if !ok {
		return command{}, fmt.Errorf("unknown command %q", fields[0])
	}
	c := command{resource: res}
	if len(fields) > 1 {
		arg := strings.Join(fields[1:], " ")
		switch {
		case strings.HasPrefix(arg, "/!"):
			c.inverse = true
			c.filter = strings.TrimPrefix(arg, "/!")
		case strings.HasPrefix(arg, "/"):
			c.filter = strings.TrimPrefix(arg, "/")
		default:
			return command{}, fmt.Errorf("unknown argument %q (use /filter)", arg)
		}
	}
	return c, nil
}

// commandMode is the ":" prompt. Like k9s it is a command interpreter for
// navigating between resource views — not a menu of actions.
type commandMode struct {
	active  bool
	input   textinput.Model
	history []string
	histPos int // -1 = editing a fresh line
	matches []resourceAlias
	err     string
}

func newCommandMode() commandMode {
	in := textinput.New()
	in.Prompt = ": "
	in.Placeholder = "service, node, volume…"
	in.CharLimit = 64
	return commandMode{input: in, histPos: -1}
}

func (c *commandMode) open() {
	c.active = true
	c.err = ""
	c.histPos = -1
	c.input.SetValue("")
	c.updateMatches()
	c.input.Focus()
}

func (c *commandMode) close() {
	c.active = false
	c.input.Blur()
}

// updateMatches recomputes the completion list for the current input.
func (c *commandMode) updateMatches() {
	word := strings.Fields(c.input.Value())
	q := ""
	if len(word) > 0 {
		q = word[0]
	}
	c.matches = nil
	for _, a := range aliases {
		if q == "" {
			c.matches = append(c.matches, a)
			continue
		}
		for _, n := range a.names() {
			if strings.HasPrefix(n, strings.ToLower(q)) {
				c.matches = append(c.matches, a)
				break
			}
		}
	}
}

// acceptCompletion replaces the typed word with the selected alias.
func (c *commandMode) acceptCompletion(a resourceAlias) {
	word := strings.Fields(c.input.Value())
	if len(word) == 0 {
		c.input.SetValue(a.kind + " ")
		c.input.CursorEnd()
		c.updateMatches()
		return
	}
	word[0] = a.kind
	c.input.SetValue(strings.Join(word, " "))
	c.input.CursorEnd()
	c.updateMatches()
}

// pushHistory records an executed command.
func (c *commandMode) pushHistory(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	// Drop a duplicate of the previous entry.
	if n := len(c.history); n > 0 && c.history[n-1] == line {
		return
	}
	c.history = append(c.history, line)
	if len(c.history) > 50 {
		c.history = c.history[len(c.history)-50:]
	}
}

// historyPrev walks back through the history.
func (c *commandMode) historyPrev() {
	if len(c.history) == 0 {
		return
	}
	if c.histPos == -1 {
		c.histPos = len(c.history) - 1
	} else if c.histPos > 0 {
		c.histPos--
	}
	c.input.SetValue(c.history[c.histPos])
	c.input.CursorEnd()
	c.updateMatches()
}

// historyNext walks forward through the history.
func (c *commandMode) historyNext() {
	if c.histPos == -1 {
		return
	}
	if c.histPos < len(c.history)-1 {
		c.histPos++
		c.input.SetValue(c.history[c.histPos])
	} else {
		c.histPos = -1
		c.input.SetValue("")
	}
	c.input.CursorEnd()
	c.updateMatches()
}
