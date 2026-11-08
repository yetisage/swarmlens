package api

import (
	"context"
	"time"
)

// StackLabel is the label `docker stack deploy` puts on every object it
// creates. Swarmlens treats a stack the way k9s treats a namespace.
const StackLabel = "com.docker.stack.namespace"

// Kind names a swarm resource type.
type Kind string

// Resource kinds.
const (
	KindService Kind = "service"
	KindTask    Kind = "task"
	KindNode    Kind = "node"
	KindStack   Kind = "stack"
	KindNetwork Kind = "network"
	KindVolume  Kind = "volume"
	KindSecret  Kind = "secret"
	KindConfig  Kind = "config"
	KindEvent   Kind = "event"
)

// NodeAvailability is the scheduling availability of a node.
type NodeAvailability string

// Node availabilities.
const (
	AvailActive NodeAvailability = "active"
	AvailPause  NodeAvailability = "pause"
	AvailDrain  NodeAvailability = "drain"
)

// NodeRole is the role of a node in the swarm.
type NodeRole string

// Node roles.
const (
	RoleManager NodeRole = "manager"
	RoleWorker  NodeRole = "worker"
)

// Summary is the cluster-wide information shown in the header.
type Summary struct {
	Context  string // docker context name
	Endpoint string // daemon endpoint, e.g. unix:///var/run/docker.sock

	SwarmState string // inactive | pending | active | error | locked
	Role       string // manager | worker | "" (not in a swarm)
	Leader     bool   // the connected node is the raft leader
	NodeID     string
	NodeName   string
	ClusterID  string
	Cluster    string // swarm name

	ServerVersion string
	APIVersion    string

	Nodes             int
	NodesReady        int
	Managers          int
	ManagersReachable int

	Services         int
	ServicesDegraded int
	TasksRunning     int
	TasksDesired     int
	Stacks           []string // names of the stacks that have services, sorted
}

// InSwarm reports whether the connected daemon is a manager of an active
// swarm, i.e. whether the management API is usable.
func (s *Summary) InSwarm() bool {
	return s != nil && s.SwarmState == "active" && s.Role == "manager"
}

// Service is one swarm service.
type Service struct {
	ID    string
	Name  string
	Stack string

	Mode      string // replicated | global | replicated-job | global-job
	Running   int
	Desired   int
	Completed int // finished tasks of a job
	JobTotal  int // total completions a replicated job is waiting for

	Image       string // without digest
	Ports       []string
	Constraints []string
	Labels      map[string]string

	UpdateState string // "" | updating | paused | completed | rollback_started | rollback_paused | rollback_completed
	UpdateMsg   string
	HasPrevSpec bool

	Created time.Time
	Updated time.Time
}

// Replicated reports whether the replica count can be changed.
func (s Service) Replicated() bool { return s.Mode == "replicated" }

// Task is one task (an instance of a service on a node).
type Task struct {
	ID          string
	Name        string // service.slot (replicated) or service.node (global)
	ServiceID   string
	ServiceName string
	Stack       string
	NodeID      string
	Node        string
	Slot        int
	Image       string
	Desired     string // desired state
	State       string // current state
	Message     string
	Error       string
	ContainerID string
	ExitCode    int
	Created     time.Time
	Updated     time.Time // last state change
}

// TaskQuery narrows a task listing.
type TaskQuery struct {
	ServiceID string
	NodeID    string
}

// Node is one swarm node.
type Node struct {
	ID           string
	Hostname     string
	Role         string // manager | worker
	Availability string // active | pause | drain
	Status       string // ready | down | unknown | disconnected
	Manager      string // leader | reachable | unreachable | ""
	Addr         string
	Engine       string
	OS           string
	Arch         string
	CPUs         float64 // capacity, in cores
	Memory       int64   // capacity, in bytes
	Tasks        int     // tasks desired to run on the node
	CPUReserved  float64 // cores reserved by those tasks
	MemReserved  int64   // bytes reserved by those tasks
	Labels       map[string]string
	Created      time.Time
}

// Stack groups the objects deployed by one `docker stack deploy`.
type Stack struct {
	Name     string
	Services int
	Running  int
	Desired  int
	Networks int
	Volumes  int
	Secrets  int
	Configs  int
	Degraded int // services that are not converged
	Created  time.Time
}

// Network is one engine network.
type Network struct {
	ID         string
	Name       string
	Stack      string
	Driver     string
	Scope      string
	Internal   bool
	Attachable bool
	Ingress    bool
	IPv6       bool
	Subnets    []string
	Labels     map[string]string
	Created    time.Time
}

// Volume is one engine volume.
type Volume struct {
	Name       string
	Stack      string
	Driver     string
	Scope      string
	Mountpoint string
	Labels     map[string]string
	Created    time.Time
}

// Secret is the metadata of one swarm secret. Values are never fetched.
type Secret struct {
	ID      string
	Name    string
	Stack   string
	Driver  string
	Labels  map[string]string
	Created time.Time
	Updated time.Time
}

// Config is one swarm config.
type Config struct {
	ID      string
	Name    string
	Stack   string
	Labels  map[string]string
	Created time.Time
	Updated time.Time
}

// Event is one daemon event.
type Event struct {
	Time   time.Time
	Type   string // service | node | container | network | volume | secret | config | error
	Action string
	ID     string
	Name   string
	Stack  string
	Attrs  map[string]string
}

// ContextInfo describes one docker context.
type ContextInfo struct {
	Name        string
	Description string
	Endpoint    string
	Current     bool
}

// LogTarget names what to read logs from.
type LogTarget struct {
	Kind Kind // service | task
	ID   string
	Name string
}

// LogOptions controls a log read.
type LogOptions struct {
	Tail   int           // number of trailing lines; 0 means all
	Since  time.Duration // only lines newer than this; 0 means no limit
	Follow bool
}

// LogLine is one parsed log line.
type LogLine struct {
	Time   time.Time
	Source string // task name, e.g. web.2
	Node   string
	Stderr bool
	Text   string
}

// Backend is everything the UI needs from a swarm. The real implementation
// talks to the Docker Engine API; the demo implementation keeps everything
// in memory.
type Backend interface {
	// Identity.
	ContextName() string
	Endpoint() string
	Summary(ctx context.Context) (*Summary, error)

	// Listings.
	Services(ctx context.Context) ([]Service, error)
	Tasks(ctx context.Context, q TaskQuery) ([]Task, error)
	Nodes(ctx context.Context) ([]Node, error)
	Stacks(ctx context.Context) ([]Stack, error)
	Networks(ctx context.Context) ([]Network, error)
	Volumes(ctx context.Context) ([]Volume, error)
	Secrets(ctx context.Context) ([]Secret, error)
	Configs(ctx context.Context) ([]Config, error)

	// Inspection.
	Describe(ctx context.Context, kind Kind, id string) (string, error)
	YAML(ctx context.Context, kind Kind, id string) (string, error)

	// Streams. Both return when ctx is cancelled.
	Logs(ctx context.Context, t LogTarget, o LogOptions, emit func([]LogLine)) error
	Events(ctx context.Context, emit func(Event)) error

	// Mutations.
	ScaleService(ctx context.Context, id string, replicas int) error
	RestartService(ctx context.Context, id string) error
	RollbackService(ctx context.Context, id string) error
	SetNodeAvailability(ctx context.Context, id string, a NodeAvailability) error
	SetNodeRole(ctx context.Context, id string, r NodeRole) error
	Remove(ctx context.Context, kind Kind, id string) error
	RemoveStack(ctx context.Context, name string) error

	// Spec editing: the YAML of a service spec and its version, and the
	// reverse operation.
	ServiceSpec(ctx context.Context, id string) (yaml string, version uint64, err error)
	UpdateServiceSpec(ctx context.Context, id string, version uint64, yaml string) error
}

// Switcher is implemented by backends that can list and switch docker
// contexts.
type Switcher interface {
	Contexts() ([]ContextInfo, error)
}

// Simulated is implemented by backends that are not a real daemon, so the
// UI can refuse things that need one (shells into containers).
type Simulated interface{ Simulated() bool }
