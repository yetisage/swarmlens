package demo

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yetisage/swarmlens/internal/api"
)

var now = time.Date(2026, 11, 8, 15, 0, 0, 0, time.UTC)

func svcByName(t *testing.T, b *Backend, name string) api.Service {
	t.Helper()
	svcs, err := b.Services(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range svcs {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no service %s", name)
	return api.Service{}
}

func TestSeedIsConsistent(t *testing.T) {
	b := NewStatic(now)
	ctx := context.Background()
	sum, err := b.Summary(ctx)
	if err != nil || !sum.InSwarm() {
		t.Fatalf("summary: %+v %v", sum, err)
	}
	if sum.Nodes != 7 || sum.NodesReady != 6 || sum.Services != 14 || sum.ServicesDegraded == 0 {
		t.Errorf("summary = %+v", sum)
	}
	if len(sum.Stacks) != 3 {
		t.Errorf("stacks = %v", sum.Stacks)
	}

	// Every task refers to a real service and node, names match their kind.
	svcs, _ := b.Services(ctx)
	ids := map[string]string{}
	for _, s := range svcs {
		ids[s.ID] = s.Name
	}
	nodes, _ := b.Nodes(ctx)
	nodeIDs := map[string]bool{}
	for _, n := range nodes {
		nodeIDs[n.ID] = true
	}
	tasks, _ := b.Tasks(ctx, api.TaskQuery{})
	if len(tasks) < 30 {
		t.Errorf("only %d tasks", len(tasks))
	}
	for _, tk := range tasks {
		if ids[tk.ServiceID] != tk.ServiceName || !nodeIDs[tk.NodeID] {
			t.Errorf("dangling task %+v", tk)
		}
		if !strings.HasPrefix(tk.Name, tk.ServiceName+".") {
			t.Errorf("task name %q does not belong to %q", tk.Name, tk.ServiceName)
		}
	}
	if s := svcByName(t, b, "shop_worker"); !s.Degraded() || s.Running != 1 || s.Desired != 2 {
		t.Errorf("shop_worker should be degraded 1/2: %+v", s)
	}
	if s := svcByName(t, b, "monitoring_node-exporter"); s.Mode != "global" || s.Running != 5 {
		t.Errorf("global service runs on every ready active node: %+v", s)
	}
}

func TestScaleSpawnsAndStopsTasks(t *testing.T) {
	b := NewStatic(now)
	ctx := context.Background()
	s := svcByName(t, b, "shop_web")
	if err := b.ScaleService(ctx, s.ID, 6); err != nil {
		t.Fatal(err)
	}
	tasks, _ := b.Tasks(ctx, api.TaskQuery{ServiceID: s.ID})
	fresh := 0
	for _, tk := range tasks {
		if tk.Desired == "running" && tk.Slot > 4 {
			fresh++
			if tk.State == "running" {
				t.Errorf("a new task must start up, not appear running: %+v", tk)
			}
		}
	}
	if fresh != 2 {
		t.Errorf("scaling 4→6 created %d tasks, want 2", fresh)
	}
	if err := b.ScaleService(ctx, s.ID, 2); err != nil {
		t.Fatal(err)
	}
	if got := svcByName(t, b, "shop_web"); got.Desired != 2 {
		t.Errorf("desired = %d", got.Desired)
	}
	if err := b.ScaleService(ctx, svcByName(t, b, "traefik").ID, 3); err == nil {
		t.Error("scaling a global service must fail")
	}
	if err := b.ScaleService(ctx, "ghost", 1); err == nil {
		t.Error("scaling an unknown service must fail")
	}
}

func TestTasksProgressThroughStatesOverTime(t *testing.T) {
	var mu sync.Mutex
	clock := now
	b := newBackend("demo", func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }, false)
	ctx := context.Background()
	s := svcByName(t, b, "registry")
	if err := b.ScaleService(ctx, s.ID, 2); err != nil {
		t.Fatal(err)
	}
	state := func() string {
		tasks, _ := b.Tasks(ctx, api.TaskQuery{ServiceID: s.ID})
		for _, tk := range tasks {
			if tk.Slot == 2 {
				return tk.State
			}
		}
		return ""
	}
	var seen []string
	for _, ms := range []int{0, 700, 1300, 1900, 2500, 3500, 5000} {
		mu.Lock()
		clock = now.Add(time.Duration(ms) * time.Millisecond)
		mu.Unlock()
		seen = append(seen, state())
	}
	if got := strings.Join(seen, ","); got != "new,pending,assigned,preparing,starting,running,running" {
		t.Errorf("states over time: %s", got)
	}
}

func TestRestartRollsAndCompletes(t *testing.T) {
	var mu sync.Mutex
	clock := now
	b := newBackend("demo", func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }, false)
	ctx := context.Background()
	s := svcByName(t, b, "shop_cache")
	if err := b.RestartService(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if got := svcByName(t, b, "shop_cache"); got.UpdateState != "updating" {
		t.Errorf("update state = %q", got.UpdateState)
	}
	mu.Lock()
	clock = now.Add(10 * time.Second)
	mu.Unlock()
	if got := svcByName(t, b, "shop_cache"); got.UpdateState != "completed" || got.Running != 1 {
		t.Errorf("after the update: %+v", got)
	}
}

func TestDrainMovesTasksOff(t *testing.T) {
	b := NewStatic(now)
	ctx := context.Background()
	nodes, _ := b.Nodes(ctx)
	var w1 api.Node
	for _, n := range nodes {
		if n.Hostname == "worker-1" {
			w1 = n
		}
	}
	if w1.Tasks == 0 {
		t.Fatal("worker-1 should be running tasks")
	}
	if err := b.SetNodeAvailability(ctx, w1.ID, api.AvailDrain); err != nil {
		t.Fatal(err)
	}
	nodes, _ = b.Nodes(ctx)
	for _, n := range nodes {
		if n.Hostname == "worker-1" && (n.Tasks != 0 || n.Availability != "drain") {
			t.Errorf("drained node = %+v", n)
		}
	}
	// Replicated services keep their replica count.
	if got := svcByName(t, b, "shop_api"); got.Desired != 3 {
		t.Errorf("shop_api desired = %d after the drain", got.Desired)
	}
}

func TestRemoveAndStackRemoval(t *testing.T) {
	b := NewStatic(now)
	ctx := context.Background()
	if err := b.Remove(ctx, api.KindService, svcByName(t, b, "registry").ID); err != nil {
		t.Fatal(err)
	}
	svcs, _ := b.Services(ctx)
	if len(svcs) != 13 {
		t.Errorf("%d services after removing one", len(svcs))
	}
	if err := b.RemoveStack(ctx, "shop"); err != nil {
		t.Fatal(err)
	}
	stacks, _ := b.Stacks(ctx)
	if len(stacks) != 2 {
		t.Errorf("stacks = %+v", stacks)
	}
	// Volumes survive, and do not resurrect the stack.
	vols, _ := b.Volumes(ctx)
	found := false
	for _, v := range vols {
		found = found || v.Name == "shop_db-data"
	}
	if !found {
		t.Error("stack removal must keep volumes")
	}
	if err := b.RemoveStack(ctx, "shop"); err == nil {
		t.Error("removing a missing stack must fail")
	}
	nets, _ := b.Networks(ctx)
	if err := b.Remove(ctx, api.KindNetwork, nets[0].ID); err == nil {
		t.Error("pre-defined networks cannot be removed")
	}
	if err := b.Remove(ctx, api.KindNode, b.local); err == nil {
		t.Error("the node we are connected to cannot be removed")
	}
}

func TestSpecEditRoundTrip(t *testing.T) {
	b := NewStatic(now)
	ctx := context.Background()
	s := svcByName(t, b, "shop_web")
	y, v, err := b.ServiceSpec(ctx, s.ID)
	if err != nil || !strings.Contains(y, "nginx:1.27-alpine") {
		t.Fatalf("spec: %q %v", y, err)
	}
	edited := strings.Replace(strings.Replace(y, "nginx:1.27-alpine", "nginx:1.28", 1), "replicas: 4", "replicas: 5", 1)
	if err := b.UpdateServiceSpec(ctx, s.ID, v, edited); err != nil {
		t.Fatal(err)
	}
	got := svcByName(t, b, "shop_web")
	if got.Image != "nginx:1.28" || got.Desired != 5 || got.UpdateState != "updating" {
		t.Errorf("after the edit: %+v", got)
	}
	if err := b.UpdateServiceSpec(ctx, s.ID, v, "name: [unclosed"); err == nil {
		t.Error("invalid YAML must be rejected")
	}
}

func TestLogsBacklogAndFollow(t *testing.T) {
	b := NewStatic(now)
	s := svcByName(t, b, "shop_web")
	var backlog []api.LogLine
	err := b.Logs(context.Background(), api.LogTarget{Kind: api.KindService, ID: s.ID}, api.LogOptions{Tail: 40}, func(l []api.LogLine) { backlog = append(backlog, l...) })
	if err != nil || len(backlog) != 40 {
		t.Fatalf("backlog = %d lines, err %v", len(backlog), err)
	}
	for i := 1; i < len(backlog); i++ {
		if backlog[i].Time.Before(backlog[i-1].Time) {
			t.Fatal("backlog is not in time order")
		}
	}
	if !strings.HasPrefix(backlog[0].Source, "shop_web.") || backlog[0].Node == "" {
		t.Errorf("line = %+v", backlog[0])
	}

	// Following keeps producing lines until cancelled.
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan struct{}, 8)
	done := make(chan struct{})
	go func() {
		_ = b.Logs(ctx, api.LogTarget{Kind: api.KindService, ID: s.ID}, api.LogOptions{Tail: 1, Follow: true}, func([]api.LogLine) { got <- struct{}{} })
		close(done)
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-got:
		case <-time.After(3 * time.Second):
			t.Fatal("no log line arrived while following")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Logs did not return after cancellation")
	}
}

func TestEventsAreDeliveredAndStop(t *testing.T) {
	b := NewStatic(now)
	ctx, cancel := context.WithCancel(context.Background())
	ev := make(chan api.Event, 4)
	done := make(chan struct{})
	go func() { _ = b.Events(ctx, func(e api.Event) { ev <- e }); close(done) }()
	time.Sleep(50 * time.Millisecond) // let it subscribe
	if err := b.RestartService(ctx, svcByName(t, b, "shop_cache").ID); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-ev:
		if e.Type != "service" || e.Name != "shop_cache" || e.Stack != "shop" {
			t.Errorf("event = %+v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Events did not stop")
	}
}

func TestDescribeAndYAML(t *testing.T) {
	b := NewStatic(now)
	ctx := context.Background()
	svcs, _ := b.Services(ctx)
	for _, kind := range []api.Kind{api.KindService, api.KindNode, api.KindTask, api.KindNetwork, api.KindVolume, api.KindSecret, api.KindConfig, api.KindStack} {
		var id string
		switch kind {
		case api.KindService:
			id = svcs[0].ID
		case api.KindNode:
			id = b.local
		case api.KindTask:
			ts, _ := b.Tasks(ctx, api.TaskQuery{})
			id = ts[0].ID
		case api.KindNetwork:
			ns, _ := b.Networks(ctx)
			id = ns[0].ID
		case api.KindVolume:
			vs, _ := b.Volumes(ctx)
			id = vs[0].Name
		case api.KindSecret:
			ss, _ := b.Secrets(ctx)
			id = ss[0].ID
		case api.KindConfig:
			cs, _ := b.Configs(ctx)
			id = cs[0].ID
		case api.KindStack:
			id = "shop"
		}
		if d, err := b.Describe(ctx, kind, id); err != nil || strings.TrimSpace(d) == "" {
			t.Errorf("Describe(%s): %q %v", kind, d, err)
		}
		if y, err := b.YAML(ctx, kind, id); err != nil || strings.TrimSpace(y) == "" {
			t.Errorf("YAML(%s): %q %v", kind, y, err)
		}
	}
	if _, err := b.Describe(ctx, api.KindService, "ghost"); err == nil {
		t.Error("describing an unknown service must fail")
	}
}
