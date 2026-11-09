// Package api talks to a Docker swarm through the Docker Engine API and
// presents it to the UI as a small, typed Backend.
//
// Docker 29 removed the old /swarm/watch endpoint. Live updates come from
// the daemon's /events stream (swarm-scope service and node events plus
// container events for task containers), with polling as the fallback.
package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/client"
	"github.com/moby/moby/client/pkg/versions"
)

// API is the Docker Engine implementation of Backend.
type API struct {
	cli      *client.Client
	ctxName  string
	endpoint string

	reconnect backoffPolicy // how the event stream reconnects

	negotiated atomic.Bool // the API version has been agreed with the daemon

	mu          sync.RWMutex
	nodeNames   map[string]string // node id -> hostname
	nodeNamesAt time.Time
}

var _ Backend = (*API)(nil)
var _ Switcher = (*API)(nil)

// ConnectOptions says which daemon to talk to.
type ConnectOptions struct {
	// Context is a docker context name. Empty means: resolve it the way the
	// docker CLI does (DOCKER_HOST, DOCKER_CONTEXT, then the current context
	// of ~/.docker/config.json, then the local socket).
	Context string
	// Host forces an endpoint and bypasses context resolution.
	Host string
}

// Connect builds a client for the requested daemon. It does not contact the
// daemon; use Summary to find out whether it is reachable.
func Connect(opts ConnectOptions) (*API, error) {
	ep, err := resolveEndpoint(opts)
	if err != nil {
		return nil, err
	}
	cli, err := newClient(ep)
	if err != nil {
		return nil, fmt.Errorf("endpoint %s: %w", ep.display(), err)
	}
	a := NewWithClient(cli)
	a.ctxName = ep.name
	a.endpoint = ep.display()
	return a, nil
}

// NewWithClient wraps an existing client (used by tests).
func NewWithClient(cli *client.Client) *API {
	return &API{
		cli:       cli,
		ctxName:   "default",
		endpoint:  cli.DaemonHost(),
		reconnect: defaultBackoff,
		nodeNames: map[string]string{},
	}
}

// endpoint is a resolved daemon address.
type endpoint struct {
	name          string // docker context name
	host          string
	skipTLSVerify bool
	caFile        string
	certFile      string
	keyFile       string
}

// display renders the endpoint for the header, hiding any ssh user name.
func (e endpoint) display() string {
	host := e.host
	if host == "" {
		host = client.DefaultDockerHost
	}
	if i := strings.Index(host, "://"); i > 0 {
		rest := host[i+3:]
		if at := strings.Index(rest, "@"); at >= 0 {
			rest = rest[at+1:]
		}
		return host[:i] + "://" + rest
	}
	return host
}

func resolveEndpoint(opts ConnectOptions) (endpoint, error) {
	if opts.Host != "" {
		return endpoint{name: "default", host: opts.Host}, nil
	}
	name := opts.Context
	if name == "" {
		if os.Getenv("DOCKER_HOST") != "" {
			return defaultEndpoint(), nil
		}
		name = currentContextName()
	}
	if name == "" || name == "default" {
		return defaultEndpoint(), nil
	}
	return contextEndpoint(name)
}

// defaultEndpoint is the "default" docker context: DOCKER_HOST and its TLS
// variables, else the local socket. Asking for that context by name (--context
// default, :ctx default) must land here too, as with the docker CLI.
func defaultEndpoint() endpoint {
	host := os.Getenv("DOCKER_HOST")
	if host == "" {
		host = client.DefaultDockerHost
	}
	ep := endpoint{name: "default", host: host}
	ep.fromEnvTLS()
	return ep
}

// fromEnvTLS applies DOCKER_TLS_VERIFY / DOCKER_CERT_PATH. The CA is always
// used (and an unreadable one is an error, so verification is never quietly
// dropped); the client certificate only if it is there, because a daemon
// that does not ask for one is a normal setup.
func (e *endpoint) fromEnvTLS() {
	if os.Getenv("DOCKER_TLS_VERIFY") == "" {
		return
	}
	dir := os.Getenv("DOCKER_CERT_PATH")
	if dir == "" {
		dir = dockerConfigDir()
	}
	e.caFile = filepath.Join(dir, "ca.pem")
	cert, key := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if fileExists(cert) && fileExists(key) {
		e.certFile, e.keyFile = cert, key
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// newClient builds the Engine API client. It negotiates the API version with
// the daemon on its first request, which is the default of this client.
func newClient(ep endpoint) (*client.Client, error) {
	var opts []client.Opt

	switch {
	case strings.HasPrefix(ep.host, "ssh://"):
		spec, err := parseSSHURL(ep.host)
		if err != nil {
			return nil, err
		}
		// Same trick as the docker CLI's connection helper: the host is a
		// placeholder, the dialer does the real work.
		opts = append(opts, client.WithHost("http://docker.example.com"), client.WithDialContext(sshDialer(spec)))

	case ep.caFile != "" || ep.certFile != "" || ep.skipTLSVerify:
		cfg, err := tlsConfig(ep)
		if err != nil {
			return nil, err
		}
		hc := &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}}
		opts = append(opts, client.WithHTTPClient(hc), client.WithHost(ep.host), client.WithScheme("https"))

	default:
		host := ep.host
		if strings.HasPrefix(host, "tcp://") {
			// Plain TCP unless TLS material was requested above.
			opts = append(opts, client.WithScheme("http"))
		}
		opts = append(opts, client.WithHost(host))
	}
	return client.New(opts...)
}

func tlsConfig(ep endpoint) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: ep.skipTLSVerify} //nolint:gosec // opt-in, mirrors the docker CLI flag
	if ep.caFile != "" {
		pem, err := os.ReadFile(ep.caFile)
		if err != nil {
			return nil, fmt.Errorf("reading CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no certificates found in %s", ep.caFile)
		}
		cfg.RootCAs = pool
	}
	if ep.certFile != "" && ep.keyFile != "" {
		cert, err := tls.LoadX509KeyPair(ep.certFile, ep.keyFile)
		if err != nil {
			return nil, fmt.Errorf("loading client certificate: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

// Close implements Backend: it lets go of the idle connections (and, for an
// ssh:// endpoint, of the ssh processes behind them).
func (a *API) Close() error { return a.cli.Close() }

// ContextName implements Backend.
func (a *API) ContextName() string { return a.ctxName }

// Endpoint implements Backend.
func (a *API) Endpoint() string { return a.endpoint }

// The oldest daemon swarmlens works with. The running count of a service, which
// the services view and the header show, comes from the service status that the
// engine reports from API 1.41, Docker Engine 20.10; the client itself goes down
// to API 1.40. The README says the same.
const (
	minAPIVersion   = "1.41"
	minDockerEngine = "20.10"
)

// negotiate agrees an API version with the daemon before the first request and
// refuses a daemon that is older than swarmlens works with. The client
// negotiates by itself, but for a daemon older than it supports it keeps quiet
// and carries on with its newest version, which that daemon rejects with an
// error that does not say why. Any other trouble with the ping is left to the
// request that follows, which reports it as it always has.
func (a *API) negotiate(ctx context.Context) error {
	if a.negotiated.Load() {
		return nil
	}
	res, err := a.cli.Ping(ctx, client.PingOptions{NegotiateAPIVersion: true})
	if res.APIVersion != "" && versions.LessThan(res.APIVersion, minAPIVersion) {
		return fmt.Errorf("swarmlens needs Docker Engine %s or newer: this daemon speaks API %s, the minimum is %s", minDockerEngine, res.APIVersion, minAPIVersion)
	}
	if err == nil {
		a.negotiated.Store(true)
	}
	return nil
}

// Summary implements Backend.
func (a *API) Summary(ctx context.Context) (*Summary, error) {
	if err := a.negotiate(ctx); err != nil {
		return nil, err
	}
	res, err := a.cli.Info(ctx, client.InfoOptions{})
	if err != nil {
		return nil, err
	}
	info := res.Info
	s := &Summary{
		Context:       a.ctxName,
		Endpoint:      a.endpoint,
		SwarmState:    string(info.Swarm.LocalNodeState),
		NodeID:        info.Swarm.NodeID,
		NodeName:      info.Name,
		ServerVersion: info.ServerVersion,
		Nodes:         info.Swarm.Nodes,
		Managers:      info.Swarm.Managers,
	}
	if v, err := a.cli.ServerVersion(ctx, client.ServerVersionOptions{}); err == nil {
		s.APIVersion = v.APIVersion
	}
	if info.Swarm.LocalNodeState != swarm.LocalNodeStateActive {
		return s, nil
	}
	if info.Swarm.ControlAvailable {
		s.Role = string(RoleManager)
	} else {
		s.Role = string(RoleWorker)
		return s, nil
	}
	if c := info.Swarm.Cluster; c != nil {
		s.ClusterID = c.ID
		s.Cluster = c.Spec.Name
	}

	// Cluster-wide health numbers for the header. Failures here are not
	// fatal: the header just shows what it has.
	if nodes, err := a.cli.NodeList(ctx, client.NodeListOptions{}); err == nil {
		s.Nodes, s.Managers = len(nodes.Items), 0
		for _, n := range nodes.Items {
			if n.Status.State == swarm.NodeStateReady {
				s.NodesReady++
			}
			if n.ManagerStatus != nil {
				s.Managers++
				if n.ManagerStatus.Reachability == swarm.ReachabilityReachable {
					s.ManagersReachable++
				}
				if n.ID == s.NodeID && n.ManagerStatus.Leader {
					s.Leader = true
				}
			}
		}
	}
	if svcs, err := a.cli.ServiceList(ctx, client.ServiceListOptions{Status: true}); err == nil {
		s.Services = len(svcs.Items)
		stacks := map[string]bool{}
		for _, raw := range svcs.Items {
			sv := toService(raw)
			if sv.Stack != "" {
				stacks[sv.Stack] = true
			}
			if sv.Mode == "replicated" || sv.Mode == "global" {
				s.TasksRunning += sv.Running
				s.TasksDesired += sv.Desired
			}
			if sv.Degraded() {
				s.ServicesDegraded++
			}
		}
		for name := range stacks {
			s.Stacks = append(s.Stacks, name)
		}
		sort.Strings(s.Stacks)
	}
	return s, nil
}

// Degraded reports whether the service is not converged: fewer running
// tasks than desired, or an update that stopped.
func (s Service) Degraded() bool {
	switch s.UpdateState {
	case "paused", "rollback_paused":
		return true
	}
	if s.Mode == "replicated" || s.Mode == "global" {
		return s.Running < s.Desired
	}
	return false
}

// nodeNameMap returns node id -> hostname, using a short-lived cache to
// avoid a NodeList round trip on every refresh.
func (a *API) nodeNameMap(ctx context.Context, maxAge time.Duration) map[string]string {
	a.mu.RLock()
	if time.Since(a.nodeNamesAt) < maxAge && len(a.nodeNames) > 0 {
		m := copyMap(a.nodeNames)
		a.mu.RUnlock()
		return m
	}
	a.mu.RUnlock()

	res, err := a.cli.NodeList(ctx, client.NodeListOptions{})
	if err != nil {
		// A stale cache beats failing the whole call.
		a.mu.RLock()
		defer a.mu.RUnlock()
		return copyMap(a.nodeNames)
	}
	m := make(map[string]string, len(res.Items))
	for _, n := range res.Items {
		m[n.ID] = n.Description.Hostname
	}
	a.setNodeNames(m)
	return m
}

func (a *API) setNodeNames(m map[string]string) {
	a.mu.Lock()
	a.nodeNames = m
	a.nodeNamesAt = time.Now()
	a.mu.Unlock()
}

func copyMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// ShortID truncates a swarm/docker id for display.
func ShortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
