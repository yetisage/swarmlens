package api

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/swarm"
)

// Convenience aliases so callers do not need to import the swarm package.
type (
	NodeAvailability = swarm.NodeAvailability
	NodeRole         = swarm.NodeRole
)

// ActionResult describes the outcome of a mutating operation.
type ActionResult struct {
	Message string
	Err     error
}

// ScaleService adjusts the desired replica count of a replicated service by
// delta and returns the new desired count. Only the replica field is
// changed; the rest of the spec is round-tripped untouched.
func (a *API) ScaleService(ctx context.Context, name string, delta int) (int, error) {
	svc, err := a.inspectService(ctx, name)
	if err != nil {
		return 0, err
	}
	if svc.Spec.Mode.Replicated == nil || svc.Spec.Mode.Replicated.Replicas == nil {
		return 0, fmt.Errorf("service %q is not replicated (use global mode for per-node counts)", name)
	}
	desired := int64(*svc.Spec.Mode.Replicated.Replicas) + int64(delta)
	if desired < 0 {
		desired = 0
	}
	replicas := uint64(desired)
	svc.Spec.Mode.Replicated.Replicas = &replicas
	if _, err := a.cli.ServiceUpdate(ctx, svc.ID, svc.Version, svc.Spec, swarm.ServiceUpdateOptions{}); err != nil {
		return 0, err
	}
	return int(desired), nil
}

// SetReplicas sets an absolute desired replica count for a replicated service.
func (a *API) SetReplicas(ctx context.Context, name string, n int) error {
	svc, err := a.inspectService(ctx, name)
	if err != nil {
		return err
	}
	if svc.Spec.Mode.Replicated == nil {
		return fmt.Errorf("service %q is not replicated", name)
	}
	if n < 0 {
		n = 0
	}
	replicas := uint64(n)
	svc.Spec.Mode.Replicated.Replicas = &replicas
	_, err = a.cli.ServiceUpdate(ctx, svc.ID, svc.Version, svc.Spec, swarm.ServiceUpdateOptions{})
	return err
}

// ForceUpdate triggers a rolling update of every task of a service by
// bumping the spec's ForceUpdate counter.
func (a *API) ForceUpdate(ctx context.Context, name string) error {
	svc, err := a.inspectService(ctx, name)
	if err != nil {
		return err
	}
	svc.Spec.TaskTemplate.ForceUpdate = svc.Spec.TaskTemplate.ForceUpdate + 1
	_, err = a.cli.ServiceUpdate(ctx, svc.ID, svc.Version, svc.Spec, swarm.ServiceUpdateOptions{})
	return err
}

// Rollback restores a service to its previous spec.
func (a *API) Rollback(ctx context.Context, name string) error {
	svc, err := a.inspectService(ctx, name)
	if err != nil {
		return err
	}
	if svc.PreviousSpec == nil {
		return fmt.Errorf("service %q has no previous spec to roll back to", name)
	}
	prev := *svc.PreviousSpec
	_, err = a.cli.ServiceUpdate(ctx, svc.ID, svc.Version, prev, swarm.ServiceUpdateOptions{})
	return err
}

// RemoveService removes a service and its tasks.
func (a *API) RemoveService(ctx context.Context, name string) error {
	svc, err := a.inspectService(ctx, name)
	if err != nil {
		return err
	}
	return a.cli.ServiceRemove(ctx, svc.ID)
}

// SetNodeAvailability drains or activates a node.
func (a *API) SetNodeAvailability(ctx context.Context, nodeID string, avail swarm.NodeAvailability) error {
	node, _, err := a.cli.NodeInspectWithRaw(ctx, nodeID)
	if err != nil {
		return err
	}
	node.Spec.Availability = avail
	return a.cli.NodeUpdate(ctx, node.ID, node.Version, node.Spec)
}

// SetNodeRole promotes or demotes a node between manager and worker.
func (a *API) SetNodeRole(ctx context.Context, nodeID string, role swarm.NodeRole) error {
	node, _, err := a.cli.NodeInspectWithRaw(ctx, nodeID)
	if err != nil {
		return err
	}
	node.Spec.Role = role
	return a.cli.NodeUpdate(ctx, node.ID, node.Version, node.Spec)
}

// RemoveNode removes a node from the swarm (force detaches it).
func (a *API) RemoveNode(ctx context.Context, nodeID string) error {
	return a.cli.NodeRemove(ctx, nodeID, swarm.NodeRemoveOptions{Force: true})
}

// CreateService creates a replicated service. It is used by the smoke test
// to exercise the mutating paths without touching existing services.
func (a *API) CreateService(ctx context.Context, name, image string, replicas int) error {
	n := uint64(replicas)
	spec := swarm.ServiceSpec{
		Annotations: swarm.Annotations{
			Name:   name,
			Labels: map[string]string{"swarmlens.managed": "true"},
		},
		TaskTemplate: swarm.TaskSpec{
			ContainerSpec: &swarm.ContainerSpec{Image: image},
		},
		Mode: swarm.ServiceMode{
			Replicated: &swarm.ReplicatedService{Replicas: &n},
		},
	}
	_, err := a.cli.ServiceCreate(ctx, spec, swarm.ServiceCreateOptions{})
	return err
}

// inspectService resolves a service by name or id.
func (a *API) inspectService(ctx context.Context, nameOrID string) (swarm.Service, error) {
	svc, _, err := a.cli.ServiceInspectWithRaw(ctx, nameOrID, swarm.ServiceInspectOptions{})
	if err == nil {
		return svc, nil
	}
	// Fall back to looking the name up in the service list.
	svcs, lerr := a.cli.ServiceList(ctx, swarm.ServiceListOptions{})
	if lerr != nil {
		return swarm.Service{}, err
	}
	for _, s := range svcs {
		if s.Spec.Name == nameOrID {
			return s, nil
		}
	}
	return swarm.Service{}, err
}
