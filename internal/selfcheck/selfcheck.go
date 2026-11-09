// Package selfcheck holds the checks behind --smoke and --debug-events. They
// talk to a daemon through the same api.Backend the UI uses and say, in plain
// text, what works.
package selfcheck

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/yetisage/swarmlens/internal/api"
)

// Options tune Smoke.
type Options struct {
	// Mutate also creates a throwaway service, scales and restarts it and
	// removes it again.
	Mutate bool
	// Image is the image of that service.
	Image string
	// EventWindow is how long to listen to the event stream at the end;
	// zero means 5 seconds.
	EventWindow time.Duration
	// Now names the throwaway service; nil means time.Now.
	Now func() time.Time
}

// Smoke runs the checks, writes its report to w and returns the exit status
// for the process: 0 when everything passed (or there is no swarm to check),
// 1 otherwise.
func Smoke(ctx context.Context, w io.Writer, be api.Backend, o Options) int {
	failures := 0
	check := func(name string, err error) {
		if err != nil {
			failures++
			fmt.Fprintf(w, "  ✗ %-22s %v\n", name, err)
			return
		}
		fmt.Fprintf(w, "  ✓ %s\n", name)
	}

	sum, err := be.Summary(ctx)
	if err != nil {
		fmt.Fprintf(w, "FAIL: %v\n", err)
		return 1
	}
	fmt.Fprintf(w, "context %s (%s)\nswarm: %s role=%s cluster=%s nodes=%d/%d managers=%d engine=%s\n",
		sum.Context, sum.Endpoint, sum.SwarmState, orDash(sum.Role), orDash(sum.Cluster), sum.NodesReady, sum.Nodes, sum.Managers, orDash(sum.ServerVersion))
	if !sum.InSwarm() {
		fmt.Fprintln(w, "\nThis daemon is not a swarm manager — nothing further to check.")
		fmt.Fprintln(w, "Start a swarm with: docker swarm init --advertise-addr <ip>")
		return 0
	}

	fmt.Fprintln(w, "\nAPI checks:")
	svcs, err := be.Services(ctx)
	check("services", err)
	for _, s := range svcs {
		fmt.Fprintf(w, "      - %-24s %-12s %d/%d  %s\n", s.Name, s.Mode, s.Running, s.Desired, s.Image)
	}
	nodes, err := be.Nodes(ctx)
	check("nodes", err)
	for _, n := range nodes {
		fmt.Fprintf(w, "      - %-18s %-8s %-8s %-8s %s\n", n.Hostname, n.Role, n.Availability, n.Status, n.Engine)
	}
	_, err = be.Tasks(ctx, api.TaskQuery{})
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
		first := svcs[0]
		_, err = be.Describe(ctx, api.KindService, first.ID)
		check("describe "+first.Name, err)
		_, err = be.YAML(ctx, api.KindService, first.ID)
		check("yaml "+first.Name, err)
		var n int
		err = be.Logs(ctx, api.LogTarget{Kind: api.KindService, ID: first.ID, Name: first.Name}, api.LogOptions{Tail: 5}, func(b []api.LogLine) { n += len(b) })
		check(fmt.Sprintf("logs %s (%d lines)", first.Name, n), err)
	}

	if o.Mutate {
		mutate(ctx, w, be, o, check)
	}

	if sum.Role == string(api.RoleManager) {
		window := o.EventWindow
		if window <= 0 {
			window = 5 * time.Second
		}
		fmt.Fprintf(w, "\nevent stream (%s, expecting swarm events):\n", window)
		evCtx, cancel := context.WithTimeout(ctx, window)
		events := 0
		_ = be.Events(evCtx, func(e api.Event) {
			events++
			fmt.Fprintf(w, "      event: %-9s %-10s %s\n", e.Type, e.Action, orDash(e.Name))
		})
		cancel()
		fmt.Fprintf(w, "      ✓ stream closed cleanly after %d events\n", events)
	}

	fmt.Fprintln(w)
	if failures > 0 {
		fmt.Fprintf(w, "FAILED: %d check(s) reported errors\n", failures)
		return 1
	}
	fmt.Fprintln(w, "PASSED")
	return 0
}

// mutate creates a throwaway service, scales and restarts it, and removes it
// again. The removal is a check like the others, and it runs before the
// verdict: a service left behind must fail the run.
func mutate(ctx context.Context, w io.Writer, be api.Backend, o Options, check func(string, error)) {
	creator, ok := be.(api.ServiceCreator)
	if !ok {
		fmt.Fprintln(w, "\n--smoke-mutate needs a backend that can create services; skipped")
		return
	}
	fmt.Fprintln(w, "\nmutating checks (--smoke-mutate):")
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	image := o.Image
	if image == "" {
		image = "registry:2"
	}
	name := fmt.Sprintf("swarmlens-smoke-%d", now().Unix()%100000)
	err := creator.CreateService(ctx, name, image, 1)
	check("create "+name, err)
	if err != nil {
		return
	}
	check("scale to 2", be.ScaleService(ctx, name, 2))
	check("restart", be.RestartService(ctx, name))
	check("remove "+name, be.Remove(ctx, api.KindService, name))
}

// DebugEvents streams the raw event feed for the window, which is useful for
// diagnosing the engine. It returns the exit status for the process.
func DebugEvents(ctx context.Context, w io.Writer, be api.Backend, window time.Duration) int {
	ctx, cancel := context.WithTimeout(ctx, window)
	defer cancel()
	fmt.Fprintf(w, "streaming /events for %s (ctrl+c to stop)…\n", window)
	count := 0
	_ = be.Events(ctx, func(e api.Event) {
		count++
		fmt.Fprintf(w, "  %-10s %-16s %s\n", e.Type, e.Action, orDash(e.Name))
	})
	fmt.Fprintf(w, "stream ended: %d events\n", count)
	return 0
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
