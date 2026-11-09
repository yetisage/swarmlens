package api

import (
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests run the real implementation against API responses recorded from
// a Docker 29.9.2 swarm (testdata/docker-29.9.2): a stack "shop" with a
// secret, a config, an overlay network and a volume, a standalone service, and
// a service whose only task was rejected because its image could not be
// pulled. The describe and YAML reports are golden files; after a change on
// purpose, refresh them with
//
//	go test ./internal/api -run Recorded -update
//
// and read the diff.
var update = flag.Bool("update", false, "rewrite the golden files in testdata/golden")

const fixtureDir = "testdata/docker-29.9.2"

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixtureDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// idOf reads a top-level string field of a recorded object.
func idOf(t *testing.T, file, field string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(fixture(t, file), &m); err != nil {
		t.Fatal(err)
	}
	s, _ := m[field].(string)
	if s == "" {
		t.Fatalf("%s has no %s", file, field)
	}
	return s
}

// withClock freezes the clock and the zone the reports are made with.
func withClock(t *testing.T, at time.Time) {
	t.Helper()
	oldNow, oldZone := now, zone
	now, zone = func() time.Time { return at }, time.UTC
	t.Cleanup(func() { now, zone = oldNow, oldZone })
}

// frozen is a little after everything in the fixtures happened.
var frozen = time.Date(2026, 11, 9, 16, 30, 0, 0, time.UTC)

// filterList applies the daemon's list filters that swarmlens uses to a
// recorded list: a map of filter name to the values it accepts.
func filterList(t *testing.T, r *http.Request, items []map[string]any, fields map[string]string) []map[string]any {
	t.Helper()
	raw := r.URL.Query().Get("filters")
	if raw == "" {
		return items
	}
	var f map[string]map[string]bool
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		t.Fatalf("filters %q: %v", raw, err)
	}
	var out []map[string]any
	for _, it := range items {
		keep := true
		for name, values := range f {
			field, ok := fields[name]
			if !ok {
				continue // a filter this fake does not know does not narrow anything
			}
			if v, _ := it[field].(string); !values[v] {
				keep = false
			}
		}
		if keep {
			out = append(out, it)
		}
	}
	return out
}

func listHandler(t *testing.T, file string, fields map[string]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var items []map[string]any
		if err := json.Unmarshal(fixture(t, file), &items); err != nil {
			t.Fatal(err)
		}
		writeJSON(t, w, filterList(t, r, items, fields))
	}
}

func fileHandler(t *testing.T, file string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, file))
	}
}

// recordedHandler is the recorded daemon.
func recordedHandler(t *testing.T) http.Handler {
	t.Helper()
	return route(t, map[string]http.HandlerFunc{
		"/_ping": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("API-Version", "1.51")
			_, _ = w.Write([]byte("OK"))
		},
		"/info":     fileHandler(t, "info.json"),
		"/version":  fileHandler(t, "version.json"),
		"/services": listHandler(t, "services.json", map[string]string{"id": "ID"}),
		"/services/" + idOf(t, "service.json", "ID"): fileHandler(t, "service.json"),
		"/tasks":                               listHandler(t, "tasks.json", map[string]string{"service": "ServiceID", "node": "NodeID", "desired-state": "DesiredState"}),
		"/tasks/" + idOf(t, "task.json", "ID"): fileHandler(t, "task.json"),
		"/tasks/" + idOf(t, "task-failed.json", "ID"): fileHandler(t, "task-failed.json"),
		"/nodes":                               fileHandler(t, "nodes.json"),
		"/nodes/" + idOf(t, "node.json", "ID"): fileHandler(t, "node.json"),
		"/networks":                            fileHandler(t, "networks.json"),
		"/networks/" + idOf(t, "network.json", "Id"): fileHandler(t, "network.json"),
		"/volumes": fileHandler(t, "volumes.json"),
		"/volumes/" + idOf(t, "volume.json", "Name"): fileHandler(t, "volume.json"),
		"/secrets": fileHandler(t, "secrets.json"),
		"/secrets/" + idOf(t, "secret.json", "ID"): fileHandler(t, "secret.json"),
		"/configs": fileHandler(t, "configs.json"),
		"/configs/" + idOf(t, "config.json", "ID"): fileHandler(t, "config.json"),
	})
}

func recordedAPI(t *testing.T) *API {
	t.Helper()
	withClock(t, frozen)
	return newTestAPI(t, recordedHandler(t))
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden file %s (run with -update): %v", path, err)
	}
	if got != string(want) {
		t.Errorf("%s differs from %s\n--- got ---\n%s\n--- want ---\n%s", name, path, got, want)
	}
}

type recorded struct {
	name string
	kind Kind
	id   string
}

func recordedObjects(t *testing.T) []recorded {
	t.Helper()
	return []recorded{
		{"service", KindService, idOf(t, "service.json", "ID")},
		{"task", KindTask, idOf(t, "task.json", "ID")},
		{"task-rejected", KindTask, idOf(t, "task-failed.json", "ID")},
		{"node", KindNode, idOf(t, "node.json", "ID")},
		{"network", KindNetwork, idOf(t, "network.json", "Id")},
		{"volume", KindVolume, idOf(t, "volume.json", "Name")},
		{"secret", KindSecret, idOf(t, "secret.json", "ID")},
		{"config", KindConfig, idOf(t, "config.json", "ID")},
		{"stack", KindStack, "shop"},
	}
}

func TestDescribeRecordedObjects(t *testing.T) {
	a := recordedAPI(t)
	for _, o := range recordedObjects(t) {
		t.Run(o.name, func(t *testing.T) {
			got, err := a.Describe(context.Background(), o.kind, o.id)
			if err != nil {
				t.Fatal(err)
			}
			golden(t, "describe-"+o.name+".txt", got)
		})
	}
}

func TestYAMLOfRecordedObjects(t *testing.T) {
	a := recordedAPI(t)
	for _, o := range recordedObjects(t) {
		t.Run(o.name, func(t *testing.T) {
			got, err := a.YAML(context.Background(), o.kind, o.id)
			if err != nil {
				t.Fatal(err)
			}
			golden(t, "yaml-"+o.name+".yaml", got)
		})
	}
}

// The reports must say what a person needs to know, not only keep their shape.
func TestRecordedReportsSayWhatMatters(t *testing.T) {
	a := recordedAPI(t)
	ctx := context.Background()
	must := func(kind Kind, id string, want ...string) {
		t.Helper()
		got, err := a.Describe(ctx, kind, id)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("describe %s: %q is missing from\n%s", kind, w, got)
			}
		}
	}
	must(KindService, idOf(t, "service.json", "ID"), "shop_api", "Stack:", "shop", "2 running, 2 desired", "*:8080->80/tcp", "TOKEN=abc123", "shop_dbpass", "Networks:")
	must(KindTask, idOf(t, "task-failed.json", "ID"), "rejected", "no-such-registry.invalid")
	must(KindSecret, idOf(t, "secret.json", "ID"), "<hidden", "Used By", "shop_api")
	must(KindConfig, idOf(t, "config.json", "ID"), "key=value", "Used By")
	must(KindStack, "shop", "Services", "shop_api", "shop_worker")
	// the YAML of a config shows its data, not base64
	y, err := a.YAML(ctx, KindConfig, idOf(t, "config.json", "ID"))
	if err != nil || !strings.Contains(y, "key=value") {
		t.Errorf("config YAML = %q, %v", y, err)
	}
	// and a kind that cannot be described says so
	if _, err := a.Describe(ctx, KindEvent, "x"); err == nil {
		t.Error("describing an event must fail")
	}
}

func TestListingsOfRecordedResponses(t *testing.T) {
	a := recordedAPI(t)
	ctx := context.Background()

	svcs, err := a.Services(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Service{}
	for _, s := range svcs {
		byName[s.Name] = s
	}
	if api := byName["shop_api"]; api.Stack != "shop" || api.Mode != "replicated" || api.Running != 2 || api.Desired != 2 || api.Image != "hello:1" || len(api.Ports) != 1 || api.Ports[0] != "*:8080->80/tcp" {
		t.Errorf("shop_api = %+v", api)
	}
	if b := byName["broken"]; b.Running != 0 || b.Desired != 1 || !b.Degraded() {
		t.Errorf("broken = %+v: a service with nothing running must read as degraded", b)
	}
	if w := byName["web"]; w.Stack != "" || w.Labels["owner"] != "review" {
		t.Errorf("web = %+v", w)
	}

	tasks, err := a.Tasks(ctx, TaskQuery{})
	if err != nil {
		t.Fatal(err)
	}
	var rejected, running int
	for _, tk := range tasks {
		switch {
		case tk.State == "rejected" && tk.ServiceName == "broken":
			rejected++
			if !strings.Contains(tk.Error, "no-such-registry.invalid") || tk.Name != "broken.1" {
				t.Errorf("rejected task = %+v", tk)
			}
		case tk.State == "running" && tk.ServiceName == "shop_api":
			running++
			if tk.Node != "vm" || tk.Stack != "shop" || !strings.HasPrefix(tk.Name, "shop_api.") {
				t.Errorf("running task = %+v", tk)
			}
		}
	}
	if rejected == 0 || running != 2 {
		t.Errorf("%d rejected, %d running shop_api tasks", rejected, running)
	}

	nodes, err := a.Nodes(ctx)
	if err != nil || len(nodes) != 1 || nodes[0].Hostname != "vm" || nodes[0].Role != "manager" || nodes[0].Manager != "leader" || nodes[0].Status != "ready" || nodes[0].Engine != "29.9.2" {
		t.Errorf("nodes = %+v, %v", nodes, err)
	}

	stacks, err := a.Stacks(ctx)
	if err != nil || len(stacks) != 1 {
		t.Fatalf("stacks = %+v, %v", stacks, err)
	}
	if s := stacks[0]; s.Name != "shop" || s.Services != 2 || s.Running != 3 || s.Desired != 3 || s.Networks < 1 || s.Secrets != 1 || s.Configs != 1 || s.Volumes != 1 {
		t.Errorf("stack = %+v", s)
	}

	sum, err := a.Summary(ctx)
	if err != nil || !sum.InSwarm() || sum.NodeName != "vm" || sum.ServerVersion != "29.9.2" || sum.Services != 4 {
		t.Errorf("summary = %+v, %v", sum, err)
	}
}
