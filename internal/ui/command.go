package ui

import (
	"errors"
	"fmt"
	"sort"
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

// commandMode is the ":" prompt.
//
// Like k9s it is a command interpreter for navigating between resource
// views — not a menu of actions — and it uses k9s' fish-style inline
// suggestions: the completion is shown as ghost text after the cursor,
// tab accepts it and the arrow keys cycle through the candidates.
type commandMode struct {
	active      bool
	input       textinput.Model
	suggestions []string
	suggIndex   int
	history     []string
	err         string
}

func newCommandMode() commandMode {
	in := textinput.New()
	in.Prompt = ": "
	in.Placeholder = "service, node, volume…"
	in.CharLimit = 64
	return commandMode{input: in, suggIndex: -1}
}

func (c *commandMode) open() {
	c.active = true
	c.err = ""
	c.input.SetValue("")
	c.suggest()
	c.input.Focus()
}

func (c *commandMode) close() {
	c.active = false
	c.input.Blur()
	c.suggestions = nil
	c.suggIndex = -1
}

// suggest recomputes the inline suggestions for the current text.
//
// Like k9s, an empty prompt offers the command history and a non-empty one
// offers the alias names that extend it.
func (c *commandMode) suggest() {
	text := c.input.Value()
	c.suggestions = nil
	c.suggIndex = -1

	if text == "" {
		// The history, most recent first.
		for i := len(c.history) - 1; i >= 0 && len(c.suggestions) < 10; i-- {
			c.suggestions = append(c.suggestions, c.history[i])
		}
		if len(c.suggestions) > 0 {
			c.suggIndex = 0
		}
		return
	}

	word := strings.Fields(text)
	q := strings.ToLower(word[0])
	for _, al := range aliases {
		for _, n := range al.names() {
			if n != q && strings.HasPrefix(n, q) {
				c.suggestions = append(c.suggestions, strings.TrimPrefix(n, q))
				break
			}
		}
	}
	sort.Strings(c.suggestions)
	if len(c.suggestions) > 0 {
		c.suggIndex = 0
	}
}

// ghost returns the suggestion to show after the cursor.
func (c *commandMode) ghost() string {
	if c.suggIndex < 0 || c.suggIndex >= len(c.suggestions) {
		return ""
	}
	return c.suggestions[c.suggIndex]
}

// acceptSuggestion appends the current suggestion to the text, like k9s'
// tab completion.
func (c *commandMode) acceptSuggestion() {
	g := c.ghost()
	if g == "" {
		return
	}
	c.input.SetValue(c.input.Value() + g)
	c.input.CursorEnd()
	c.suggest()
}

// cycleSuggestion moves through the candidates, like k9s' up/down keys.
func (c *commandMode) cycleSuggestion(delta int) {
	if len(c.suggestions) == 0 {
		return
	}
	if c.suggIndex < 0 {
		c.suggIndex = 0
	} else {
		c.suggIndex += delta
	}
	if c.suggIndex < 0 {
		c.suggIndex = len(c.suggestions) - 1
	}
	if c.suggIndex >= len(c.suggestions) {
		c.suggIndex = 0
	}
}

// clear empties the prompt.
func (c *commandMode) clear() {
	c.input.SetValue("")
	c.input.CursorStart()
	c.suggest()
}

// pushHistory records an executed command.
func (c *commandMode) pushHistory(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	if n := len(c.history); n > 0 && c.history[n-1] == line {
		return
	}
	c.history = append(c.history, line)
	if len(c.history) > 50 {
		c.history = c.history[len(c.history)-50:]
	}
}
