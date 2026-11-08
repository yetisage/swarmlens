package api

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLive exercises the read-only half of the backend against a real
// daemon. It is skipped unless SWARMLENS_LIVE=1, never mutates anything and
// tolerates any swarm contents:
//
//	SWARMLENS_LIVE=1 go test ./internal/api -run Live -v
func TestLive(t *testing.T) {
	if os.Getenv("SWARMLENS_LIVE") != "1" {
		t.Skip("set SWARMLENS_LIVE=1 to run against the local daemon")
	}
	a, err := Connect(ConnectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	sum, err := a.Summary(ctx)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	t.Logf("context=%s endpoint=%s swarm=%s role=%s cluster=%q nodes=%d/%d services=%d tasks=%d/%d engine=%s api=%s",
		sum.Context, sum.Endpoint, sum.SwarmState, sum.Role, sum.Cluster, sum.NodesReady, sum.Nodes,
		sum.Services, sum.TasksRunning, sum.TasksDesired, sum.ServerVersion, sum.APIVersion)
	if !sum.InSwarm() {
		t.Skip("daemon is not a swarm manager")
	}

	svcs, err := a.Services(ctx)
	if err != nil {
		t.Fatalf("Services: %v", err)
	}
	for _, s := range svcs {
		t.Logf("service %-20s stack=%-8q mode=%-10s %d/%d image=%s ports=%v update=%q", s.Name, s.Stack, s.Mode, s.Running, s.Desired, s.Image, s.Ports, s.UpdateState)
	}

	nodes, err := a.Nodes(ctx)
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	for _, n := range nodes {
		t.Logf("node %-14s role=%s avail=%s status=%s mgr=%s cpus=%.0f mem=%s tasks=%d engine=%s", n.Hostname, n.Role, n.Availability, n.Status, n.Manager, n.CPUs, HumanBytes(n.Memory), n.Tasks, n.Engine)
	}

	tasks, err := a.Tasks(ctx, TaskQuery{})
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	for _, tk := range tasks {
		t.Logf("task %-22s node=%-10s desired=%-8s state=%-9s age=%s err=%q", tk.Name, tk.Node, tk.Desired, tk.State, HumanDuration(time.Since(tk.Created)), tk.Error)
	}

	if _, err := a.Stacks(ctx); err != nil {
		t.Errorf("Stacks: %v", err)
	}
	nets, err := a.Networks(ctx)
	if err != nil {
		t.Errorf("Networks: %v", err)
	}
	if _, err := a.Volumes(ctx); err != nil {
		t.Errorf("Volumes: %v", err)
	}
	if _, err := a.Secrets(ctx); err != nil {
		t.Errorf("Secrets: %v", err)
	}
	if _, err := a.Configs(ctx); err != nil {
		t.Errorf("Configs: %v", err)
	}
	if _, err := a.Contexts(); err != nil {
		t.Errorf("Contexts: %v", err)
	}

	// Describe and YAML for the first object of each kind.
	check := func(kind Kind, id string) {
		d, err := a.Describe(ctx, kind, id)
		if err != nil || strings.TrimSpace(d) == "" {
			t.Errorf("Describe(%s %s): %q %v", kind, id, d, err)
		}
		y, err := a.YAML(ctx, kind, id)
		if err != nil || strings.TrimSpace(y) == "" {
			t.Errorf("YAML(%s %s): %q %v", kind, id, y, err)
		}
		if kind == KindService {
			t.Logf("--- describe %s\n%s", kind, d)
		}
	}
	if len(svcs) > 0 {
		check(KindService, svcs[0].ID)
	}
	if len(tasks) > 0 {
		check(KindTask, tasks[0].ID)
	}
	if len(nodes) > 0 {
		check(KindNode, nodes[0].ID)
	}
	if len(nets) > 0 {
		check(KindNetwork, nets[0].ID)
	}

	// A short, non-following read of the first service's logs.
	if len(svcs) > 0 {
		var n int
		err := a.Logs(ctx, LogTarget{Kind: KindService, ID: svcs[0].ID, Name: svcs[0].Name}, LogOptions{Tail: 5}, func(b []LogLine) {
			for _, l := range b {
				n++
				t.Logf("log [%s@%s] %s", l.Source, l.Node, l.Text)
			}
		})
		if err != nil {
			t.Errorf("Logs: %v", err)
		}
		if n > 0 && len(tasks) > 0 {
			t.Logf("received %d log lines", n)
		}
	}
}
