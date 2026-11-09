package api

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/api/types/volume"
)

// imageDisplay strips the immutable digest for display ("img@sha256:…" → "img").
func imageDisplay(img string) string {
	if i := strings.IndexByte(img, '@'); i > 0 {
		return img[:i]
	}
	return img
}

func modeOf(m swarm.ServiceMode) string {
	switch {
	case m.Global != nil:
		return "global"
	case m.Replicated != nil:
		return "replicated"
	case m.ReplicatedJob != nil:
		return "replicated-job"
	case m.GlobalJob != nil:
		return "global-job"
	default:
		return "unknown"
	}
}

// formatPorts renders published ports like `docker service ls`:
// "*:8080->80/tcp". Host-mode ports are marked.
func formatPorts(ports []swarm.PortConfig) []string {
	var out []string
	for _, p := range ports {
		if p.PublishedPort == 0 {
			continue
		}
		proto := string(p.Protocol)
		if proto == "" {
			proto = "tcp"
		}
		s := fmt.Sprintf("*:%d->%d/%s", p.PublishedPort, p.TargetPort, proto)
		if p.PublishMode == swarm.PortConfigPublishModeHost {
			s = fmt.Sprintf("host:%d->%d/%s", p.PublishedPort, p.TargetPort, proto)
		}
		out = append(out, s)
	}
	return out
}

func toService(s swarm.Service) Service {
	sv := Service{
		ID:          s.ID,
		Name:        s.Spec.Name,
		Stack:       s.Spec.Labels[StackLabel],
		Mode:        modeOf(s.Spec.Mode),
		Labels:      s.Spec.Labels,
		HasPrevSpec: s.PreviousSpec != nil,
		Created:     s.CreatedAt,
		Updated:     s.UpdatedAt,
	}
	if cs := s.Spec.TaskTemplate.ContainerSpec; cs != nil {
		sv.Image = imageDisplay(cs.Image)
	}
	if pl := s.Spec.TaskTemplate.Placement; pl != nil {
		sv.Constraints = pl.Constraints
	}

	ports := s.Endpoint.Ports
	if len(ports) == 0 && s.Spec.EndpointSpec != nil {
		ports = s.Spec.EndpointSpec.Ports
	}
	sv.Ports = formatPorts(ports)

	switch {
	case s.ServiceStatus != nil:
		sv.Running = int(s.ServiceStatus.RunningTasks)
		sv.Desired = int(s.ServiceStatus.DesiredTasks)
		sv.Completed = int(s.ServiceStatus.CompletedTasks)
	case s.Spec.Mode.Replicated != nil && s.Spec.Mode.Replicated.Replicas != nil:
		sv.Desired = int(*s.Spec.Mode.Replicated.Replicas)
	}
	if j := s.Spec.Mode.ReplicatedJob; j != nil {
		if j.MaxConcurrent != nil {
			sv.Desired = int(*j.MaxConcurrent)
		}
		if j.TotalCompletions != nil {
			sv.JobTotal = int(*j.TotalCompletions)
		}
	}
	if s.UpdateStatus != nil {
		sv.UpdateState = string(s.UpdateStatus.State)
		sv.UpdateMsg = s.UpdateStatus.Message
	}
	return sv
}

// Services implements Backend.
func (a *API) Services(ctx context.Context) ([]Service, error) {
	svcs, err := a.cli.ServiceList(ctx, swarm.ServiceListOptions{Status: true})
	if err != nil {
		return nil, err
	}
	out := make([]Service, 0, len(svcs))
	for _, s := range svcs {
		out = append(out, toService(s))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Tasks implements Backend. Names are derived because the engine API no
// longer returns a task name: <service>.<slot>, or <service>.<node> for
// global services.
func (a *API) Tasks(ctx context.Context, q TaskQuery) ([]Task, error) {
	f := filters.NewArgs()
	if q.ServiceID != "" {
		f.Add("service", q.ServiceID)
	}
	if q.NodeID != "" {
		f.Add("node", q.NodeID)
	}
	tasks, err := a.cli.TaskList(ctx, swarm.TaskListOptions{Filters: f})
	if err != nil {
		return nil, err
	}
	svcs, err := a.cli.ServiceList(ctx, swarm.ServiceListOptions{})
	if err != nil {
		return nil, err
	}
	type svcInfo struct {
		name, stack string
		global      bool
	}
	bySvc := make(map[string]svcInfo, len(svcs))
	for _, s := range svcs {
		bySvc[s.ID] = svcInfo{name: s.Spec.Name, stack: s.Spec.Labels[StackLabel], global: s.Spec.Mode.Global != nil || s.Spec.Mode.GlobalJob != nil}
	}
	nodes := a.nodeNameMap(ctx, 30*time.Second)

	out := make([]Task, 0, len(tasks))
	for _, t := range tasks {
		si := bySvc[t.ServiceID]
		node := nodes[t.NodeID]
		r := Task{
			ID:          t.ID,
			ServiceID:   t.ServiceID,
			ServiceName: si.name,
			Stack:       si.stack,
			NodeID:      t.NodeID,
			Node:        node,
			Slot:        t.Slot,
			Desired:     string(t.DesiredState),
			State:       string(t.Status.State),
			Message:     t.Status.Message,
			Error:       t.Status.Err,
			Created:     t.CreatedAt,
			Updated:     t.Status.Timestamp,
		}
		switch {
		case si.global && node != "":
			r.Name = si.name + "." + node
		case t.Slot > 0:
			r.Name = si.name + "." + strconv.Itoa(t.Slot)
		default:
			r.Name = si.name + "." + ShortID(t.ID)
		}
		if cs := t.Spec.ContainerSpec; cs != nil {
			r.Image = imageDisplay(cs.Image)
		}
		if st := t.Status.ContainerStatus; st != nil {
			r.ContainerID = st.ContainerID
			r.ExitCode = st.ExitCode
		}
		out = append(out, r)
	}

	// Newest first within a name, so a slot's history reads top-down.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Created.After(out[j].Created)
	})
	return out, nil
}

// Nodes implements Backend.
func (a *API) Nodes(ctx context.Context) ([]Node, error) {
	nodes, err := a.cli.NodeList(ctx, swarm.NodeListOptions{})
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(nodes))
	for _, n := range nodes {
		names[n.ID] = n.Description.Hostname
	}
	a.setNodeNames(names)

	// What the scheduler has promised each node: the tasks that are meant to
	// run there and the resources they reserve.
	type load struct {
		tasks int
		cpu   int64
		mem   int64
	}
	loads := map[string]*load{}
	if tasks, err := a.cli.TaskList(ctx, swarm.TaskListOptions{Filters: filters.NewArgs(filters.Arg("desired-state", "running"))}); err == nil {
		for _, t := range tasks {
			l := loads[t.NodeID]
			if l == nil {
				l = &load{}
				loads[t.NodeID] = l
			}
			l.tasks++
			if r := t.Spec.Resources; r != nil && r.Reservations != nil {
				l.cpu += r.Reservations.NanoCPUs
				l.mem += r.Reservations.MemoryBytes
			}
		}
	}

	out := make([]Node, 0, len(nodes))
	for _, n := range nodes {
		r := Node{
			ID:           n.ID,
			Hostname:     n.Description.Hostname,
			Role:         string(n.Spec.Role),
			Availability: string(n.Spec.Availability),
			Status:       string(n.Status.State),
			Addr:         n.Status.Addr,
			Engine:       n.Description.Engine.EngineVersion,
			OS:           n.Description.Platform.OS,
			Arch:         n.Description.Platform.Architecture,
			CPUs:         float64(n.Description.Resources.NanoCPUs) / 1e9,
			Memory:       n.Description.Resources.MemoryBytes,
			Created:      n.CreatedAt,
			Labels:       mergeLabels(n.Spec.Labels, n.Description.Engine.Labels),
		}
		if n.ManagerStatus != nil {
			r.Manager = string(n.ManagerStatus.Reachability)
			if n.ManagerStatus.Leader {
				r.Manager = "leader"
			}
		}
		if l := loads[n.ID]; l != nil {
			r.Tasks = l.tasks
			r.CPUReserved = float64(l.cpu) / 1e9
			r.MemReserved = l.mem
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Hostname < out[j].Hostname })
	return out, nil
}

func mergeLabels(maps ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, m := range maps {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

// Networks implements Backend.
func (a *API) Networks(ctx context.Context) ([]Network, error) {
	nets, err := a.cli.NetworkList(ctx, network.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]Network, 0, len(nets))
	for _, n := range nets {
		r := Network{
			ID:         n.ID,
			Name:       n.Name,
			Stack:      n.Labels[StackLabel],
			Driver:     n.Driver,
			Scope:      n.Scope,
			Internal:   n.Internal,
			Attachable: n.Attachable,
			Ingress:    n.Ingress,
			IPv6:       n.EnableIPv6,
			Labels:     n.Labels,
			Created:    n.Created,
		}
		for _, c := range n.IPAM.Config {
			if c.Subnet != "" {
				r.Subnets = append(r.Subnets, c.Subnet)
			}
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Volumes implements Backend.
func (a *API) Volumes(ctx context.Context) ([]Volume, error) {
	res, err := a.cli.VolumeList(ctx, volume.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]Volume, 0, len(res.Volumes))
	for _, v := range res.Volumes {
		r := Volume{
			Name:       v.Name,
			Stack:      v.Labels[StackLabel],
			Driver:     v.Driver,
			Scope:      v.Scope,
			Mountpoint: v.Mountpoint,
			Labels:     v.Labels,
		}
		if t, err := time.Parse(time.RFC3339, v.CreatedAt); err == nil {
			r.Created = t
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Secrets implements Backend. Secret values are never fetched.
func (a *API) Secrets(ctx context.Context) ([]Secret, error) {
	secrets, err := a.cli.SecretList(ctx, swarm.SecretListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]Secret, 0, len(secrets))
	for _, s := range secrets {
		r := Secret{
			ID:      s.ID,
			Name:    s.Spec.Name,
			Stack:   s.Spec.Labels[StackLabel],
			Labels:  s.Spec.Labels,
			Created: s.CreatedAt,
			Updated: s.UpdatedAt,
		}
		if s.Spec.Driver != nil {
			r.Driver = s.Spec.Driver.Name
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Configs implements Backend.
func (a *API) Configs(ctx context.Context) ([]Config, error) {
	configs, err := a.cli.ConfigList(ctx, swarm.ConfigListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]Config, 0, len(configs))
	for _, c := range configs {
		out = append(out, Config{
			ID:      c.ID,
			Name:    c.Spec.Name,
			Stack:   c.Spec.Labels[StackLabel],
			Labels:  c.Spec.Labels,
			Created: c.CreatedAt,
			Updated: c.UpdatedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Stacks implements Backend. A stack is not an engine object: it is the
// set of objects carrying the stack label. Like `docker stack ls`, a stack
// is listed while it has services; networks, volumes, secrets and configs
// are only counted towards stacks that exist (a volume kept after a stack
// was removed does not resurrect it). Those counts are best effort.
func (a *API) Stacks(ctx context.Context) ([]Stack, error) {
	svcs, err := a.Services(ctx)
	if err != nil {
		return nil, err
	}
	byName := map[string]*Stack{}
	get := func(name string) *Stack {
		s := byName[name]
		if s == nil {
			s = &Stack{Name: name}
			byName[name] = s
		}
		return s
	}
	for _, sv := range svcs {
		if sv.Stack == "" {
			continue
		}
		s := get(sv.Stack)
		s.Services++
		if sv.Mode == "replicated" || sv.Mode == "global" {
			s.Running += sv.Running
			s.Desired += sv.Desired
		}
		if sv.Degraded() {
			s.Degraded++
		}
		if s.Created.IsZero() || (!sv.Created.IsZero() && sv.Created.Before(s.Created)) {
			s.Created = sv.Created
		}
	}
	if nets, err := a.Networks(ctx); err == nil {
		for _, n := range nets {
			if s := byName[n.Stack]; s != nil {
				s.Networks++
			}
		}
	}
	if vols, err := a.Volumes(ctx); err == nil {
		for _, v := range vols {
			if s := byName[v.Stack]; s != nil {
				s.Volumes++
			}
		}
	}
	if secs, err := a.Secrets(ctx); err == nil {
		for _, sc := range secs {
			if s := byName[sc.Stack]; s != nil {
				s.Secrets++
			}
		}
	}
	if cfgs, err := a.Configs(ctx); err == nil {
		for _, c := range cfgs {
			if s := byName[c.Stack]; s != nil {
				s.Configs++
			}
		}
	}
	out := make([]Stack, 0, len(byName))
	for _, s := range byName {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
