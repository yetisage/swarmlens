package api

import (
	"context"
	"fmt"
	"sort"

	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/api/types/volume"
)

// VolumeRow is one row of the volumes table.
type VolumeRow struct {
	Name    string
	Driver  string
	Scope   string
	Mounts  int
	Created string
}

// Volumes lists engine volumes.
func (a *API) Volumes(ctx context.Context) ([]VolumeRow, error) {
	res, err := a.cli.VolumeList(ctx, volume.ListOptions{})
	if err != nil {
		return nil, err
	}
	rows := make([]VolumeRow, 0, len(res.Volumes))
	for _, v := range res.Volumes {
		r := VolumeRow{Name: v.Name, Driver: v.Driver, Scope: v.Scope}
		if v.CreatedAt != "" {
			r.Created = v.CreatedAt
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows, nil
}

// NetworkRow is one row of the networks table.
type NetworkRow struct {
	Name     string
	Driver   string
	Scope    string
	Internal bool
	Labels   int
}

// Networks lists engine networks.
func (a *API) Networks(ctx context.Context) ([]NetworkRow, error) {
	nets, err := a.cli.NetworkList(ctx, network.ListOptions{})
	if err != nil {
		return nil, err
	}
	rows := make([]NetworkRow, 0, len(nets))
	for _, n := range nets {
		rows = append(rows, NetworkRow{
			Name:     n.Name,
			Driver:   n.Driver,
			Scope:    n.Scope,
			Internal: n.Internal,
			Labels:   len(n.Labels),
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows, nil
}

// SecretRow is one row of the secrets table. Secret *values* are never
// fetched or displayed — only metadata.
type SecretRow struct {
	Name    string
	Driver  string
	Labels  int
	Created string
}

// Secrets lists swarm secrets (manager only).
func (a *API) Secrets(ctx context.Context) ([]SecretRow, error) {
	secrets, err := a.cli.SecretList(ctx, swarm.SecretListOptions{})
	if err != nil {
		return nil, err
	}
	rows := make([]SecretRow, 0, len(secrets))
	for _, s := range secrets {
		rows = append(rows, SecretRow{
			Name:    s.Spec.Name,
			Driver:  s.Spec.Driver.Name,
			Labels:  len(s.Spec.Annotations.Labels),
			Created: s.CreatedAt.Local().Format("2006-01-02"),
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows, nil
}

// ConfigRow is one row of the configs table.
type ConfigRow struct {
	Name    string
	Labels  int
	Created string
}

// Configs lists swarm configs (manager only).
func (a *API) Configs(ctx context.Context) ([]ConfigRow, error) {
	configs, err := a.cli.ConfigList(ctx, swarm.ConfigListOptions{})
	if err != nil {
		return nil, err
	}
	rows := make([]ConfigRow, 0, len(configs))
	for _, c := range configs {
		rows = append(rows, ConfigRow{
			Name:    c.Spec.Name,
			Labels:  len(c.Spec.Annotations.Labels),
			Created: c.CreatedAt.Local().Format("2006-01-02"),
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows, nil
}

// RemoveSecret deletes a swarm secret.
func (a *API) RemoveSecret(ctx context.Context, name string) error {
	id, err := a.lookupSecretID(ctx, name)
	if err != nil {
		return err
	}
	return a.cli.SecretRemove(ctx, id)
}

// RemoveConfig deletes a swarm config.
func (a *API) RemoveConfig(ctx context.Context, name string) error {
	id, err := a.lookupConfigID(ctx, name)
	if err != nil {
		return err
	}
	return a.cli.ConfigRemove(ctx, id)
}

func (a *API) lookupSecretID(ctx context.Context, name string) (string, error) {
	secrets, err := a.cli.SecretList(ctx, swarm.SecretListOptions{})
	if err != nil {
		return "", err
	}
	for _, s := range secrets {
		if s.Spec.Name == name {
			return s.ID, nil
		}
	}
	return "", fmt.Errorf("secret %q not found", name)
}

func (a *API) lookupConfigID(ctx context.Context, name string) (string, error) {
	configs, err := a.cli.ConfigList(ctx, swarm.ConfigListOptions{})
	if err != nil {
		return "", err
	}
	for _, c := range configs {
		if c.Spec.Name == name {
			return c.ID, nil
		}
	}
	return "", fmt.Errorf("config %q not found", name)
}

// SecretOrConfigUsage reports which services reference a named secret or
// config, so the UI can warn before deleting one.
func (a *API) SecretOrConfigUsage(ctx context.Context, kind, name string) ([]string, error) {
	svcs, err := a.cli.ServiceList(ctx, swarm.ServiceListOptions{})
	if err != nil {
		return nil, err
	}
	var used []string
	for _, s := range svcs {
		spec := s.Spec.TaskTemplate.ContainerSpec
		if spec == nil {
			continue
		}
		switch kind {
		case "secret":
			for _, ref := range spec.Secrets {
				if ref.SecretName == name {
					used = append(used, s.Spec.Name)
					break
				}
			}
		case "config":
			for _, ref := range spec.Configs {
				if ref.ConfigName == name {
					used = append(used, s.Spec.Name)
					break
				}
			}
		}
	}
	return used, nil
}

// SecretUsage returns the services that reference a named secret.
func (a *API) SecretUsage(ctx context.Context, name string) ([]string, error) {
	return a.SecretOrConfigUsage(ctx, "secret", name)
}

// ConfigUsage returns the services that reference a named config.
func (a *API) ConfigUsage(ctx context.Context, name string) ([]string, error) {
	return a.SecretOrConfigUsage(ctx, "config", name)
}
