// swarmlens is a k9s-style terminal UI for Docker Swarm.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"swarmlens/internal/api"
	"swarmlens/internal/config"
	"swarmlens/internal/demo"
	"swarmlens/internal/ui"
)

// version is set at build time via -ldflags "-X main.version=…".
var version = "dev"

const usageText = `swarmlens — a k9s-style TUI for Docker Swarm

usage: swarmlens [flags]

flags:
  -c, --command CMD   run a command at startup, e.g. nodes or "services shop"
  -n, --stack NAME    start in a stack (like k9s' namespace)
      --context NAME  docker context to use (default: the current one)
  -r, --refresh SECS  refresh rate in seconds (default 2)
      --readonly      disable every command that changes the swarm
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
      --debug-events  stream raw /events and exit
      --version       print the version and exit

environment:
  DOCKER_HOST, DOCKER_CONTEXT, DOCKER_CONFIG   as for the docker CLI (tcp://,
                      unix://, ssh:// and TLS endpoints; docker contexts)
  SWARMLENS_CONFIG_DIR  where config.yaml, aliases.yaml and plugins.yaml live
                      (default ~/.config/swarmlens)

keys:  : command · / filter · ? help · enter drill down · esc back · ctrl-c quit
`

func main() {
	var (
		command, stack, ctxName string
		refresh                 string
		readonly, headless      bool
		logoless, crumbsless    bool
		noIcons, demoMode       bool
		render, smoke, mutate   bool
		debugEvents, showVer    bool
		width, height           int
	)
	flag.StringVar(&command, "c", "", "")
	flag.StringVar(&command, "command", "", "")
	flag.StringVar(&stack, "n", "", "")
	flag.StringVar(&stack, "stack", "", "")
	flag.StringVar(&ctxName, "context", "", "")
	flag.StringVar(&refresh, "r", "", "")
	flag.StringVar(&refresh, "refresh", "", "")
	flag.BoolVar(&readonly, "readonly", false, "")
	flag.BoolVar(&headless, "headless", false, "")
	flag.BoolVar(&logoless, "logoless", false, "")
	flag.BoolVar(&crumbsless, "crumbsless", false, "")
	flag.BoolVar(&noIcons, "no-icons", false, "")
	flag.BoolVar(&demoMode, "demo", false, "")
	flag.BoolVar(&render, "render", false, "")
	flag.IntVar(&width, "width", 140, "")
	flag.IntVar(&height, "height", 40, "")
	flag.BoolVar(&smoke, "smoke", false, "")
	flag.BoolVar(&mutate, "smoke-mutate", false, "")
	flag.BoolVar(&debugEvents, "debug-events", false, "")
	flag.BoolVar(&showVer, "version", false, "")
	flag.Usage = func() { fmt.Fprint(os.Stderr, usageText) }
	flag.Parse()

	if showVer {
		fmt.Printf("swarmlens %s\n", versionString())
		return
	}

	cfg, err := config.Load(config.Dir())
	if err != nil {
		fatal(fmt.Errorf("config: %w", err))
	}
	refreshEvery, err := parseRefresh(refresh)
	if err != nil {
		fatal(err)
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
			fatal(err)
		}
		be = a
		connect = func(name string) (api.Backend, error) { return api.Connect(api.ConnectOptions{Context: name}) }
	}

	ctx := context.Background()
	switch {
	case debugEvents:
		os.Exit(debugEventsRun(ctx, be))
	case smoke || mutate:
		os.Exit(smokeRun(ctx, be, mutate))
	}

	opt := ui.Options{
		Backend: be, Connect: connect, Config: cfg, Version: versionString(),
		Command: command, Stack: stack, ReadOnly: readonly, Headless: headless,
		Logoless: logoless, Crumbsless: crumbsless, NoIcons: noIcons, Refresh: refreshEvery,
	}
	if render {
		fmt.Println(ui.Render(opt, width, height))
		return
	}
	if err := ui.Run(opt); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "swarmlens: %v\n", err)
	os.Exit(1)
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

// debugEventsRun streams raw events, useful for diagnosing the engine.
func debugEventsRun(ctx context.Context, be api.Backend) int {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	fmt.Println("streaming /events for 30s (ctrl+c to stop)…")
	count := 0
	_ = be.Events(ctx, func(e api.Event) {
		count++
		fmt.Printf("  %-10s %-16s %s\n", e.Type, e.Action, orDash(e.Name))
	})
	fmt.Printf("stream ended: %d events\n", count)
	return 0
}

// smokeRun is a self-test against the daemon. It only reads unless mutate
// is set, in which case it also creates, scales and removes a throwaway
// service (which needs a real daemon).
func smokeRun(ctx context.Context, be api.Backend, mutate bool) int {
	failures := 0
	check := func(name string, err error) {
		if err != nil {
			failures++
			fmt.Printf("  ✗ %-22s %v\n", name, err)
			return
		}
		fmt.Printf("  ✓ %s\n", name)
	}

	sum, err := be.Summary(ctx)
	if err != nil {
		fmt.Printf("FAIL: %v\n", err)
		return 1
	}
	fmt.Printf("context %s (%s)\nswarm: %s role=%s cluster=%s nodes=%d/%d managers=%d engine=%s\n",
		sum.Context, sum.Endpoint, sum.SwarmState, orDash(sum.Role), orDash(sum.Cluster), sum.NodesReady, sum.Nodes, sum.Managers, orDash(sum.ServerVersion))
	if !sum.InSwarm() {
		fmt.Println("\nThis daemon is not a swarm manager — nothing further to check.")
		fmt.Println("Start a swarm with: docker swarm init --advertise-addr <ip>")
		return 0
	}

	fmt.Println("\nAPI checks:")
	svcs, err := be.Services(ctx)
	check("services", err)
	for _, s := range svcs {
		fmt.Printf("      - %-24s %-12s %d/%d  %s\n", s.Name, s.Mode, s.Running, s.Desired, s.Image)
	}
	nodes, err := be.Nodes(ctx)
	check("nodes", err)
	for _, n := range nodes {
		fmt.Printf("      - %-18s %-8s %-8s %-8s %s\n", n.Hostname, n.Role, n.Availability, n.Status, n.Engine)
	}
	tasks, err := be.Tasks(ctx, api.TaskQuery{})
	check("tasks", err)
	_, err = be.Stacks(ctx)
	check("stacks", err)
	_, err = be.Networks(ctx)
	check("networks", err)
	_, err = be.Volumes(ctx)
	check("volumes", err)
	_, err = be.Secrets(ctx)
	check("secrets", err)
	_, err = be.Configs(ctx)
	check("configs", err)

	if len(svcs) > 0 {
		_, err = be.Describe(ctx, api.KindService, svcs[0].ID)
		check("describe "+svcs[0].Name, err)
		_, err = be.YAML(ctx, api.KindService, svcs[0].ID)
		check("yaml "+svcs[0].Name, err)
		var n int
		err = be.Logs(ctx, api.LogTarget{Kind: api.KindService, ID: svcs[0].ID, Name: svcs[0].Name}, api.LogOptions{Tail: 5}, func(b []api.LogLine) { n += len(b) })
		check(fmt.Sprintf("logs %s (%d lines)", svcs[0].Name, n), err)
	}
	_ = tasks

	if mutate {
		a, ok := be.(*api.API)
		if !ok {
			fmt.Println("\n--smoke-mutate needs a real daemon; skipped")
		} else {
			fmt.Println("\nmutating checks (--smoke-mutate):")
			name := fmt.Sprintf("swarmlens-smoke-%d", time.Now().Unix()%100000)
			if err := a.CreateService(ctx, name, "registry:2", 1); err != nil {
				check("create "+name, err)
			} else {
				check("create "+name, nil)
				defer func() { check("remove "+name, a.Remove(ctx, api.KindService, name)) }()
				check("scale to 2", a.ScaleService(ctx, name, 2))
				check("restart", a.RestartService(ctx, name))
			}
		}
	}

	if sum.Role == "manager" {
		fmt.Println("\nevent stream (5s, expecting swarm events):")
		evCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		events := 0
		_ = be.Events(evCtx, func(e api.Event) {
			events++
			fmt.Printf("      event: %-9s %-10s %s\n", e.Type, e.Action, orDash(e.Name))
		})
		fmt.Printf("      ✓ stream closed cleanly after %d events\n", events)
	}

	fmt.Println()
	if failures > 0 {
		fmt.Printf("FAILED: %d check(s) reported errors\n", failures)
		return 1
	}
	fmt.Println("PASSED")
	return 0
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
