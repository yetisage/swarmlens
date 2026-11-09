package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ev "github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/client"
)

func toEventMessage(typ, id string) ev.Message {
	return ev.Message{Type: ev.Type(typ), Actor: ev.Actor{ID: id}}
}

// newTestAPI points an API at an in-process fake Docker daemon.
func newTestAPI(t *testing.T, h http.Handler) *API {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	cli, err := client.New(
		client.WithHost("tcp://"+srv.Listener.Addr().String()),
		client.WithAPIVersion("1.51"),
	)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	return NewWithClient(cli)
}

// route dispatches on the request path after the /v1.xx version prefix.
func route(t *testing.T, handlers map[string]http.HandlerFunc) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasPrefix(path, "/v1.") {
			if parts := strings.SplitN(path, "/", 3); len(parts) == 3 {
				path = "/" + parts[2]
			}
		}
		h, ok := handlers[path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "not found: " + path})
			return
		}
		h(w, r)
	})
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("encode: %v", err)
	}
}

func uptr(n uint64) *uint64 { return &n }

func TestServicesParsesReplicasImagePortsAndStack(t *testing.T) {
	svcs := []swarm.Service{{
		ID: "abcdef0123456789",
		Spec: swarm.ServiceSpec{
			Annotations: swarm.Annotations{Name: "shop_web", Labels: map[string]string{StackLabel: "shop"}},
			TaskTemplate: swarm.TaskSpec{
				ContainerSpec: &swarm.ContainerSpec{Image: "nginx:1.25@sha256:deadbeef"},
				Placement:     &swarm.Placement{Constraints: []string{"node.role==worker"}},
			},
			Mode: swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: uptr(3)}},
		},
		Endpoint: swarm.Endpoint{Ports: []swarm.PortConfig{
			{Protocol: "tcp", TargetPort: 80, PublishedPort: 8080},
			{Protocol: "udp", TargetPort: 53, PublishedPort: 5353, PublishMode: swarm.PortConfigPublishModeHost},
			{Protocol: "tcp", TargetPort: 9000}, // not published
		}},
		ServiceStatus: &swarm.ServiceStatus{RunningTasks: 2, DesiredTasks: 3},
		UpdateStatus:  &swarm.UpdateStatus{State: swarm.UpdateStateUpdating, Message: "update in progress"},
	}}

	a := newTestAPI(t, route(t, map[string]http.HandlerFunc{
		"/services": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("status") != "true" {
				t.Error("service list must request task status")
			}
			writeJSON(t, w, svcs)
		},
	}))

	rows, err := a.Services(context.Background())
	if err != nil {
		t.Fatalf("Services: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 service, got %d", len(rows))
	}
	got := rows[0]
	for name, c := range map[string][2]any{
		"name":    {got.Name, "shop_web"},
		"stack":   {got.Stack, "shop"},
		"image":   {got.Image, "nginx:1.25"},
		"running": {got.Running, 2},
		"desired": {got.Desired, 3},
		"mode":    {got.Mode, "replicated"},
		"update":  {got.UpdateState, "updating"},
	} {
		if c[0] != c[1] {
			t.Errorf("%s = %v, want %v", name, c[0], c[1])
		}
	}
	if want := []string{"*:8080->80/tcp", "host:5353->53/udp"}; strings.Join(got.Ports, ",") != strings.Join(want, ",") {
		t.Errorf("ports = %v, want %v", got.Ports, want)
	}
	if !got.Degraded() {
		t.Error("2/3 running must count as degraded")
	}
	if len(got.Constraints) != 1 {
		t.Errorf("constraints = %v", got.Constraints)
	}
}

func TestTasksDeriveNamesAndResolveNodesAndStacks(t *testing.T) {
	a := newTestAPI(t, route(t, map[string]http.HandlerFunc{
		"/tasks": func(w http.ResponseWriter, r *http.Request) {
			if !strings.Contains(r.URL.Query().Get("filters"), "svc1") {
				t.Errorf("tasks must be filtered by service, got %q", r.URL.Query().Get("filters"))
			}
			writeJSON(t, w, []swarm.Task{
				{
					ID: "task0000000001", ServiceID: "svc1", Slot: 2, NodeID: "node1", DesiredState: "running",
					Status: swarm.TaskStatus{State: "running", ContainerStatus: &swarm.ContainerStatus{ContainerID: "container0000000001"}},
				},
				{
					ID: "task0000000002", ServiceID: "svc2", NodeID: "node1", DesiredState: "running",
					Status: swarm.TaskStatus{State: "failed", Err: "task: non-zero exit (1)"},
				},
			})
		},
		"/services": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, []swarm.Service{
				{ID: "svc1", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: "web", Labels: map[string]string{StackLabel: "shop"}}}},
				{ID: "svc2", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: "agent"}, Mode: swarm.ServiceMode{Global: &swarm.GlobalService{}}}},
			})
		},
		"/nodes": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, []swarm.Node{{ID: "node1", Description: swarm.NodeDescription{Hostname: "worker-1"}}})
		},
	}))

	rows, err := a.Tasks(context.Background(), TaskQuery{ServiceID: "svc1"})
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(rows))
	}
	byID := map[string]Task{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	if r := byID["task0000000001"]; r.Name != "web.2" || r.Node != "worker-1" || r.Stack != "shop" || r.ContainerID != "container0000000001" {
		t.Errorf("replicated task = %+v", r)
	}
	if r := byID["task0000000002"]; r.Name != "agent.worker-1" || r.Error == "" {
		t.Errorf("global task = %+v", r)
	}
}

func TestScaleServiceOnlyChangesReplicas(t *testing.T) {
	var gotSpec swarm.ServiceSpec
	var gotVersion string
	a := newTestAPI(t, route(t, map[string]http.HandlerFunc{
		"/services/svc1": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, swarm.Service{
				ID: "svc1",
				Spec: swarm.ServiceSpec{
					Annotations:  swarm.Annotations{Name: "web"},
					TaskTemplate: swarm.TaskSpec{ContainerSpec: &swarm.ContainerSpec{Image: "nginx"}},
					Mode:         swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: uptr(2)}},
				},
				Meta: swarm.Meta{Version: swarm.Version{Index: 7}},
			})
		},
		"/services/svc1/update": func(w http.ResponseWriter, r *http.Request) {
			gotVersion = r.URL.Query().Get("version")
			if err := json.NewDecoder(r.Body).Decode(&gotSpec); err != nil {
				t.Errorf("decode update body: %v", err)
			}
			writeJSON(t, w, swarm.ServiceUpdateResponse{})
		},
	}))

	if err := a.ScaleService(context.Background(), "svc1", 5); err != nil {
		t.Fatalf("ScaleService: %v", err)
	}
	if gotSpec.Mode.Replicated == nil || gotSpec.Mode.Replicated.Replicas == nil || *gotSpec.Mode.Replicated.Replicas != 5 {
		t.Fatalf("replicas not updated: %+v", gotSpec.Mode)
	}
	if gotVersion != "7" {
		t.Errorf("update sent version %q, want the inspected version 7", gotVersion)
	}
	// The engine normalises image references on decode ("nginx" becomes
	// "nginx:latest"), so compare on the repository.
	if gotSpec.TaskTemplate.ContainerSpec == nil || !strings.HasPrefix(gotSpec.TaskTemplate.ContainerSpec.Image, "nginx") {
		t.Errorf("container spec lost during scale: %+v", gotSpec.TaskTemplate.ContainerSpec)
	}
}

func TestScaleServiceRejectsGlobalMode(t *testing.T) {
	a := newTestAPI(t, route(t, map[string]http.HandlerFunc{
		"/services/svc1": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, swarm.Service{ID: "svc1", Spec: swarm.ServiceSpec{Mode: swarm.ServiceMode{Global: &swarm.GlobalService{}}}})
		},
	}))
	if err := a.ScaleService(context.Background(), "svc1", 3); err == nil {
		t.Fatal("expected an error scaling a global service")
	}
}

func TestRestartBumpsForceUpdate(t *testing.T) {
	var got swarm.ServiceSpec
	a := newTestAPI(t, route(t, map[string]http.HandlerFunc{
		"/services/svc1": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, swarm.Service{ID: "svc1", Spec: swarm.ServiceSpec{TaskTemplate: swarm.TaskSpec{ForceUpdate: 4}}})
		},
		"/services/svc1/update": func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&got)
			writeJSON(t, w, swarm.ServiceUpdateResponse{})
		},
	}))
	if err := a.RestartService(context.Background(), "svc1"); err != nil {
		t.Fatal(err)
	}
	if got.TaskTemplate.ForceUpdate != 5 {
		t.Errorf("ForceUpdate = %d, want 5", got.TaskTemplate.ForceUpdate)
	}
}

func TestRollbackUsesTheEnginesRollbackSemantics(t *testing.T) {
	var rollback string
	a := newTestAPI(t, route(t, map[string]http.HandlerFunc{
		"/services/svc1": func(w http.ResponseWriter, r *http.Request) {
			prev := swarm.ServiceSpec{Annotations: swarm.Annotations{Name: "web"}}
			writeJSON(t, w, swarm.Service{ID: "svc1", PreviousSpec: &prev})
		},
		"/services/svc1/update": func(w http.ResponseWriter, r *http.Request) {
			rollback = r.URL.Query().Get("rollback")
			writeJSON(t, w, swarm.ServiceUpdateResponse{})
		},
	}))
	if err := a.RollbackService(context.Background(), "svc1"); err != nil {
		t.Fatal(err)
	}
	if rollback != "previous" {
		t.Errorf("rollback param = %q, want previous (so rollback config and status apply)", rollback)
	}
}

func TestRollbackWithoutPreviousSpecFails(t *testing.T) {
	a := newTestAPI(t, route(t, map[string]http.HandlerFunc{
		"/services/svc1": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, swarm.Service{ID: "svc1"})
		},
	}))
	if err := a.RollbackService(context.Background(), "svc1"); err == nil {
		t.Fatal("expected an error without a previous spec")
	}
}

func TestSetNodeAvailabilityPatchesOnlyAvailability(t *testing.T) {
	var gotSpec swarm.NodeSpec
	a := newTestAPI(t, route(t, map[string]http.HandlerFunc{
		"/nodes/node1": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, swarm.Node{ID: "node1", Spec: swarm.NodeSpec{Role: "worker", Availability: "active"}})
		},
		"/nodes/node1/update": func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&gotSpec); err != nil {
				t.Errorf("decode: %v", err)
			}
			w.WriteHeader(http.StatusOK)
		},
	}))
	if err := a.SetNodeAvailability(context.Background(), "node1", AvailDrain); err != nil {
		t.Fatalf("SetNodeAvailability: %v", err)
	}
	if gotSpec.Availability != "drain" || gotSpec.Role != "worker" {
		t.Errorf("spec = %+v, want drain with the role preserved", gotSpec)
	}
}

func TestNodesCountsTasksAndReservations(t *testing.T) {
	a := newTestAPI(t, route(t, map[string]http.HandlerFunc{
		"/nodes": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, []swarm.Node{{
				ID:   "n1",
				Spec: swarm.NodeSpec{Role: "manager", Availability: "active"},
				Description: swarm.NodeDescription{
					Hostname:  "mgr",
					Resources: swarm.Resources{NanoCPUs: 4e9, MemoryBytes: 8 << 30},
				},
				Status:        swarm.NodeStatus{State: "ready", Addr: "10.0.0.1"},
				ManagerStatus: &swarm.ManagerStatus{Leader: true, Reachability: "reachable"},
			}})
		},
		"/tasks": func(w http.ResponseWriter, r *http.Request) {
			res := &swarm.ResourceRequirements{Reservations: &swarm.Resources{NanoCPUs: 5e8, MemoryBytes: 256 << 20}}
			writeJSON(t, w, []swarm.Task{
				{ID: "a", NodeID: "n1", Spec: swarm.TaskSpec{Resources: res}},
				{ID: "b", NodeID: "n1", Spec: swarm.TaskSpec{Resources: res}},
			})
		},
	}))
	nodes, err := a.Nodes(context.Background())
	if err != nil || len(nodes) != 1 {
		t.Fatalf("Nodes: %v %v", nodes, err)
	}
	n := nodes[0]
	if n.Manager != "leader" || n.Tasks != 2 || n.CPUReserved != 1.0 || n.MemReserved != 512<<20 || n.CPUs != 4 {
		t.Errorf("node = %+v", n)
	}
}

func TestStacksAreAssembledFromLabels(t *testing.T) {
	label := map[string]string{StackLabel: "shop"}
	a := newTestAPI(t, route(t, map[string]http.HandlerFunc{
		"/services": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, []swarm.Service{
				{ID: "1", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: "shop_web", Labels: label}, Mode: swarm.ServiceMode{Replicated: &swarm.ReplicatedService{}}}, ServiceStatus: &swarm.ServiceStatus{RunningTasks: 1, DesiredTasks: 2}},
				{ID: "2", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: "shop_db", Labels: label}, Mode: swarm.ServiceMode{Replicated: &swarm.ReplicatedService{}}}, ServiceStatus: &swarm.ServiceStatus{RunningTasks: 1, DesiredTasks: 1}},
				{ID: "3", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: "loner"}}},
			})
		},
		"/networks": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, []map[string]any{{"Name": "shop_default", "Id": "n1", "Labels": label}, {"Name": "bridge", "Id": "n2"}})
		},
		"/volumes": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, map[string]any{"Volumes": []map[string]any{{"Name": "shop_data", "Labels": label}}})
		},
		"/secrets": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, []swarm.Secret{{ID: "s1", Spec: swarm.SecretSpec{Annotations: swarm.Annotations{Name: "shop_pw", Labels: label}}}})
		},
		"/configs": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, []swarm.Config{})
		},
	}))
	stacks, err := a.Stacks(context.Background())
	if err != nil {
		t.Fatalf("Stacks: %v", err)
	}
	if len(stacks) != 1 {
		t.Fatalf("stacks = %+v, want only the labelled one", stacks)
	}
	s := stacks[0]
	if s.Name != "shop" || s.Services != 2 || s.Running != 2 || s.Desired != 3 || s.Degraded != 1 || s.Networks != 1 || s.Volumes != 1 || s.Secrets != 1 || s.Configs != 0 {
		t.Errorf("stack = %+v", s)
	}
}

func TestRemoveStackRemovesServicesThenNetworks(t *testing.T) {
	var order []string
	a := newTestAPI(t, route(t, map[string]http.HandlerFunc{
		"/services": func(w http.ResponseWriter, r *http.Request) {
			if !strings.Contains(r.URL.Query().Get("filters"), StackLabel+"=shop") {
				t.Errorf("services not filtered by stack label: %q", r.URL.Query().Get("filters"))
			}
			writeJSON(t, w, []swarm.Service{{ID: "svc1", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: "shop_web"}}}})
		},
		"/networks": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, []map[string]any{{"Name": "shop_default", "Id": "net1"}})
		},
		"/secrets": func(w http.ResponseWriter, r *http.Request) { writeJSON(t, w, []swarm.Secret{}) },
		"/configs": func(w http.ResponseWriter, r *http.Request) { writeJSON(t, w, []swarm.Config{}) },
		"/services/svc1": func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "service")
			w.WriteHeader(http.StatusOK)
		},
		"/networks/net1": func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "network")
			w.WriteHeader(http.StatusNoContent)
		},
	}))
	if err := a.RemoveStack(context.Background(), "shop"); err != nil {
		t.Fatalf("RemoveStack: %v", err)
	}
	if strings.Join(order, ",") != "service,network" {
		t.Errorf("removal order = %v, want services before networks", order)
	}
}

func TestRemoveStackUnknownStackFails(t *testing.T) {
	a := newTestAPI(t, route(t, map[string]http.HandlerFunc{
		"/services": func(w http.ResponseWriter, r *http.Request) { writeJSON(t, w, []swarm.Service{}) },
		"/networks": func(w http.ResponseWriter, r *http.Request) { writeJSON(t, w, []map[string]any{}) },
		"/secrets":  func(w http.ResponseWriter, r *http.Request) { writeJSON(t, w, []swarm.Secret{}) },
		"/configs":  func(w http.ResponseWriter, r *http.Request) { writeJSON(t, w, []swarm.Config{}) },
	}))
	if err := a.RemoveStack(context.Background(), "ghost"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want a not-found error", err)
	}
}

// The objects of a stack disappear on their own as its tasks stop, so one
// that is already gone when it is removed counts as removed.
func TestRemoveStackTreatsGoneObjectsAsRemoved(t *testing.T) {
	gone := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "No such object"})
	}
	a := newTestAPI(t, route(t, map[string]http.HandlerFunc{
		"/services": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, []swarm.Service{{ID: "svc1", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: "shop_web"}}}})
		},
		"/networks": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, []map[string]any{{"Name": "shop_default", "Id": "net1"}})
		},
		"/secrets":       func(w http.ResponseWriter, r *http.Request) { writeJSON(t, w, []swarm.Secret{{ID: "sec1"}}) },
		"/configs":       func(w http.ResponseWriter, r *http.Request) { writeJSON(t, w, []swarm.Config{{ID: "cfg1"}}) },
		"/services/svc1": gone, "/networks/net1": gone, "/secrets/sec1": gone, "/configs/cfg1": gone,
	}))
	start := time.Now()
	if err := a.RemoveStack(context.Background(), "shop"); err != nil {
		t.Fatalf("RemoveStack: %v", err)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("took %v: a network that is gone must not be retried until the deadline", took)
	}
}

// Everything is listed before anything is removed: a listing that fails must
// leave the stack whole, not half removed.
func TestRemoveStackRemovesNothingWhenAListingFails(t *testing.T) {
	var removed []string
	record := func(w http.ResponseWriter, r *http.Request) {
		removed = append(removed, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}
	a := newTestAPI(t, route(t, map[string]http.HandlerFunc{
		"/services": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, []swarm.Service{{ID: "svc1", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: "shop_web"}}}})
		},
		"/networks": func(w http.ResponseWriter, r *http.Request) { writeJSON(t, w, []map[string]any{}) },
		"/secrets": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "secrets are not for you"})
		},
		"/configs":       func(w http.ResponseWriter, r *http.Request) { writeJSON(t, w, []swarm.Config{}) },
		"/services/svc1": record,
	}))
	err := a.RemoveStack(context.Background(), "shop")
	if err == nil || !strings.Contains(err.Error(), "secrets are not for you") {
		t.Fatalf("err = %v, want the listing failure", err)
	}
	if len(removed) != 0 {
		t.Errorf("removed %v although a listing had failed", removed)
	}
}

func TestUpdateServiceSpecRejectsUnknownFields(t *testing.T) {
	a := newTestAPI(t, route(t, map[string]http.HandlerFunc{}))
	err := a.UpdateServiceSpec(context.Background(), "svc1", 3, "Name: web\nTypoField: 1\n")
	if err == nil || !strings.Contains(err.Error(), "invalid service spec") {
		t.Fatalf("err = %v, want a strict-decode error", err)
	}
}

func TestServiceSpecRoundTrip(t *testing.T) {
	var updated swarm.ServiceSpec
	var version string
	a := newTestAPI(t, route(t, map[string]http.HandlerFunc{
		"/services/svc1": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, swarm.Service{
				ID:   "svc1",
				Meta: swarm.Meta{Version: swarm.Version{Index: 9}},
				Spec: swarm.ServiceSpec{
					Annotations:  swarm.Annotations{Name: "web"},
					TaskTemplate: swarm.TaskSpec{ContainerSpec: &swarm.ContainerSpec{Image: "nginx:1"}},
					Mode:         swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: uptr(2)}},
				},
			})
		},
		"/services/svc1/update": func(w http.ResponseWriter, r *http.Request) {
			version = r.URL.Query().Get("version")
			_ = json.NewDecoder(r.Body).Decode(&updated)
			writeJSON(t, w, swarm.ServiceUpdateResponse{})
		},
	}))
	y, v, err := a.ServiceSpec(context.Background(), "svc1")
	if err != nil || v != 9 {
		t.Fatalf("ServiceSpec: %v v=%d", err, v)
	}
	if !strings.Contains(y, "Name: web") || !strings.Contains(y, "Replicas: 2") {
		t.Fatalf("yaml missing fields:\n%s", y)
	}
	edited := strings.Replace(y, "Replicas: 2", "Replicas: 6", 1)
	if err := a.UpdateServiceSpec(context.Background(), "svc1", v, edited); err != nil {
		t.Fatalf("UpdateServiceSpec: %v", err)
	}
	if version != "9" || updated.Mode.Replicated == nil || *updated.Mode.Replicated.Replicas != 6 {
		t.Errorf("update version=%q spec=%+v", version, updated.Mode)
	}
}

func TestEventStreamDeliversAndReconnects(t *testing.T) {
	var attempts atomic.Int32
	a := newTestAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/events") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		attempts.Add(1)
		w.Header().Set("Content-Type", "application/jsonl")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 2; i++ {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Type": "service", "Action": "update",
				"Actor": map[string]any{"ID": "svc", "Attributes": map[string]string{"name": "web", StackLabel: "shop"}},
			})
			if flusher != nil {
				flusher.Flush()
			}
		}
		// Returning closes the stream, which must trigger a reconnect.
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	received := make(chan Event, 20)
	go func() { _ = a.Events(ctx, func(e Event) { received <- e }) }()

	deadline := time.After(4 * time.Second)
	for got := 0; got < 4; got++ {
		select {
		case e := <-received:
			if e.Type != "service" || e.Name != "web" || e.Stack != "shop" {
				t.Fatalf("event = %+v", e)
			}
		case <-deadline:
			t.Fatalf("only received %d events (attempts=%d)", got, attempts.Load())
		}
	}
	if attempts.Load() < 2 {
		t.Errorf("expected the stream to reconnect, attempts=%d", attempts.Load())
	}
}

func TestEventActionDetailBecomesAnAttribute(t *testing.T) {
	m := toEventMessage("container", "c1")
	m.Action = "health_status: healthy"
	m.Actor.Attributes = map[string]string{"name": "web.1.abc", StackLabel: "shop", "image": "nginx"}
	e := toEvent(m)
	if e.Action != "health_status" || e.Attrs["detail"] != "healthy" || e.Attrs["image"] != "nginx" || e.Name != "web.1.abc" || e.Stack != "shop" {
		t.Errorf("event = %+v", e)
	}
	if m.Actor.Attributes["detail"] != "" {
		t.Error("the source message must not be modified")
	}
}

func TestEventFilterIgnoresForeignContainers(t *testing.T) {
	mk := func(typ, action string, attrs map[string]string) ev.Message {
		m := toEventMessage(typ, "someid")
		m.Action = ev.Action(action)
		m.Actor.Attributes = attrs
		return m
	}
	for _, typ := range []string{"service", "node", "secret", "config"} {
		if !relevant(mk(typ, "update", nil)) {
			t.Errorf("swarm-scope %s events must pass", typ)
		}
	}
	task := map[string]string{taskIDLabel: "t1", "com.docker.swarm.service.name": "web"}
	for _, c := range []struct {
		typ, action string
		attrs       map[string]string
		pass        bool
	}{
		{"network", "create", nil, true}, {"network", "destroy", nil, true}, {"network", "connect", nil, false}, {"network", "disconnect", nil, false},
		{"volume", "create", nil, true}, {"volume", "mount", nil, false}, {"volume", "unmount", nil, false}, {"volume", "destroy", nil, true},
		// a task's container passes from its very first event: no listing has to know it first
		{"container", "create", task, true}, {"container", "start", task, true}, {"container", "die", task, true},
		{"container", "health_status: healthy", task, true},
		{"container", "exec_start: sh -c ls", task, false}, {"container", "exec_die", task, false},
		{"container", "attach", task, false}, {"container", "resize", task, false},
		// containers that are not swarm tasks are somebody else's business
		{"container", "start", nil, false}, {"container", "die", map[string]string{"name": "nginx"}, false},
	} {
		if got := relevant(mk(c.typ, c.action, c.attrs)); got != c.pass {
			t.Errorf("%s %q %v: relevant = %v, want %v", c.typ, c.action, c.attrs, got, c.pass)
		}
	}
}

func TestBackoffPolicyStep(t *testing.T) {
	p := backoffPolicy{initial: time.Second, max: 8 * time.Second}
	type step struct {
		delay        time.Duration
		worked       bool
		wait, follow time.Duration
	}
	for i, c := range []step{
		{time.Second, false, time.Second, 2 * time.Second},
		{2 * time.Second, false, 2 * time.Second, 4 * time.Second},
		{4 * time.Second, false, 4 * time.Second, 8 * time.Second},
		{8 * time.Second, false, 8 * time.Second, 8 * time.Second}, // capped
		{8 * time.Second, true, time.Second, 2 * time.Second},      // a stream that worked starts over
		{0, false, time.Second, 2 * time.Second},                   // the zero value is safe
	} {
		if wait, next := p.step(c.delay, c.worked); wait != c.wait || next != c.follow {
			t.Errorf("step %d (%v, worked=%v) = %v, %v; want %v, %v", i, c.delay, c.worked, wait, next, c.wait, c.follow)
		}
	}
}

// The reconnect delay doubles while the daemon keeps failing and starts over
// after a stream that worked; it used to stay at its high-water mark for the
// rest of the session.
func TestEventsReconnectDelayStartsOverAfterAHealthyStream(t *testing.T) {
	var mu sync.Mutex
	var starts, ends []time.Time
	attempt := 0
	a := newTestAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/events") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		mu.Lock()
		attempt++
		n := attempt
		starts = append(starts, time.Now())
		mu.Unlock()
		w.Header().Set("Content-Type", "application/jsonl")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		if n == 3 { // a stream that works: one event, then up for a while
			_ = json.NewEncoder(w).Encode(map[string]any{"Type": "service", "Action": "update", "Actor": map[string]any{"ID": "s"}})
			w.(http.Flusher).Flush()
			time.Sleep(150 * time.Millisecond)
		}
		mu.Lock()
		ends = append(ends, time.Now())
		mu.Unlock()
	}))
	a.reconnect = backoffPolicy{initial: 150 * time.Millisecond, max: 2 * time.Second, healthy: time.Hour}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = a.Events(ctx, func(Event) {}) }()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := len(starts) >= 4 && len(ends) >= 3
		mu.Unlock()
		if done {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(starts) < 4 {
		t.Fatalf("only %d connection attempts", len(starts))
	}
	// without the reset the delay after attempt 3 is 600ms (150, 300, 600);
	// with it, 150ms. The margins are wide on both sides for a busy machine.
	if gap := starts[3].Sub(ends[2]); gap > 400*time.Millisecond {
		t.Errorf("reconnected after %v, want about 150ms: the delay must start over after a stream that worked", gap.Round(time.Millisecond))
	}
}

// TestEventStreamStopsOnCancel verifies the stream terminates when the
// context is cancelled, even while the daemon keeps the connection open.
func TestEventStreamStopsOnCancel(t *testing.T) {
	a := newTestAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/events") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/jsonl")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for r.Context().Err() == nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"Type": "service", "Action": "update", "Actor": map[string]any{"ID": "svc"}})
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(10 * time.Millisecond)
		}
	}))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = a.Events(ctx, func(Event) {})
		close(done)
	}()
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Events did not stop when the context was cancelled")
	}
}

func TestSummaryCountsHealth(t *testing.T) {
	a := newTestAPI(t, route(t, map[string]http.HandlerFunc{
		"/info": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, map[string]any{
				"Name": "mgr1", "ServerVersion": "29.0.0",
				"Swarm": map[string]any{
					"NodeID": "n1", "LocalNodeState": "active", "ControlAvailable": true,
					"Cluster": map[string]any{"ID": "clusterid", "Spec": map[string]any{"Name": "default"}},
				},
			})
		},
		"/version": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, map[string]any{"Version": "29.0.0", "ApiVersion": "1.52"})
		},
		"/nodes": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, []swarm.Node{
				{ID: "n1", Status: swarm.NodeStatus{State: "ready"}, ManagerStatus: &swarm.ManagerStatus{Leader: true, Reachability: "reachable"}},
				{ID: "n2", Status: swarm.NodeStatus{State: "down"}},
			})
		},
		"/services": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, []swarm.Service{
				{ID: "1", Spec: swarm.ServiceSpec{Mode: swarm.ServiceMode{Replicated: &swarm.ReplicatedService{}}}, ServiceStatus: &swarm.ServiceStatus{RunningTasks: 3, DesiredTasks: 3}},
				{ID: "2", Spec: swarm.ServiceSpec{Mode: swarm.ServiceMode{Replicated: &swarm.ReplicatedService{}}}, ServiceStatus: &swarm.ServiceStatus{RunningTasks: 1, DesiredTasks: 2}},
			})
		},
	}))
	s, err := a.Summary(context.Background())
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if !s.InSwarm() || !s.Leader || s.Cluster != "default" || s.Nodes != 2 || s.NodesReady != 1 || s.Managers != 1 ||
		s.Services != 2 || s.ServicesDegraded != 1 || s.TasksRunning != 4 || s.TasksDesired != 5 {
		t.Errorf("summary = %+v", s)
	}
}
