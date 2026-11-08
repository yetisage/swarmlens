// swarmlens is a k9s-like terminal UI for Docker Swarm: live service,
// node, task and resource views driven by the engine's /events stream,
// with periodic polling as fallback.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"swarmlens/internal/api"
	"swarmlens/internal/ui"
)

// version is set at build time via -ldflags "-X main.version=…".
var version = "dev"

const usageText = `swarmlens — a k9s-like TUI for Docker Swarm

usage: swarmlens [flags]

flags:
  --smoke          run a non-interactive self-test and exit
  --smoke-mutate   allow the self-test to create and scale a throwaway service
  --render         render one static frame (fixed 100x32) and exit
  --width          width for --render (default 100)
  --height         height for --render (default 32)
  --version        print the version and exit
  --debug-events   stream raw /events and exit

environment:
  DOCKER_HOST      endpoint to use (default unix:///var/run/docker.sock);
                   tcp://, ssh:// and TLS (DOCKER_TLS_VERIFY/DOCKER_CERT_PATH)
                   are supported

keys:
  s/n/v/w/k/c  services · nodes · volumes · networks · secrets · configs
  ↑↓ navigate · enter/d detail · l logs · f follow logs
  / search · : command palette · ? help · q quit
`

func main() {
	smoke := flag.Bool("smoke", false, "run a non-interactive self-test and exit")
	smokeMutate := flag.Bool("smoke-mutate", false, "let the self-test create and scale a throwaway service")
	render := flag.Bool("render", false, "render one static frame and exit")
	width := flag.Int("width", 100, "width for --render")
	height := flag.Int("height", 32, "height for --render")
	debugEvents := flag.Bool("debug-events", false, "stream raw /events and exit")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Usage = func() { fmt.Fprint(os.Stderr, usageText) }
	flag.Parse()

	if *showVersion {
		fmt.Printf("swarmlens %s\n", versionString())
		return
	}

	a, err := api.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "swarmlens: %v\n", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *debugEvents {
		os.Exit(debugEventsRun(ctx, a))
	}

	// --smoke-mutate implies --smoke.
	if *smoke || *smokeMutate {
		os.Exit(smokeRun(ctx, a, *smokeMutate))
	}

	state, err := a.SwarmState(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "swarmlens: %v\n", err)
		os.Exit(1)
	}

	if *render {
		fmt.Print(ui.RenderFrame(a, state, *width, *height))
		return
	}

	app := ui.NewApp(a, state)
	p := tea.NewProgram(app, tea.WithAltScreen())
	app.SetProgram(p)

	// The /events stream only delivers swarm-scope events on managers;
	// workers rely on the periodic polling in the UI.
	var streamStop chan struct{}
	if state.Role == "manager" {
		streamStop = make(chan struct{})
		go func() {
			_ = a.EventStream(ctx, streamStop, a.DefaultFilter(), func(msg api.Event) {
				p.Send(ui.EventMsg{Ev: msg})
			})
		}()
	}
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "swarmlens: %v\n", err)
		os.Exit(1)
	}
	if streamStop != nil {
		close(streamStop)
	}
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
func debugEventsRun(ctx context.Context, a *api.API) int {
	fmt.Println("streaming /events for 30s (ctrl+c to stop)…")
	stop := make(chan struct{})
	count := 0
	err := a.EventStream(ctx, stop, nil, func(m api.Event) {
		count++
		name := m.Actor.Attributes["name"]
		fmt.Printf("  %-10s %-16s %s\n", m.Type, m.Action, orDash(name))
	})
	fmt.Printf("stream ended: %d events (err=%v)\n", count, err)
	return 0
}

// smokeRun is a non-interactive self-test. It never mutates the swarm
// unless mutate is explicitly requested.
func smokeRun(ctx context.Context, a *api.API, mutate bool) int {
	failures := 0
	check := func(name string, err error) {
		if err != nil {
			failures++
			fmt.Printf("  ✗ %-22s %v\n", name, err)
			return
		}
		fmt.Printf("  ✓ %s\n", name)
	}

	state, err := a.SwarmState(ctx)
	if err != nil {
		fmt.Printf("FAIL: swarm state: %v\n", err)
		return 1
	}
	fmt.Printf("swarm: %s (role=%s name=%s nodes=%d managers=%d version=%s)\n",
		state.State, state.Role, orDash(state.SwarmName), state.Nodes, state.Managers, orDash(state.Version))

	if state.State != "active" {
		fmt.Println("\nThis node is not part of an active swarm — nothing further to check.")
		fmt.Println("Start one with: docker swarm init --advertise-addr <ip>")
		return 0
	}

	fmt.Println("\nAPI checks:")
	svcs, err := a.Services(ctx)
	check("services", err)
	for _, s := range svcs {
		fmt.Printf("      - %-24s %-24s mode=%-12s replicas=%s updated=%s\n",
			s.Name, s.Image, s.Mode, s.Replicas, s.Updated)
	}

	nodes, err := a.Nodes(ctx)
	check("nodes", err)
	for _, n := range nodes {
		fmt.Printf("      - %-24s role=%-8s avail=%-8s status=%-10s engine=%s\n",
			n.Hostname, n.Role, n.Availability, n.Status, n.Engine)
	}

	if _, err := a.Volumes(ctx); err != nil {
		fmt.Printf("      ! volumes: %v\n", err)
	} else {
		fmt.Printf("      ✓ volumes\n")
	}
	if _, err := a.Networks(ctx); err != nil {
		fmt.Printf("      ! networks: %v\n", err)
	} else {
		fmt.Printf("      ✓ networks\n")
	}
	if _, err := a.Secrets(ctx); err != nil {
		fmt.Printf("      ! secrets: %v\n", err)
	} else {
		fmt.Printf("      ✓ secrets\n")
	}
	if _, err := a.Configs(ctx); err != nil {
		fmt.Printf("      ! configs: %v\n", err)
	} else {
		fmt.Printf("      ✓ configs\n")
	}

	if len(svcs) > 0 {
		s := svcs[0]
		tasks, err := a.Tasks(ctx, s.ID, s.Name)
		check("tasks for "+s.Name, err)
		for _, t := range tasks {
			fmt.Printf("      - %-24s node=%-16s desired=%-10s state=%-10s since=%s\n",
				t.Name, t.Node, t.Desired, t.Current, t.Since)
		}
		if len(tasks) > 0 {
			logs, err := a.TaskLogs(ctx, tasks[0].ID, "5")
			if err != nil {
				fmt.Printf("      ! logs: %v\n", err)
			} else {
				lines := splitLines(logs)
				fmt.Printf("      ✓ logs (%d lines)\n", len(lines))
				for i, line := range lines {
					if i >= 3 {
						break
					}
					fmt.Printf("        %s\n", line)
				}
			}
		}
	}

	if mutate {
		fmt.Println("\nmutating checks (--smoke-mutate):")
		name := fmt.Sprintf("swarmlens-smoke-%d", time.Now().Unix()%100000)
		if err := a.CreateService(ctx, name, "registry:2", 1); err != nil {
			fmt.Printf("      ! create %s: %v\n", name, err)
			failures++
		} else {
			fmt.Printf("      ✓ created throwaway service %s\n", name)
			defer func() {
				if err := a.RemoveService(ctx, name); err != nil {
					fmt.Printf("      ! cleanup %s: %v\n", name, err)
				} else {
					fmt.Printf("      ✓ removed throwaway service %s\n", name)
				}
			}()
			if n, err := a.ScaleService(ctx, name, 1); err != nil {
				fmt.Printf("      ! scale: %v\n", err)
				failures++
			} else {
				fmt.Printf("      ✓ scaled to %d replicas\n", n)
			}
		}
	}

	if state.Role == "manager" {
		fmt.Println("\nevent stream (10s, expecting swarm events):")
		done := make(chan struct{})
		go func() {
			time.Sleep(10 * time.Second)
			close(done)
		}()
		events := 0
		err := a.EventStream(ctx, done, a.DefaultFilter(), func(m api.Event) {
			events++
			name := m.Actor.Attributes["name"]
			fmt.Printf("      event: type=%-9s action=%-10s name=%s\n", m.Type, m.Action, orDash(name))
		})
		if err != nil {
			fmt.Printf("      ! stream: %v\n", err)
			failures++
		} else {
			fmt.Printf("      ✓ stream closed cleanly after %d events\n", events)
		}
	} else {
		fmt.Println("\nnot a manager: swarm event stream skipped (polling only)")
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
	if s == "" {
		return "-"
	}
	return s
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
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
