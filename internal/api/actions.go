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
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/client"
)

// ScaleService implements Backend. Only the replica count changes; the rest
// of the spec is round-tripped untouched.
func (a *API) ScaleService(ctx context.Context, id string, replicas int) error {
	res, err := a.cli.ServiceInspect(ctx, id, client.ServiceInspectOptions{})
	if err != nil {
		return err
	}
	svc := res.Service
	if svc.Spec.Mode.Replicated == nil {
		return fmt.Errorf("service %q is not replicated", svc.Spec.Name)
	}
	if replicas < 0 {
		replicas = 0
	}
	n := uint64(replicas)
	svc.Spec.Mode.Replicated.Replicas = &n
	_, err = a.cli.ServiceUpdate(ctx, svc.ID, client.ServiceUpdateOptions{Version: svc.Version, Spec: svc.Spec})
	return err
}

// RestartService implements Backend: a forced rolling update of every task,
// like `docker service update --force`.
func (a *API) RestartService(ctx context.Context, id string) error {
	res, err := a.cli.ServiceInspect(ctx, id, client.ServiceInspectOptions{})
	if err != nil {
		return err
	}
	svc := res.Service
	svc.Spec.TaskTemplate.ForceUpdate++
	_, err = a.cli.ServiceUpdate(ctx, svc.ID, client.ServiceUpdateOptions{Version: svc.Version, Spec: svc.Spec})
	return err
}

// RollbackService implements Backend, like `docker service rollback`: the
// engine swaps in the previous spec using the rollback configuration.
func (a *API) RollbackService(ctx context.Context, id string) error {
	res, err := a.cli.ServiceInspect(ctx, id, client.ServiceInspectOptions{})
	if err != nil {
		return err
	}
	svc := res.Service
	if svc.PreviousSpec == nil {
		return fmt.Errorf("service %q has no previous spec to roll back to", svc.Spec.Name)
	}
	_, err = a.cli.ServiceUpdate(ctx, svc.ID, client.ServiceUpdateOptions{Version: svc.Version, Spec: svc.Spec, Rollback: "previous"})
	return err
}

// SetNodeAvailability implements Backend.
func (a *API) SetNodeAvailability(ctx context.Context, id string, avail NodeAvailability) error {
	res, err := a.cli.NodeInspect(ctx, id, client.NodeInspectOptions{})
	if err != nil {
		return err
	}
	node := res.Node
	node.Spec.Availability = swarm.NodeAvailability(avail)
	_, err = a.cli.NodeUpdate(ctx, node.ID, client.NodeUpdateOptions{Version: node.Version, Spec: node.Spec})
	return err
}

// SetNodeRole implements Backend.
func (a *API) SetNodeRole(ctx context.Context, id string, role NodeRole) error {
	res, err := a.cli.NodeInspect(ctx, id, client.NodeInspectOptions{})
	if err != nil {
		return err
	}
	node := res.Node
	node.Spec.Role = swarm.NodeRole(role)
	_, err = a.cli.NodeUpdate(ctx, node.ID, client.NodeUpdateOptions{Version: node.Version, Spec: node.Spec})
	return err
}

// Remove implements Backend.
func (a *API) Remove(ctx context.Context, kind Kind, id string) error {
	var err error
	switch kind {
	case KindService:
		_, err = a.cli.ServiceRemove(ctx, id, client.ServiceRemoveOptions{})
	case KindNode:
		_, err = a.cli.NodeRemove(ctx, id, client.NodeRemoveOptions{Force: true})
	case KindNetwork:
		_, err = a.cli.NetworkRemove(ctx, id, client.NetworkRemoveOptions{})
	case KindVolume:
		_, err = a.cli.VolumeRemove(ctx, id, client.VolumeRemoveOptions{})
	case KindSecret:
		_, err = a.cli.SecretRemove(ctx, id, client.SecretRemoveOptions{})
	case KindConfig:
		_, err = a.cli.ConfigRemove(ctx, id, client.ConfigRemoveOptions{})
	case KindStack:
		err = a.RemoveStack(ctx, id)
	default:
		err = fmt.Errorf("cannot remove a %s", kind)
	}
	return err
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
	f := make(client.Filters).Add("label", StackLabel+"="+name)

	svcList, err := a.cli.ServiceList(ctx, client.ServiceListOptions{Filters: f})
	if err != nil {
		return err
	}
	netList, err := a.cli.NetworkList(ctx, client.NetworkListOptions{Filters: f})
	if err != nil {
		return err
	}
	secretList, err := a.cli.SecretList(ctx, client.SecretListOptions{Filters: f})
	if err != nil {
		return err
	}
	configList, err := a.cli.ConfigList(ctx, client.ConfigListOptions{Filters: f})
	if err != nil {
		return err
	}
	svcs, nets, secrets, configs := svcList.Items, netList.Items, secretList.Items, configList.Items
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
		_, err := a.cli.ServiceRemove(ctx, s.ID, client.ServiceRemoveOptions{})
		failed("service", s.Spec.Name, err)
	}
	pending := nets
	deadline := time.Now().Add(10 * time.Second)
	for len(pending) > 0 {
		var busy []string
		var left []network.Summary
		var lastErr error
		for _, n := range pending {
			if _, err := a.cli.NetworkRemove(ctx, n.ID, client.NetworkRemoveOptions{}); err != nil && !cerrdefs.IsNotFound(err) {
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
		_, err := a.cli.SecretRemove(ctx, s.ID, client.SecretRemoveOptions{})
		failed("secret", s.Spec.Name, err)
	}
	for _, c := range configs {
		_, err := a.cli.ConfigRemove(ctx, c.ID, client.ConfigRemoveOptions{})
		failed("config", c.Spec.Name, err)
	}
	return errors.Join(errs...)
}

// ServiceSpec implements Backend: the service spec as YAML plus the version
// it was read at, so a later update can detect concurrent changes.
func (a *API) ServiceSpec(ctx context.Context, id string) (string, uint64, error) {
	res, err := a.cli.ServiceInspect(ctx, id, client.ServiceInspectOptions{})
	if err != nil {
		return "", 0, err
	}
	var env struct {
		Version swarm.Version
		Spec    json.RawMessage
	}
	if err := json.Unmarshal(res.Raw, &env); err != nil {
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
	_, err = a.cli.ServiceUpdate(ctx, id, client.ServiceUpdateOptions{Version: swarm.Version{Index: version}, Spec: spec})
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
	_, err := a.cli.ServiceCreate(ctx, client.ServiceCreateOptions{Spec: spec})
	return err
}
