package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
)

// newTestAPI points an API at an in-process fake Docker daemon.
func newTestAPI(t *testing.T, h http.Handler) *API {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	cli, err := client.NewClientWithOpts(
		client.WithHost("tcp://"+srv.Listener.Addr().String()),
		client.WithVersion("1.51"),
	)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	return NewWithClient(cli)
}

// route builds a mux that dispatches on the path after the version prefix.
func route(t *testing.T, handlers map[string]http.HandlerFunc) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if i := strings.Index(path, "/v1."); i == 0 {
			rest := path
			_ = rest
			// strip /v1.51 prefix
			parts := strings.SplitN(path, "/", 3)
			if len(parts) >= 3 {
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

func TestServicesParsesReplicasAndImage(t *testing.T) {
	replicas := uint64(3)
	svcs := []swarm.Service{{
		ID: "abcdef0123456789",
		Spec: swarm.ServiceSpec{
			Annotations: swarm.Annotations{Name: "web"},
			TaskTemplate: swarm.TaskSpec{
				ContainerSpec: &swarm.ContainerSpec{Image: "nginx:1.25@sha256:deadbeef"},
			},
			Mode: swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: &replicas}},
		},
		ServiceStatus: &swarm.ServiceStatus{RunningTasks: 2, DesiredTasks: 3},
	}}

	a := newTestAPI(t, route(t, map[string]http.HandlerFunc{
		"/services": func(w http.ResponseWriter, r *http.Request) {
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
	if got.Name != "web" {
		t.Errorf("name = %q, want web", got.Name)
	}
	if got.Replicas != "2/3" {
		t.Errorf("replicas = %q, want 2/3", got.Replicas)
	}
	if got.Image != "nginx:1.25" {
		t.Errorf("image = %q, want nginx:1.25 (digest stripped)", got.Image)
	}
	if got.ID != "abcdef0123456789" {
		t.Errorf("id = %q, want the full id", got.ID)
	}
	if got.ShortID != "abcdef012345" {
		t.Errorf("short id = %q, want abcdef012345", got.ShortID)
	}
	if got.Mode != "replicated" {
		t.Errorf("mode = %q, want replicated", got.Mode)
	}
}

func TestTasksDerivesNamesAndResolvesNodes(t *testing.T) {
	tasks := []swarm.Task{{
		ID:           "task0000000001",
		ServiceID:    "svc",
		Slot:         2,
		NodeID:       "node1",
		DesiredState: "running",
		Status: swarm.TaskStatus{
			State:           "running",
			ContainerStatus: &swarm.ContainerStatus{ContainerID: "container0000000001"},
		},
	}}

	a := newTestAPI(t, route(t, map[string]http.HandlerFunc{
		"/tasks": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, tasks)
		},
		"/nodes": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, []swarm.Node{{
				ID:          "node1",
				Description: swarm.NodeDescription{Hostname: "worker-1"},
			}})
		},
	}))

	rows, err := a.Tasks(context.Background(), "svc", "web")
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 task, got %d", len(rows))
	}
	if rows[0].Name != "web.2" {
		t.Errorf("name = %q, want web.2", rows[0].Name)
	}
	if rows[0].Node != "worker-1" {
		t.Errorf("node = %q, want worker-1", rows[0].Node)
	}
	if rows[0].ContainerID != "container0000000001" {
		t.Errorf("container id = %q", rows[0].ContainerID)
	}
}

func TestScaleServiceOnlyChangesReplicas(t *testing.T) {
	replicas := uint64(2)
	var gotSpec swarm.ServiceSpec

	a := newTestAPI(t, route(t, map[string]http.HandlerFunc{
		"/services/web": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, swarm.Service{
				ID: "svc1",
				Spec: swarm.ServiceSpec{
					Annotations:  swarm.Annotations{Name: "web"},
					TaskTemplate: swarm.TaskSpec{ContainerSpec: &swarm.ContainerSpec{Image: "nginx"}},
					Mode:         swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: &replicas}},
				},
				Version: swarm.Version{Index: 7},
			})
		},
		"/services/svc1/update": func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&gotSpec); err != nil {
				t.Errorf("decode update body: %v", err)
			}
			writeJSON(t, w, swarm.ServiceUpdateResponse{})
		},
	}))

	n, err := a.ScaleService(context.Background(), "web", 1)
	if err != nil {
		t.Fatalf("ScaleService: %v", err)
	}
	if n != 3 {
		t.Errorf("desired = %d, want 3", n)
	}
	if gotSpec.Mode.Replicated == nil || gotSpec.Mode.Replicated.Replicas == nil {
		t.Fatal("replicas missing from update spec")
	}
	if *gotSpec.Mode.Replicated.Replicas != 3 {
		t.Errorf("replicas = %d, want 3", *gotSpec.Mode.Replicated.Replicas)
	}
	// The rest of the spec must survive the round trip. Note that the
	// swarm API normalizes image references on decode ("nginx" becomes
	// "nginx:latest"), so compare on the repository part.
	if gotSpec.TaskTemplate.ContainerSpec == nil {
		t.Fatal("container spec lost during scale update")
	}
	if !strings.HasPrefix(gotSpec.TaskTemplate.ContainerSpec.Image, "nginx") {
		t.Errorf("image = %q, want it to start with nginx", gotSpec.TaskTemplate.ContainerSpec.Image)
	}
}

func TestScaleServiceRejectsGlobalMode(t *testing.T) {
	a := newTestAPI(t, route(t, map[string]http.HandlerFunc{
		"/services/web": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, swarm.Service{
				ID:   "svc1",
				Spec: swarm.ServiceSpec{Mode: swarm.ServiceMode{Global: &swarm.GlobalService{}}},
			})
		},
	}))
	if _, err := a.ScaleService(context.Background(), "web", 1); err == nil {
		t.Fatal("expected an error scaling a global service")
	}
}

func TestRemoveServiceCallsRemoveEndpoint(t *testing.T) {
	called := false
	a := newTestAPI(t, route(t, map[string]http.HandlerFunc{
		"/services/web": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, swarm.Service{ID: "svc1", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: "web"}}})
		},
		"/services/svc1": func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusNoContent)
		},
	}))
	if err := a.RemoveService(context.Background(), "web"); err != nil {
		t.Fatalf("RemoveService: %v", err)
	}
	if !called {
		t.Fatal("service remove endpoint was not called")
	}
}

func TestSetNodeAvailabilityPatchesOnlyAvailability(t *testing.T) {
	var gotSpec swarm.NodeSpec
	a := newTestAPI(t, route(t, map[string]http.HandlerFunc{
		"/nodes/node1": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, swarm.Node{
				ID:   "node1",
				Spec: swarm.NodeSpec{Role: "worker", Availability: "active"},
			})
		},
		"/nodes/node1/update": func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&gotSpec); err != nil {
				t.Errorf("decode: %v", err)
			}
			w.WriteHeader(http.StatusOK)
		},
	}))

	if err := a.SetNodeAvailability(context.Background(), "node1", "drain"); err != nil {
		t.Fatalf("SetNodeAvailability: %v", err)
	}
	if gotSpec.Availability != "drain" {
		t.Errorf("availability = %q, want drain", gotSpec.Availability)
	}
	if gotSpec.Role != "worker" {
		t.Errorf("role = %q, want worker (must be preserved)", gotSpec.Role)
	}
}

func TestEventStreamDeliversAndReconnects(t *testing.T) {
	var attempts int
	a := newTestAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/events") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		attempts++
		w.Header().Set("Content-Type", "application/jsonl")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 2; i++ {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Type":   "service",
				"Action": "update",
				"Actor":  map[string]any{"ID": "svc", "Attributes": map[string]string{"name": "web"}},
			})
			if flusher != nil {
				flusher.Flush()
			}
		}
		// Returning closes the stream, which must trigger a reconnect.
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stop := make(chan struct{})
	received := make(chan string, 20)
	go func() {
		_ = a.EventStream(ctx, stop, nil, func(m Event) {
			received <- string(m.Type) + "/" + string(m.Action)
		})
	}()

	deadline := time.After(4 * time.Second)
	got := 0
	for got < 4 {
		select {
		case <-received:
			got++
		case <-deadline:
			t.Fatalf("only received %d events (attempts=%d)", got, attempts)
		}
	}
	if attempts < 2 {
		t.Errorf("expected the stream to reconnect, attempts=%d", attempts)
	}
}

func TestDefaultFilterIgnoresForeignContainers(t *testing.T) {
	a := newTestAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	a.trackContainers([]string{"swarmcontainer"})

	f := a.DefaultFilter()
	if !f(Event{Type: "service", Action: "update"}) {
		t.Error("service events must pass the filter")
	}
	if !f(Event{Type: "node", Action: "update"}) {
		t.Error("node events must pass the filter")
	}
	if !f(Event{Type: "container", Actor: actorWithID("swarmcontainer")}) {
		t.Error("known swarm containers must pass the filter")
	}
	if f(Event{Type: "container", Actor: actorWithID("othercontainer")}) {
		t.Error("unrelated host containers must be filtered out")
	}
	if f(Event{Type: "volume", Action: "create"}) {
		t.Error("unrelated event types must be filtered out")
	}
}

// actorWithID builds an event actor with the given id.
func actorWithID(id string) (a struct {
	ID         string
	Attributes map[string]string
}) {
	a.ID = id
	return
}

// TestEventStreamStopsOnStopChannel verifies the stream terminates when
// the stop channel is closed, even while the daemon keeps the connection
// open (the healthy case).
func TestEventStreamStopsOnStopChannel(t *testing.T) {
	a := newTestAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/events") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/jsonl")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for {
			select {
			case <-r.Context().Done():
				return
			default:
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Type":   "service",
				"Action": "update",
				"Actor":  map[string]any{"ID": "svc"},
			})
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(10 * time.Millisecond)
		}
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = a.EventStream(ctx, stop, nil, func(Event) {})
		close(done)
	}()

	time.Sleep(200 * time.Millisecond)
	close(stop)

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("EventStream did not stop when the stop channel closed")
	}
}
