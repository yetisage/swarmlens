package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/swarm"
)

// ScaleService implements Backend. Only the replica count changes; the rest
// of the spec is round-tripped untouched.
func (a *API) ScaleService(ctx context.Context, id string, replicas int) error {
	svc, _, err := a.cli.ServiceInspectWithRaw(ctx, id, swarm.ServiceInspectOptions{})
	if err != nil {
		return err
	}
	if svc.Spec.Mode.Replicated == nil {
		return fmt.Errorf("service %q is not replicated", svc.Spec.Name)
	}
	if replicas < 0 {
		replicas = 0
	}
	n := uint64(replicas)
	svc.Spec.Mode.Replicated.Replicas = &n
	_, err = a.cli.ServiceUpdate(ctx, svc.ID, svc.Version, svc.Spec, swarm.ServiceUpdateOptions{})
	return err
}

// RestartService implements Backend: a forced rolling update of every task,
// like `docker service update --force`.
func (a *API) RestartService(ctx context.Context, id string) error {
	svc, _, err := a.cli.ServiceInspectWithRaw(ctx, id, swarm.ServiceInspectOptions{})
	if err != nil {
		return err
	}
	svc.Spec.TaskTemplate.ForceUpdate++
	_, err = a.cli.ServiceUpdate(ctx, svc.ID, svc.Version, svc.Spec, swarm.ServiceUpdateOptions{})
	return err
}

// RollbackService implements Backend, like `docker service rollback`: the
// engine swaps in the previous spec using the rollback configuration.
func (a *API) RollbackService(ctx context.Context, id string) error {
	svc, _, err := a.cli.ServiceInspectWithRaw(ctx, id, swarm.ServiceInspectOptions{})
	if err != nil {
		return err
	}
	if svc.PreviousSpec == nil {
		return fmt.Errorf("service %q has no previous spec to roll back to", svc.Spec.Name)
	}
	_, err = a.cli.ServiceUpdate(ctx, svc.ID, svc.Version, svc.Spec, swarm.ServiceUpdateOptions{Rollback: "previous"})
	return err
}

// SetNodeAvailability implements Backend.
func (a *API) SetNodeAvailability(ctx context.Context, id string, avail NodeAvailability) error {
	node, _, err := a.cli.NodeInspectWithRaw(ctx, id)
	if err != nil {
		return err
	}
	node.Spec.Availability = swarm.NodeAvailability(avail)
	return a.cli.NodeUpdate(ctx, node.ID, node.Version, node.Spec)
}

// SetNodeRole implements Backend.
func (a *API) SetNodeRole(ctx context.Context, id string, role NodeRole) error {
	node, _, err := a.cli.NodeInspectWithRaw(ctx, id)
	if err != nil {
		return err
	}
	node.Spec.Role = swarm.NodeRole(role)
	return a.cli.NodeUpdate(ctx, node.ID, node.Version, node.Spec)
}

// Remove implements Backend.
func (a *API) Remove(ctx context.Context, kind Kind, id string) error {
	switch kind {
	case KindService:
		return a.cli.ServiceRemove(ctx, id)
	case KindNode:
		return a.cli.NodeRemove(ctx, id, swarm.NodeRemoveOptions{Force: true})
	case KindNetwork:
		return a.cli.NetworkRemove(ctx, id)
	case KindVolume:
		return a.cli.VolumeRemove(ctx, id, false)
	case KindSecret:
		return a.cli.SecretRemove(ctx, id)
	case KindConfig:
		return a.cli.ConfigRemove(ctx, id)
	case KindStack:
		return a.RemoveStack(ctx, id)
	}
	return fmt.Errorf("cannot remove a %s", kind)
}

// RemoveStack implements Backend, like `docker stack rm`: the stack's
// services go first, then its networks, secrets and configs. Volumes are
// kept. Networks stay busy until the tasks have stopped, so they are
// retried for a few seconds. Everything is listed before anything is
// removed, so a listing that fails (a permission, say) leaves the stack
// whole instead of half removed, and an object that is already gone counts
// as removed: the objects of a stack disappear on their own as its tasks
// stop.
func (a *API) RemoveStack(ctx context.Context, name string) error {
	f := filters.NewArgs(filters.Arg("label", StackLabel+"="+name))

	svcs, err := a.cli.ServiceList(ctx, swarm.ServiceListOptions{Filters: f})
	if err != nil {
		return err
	}
	nets, err := a.cli.NetworkList(ctx, network.ListOptions{Filters: f})
	if err != nil {
		return err
	}
	secrets, err := a.cli.SecretList(ctx, swarm.SecretListOptions{Filters: f})
	if err != nil {
		return err
	}
	configs, err := a.cli.ConfigList(ctx, swarm.ConfigListOptions{Filters: f})
	if err != nil {
		return err
	}
	if len(svcs)+len(nets)+len(secrets)+len(configs) == 0 {
		return fmt.Errorf("stack %q not found", name)
	}

	var errs []error
	failed := func(what, name string, err error) {
		if err != nil && !cerrdefs.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("%s %s: %w", what, name, err))
		}
	}
	for _, s := range svcs {
		failed("service", s.Spec.Name, a.cli.ServiceRemove(ctx, s.ID))
	}
	pending := nets
	deadline := time.Now().Add(10 * time.Second)
	for len(pending) > 0 {
		var busy []string
		var left []network.Summary
		var lastErr error
		for _, n := range pending {
			if err := a.cli.NetworkRemove(ctx, n.ID); err != nil && !cerrdefs.IsNotFound(err) {
				left = append(left, n)
				busy = append(busy, n.Name)
				lastErr = err
			}
		}
		pending = left
		if len(pending) == 0 {
			break
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			errs = append(errs, fmt.Errorf("network %s: %w", strings.Join(busy, ", "), lastErr))
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(500 * time.Millisecond):
		}
	}
	for _, s := range secrets {
		failed("secret", s.Spec.Name, a.cli.SecretRemove(ctx, s.ID))
	}
	for _, c := range configs {
		failed("config", c.Spec.Name, a.cli.ConfigRemove(ctx, c.ID))
	}
	return errors.Join(errs...)
}

// ServiceSpec implements Backend: the service spec as YAML plus the version
// it was read at, so a later update can detect concurrent changes.
func (a *API) ServiceSpec(ctx context.Context, id string) (string, uint64, error) {
	_, raw, err := a.cli.ServiceInspectWithRaw(ctx, id, swarm.ServiceInspectOptions{})
	if err != nil {
		return "", 0, err
	}
	var env struct {
		Version swarm.Version
		Spec    json.RawMessage
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", 0, err
	}
	y, err := JSONToYAML(env.Spec)
	return y, env.Version.Index, err
}

// UpdateServiceSpec implements Backend. Unknown fields are rejected so a
// typo in the editor does not silently do nothing.
func (a *API) UpdateServiceSpec(ctx context.Context, id string, version uint64, yamlText string) error {
	raw, err := yamlToJSON(yamlText)
	if err != nil {
		return fmt.Errorf("invalid YAML: %w", err)
	}
	var spec swarm.ServiceSpec
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&spec); err != nil {
		return fmt.Errorf("invalid service spec: %w", err)
	}
	_, err = a.cli.ServiceUpdate(ctx, id, swarm.Version{Index: version}, spec, swarm.ServiceUpdateOptions{})
	return err
}

// CreateService implements ServiceCreator: a replicated service, for the
// --smoke-mutate self-test.
func (a *API) CreateService(ctx context.Context, name, image string, replicas int) error {
	n := uint64(replicas)
	spec := swarm.ServiceSpec{
		Annotations: swarm.Annotations{
			Name:   name,
			Labels: map[string]string{"swarmlens.managed": "true"},
		},
		TaskTemplate: swarm.TaskSpec{ContainerSpec: &swarm.ContainerSpec{Image: image}},
		Mode:         swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: &n}},
	}
	_, err := a.cli.ServiceCreate(ctx, spec, swarm.ServiceCreateOptions{})
	return err
}
