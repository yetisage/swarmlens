// Package demo is an in-memory swarm. It implements api.Backend so the UI
// can be tried without a swarm (--demo), documented with realistic
// screens, and tested against a deterministic fixture. It simulates just
// enough life to exercise the UI: tasks start up over a few seconds,
// scaling and draining move tasks around, updates roll.
package demo

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"swarmlens/internal/api"
)

// Backend is the in-memory swarm.
type Backend struct {
	mu      sync.Mutex
	name    string
	clock   func() time.Time
	rng     *rand.Rand
	chatter bool

	local    string // id of the node "we are connected to"
	nodes    []*api.Node
	services []*service
	tasks    []*task
	networks []api.Network
	volumes  []api.Volume
	secrets  []api.Secret
	configs  []api.Config

	subs    map[int]chan api.Event
	nextSub int
}

type service struct {
	api.Service
	logKind    string
	updateEnds time.Time
	prevImage  string
}

type task struct {
	api.Task
	fresh  bool   // created during the session: its state progresses over time
	broken string // the error a fresh task ends in
}

var (
	_ api.Backend   = (*Backend)(nil)
	_ api.Switcher  = (*Backend)(nil)
	_ api.Simulated = (*Backend)(nil)
)

// New returns a live demo: the clock runs, events trickle in.
func New() *Backend {
	b := newBackend("demo", time.Now, true)
	return b
}

// NewStatic returns a deterministic demo whose clock is frozen at now and
// that produces no background chatter. It is what tests use.
func NewStatic(now time.Time) *Backend {
	return newBackend("demo", func() time.Time { return now }, false)
}

func newBackend(name string, clock func() time.Time, chatter bool) *Backend {
	b := &Backend{
		name: name, clock: clock, chatter: chatter,
		rng:  rand.New(rand.NewSource(42)),
		subs: map[int]chan api.Event{},
	}
	b.seed()
	return b
}

// Simulated implements api.Simulated.
func (b *Backend) Simulated() bool { return true }

// Close implements api.Backend; there is nothing to release.
func (b *Backend) Close() error { return nil }

func mkID(kind, name string) string {
	sum := sha256.Sum256([]byte(kind + "/" + name))
	return strings.ToLower(base32.StdEncoding.EncodeToString(sum[:])[:25])
}

func ago(now time.Time, d time.Duration) time.Time { return now.Add(-d) }

// ---- seed data ----

func (b *Backend) seed() {
	now := b.clock()

	mkNode := func(name, role, avail, status, mgr, addr string, cpus float64, memGiB int64, age time.Duration, labels map[string]string) *api.Node {
		return &api.Node{
			ID: mkID("node", name), Hostname: name, Role: role, Availability: avail, Status: status, Manager: mgr,
			Addr: addr, Engine: "29.8.2", OS: "linux", Arch: "x86_64", CPUs: cpus, Memory: memGiB << 30,
			Labels: labels, Created: ago(now, age),
		}
	}
	b.nodes = []*api.Node{
		mkNode("manager-1", "manager", "active", "ready", "leader", "10.0.0.11", 8, 32, 212*24*time.Hour, map[string]string{"zone": "a"}),
		mkNode("manager-2", "manager", "active", "ready", "reachable", "10.0.0.12", 8, 32, 212*24*time.Hour, map[string]string{"zone": "b"}),
		mkNode("manager-3", "manager", "active", "ready", "reachable", "10.0.0.13", 8, 32, 212*24*time.Hour, map[string]string{"zone": "c"}),
		mkNode("worker-1", "worker", "active", "ready", "", "10.0.0.21", 16, 64, 190*24*time.Hour, map[string]string{"zone": "a"}),
		mkNode("worker-2", "worker", "active", "ready", "", "10.0.0.22", 16, 64, 190*24*time.Hour, map[string]string{"zone": "b", "db": "true"}),
		mkNode("worker-3", "worker", "drain", "ready", "", "10.0.0.23", 16, 64, 88*24*time.Hour, map[string]string{"zone": "c"}),
		mkNode("worker-4", "worker", "active", "down", "", "10.0.0.24", 16, 64, 31*24*time.Hour, map[string]string{"zone": "c"}),
	}
	b.local = b.nodes[0].ID

	stack := func(s string) map[string]string {
		if s == "" {
			return nil
		}
		return map[string]string{api.StackLabel: s}
	}
	mkSvc := func(stk, name, mode string, replicas int, image string, ports []string, cons []string, age time.Duration, kind string) *service {
		full := name
		if stk != "" {
			full = stk + "_" + name
		}
		return &service{
			Service: api.Service{
				ID: mkID("service", full), Name: full, Stack: stk, Mode: mode, Desired: replicas,
				Image: image, Ports: ports, Constraints: cons, Labels: stack(stk),
				Created: ago(now, age), Updated: ago(now, age/3),
			},
			logKind: kind,
		}
	}
	web := mkSvc("shop", "web", "replicated", 4, "nginx:1.27-alpine", []string{"*:8080->80/tcp"}, nil, 41*24*time.Hour, "nginx")
	api_ := mkSvc("shop", "api", "replicated", 3, "ghcr.io/acme/shop-api:2.4.1", nil, []string{"node.role==worker"}, 41*24*time.Hour, "json")
	wrk := mkSvc("shop", "worker", "replicated", 2, "ghcr.io/acme/shop-worker:2.4.1", nil, nil, 41*24*time.Hour, "ansi")
	db := mkSvc("shop", "db", "replicated", 1, "postgres:16.4", nil, []string{"node.labels.db==true"}, 120*24*time.Hour, "postgres")
	cache := mkSvc("shop", "cache", "replicated", 1, "redis:7.4-alpine", nil, nil, 120*24*time.Hour, "plain")
	prom := mkSvc("monitoring", "prometheus", "replicated", 1, "prom/prometheus:v2.54.1", []string{"*:9090->9090/tcp"}, []string{"node.role==manager"}, 77*24*time.Hour, "plain")
	graf := mkSvc("monitoring", "grafana", "replicated", 1, "grafana/grafana:11.2.0", []string{"*:3000->3000/tcp"}, nil, 77*24*time.Hour, "plain")
	nexp := mkSvc("monitoring", "node-exporter", "global", 0, "quay.io/prometheus/node-exporter:v1.8.2", nil, nil, 77*24*time.Hour, "plain")
	cadv := mkSvc("monitoring", "cadvisor", "global", 0, "gcr.io/cadvisor/cadvisor:v0.49.1", nil, nil, 77*24*time.Hour, "plain")
	runner := mkSvc("ci", "runner", "replicated", 3, "gitlab/gitlab-runner:v17.5.0", nil, nil, 19*24*time.Hour, "json")
	legacy := mkSvc("ci", "legacy", "replicated", 0, "acme/legacy-ci:1.0", nil, nil, 300*24*time.Hour, "plain")
	backup := mkSvc("ci", "backup", "replicated-job", 1, "acme/backup:3.2", nil, nil, 9*time.Hour, "plain")
	backup.JobTotal, backup.Completed = 1, 1
	traefik := mkSvc("", "traefik", "global", 0, "traefik:v3.1", []string{"*:80->80/tcp", "*:443->443/tcp"}, []string{"node.role==manager"}, 150*24*time.Hour, "plain")
	registry := mkSvc("", "registry", "replicated", 1, "registry:2", []string{"*:5000->5000/tcp"}, nil, 200*24*time.Hour, "plain")

	runner.UpdateState, runner.UpdateMsg = "updating", "update in progress"
	runner.prevImage, runner.HasPrevSpec = "gitlab/gitlab-runner:v17.4.0", true
	runner.updateEnds = now.Add(10 * time.Second)
	web.HasPrevSpec, web.prevImage = true, "nginx:1.26-alpine"
	api_.UpdateState, api_.UpdateMsg = "completed", "update completed"
	api_.HasPrevSpec, api_.prevImage = true, "ghcr.io/acme/shop-api:2.4.0"

	b.services = []*service{web, api_, wrk, db, cache, prom, graf, nexp, cadv, runner, legacy, backup, traefik, registry}

	// Running tasks, spread over the nodes that can take them.
	for _, s := range b.services {
		switch s.Mode {
		case "global":
			for _, n := range b.eligible(s) {
				b.addTask(s, n, 0, "running", "running", 0, now, "")
			}
		case "replicated":
			for slot := 1; slot <= s.Desired; slot++ {
				n := b.pick(s, slot)
				b.addTask(s, n, slot, "running", "running", 0, now, "")
			}
		}
	}
	// A finished job.
	b.addTask(backup, b.nodes[3], 1, "complete", "shutdown", 0, now, "")

	// shop_worker: slot 2 keeps crashing.
	for _, t := range b.tasks {
		if t.ServiceID == wrk.ID && t.Slot == 2 {
			t.State, t.Error, t.Message = "failed", "task: non-zero exit (137): OOM killed", "started"
			t.Created, t.Updated = ago(now, 95*time.Second), ago(now, 60*time.Second)
		}
	}
	for i := 0; i < 3; i++ { // the earlier attempts, each older than the next
		t := b.addTask(wrk, b.nodes[4], 2, "failed", "shutdown", time.Duration(i+1)*7*time.Minute, now, "task: non-zero exit (137): OOM killed")
		t.Created = ago(now, time.Duration(i+1)*7*time.Minute+3*time.Minute)
	}
	// Rolling update history on shop_api and the one in flight on ci_runner.
	for slot := 1; slot <= 3; slot++ {
		b.addTask(api_, b.pick(api_, slot), slot, "shutdown", "shutdown", 26*time.Hour, now, "")
	}
	for _, t := range b.tasks {
		if t.ServiceID == runner.ID && t.Slot == 2 {
			t.State, t.Message = "starting", "starting"
			t.Created, t.fresh = ago(now, 2400*time.Millisecond), true
		}
	}
	b.addTask(runner, b.nodes[3], 1, "shutdown", "shutdown", 4*time.Minute, now, "")

	// A task that is still being scheduled, and a rejected one.
	b.addTask(web, b.nodes[5], 4, "shutdown", "shutdown", 3*time.Hour, now, "")
	rej := b.addTask(cache, b.nodes[6], 1, "rejected", "shutdown", 5*24*time.Hour, now, `no suitable node (scheduling constraints not satisfied on 4 nodes)`)
	rej.Message = "rejected"

	// Everything else the stacks own.
	mkNet := func(stk, name, driver, scope, subnet string, age time.Duration, internal bool) api.Network {
		n := api.Network{
			ID: mkID("network", name), Name: name, Stack: stk, Driver: driver, Scope: scope,
			Subnets: []string{subnet}, Created: ago(now, age), Internal: internal, Attachable: stk == "",
			Labels: stack(stk),
		}
		return n
	}
	b.networks = []api.Network{
		mkNet("", "bridge", "bridge", "local", "172.17.0.0/16", 212*24*time.Hour, false),
		mkNet("", "docker_gwbridge", "bridge", "local", "172.18.0.0/16", 212*24*time.Hour, false),
		mkNet("", "host", "host", "local", "", 212*24*time.Hour, false),
		{ID: mkID("network", "ingress"), Name: "ingress", Driver: "overlay", Scope: "swarm", Ingress: true, Subnets: []string{"10.0.0.0/24"}, Created: ago(now, 212*24*time.Hour)},
		mkNet("", "none", "null", "local", "", 212*24*time.Hour, false),
		mkNet("", "traefik-public", "overlay", "swarm", "10.0.4.0/24", 150*24*time.Hour, false),
		mkNet("shop", "shop_default", "overlay", "swarm", "10.0.1.0/24", 41*24*time.Hour, false),
		mkNet("shop", "shop_backend", "overlay", "swarm", "10.0.2.0/24", 41*24*time.Hour, true),
		mkNet("monitoring", "monitoring_default", "overlay", "swarm", "10.0.3.0/24", 77*24*time.Hour, false),
		mkNet("ci", "ci_default", "overlay", "swarm", "10.0.5.0/24", 19*24*time.Hour, false),
	}
	mkVol := func(stk, name string, age time.Duration) api.Volume {
		return api.Volume{Name: name, Stack: stk, Driver: "local", Scope: "local", Mountpoint: "/var/lib/docker/volumes/" + name + "/_data", Labels: stack(stk), Created: ago(now, age)}
	}
	b.volumes = []api.Volume{
		mkVol("shop", "shop_db-data", 120*24*time.Hour),
		mkVol("monitoring", "monitoring_prometheus-data", 77*24*time.Hour),
		mkVol("monitoring", "monitoring_grafana-data", 77*24*time.Hour),
		mkVol("", "registry-data", 200*24*time.Hour),
	}
	mkSecret := func(stk, name string, age time.Duration) api.Secret {
		return api.Secret{ID: mkID("secret", name), Name: name, Stack: stk, Labels: stack(stk), Created: ago(now, age), Updated: ago(now, age)}
	}
	b.secrets = []api.Secret{
		mkSecret("shop", "shop_db_password", 120*24*time.Hour),
		mkSecret("shop", "shop_api_key", 41*24*time.Hour),
		mkSecret("ci", "ci_registration_token", 19*24*time.Hour),
	}
	mkCfg := func(stk, name string, age time.Duration) api.Config {
		return api.Config{ID: mkID("config", name), Name: name, Stack: stk, Labels: stack(stk), Created: ago(now, age), Updated: ago(now, age/2)}
	}
	b.configs = []api.Config{
		mkCfg("shop", "shop_nginx.conf", 41*24*time.Hour),
		mkCfg("monitoring", "monitoring_prometheus.yml", 77*24*time.Hour),
		mkCfg("", "traefik.toml", 150*24*time.Hour),
	}
}

// eligible are the nodes a service may place tasks on.
func (b *Backend) eligible(s *service) []*api.Node {
	var out []*api.Node
	for _, n := range b.nodes {
		if n.Status != "ready" || n.Availability != "active" {
			continue
		}
		ok := true
		for _, c := range s.Constraints {
			switch {
			case c == "node.role==worker" && n.Role != "worker":
				ok = false
			case c == "node.role==manager" && n.Role != "manager":
				ok = false
			case c == "node.labels.db==true" && n.Labels["db"] != "true":
				ok = false
			}
		}
		if ok {
			out = append(out, n)
		}
	}
	return out
}

// pick places the task of a slot on an eligible node, round robin.
func (b *Backend) pick(s *service, slot int) *api.Node {
	el := b.eligible(s)
	if len(el) == 0 {
		return b.nodes[0]
	}
	return el[(slot-1)%len(el)]
}

func (b *Backend) addTask(s *service, n *api.Node, slot int, state, desired string, age time.Duration, now time.Time, errMsg string) *task {
	name := s.Name + "." + fmt.Sprint(slot)
	if s.Mode == "global" || s.Mode == "global-job" {
		name = s.Name + "." + n.Hostname
	}
	id := mkID("task", fmt.Sprintf("%s/%d/%d/%d", s.Name, slot, len(b.tasks), age))
	t := &task{Task: api.Task{
		ID: id, Name: name, ServiceID: s.ID, ServiceName: s.Name, Stack: s.Stack, NodeID: n.ID, Node: n.Hostname,
		Slot: slot, Image: s.Image, Desired: desired, State: state, Error: errMsg,
		Created: ago(now, age+time.Duration(slot)*time.Minute*17+9*time.Hour), Updated: ago(now, age+time.Minute),
	}}
	if age == 0 {
		t.Created = s.Created.Add(time.Duration(slot) * time.Minute)
		if s.Updated.After(t.Created) {
			t.Created = s.Updated.Add(time.Duration(slot) * time.Second)
		}
		t.Updated = t.Created.Add(4 * time.Second)
	}
	if state == "running" || state == "failed" || state == "complete" {
		t.ContainerID = strings.ToLower(base32.StdEncoding.EncodeToString([]byte(id))[:24])
	}
	b.tasks = append(b.tasks, t)
	return t
}

// ---- simulation ----

// advance moves tasks created during the session through their states and
// completes updates whose time has come. Callers hold the lock.
func (b *Backend) advance() {
	now := b.clock()
	for _, t := range b.tasks {
		if !t.fresh || t.State == "running" || t.State == "failed" {
			continue
		}
		age := now.Sub(t.Created)
		switch {
		case age < 600*time.Millisecond:
			t.State, t.Message = "new", "created"
		case age < 1200*time.Millisecond:
			t.State, t.Message = "pending", "scheduling"
		case age < 1800*time.Millisecond:
			t.State, t.Message = "assigned", "assigned to "+t.Node
		case age < 2400*time.Millisecond:
			t.State, t.Message = "preparing", "preparing"
		case age < 3200*time.Millisecond:
			t.State, t.Message = "starting", "starting"
		case t.broken != "":
			t.State, t.Error, t.Updated = "failed", t.broken, now
		default:
			t.State, t.Message, t.Updated = "running", "started", now
			t.ContainerID = strings.ToLower(base32.StdEncoding.EncodeToString([]byte(t.ID))[:24])
		}
	}
	for _, s := range b.services {
		if s.UpdateState == "updating" || s.UpdateState == "rollback_started" {
			if !now.Before(s.updateEnds) {
				s.UpdateState, s.UpdateMsg = "completed", "update completed"
				s.Updated = now
			}
		}
	}
}

func (b *Backend) svcByID(id string) *service {
	for _, s := range b.services {
		if s.ID == id || s.Name == id {
			return s
		}
	}
	return nil
}

func (b *Backend) nodeByID(id string) *api.Node {
	for _, n := range b.nodes {
		if n.ID == id || n.Hostname == id {
			return n
		}
	}
	return nil
}

// counts computes running and desired tasks for a service.
func (b *Backend) counts(s *service) (running, desired int) {
	for _, t := range b.tasks {
		if t.ServiceID != s.ID {
			continue
		}
		if t.Desired == "running" || t.Desired == "ready" {
			desired++
			if t.State == "running" {
				running++
			}
		}
	}
	if s.Mode == "replicated" {
		desired = s.Desired
	}
	if s.Mode == "replicated-job" {
		return 0, s.Desired
	}
	return running, desired
}

func (b *Backend) publish(e api.Event) {
	e.Time = b.clock()
	for _, ch := range b.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

// spawnTask adds a fresh task for a slot, which then starts up over time.
func (b *Backend) spawnTask(s *service, slot int, n *api.Node) *task {
	t := b.addTask(s, n, slot, "new", "running", 0, b.clock(), "")
	t.Created, t.Updated, t.fresh = b.clock(), b.clock(), true
	t.ContainerID = ""
	return t
}

// ---- api.Backend: identity ----

func (b *Backend) ContextName() string { return b.name }
func (b *Backend) Endpoint() string    { return "demo://in-memory" }

func (b *Backend) Summary(ctx context.Context) (*api.Summary, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.advance()
	s := &api.Summary{
		Context: b.name, Endpoint: b.Endpoint(), SwarmState: "active", Role: "manager", Leader: true,
		NodeID: b.local, NodeName: b.nodeByID(b.local).Hostname,
		ClusterID: mkID("cluster", "demo"), Cluster: "acme-prod",
		ServerVersion: "29.8.2", APIVersion: "1.56",
		Nodes: len(b.nodes), Services: len(b.services),
	}
	stacks := map[string]bool{}
	for _, n := range b.nodes {
		if n.Status == "ready" {
			s.NodesReady++
		}
		if n.Role == "manager" {
			s.Managers++
			if n.Manager == "leader" || n.Manager == "reachable" {
				s.ManagersReachable++
			}
		}
	}
	for _, sv := range b.services {
		run, des := b.counts(sv)
		sv.Running, sv.Desired = run, des
		if sv.Mode == "replicated" || sv.Mode == "global" {
			s.TasksRunning += run
			s.TasksDesired += des
		}
		if sv.Degraded() {
			s.ServicesDegraded++
		}
		if sv.Stack != "" {
			stacks[sv.Stack] = true
		}
	}
	for st := range stacks {
		s.Stacks = append(s.Stacks, st)
	}
	sort.Strings(s.Stacks)
	return s, nil
}

// ---- listings ----

func (b *Backend) Services(ctx context.Context) ([]api.Service, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.advance()
	out := make([]api.Service, 0, len(b.services))
	for _, s := range b.services {
		s.Running, s.Desired = b.counts(s)
		if s.Mode == "replicated-job" {
			s.Running = 0
		}
		out = append(out, s.Service)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (b *Backend) Tasks(ctx context.Context, q api.TaskQuery) ([]api.Task, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.advance()
	var out []api.Task
	for _, t := range b.tasks {
		if q.ServiceID != "" && t.ServiceID != q.ServiceID {
			continue
		}
		if q.NodeID != "" && t.NodeID != q.NodeID {
			continue
		}
		out = append(out, t.Task)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Created.After(out[j].Created)
	})
	return out, nil
}

func (b *Backend) Nodes(ctx context.Context) ([]api.Node, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.advance()
	out := make([]api.Node, 0, len(b.nodes))
	for _, n := range b.nodes {
		c := *n
		c.Tasks, c.CPUReserved, c.MemReserved = 0, 0, 0
		for _, t := range b.tasks {
			if t.NodeID == n.ID && t.Desired == "running" {
				c.Tasks++
				c.CPUReserved += 0.25
				c.MemReserved += 256 << 20
			}
		}
		out = append(out, c)
	}
	return out, nil
}

func (b *Backend) Stacks(ctx context.Context) ([]api.Stack, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.advance()
	by := map[string]*api.Stack{}
	get := func(n string) *api.Stack {
		if by[n] == nil {
			by[n] = &api.Stack{Name: n}
		}
		return by[n]
	}
	for _, s := range b.services {
		if s.Stack == "" {
			continue
		}
		st := get(s.Stack)
		run, des := b.counts(s)
		s.Running, s.Desired = run, des
		st.Services++
		st.Running += run
		st.Desired += des
		if s.Degraded() {
			st.Degraded++
		}
		if st.Created.IsZero() || s.Created.Before(st.Created) {
			st.Created = s.Created
		}
	}
	// Like `docker stack ls`: only stacks that still have services count.
	for _, n := range b.networks {
		if s := by[n.Stack]; s != nil {
			s.Networks++
		}
	}
	for _, v := range b.volumes {
		if s := by[v.Stack]; s != nil {
			s.Volumes++
		}
	}
	for _, sc := range b.secrets {
		if s := by[sc.Stack]; s != nil {
			s.Secrets++
		}
	}
	for _, c := range b.configs {
		if s := by[c.Stack]; s != nil {
			s.Configs++
		}
	}
	var out []api.Stack
	for _, s := range by {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (b *Backend) Networks(ctx context.Context) ([]api.Network, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]api.Network(nil), b.networks...), nil
}

func (b *Backend) Volumes(ctx context.Context) ([]api.Volume, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]api.Volume(nil), b.volumes...), nil
}

func (b *Backend) Secrets(ctx context.Context) ([]api.Secret, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]api.Secret(nil), b.secrets...), nil
}

func (b *Backend) Configs(ctx context.Context) ([]api.Config, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]api.Config(nil), b.configs...), nil
}

// Contexts implements api.Switcher.
func (b *Backend) Contexts() ([]api.ContextInfo, error) {
	return []api.ContextInfo{
		{Name: "demo", Description: "In-memory demo swarm", Endpoint: "demo://in-memory", Current: b.name == "demo"},
		{Name: "lab", Description: "Another demo swarm", Endpoint: "demo://lab", Current: b.name == "lab"},
	}, nil
}

// WithName returns a copy of the demo under another context name.
func (b *Backend) WithName(name string) *Backend {
	nb := newBackend(name, b.clock, b.chatter)
	return nb
}

// ---- inspection ----

func (b *Backend) object(kind api.Kind, id string) (any, error) {
	switch kind {
	case api.KindService:
		if s := b.svcByID(id); s != nil {
			return s.Service, nil
		}
	case api.KindTask:
		for _, t := range b.tasks {
			if t.ID == id {
				return t.Task, nil
			}
		}
	case api.KindNode:
		if n := b.nodeByID(id); n != nil {
			return *n, nil
		}
	case api.KindNetwork:
		for _, n := range b.networks {
			if n.ID == id || n.Name == id {
				return n, nil
			}
		}
	case api.KindVolume:
		for _, v := range b.volumes {
			if v.Name == id {
				return v, nil
			}
		}
	case api.KindSecret:
		for _, s := range b.secrets {
			if s.ID == id || s.Name == id {
				return s, nil
			}
		}
	case api.KindConfig:
		for _, c := range b.configs {
			if c.ID == id || c.Name == id {
				return c, nil
			}
		}
	case api.KindStack:
		for _, s := range b.services {
			if s.Stack == id {
				return map[string]any{"Name": id, "Orchestrator": "Swarm"}, nil
			}
		}
	}
	return nil, fmt.Errorf("%s %q not found", kind, id)
}

func (b *Backend) YAML(ctx context.Context, kind api.Kind, id string) (string, error) {
	b.mu.Lock()
	obj, err := b.object(kind, id)
	b.mu.Unlock()
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(obj)
	if err != nil {
		return "", err
	}
	return api.JSONToYAML(raw)
}

func (b *Backend) Describe(ctx context.Context, kind api.Kind, id string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.advance()
	obj, err := b.object(kind, id)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	kv := func(k string, v any) {
		if s := fmt.Sprint(v); s != "" && s != "0" && s != "false" && s != "[]" && s != "map[]" {
			fmt.Fprintf(&sb, "%-18s%s\n", k+":", s)
		}
	}
	now := b.clock()
	switch o := obj.(type) {
	case api.Service:
		s := b.svcByID(id)
		run, des := b.counts(s)
		kv("Name", o.Name)
		kv("ID", o.ID)
		kv("Stack", o.Stack)
		kv("Mode", o.Mode)
		kv("Replicas", fmt.Sprintf("%d running, %d desired", run, des))
		kv("Image", o.Image)
		kv("Ports", strings.Join(o.Ports, "\n                  "))
		kv("Constraints", strings.Join(o.Constraints, ", "))
		kv("Created", fmt.Sprintf("%s (%s ago)", o.Created.Format("2006-01-02 15:04:05"), api.HumanDuration(now.Sub(o.Created))))
		kv("Update Status", strings.TrimSpace(o.UpdateState+" — "+o.UpdateMsg))
		sb.WriteString("Tasks:\n")
		for _, t := range b.tasks {
			if t.ServiceID == o.ID {
				fmt.Fprintf(&sb, "  %-26s %-10s %-12s %s\n", t.Name, t.State, t.Node, t.Error)
			}
		}
	case api.Task:
		kv("Name", o.Name)
		kv("ID", o.ID)
		kv("Service", o.ServiceName)
		kv("Node", o.Node)
		kv("Image", o.Image)
		kv("Desired State", o.Desired)
		kv("State", o.State)
		kv("Error", o.Error)
		kv("Container ID", o.ContainerID)
	case api.Node:
		kv("Hostname", o.Hostname)
		kv("ID", o.ID)
		kv("Role", o.Role)
		kv("Availability", o.Availability)
		kv("Status", o.Status)
		kv("Manager", o.Manager)
		kv("Address", o.Addr)
		kv("Engine", o.Engine)
		kv("Resources", fmt.Sprintf("cpu %.0f, memory %s", o.CPUs, api.HumanBytes(o.Memory)))
	default:
		raw, _ := json.Marshal(obj)
		y, _ := api.JSONToYAML(raw)
		sb.WriteString(y)
	}
	return sb.String(), nil
}

// ---- mutations ----

func (b *Backend) ScaleService(ctx context.Context, id string, replicas int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.svcByID(id)
	if s == nil {
		return fmt.Errorf("service %q not found", id)
	}
	if s.Mode != "replicated" {
		return fmt.Errorf("service %q is not replicated", s.Name)
	}
	replicas = max(replicas, 0)
	// Count the live slots.
	live := map[int]bool{}
	for _, t := range b.tasks {
		if t.ServiceID == s.ID && t.Desired == "running" {
			live[t.Slot] = true
		}
	}
	for slot := 1; slot <= replicas; slot++ {
		if !live[slot] {
			b.spawnTask(s, slot, b.pick(s, slot))
		}
	}
	for _, t := range b.tasks {
		if t.ServiceID == s.ID && t.Desired == "running" && t.Slot > replicas {
			t.Desired, t.State, t.Updated = "shutdown", "shutdown", b.clock()
		}
	}
	s.Desired = replicas
	s.Updated = b.clock()
	b.publish(api.Event{Type: "service", Action: "update", ID: s.ID, Name: s.Name, Stack: s.Stack})
	return nil
}

func (b *Backend) RestartService(ctx context.Context, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.svcByID(id)
	if s == nil {
		return fmt.Errorf("service %q not found", id)
	}
	now := b.clock()
	for _, t := range append([]*task(nil), b.tasks...) {
		if t.ServiceID == s.ID && t.Desired == "running" {
			t.Desired, t.State, t.Updated = "shutdown", "shutdown", now
			nt := b.spawnTask(s, t.Slot, b.nodeByID(t.NodeID))
			_ = nt
		}
	}
	s.UpdateState, s.UpdateMsg = "updating", "update in progress"
	s.updateEnds = now.Add(6 * time.Second)
	s.Updated = now
	b.publish(api.Event{Type: "service", Action: "update", ID: s.ID, Name: s.Name, Stack: s.Stack, Attrs: map[string]string{"updatestate.new": "updating"}})
	return nil
}

func (b *Backend) RollbackService(ctx context.Context, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.svcByID(id)
	if s == nil {
		return fmt.Errorf("service %q not found", id)
	}
	if !s.HasPrevSpec {
		return fmt.Errorf("service %q has no previous spec to roll back to", s.Name)
	}
	s.Image, s.prevImage = s.prevImage, s.Image
	s.UpdateState, s.UpdateMsg = "rollback_started", "rollback in progress"
	s.updateEnds = b.clock().Add(6 * time.Second)
	for _, t := range b.tasks {
		if t.ServiceID == s.ID && t.Desired == "running" {
			t.Image = s.Image
		}
	}
	b.publish(api.Event{Type: "service", Action: "update", ID: s.ID, Name: s.Name, Stack: s.Stack, Attrs: map[string]string{"updatestate.new": "rollback_started"}})
	return nil
}

func (b *Backend) SetNodeAvailability(ctx context.Context, id string, a api.NodeAvailability) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := b.nodeByID(id)
	if n == nil {
		return fmt.Errorf("node %q not found", id)
	}
	n.Availability = string(a)
	if a == api.AvailDrain {
		for _, t := range append([]*task(nil), b.tasks...) {
			if t.NodeID != n.ID || t.Desired != "running" {
				continue
			}
			s := b.svcByID(t.ServiceID)
			t.Desired, t.State, t.Updated = "shutdown", "shutdown", b.clock()
			if s != nil && s.Mode == "replicated" {
				b.spawnTask(s, t.Slot, b.pick(s, t.Slot+1))
			}
		}
	}
	b.publish(api.Event{Type: "node", Action: "update", ID: n.ID, Name: n.Hostname})
	return nil
}

func (b *Backend) SetNodeRole(ctx context.Context, id string, r api.NodeRole) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := b.nodeByID(id)
	if n == nil {
		return fmt.Errorf("node %q not found", id)
	}
	n.Role = string(r)
	if r == api.RoleManager {
		n.Manager = "reachable"
	} else {
		n.Manager = ""
	}
	b.publish(api.Event{Type: "node", Action: "update", ID: n.ID, Name: n.Hostname})
	return nil
}

func (b *Backend) Remove(ctx context.Context, kind api.Kind, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch kind {
	case api.KindService:
		s := b.svcByID(id)
		if s == nil {
			return fmt.Errorf("service %q not found", id)
		}
		b.dropService(s)
		b.publish(api.Event{Type: "service", Action: "remove", ID: s.ID, Name: s.Name, Stack: s.Stack})
	case api.KindNode:
		n := b.nodeByID(id)
		if n == nil {
			return fmt.Errorf("node %q not found", id)
		}
		if n.ID == b.local {
			return fmt.Errorf("node %q is the one you are connected to", n.Hostname)
		}
		kept := b.nodes[:0:0]
		for _, x := range b.nodes {
			if x != n {
				kept = append(kept, x)
			}
		}
		b.nodes = kept
		b.publish(api.Event{Type: "node", Action: "remove", ID: n.ID, Name: n.Hostname})
	case api.KindNetwork:
		for i, n := range b.networks {
			if n.ID == id {
				if n.Ingress || n.Name == "bridge" || n.Name == "host" || n.Name == "none" || n.Name == "docker_gwbridge" {
					return fmt.Errorf("%s is a pre-defined network and cannot be removed", n.Name)
				}
				b.networks = append(b.networks[:i], b.networks[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("network %q not found", id)
	case api.KindVolume:
		for i, v := range b.volumes {
			if v.Name == id {
				b.volumes = append(b.volumes[:i], b.volumes[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("volume %q not found", id)
	case api.KindSecret:
		for i, s := range b.secrets {
			if s.ID == id {
				b.secrets = append(b.secrets[:i], b.secrets[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("secret %q not found", id)
	case api.KindConfig:
		for i, c := range b.configs {
			if c.ID == id {
				b.configs = append(b.configs[:i], b.configs[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("config %q not found", id)
	case api.KindStack:
		return b.removeStackLocked(id)
	default:
		return fmt.Errorf("cannot remove a %s", kind)
	}
	return nil
}

func (b *Backend) dropService(s *service) {
	kept := b.services[:0:0]
	for _, x := range b.services {
		if x != s {
			kept = append(kept, x)
		}
	}
	b.services = kept
	tk := b.tasks[:0:0]
	for _, t := range b.tasks {
		if t.ServiceID != s.ID {
			tk = append(tk, t)
		}
	}
	b.tasks = tk
}

func (b *Backend) RemoveStack(ctx context.Context, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.removeStackLocked(name)
}

func (b *Backend) removeStackLocked(name string) error {
	found := false
	for _, s := range append([]*service(nil), b.services...) {
		if s.Stack == name {
			found = true
			b.dropService(s)
		}
	}
	netw := b.networks[:0:0]
	for _, n := range b.networks {
		if n.Stack == name {
			found = true
			continue
		}
		netw = append(netw, n)
	}
	b.networks = netw
	sec := b.secrets[:0:0]
	for _, s := range b.secrets {
		if s.Stack == name {
			found = true
			continue
		}
		sec = append(sec, s)
	}
	b.secrets = sec
	cfg := b.configs[:0:0]
	for _, c := range b.configs {
		if c.Stack == name {
			found = true
			continue
		}
		cfg = append(cfg, c)
	}
	b.configs = cfg
	if !found {
		return fmt.Errorf("stack %q not found", name)
	}
	b.publish(api.Event{Type: "service", Action: "remove", Name: name, Stack: name})
	return nil
}

// specDoc is the part of a service spec the demo lets you edit.
type specDoc struct {
	Name         string
	TaskTemplate struct{ ContainerSpec struct{ Image string } }
	Mode         struct{ Replicated struct{ Replicas int } }
}

func (b *Backend) ServiceSpec(ctx context.Context, id string) (string, uint64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.svcByID(id)
	if s == nil {
		return "", 0, fmt.Errorf("service %q not found", id)
	}
	var d specDoc
	d.Name = s.Name
	d.TaskTemplate.ContainerSpec.Image = s.Image
	d.Mode.Replicated.Replicas = s.Desired
	out, err := yaml.Marshal(&d)
	return string(out), 1, err
}

func (b *Backend) UpdateServiceSpec(ctx context.Context, id string, version uint64, text string) error {
	var d specDoc
	if err := yaml.Unmarshal([]byte(text), &d); err != nil {
		return fmt.Errorf("invalid YAML: %w", err)
	}
	b.mu.Lock()
	s := b.svcByID(id)
	if s == nil {
		b.mu.Unlock()
		return fmt.Errorf("service %q not found", id)
	}
	if img := d.TaskTemplate.ContainerSpec.Image; img != "" && img != s.Image {
		s.prevImage, s.Image, s.HasPrevSpec = s.Image, img, true
		s.UpdateState, s.UpdateMsg = "updating", "update in progress"
		s.updateEnds = b.clock().Add(6 * time.Second)
	}
	n := d.Mode.Replicated.Replicas
	b.mu.Unlock()
	if s.Mode == "replicated" && n != s.Desired {
		return b.ScaleService(ctx, id, n)
	}
	return nil
}

// ---- streams ----

func (b *Backend) Events(ctx context.Context, emit func(api.Event)) error {
	b.mu.Lock()
	id := b.nextSub
	b.nextSub++
	ch := make(chan api.Event, 64)
	b.subs[id] = ch
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.subs, id)
		b.mu.Unlock()
	}()

	var tick <-chan time.Time
	if b.chatter {
		t := time.NewTicker(7 * time.Second)
		defer t.Stop()
		tick = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case e := <-ch:
			emit(e)
		case <-tick:
			b.mu.Lock()
			var running []*task
			for _, t := range b.tasks {
				if t.State == "running" {
					running = append(running, t)
				}
			}
			var e api.Event
			if len(running) > 0 {
				t := running[b.rng.Intn(len(running))]
				e = api.Event{Type: "container", Action: "health_status", ID: t.ContainerID, Name: t.Name, Stack: t.Stack,
					Attrs: map[string]string{"image": t.Image, "health_status": "healthy"}}
			}
			b.mu.Unlock()
			if e.Type != "" {
				e.Time = b.clock()
				emit(e)
			}
		}
	}
}
