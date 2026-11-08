// Package api wraps the Docker Engine API for swarm management.
//
// It uses the official typed Docker client for services, nodes, tasks,
// logs, resources and the daemon's /events stream. Docker 29 removed the
// old /swarm/watch endpoint; the /events stream (swarm-scope service
// events plus container lifecycle events for service task containers) is
// the live-update source, with polling as the fallback.
package api

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
)

// API is a small facade over the Docker Engine API.
type API struct {
	cli *client.Client

	mu              sync.RWMutex
	nodeNames       map[string]string // node id -> hostname
	nodeNamesAt     time.Time
	swarmContainers map[string]bool // container ids known to be swarm task containers
}

// New connects to the Docker daemon using DOCKER_HOST (default
// unix:///var/run/docker.sock). TLS (DOCKER_TLS_VERIFY + DOCKER_CERT_PATH)
// and ssh:// endpoints are supported.
func New() (*API, error) {
	opts := []client.Opt{
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	}

	host := os.Getenv("DOCKER_HOST")
	switch {
	case strings.HasPrefix(host, "ssh://"):
		dial, err := sshDialer(host)
		if err != nil {
			return nil, fmt.Errorf("ssh endpoint %q: %w", host, err)
		}
		opts = append(opts, client.WithDialContext(dial))
	case strings.HasPrefix(host, "tcp://"), strings.HasPrefix(host, "http://"):
		// Plain TCP: no TLS material requested.
		if os.Getenv("DOCKER_TLS_VERIFY") == "" {
			opts = append(opts, client.WithScheme("http"))
		}
	}

	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("dialing docker daemon: %w", err)
	}
	return &API{
		cli:             cli,
		nodeNames:       map[string]string{},
		swarmContainers: map[string]bool{},
	}, nil
}

// Endpoint returns a short description of the daemon endpoint in use, for
// display in the UI header.
func (a *API) Endpoint() string { return endpointSummary() }

// NewWithClient wraps an existing client (used by tests).
func NewWithClient(cli *client.Client) *API {
	return &API{
		cli:             cli,
		nodeNames:       map[string]string{},
		swarmContainers: map[string]bool{},
	}
}

// SwarmState describes the local daemon's participation in a swarm.
type SwarmState struct {
	State     string // inactive | active | pending | error | locked
	Role      string // manager | worker | ""
	NodeID    string
	SwarmName string
	Nodes     int
	Managers  int
	Version   string
}

// SwarmState queries the daemon for its swarm participation.
func (a *API) SwarmState(ctx context.Context) (*SwarmState, error) {
	info, err := a.cli.Info(ctx)
	if err != nil {
		return nil, err
	}
	st := &SwarmState{
		State:    string(info.Swarm.LocalNodeState),
		NodeID:   info.Swarm.NodeID,
		Nodes:    info.Swarm.Nodes,
		Managers: info.Swarm.Managers,
	}
	if v, err := a.cli.ServerVersion(ctx); err == nil {
		st.Version = v.Version
	}
	if info.Swarm.LocalNodeState == swarm.LocalNodeStateActive {
		if info.Swarm.ControlAvailable {
			st.Role = "manager"
		} else {
			st.Role = "worker"
		}
		if s, err := a.cli.SwarmInspect(ctx); err == nil {
			st.SwarmName = s.Spec.Name
		}
	}
	return st, nil
}

// nodeNameMap returns node id -> hostname, using a short-lived cache to
// avoid a NodeList round trip on every refresh.
func (a *API) nodeNameMap(ctx context.Context, maxAge time.Duration) map[string]string {
	a.mu.RLock()
	if time.Since(a.nodeNamesAt) < maxAge && len(a.nodeNames) > 0 {
		m := make(map[string]string, len(a.nodeNames))
		for k, v := range a.nodeNames {
			m[k] = v
		}
		a.mu.RUnlock()
		return m
	}
	a.mu.RUnlock()

	nodes, err := a.cli.NodeList(ctx, swarm.NodeListOptions{})
	if err != nil {
		// Fall back to the stale cache rather than failing the whole call.
		a.mu.RLock()
		defer a.mu.RUnlock()
		m := make(map[string]string, len(a.nodeNames))
		for k, v := range a.nodeNames {
			m[k] = v
		}
		return m
	}
	m := make(map[string]string, len(nodes))
	for _, n := range nodes {
		m[n.ID] = n.Description.Hostname
	}
	a.mu.Lock()
	a.nodeNames = m
	a.nodeNamesAt = time.Now()
	a.mu.Unlock()
	return m
}

// trackContainers records container ids that belong to swarm tasks so the
// event stream can ignore unrelated host containers.
func (a *API) trackContainers(ids []string) {
	a.mu.Lock()
	for _, id := range ids {
		if id != "" {
			a.swarmContainers[id] = true
		}
	}
	a.mu.Unlock()
}

// isSwarmContainer reports whether a container id is known to be a swarm
// task container.
func (a *API) isSwarmContainer(id string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.swarmContainers[id]
}
