package api

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/swarm"
)

// ServiceRow is one row of the services table.
type ServiceRow struct {
	ID       string // full service id (used for API calls)
	ShortID  string // truncated id for display
	Name     string
	Image    string
	Mode     string // replicated | global | replicated-job | global-job
	Replicas string // "running/desired", or "global"
	Desired  int
	Running  int
	Updated  string // update state, e.g. "none", "updating (since …)"
	HasPrev  bool   // a previous spec is available for rollback
}

// Services lists all services with live running/desired task counts.
func (a *API) Services(ctx context.Context) ([]ServiceRow, error) {
	svcs, err := a.cli.ServiceList(ctx, swarm.ServiceListOptions{Status: true})
	if err != nil {
		return nil, err
	}
	rows := make([]ServiceRow, 0, len(svcs))
	for _, s := range svcs {
		r := ServiceRow{
			ID:      s.ID,
			ShortID: ShortID(s.ID),
			Name:    s.Spec.Name,
			Mode:    modeString(s),
			HasPrev: s.PreviousSpec != nil,
		}
		if cs := s.Spec.TaskTemplate.ContainerSpec; cs != nil {
			r.Image = imageDisplay(cs.Image)
		}
		switch {
		case s.Spec.Mode.Global != nil:
			r.Replicas = "global"
		case s.ServiceStatus != nil:
			r.Running = int(s.ServiceStatus.RunningTasks)
			r.Desired = int(s.ServiceStatus.DesiredTasks)
			r.Replicas = fmt.Sprintf("%d/%d", r.Running, r.Desired)
		case s.Spec.Mode.Replicated != nil && s.Spec.Mode.Replicated.Replicas != nil:
			r.Desired = int(*s.Spec.Mode.Replicated.Replicas)
			r.Replicas = fmt.Sprintf("0/%d", r.Desired)
		}
		r.Updated = updatedString(s)
		rows = append(rows, r)
	}
	return rows, nil
}

// imageDisplay strips the immutable digest for display ("img@sha256:…" → "img").
func imageDisplay(img string) string {
	if i := strings.IndexByte(img, '@'); i > 0 {
		return img[:i]
	}
	return img
}

func modeString(s swarm.Service) string {
	switch {
	case s.Spec.Mode.Global != nil:
		return "global"
	case s.Spec.Mode.Replicated != nil:
		return "replicated"
	case s.Spec.Mode.ReplicatedJob != nil:
		return "replicated-job"
	case s.Spec.Mode.GlobalJob != nil:
		return "global-job"
	default:
		return "unknown"
	}
}

func updatedString(s swarm.Service) string {
	if s.UpdateStatus == nil {
		return "none"
	}
	st := string(s.UpdateStatus.State)
	if s.UpdateStatus.CompletedAt != nil {
		st += " (done " + s.UpdateStatus.CompletedAt.Local().Format("01/02 15:04") + ")"
	} else if s.UpdateStatus.StartedAt != nil {
		st += " (since " + s.UpdateStatus.StartedAt.Local().Format("15:04:05") + ")"
	}
	return st
}

// NodeRow is one row of the nodes table.
type NodeRow struct {
	ID           string
	ShortID      string
	Hostname     string
	Role         string // manager | worker
	Availability string // active | drain | pause
	Status       string // ready | down | not ready
	Engine       string
	Platform     string
	Addr         string
}

// Nodes lists all swarm nodes.
func (a *API) Nodes(ctx context.Context) ([]NodeRow, error) {
	nodes, err := a.cli.NodeList(ctx, swarm.NodeListOptions{})
	if err != nil {
		return nil, err
	}
	rows := make([]NodeRow, 0, len(nodes))
	for _, n := range nodes {
		rows = append(rows, NodeRow{
			ID:           n.ID,
			ShortID:      ShortID(n.ID),
			Hostname:     n.Description.Hostname,
			Role:         string(n.Spec.Role),
			Availability: string(n.Spec.Availability),
			Status:       string(n.Status.State),
			Engine:       n.Description.Engine.EngineVersion,
			Platform:     n.Description.Platform.Architecture,
			Addr:         n.Status.Addr,
		})
	}
	// Refresh the node cache while we are here.
	m := make(map[string]string, len(nodes))
	for _, n := range nodes {
		m[n.ID] = n.Description.Hostname
	}
	a.mu.Lock()
	a.nodeNames = m
	a.nodeNamesAt = time.Now()
	a.mu.Unlock()
	return rows, nil
}

// TaskRow is one row of the service detail table.
type TaskRow struct {
	ID          string // full task id (used for API calls)
	ShortID     string
	Name        string
	Node        string
	NodeID      string
	Desired     string // desired state, e.g. running
	Current     string // actual state, e.g. running, failed
	ContainerID string
	Since       string // last state change, hh:mm:ss
	Slot        int
}

// Tasks lists the tasks of one service, resolving node hostnames. The
// display name is derived as <service>.<slot>, since the engine API no
// longer returns a task name field.
func (a *API) Tasks(ctx context.Context, serviceID, serviceName string) ([]TaskRow, error) {
	tasks, err := a.cli.TaskList(ctx, swarm.TaskListOptions{
		Filters: filters.NewArgs(filters.Arg("service", serviceID)),
	})
	if err != nil {
		return nil, err
	}
	names := a.nodeNameMap(ctx, 30*time.Second)

	containerIDs := make([]string, 0, len(tasks))
	rows := make([]TaskRow, 0, len(tasks))
	for _, t := range tasks {
		r := TaskRow{
			ID:      t.ID,
			ShortID: ShortID(t.ID),
			Name:    serviceName + "." + strconv.Itoa(t.Slot),
			Node:    names[t.NodeID],
			NodeID:  t.NodeID,
			Desired: string(t.DesiredState),
			Current: string(t.Status.State),
			Slot:    t.Slot,
		}
		if t.Status.ContainerStatus != nil && t.Status.ContainerStatus.ContainerID != "" {
			r.ContainerID = t.Status.ContainerStatus.ContainerID
			containerIDs = append(containerIDs, r.ContainerID)
		}
		if !t.Status.Timestamp.IsZero() {
			r.Since = t.Status.Timestamp.Local().Format("15:04:05")
		}
		rows = append(rows, r)
	}
	a.trackContainers(containerIDs)
	return rows, nil
}

// ShortID truncates a swarm/docker id for display.
func ShortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
