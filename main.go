// swarmlens is a k9s-style terminal UI for Docker Swarm.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/yetisage/swarmlens/internal/api"
	"github.com/yetisage/swarmlens/internal/config"
	"github.com/yetisage/swarmlens/internal/demo"
	"github.com/yetisage/swarmlens/internal/selfcheck"
	"github.com/yetisage/swarmlens/internal/ui"
)

// version is set at build time via -ldflags "-X main.version=…".
var version = "dev"

// How long the self-test commands listen to the event stream; tests shorten
// them. Zero means the default of the command.
var (
	debugEventsWindow = 30 * time.Second
	smokeEventWindow  time.Duration
)

const usageText = `swarmlens — a k9s-style TUI for Docker Swarm

usage: swarmlens [flags]

flags:
  -c, --command CMD   run a command at startup, e.g. nodes or "services shop"
  -n, --stack NAME    start in a stack (like k9s' namespace)
      --context NAME  docker context to use (default: the current one)
  -r, --refresh SECS  refresh rate in seconds (default 2)
      --readonly      disable every command that changes the swarm or what runs
                      in it (this includes shells in containers)
      --headless      hide the header
      --logoless      hide the logo
      --crumbsless    hide the breadcrumbs
      --no-icons      do not use emoji
      --demo          use an in-memory demo swarm
      --render        print one frame as text and exit (see --width/--height)
      --width N       width for --render (default 140)
      --height N      height for --render (default 40)
      --smoke         run a read-only self-test against the daemon and exit
      --smoke-mutate  also create, scale and remove a throwaway service
      --smoke-image I image of that service (default registry:2)
      --debug-events  stream raw /events and exit
      --version       print the version and exit

environment:
  DOCKER_HOST, DOCKER_CONTEXT, DOCKER_CONFIG   as for the docker CLI (tcp://,
                      unix://, ssh:// and TLS endpoints; docker contexts).
                      ssh:// runs the OpenSSH client, which cannot ask
                      questions here: use ssh-agent and trust the host once
  SWARMLENS_CONFIG_DIR  where config.yaml, aliases.yaml and plugins.yaml live
                      (default ~/.config/swarmlens)

keys:  : command · / filter · ? help · enter drill down · esc back · ctrl-c quit
`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// run is main with its inputs and outputs spelled out, and returns the exit
// status: 0 for success, 1 for a failure, 2 for a mistake on the command line.
func run(args []string, stdout, stderr io.Writer) int {
	var (
		command, stack, ctxName, refresh, smokeImage string
		readonly, headless, logoless, crumbsless     bool
		noIcons, demoMode, render                    bool
		smoke, mutate, debugEvents, showVersion      bool
		width, height                                int
	)
	fs := flag.NewFlagSet("swarmlens", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // errors are reported below, with the usage
	fs.StringVar(&command, "c", "", "")
	fs.StringVar(&command, "command", "", "")
	fs.StringVar(&stack, "n", "", "")
	fs.StringVar(&stack, "stack", "", "")
	fs.StringVar(&ctxName, "context", "", "")
	fs.StringVar(&refresh, "r", "", "")
	fs.StringVar(&refresh, "refresh", "", "")
	fs.BoolVar(&readonly, "readonly", false, "")
	fs.BoolVar(&headless, "headless", false, "")
	fs.BoolVar(&logoless, "logoless", false, "")
	fs.BoolVar(&crumbsless, "crumbsless", false, "")
	fs.BoolVar(&noIcons, "no-icons", false, "")
	fs.BoolVar(&demoMode, "demo", false, "")
	fs.BoolVar(&render, "render", false, "")
	fs.IntVar(&width, "width", 140, "")
	fs.IntVar(&height, "height", 40, "")
	fs.BoolVar(&smoke, "smoke", false, "")
	fs.BoolVar(&mutate, "smoke-mutate", false, "")
	fs.StringVar(&smokeImage, "smoke-image", "registry:2", "")
	fs.BoolVar(&debugEvents, "debug-events", false, "")
	fs.BoolVar(&showVersion, "version", false, "")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, usageText)
			return 0
		}
		fmt.Fprintf(stderr, "swarmlens: %v\n\n%s", err, usageText)
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "swarmlens: unexpected argument %q\n\n%s", fs.Arg(0), usageText)
		return 2
	}
	if showVersion {
		fmt.Fprintf(stdout, "swarmlens %s\n", versionString())
		return 0
	}

	cfg, err := config.Load(config.Dir())
	if err != nil {
		return fail(stderr, fmt.Errorf("config: %w", err))
	}
	refreshEvery, err := parseRefresh(refresh)
	if err != nil {
		return fail(stderr, err)
	}

	var (
		be      api.Backend
		connect func(string) (api.Backend, error)
	)
	if demoMode {
		d := demo.New()
		be = d
		connect = func(name string) (api.Backend, error) { return d.WithName(name), nil }
	} else {
		a, err := api.Connect(api.ConnectOptions{Context: ctxName})
		if err != nil {
			return fail(stderr, err)
		}
		be = a
		connect = func(name string) (api.Backend, error) { return api.Connect(api.ConnectOptions{Context: name}) }
	}

	ctx := context.Background()
	if debugEvents || smoke || mutate {
		defer func() { _ = be.Close() }()
		if readonly || cfg.ReadOnly {
			be = api.ReadOnly(be) // the self-test obeys --readonly too: it cannot create anything
		}
		if debugEvents {
			return selfcheck.DebugEvents(ctx, stdout, be, debugEventsWindow)
		}
		return selfcheck.Smoke(ctx, stdout, be, selfcheck.Options{Mutate: mutate, Image: smokeImage, EventWindow: smokeEventWindow})
	}

	opt := ui.Options{
		Backend: be, Connect: connect, Config: cfg, Version: versionString(),
		Command: command, Stack: stack, ReadOnly: readonly, Headless: headless,
		Logoless: logoless, Crumbsless: crumbsless, NoIcons: noIcons, Refresh: refreshEvery,
	}
	if render {
		fmt.Fprintln(stdout, ui.Render(opt, width, height))
		return 0
	}
	if err := ui.Run(opt); err != nil {
		return fail(stderr, err)
	}
	return 0
}

func fail(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "swarmlens: %v\n", err)
	return 1
}

// parseRefresh accepts "2", "0.5" (seconds) or a Go duration like "500ms".
func parseRefresh(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && f > 0 {
		return time.Duration(f * float64(time.Second)), nil
	}
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d, nil
	}
	return 0, fmt.Errorf("invalid refresh rate %q (use seconds, e.g. 2, or a duration, e.g. 500ms)", s)
}

func versionString() string {
	v := version
	if info, ok := debug.ReadBuildInfo(); ok && v == "dev" {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			v = info.Main.Version
		}
	}
	return v
}
