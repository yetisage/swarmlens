package ui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"swarmlens/internal/api"
)

// scaleService adjusts the replica count of the selected service.
func (a *app) scaleService(delta int) (tea.Model, tea.Cmd) {
	svc := a.currentService()
	if svc == nil {
		a.setStatus("no service selected")
		return a, nil
	}
	name := svc.Name
	return a, actionCmd(a, func(ctx context.Context) (string, error) {
		n, err := a.api.ScaleService(ctx, name, delta)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("scaled %s to %d replicas", name, n), nil
	})
}

// forceUpdateService triggers a rolling update of the selected service.
func (a *app) forceUpdateService() (tea.Model, tea.Cmd) {
	svc := a.currentService()
	if svc == nil {
		a.setStatus("no service selected")
		return a, nil
	}
	name := svc.Name
	return a, actionCmd(a, func(ctx context.Context) (string, error) {
		if err := a.api.ForceUpdate(ctx, name); err != nil {
			return "", err
		}
		return fmt.Sprintf("forced update of %s", name), nil
	})
}

// rollbackService restores the previous spec of the selected service.
func (a *app) rollbackService() (tea.Model, tea.Cmd) {
	svc := a.currentService()
	if svc == nil {
		a.setStatus("no service selected")
		return a, nil
	}
	if !svc.HasPrev {
		a.setStatus(fmt.Sprintf("no previous spec for %s", svc.Name))
		return a, nil
	}
	name := svc.Name
	return a, actionCmd(a, func(ctx context.Context) (string, error) {
		if err := a.api.Rollback(ctx, name); err != nil {
			return "", err
		}
		return fmt.Sprintf("rolled back %s", name), nil
	})
}

// removeService asks for confirmation and then removes the service.
func (a *app) removeService() (tea.Model, tea.Cmd) {
	svc := a.currentService()
	if svc == nil {
		a.setStatus("no service selected")
		return a, nil
	}
	name := svc.Name
	a.confirm.ask(
		fmt.Sprintf("Remove service %q?", name),
		"This deletes the service and all of its tasks.",
		func(a *app) (tea.Model, tea.Cmd) {
			return a, actionCmd(a, func(ctx context.Context) (string, error) {
				if err := a.api.RemoveService(ctx, name); err != nil {
					return "", err
				}
				a.selectedServiceID = ""
				return fmt.Sprintf("removed service %s", name), nil
			})
		},
	)
	return a, nil
}

// setNodeAvailability drains or activates the selected node.
func (a *app) setNodeAvailability(avail string) (tea.Model, tea.Cmd) {
	node := a.currentNode()
	if node == nil {
		a.setStatus("no node selected")
		return a, nil
	}
	id, host := node.ID, node.Hostname
	return a, actionCmd(a, func(ctx context.Context) (string, error) {
		if err := a.api.SetNodeAvailability(ctx, id, api.NodeAvailability(avail)); err != nil {
			return "", err
		}
		return fmt.Sprintf("node %s is now %s", host, avail), nil
	})
}

// setNodeRole promotes or demotes the selected node.
func (a *app) setNodeRole(role string) (tea.Model, tea.Cmd) {
	node := a.currentNode()
	if node == nil {
		a.setStatus("no node selected")
		return a, nil
	}
	id, host := node.ID, node.Hostname
	return a, actionCmd(a, func(ctx context.Context) (string, error) {
		if err := a.api.SetNodeRole(ctx, id, api.NodeRole(role)); err != nil {
			return "", err
		}
		return fmt.Sprintf("node %s is now a %s", host, role), nil
	})
}

// removeNode asks for confirmation and then removes the node.
func (a *app) removeNode() (tea.Model, tea.Cmd) {
	node := a.currentNode()
	if node == nil {
		a.setStatus("no node selected")
		return a, nil
	}
	id, host := node.ID, node.Hostname
	a.confirm.ask(
		fmt.Sprintf("Remove node %q?", host),
		"The node is force-detached from the swarm. Running tasks are stopped.",
		func(a *app) (tea.Model, tea.Cmd) {
			return a, actionCmd(a, func(ctx context.Context) (string, error) {
				if err := a.api.RemoveNode(ctx, id); err != nil {
					return "", err
				}
				a.selectedNodeID = ""
				return fmt.Sprintf("removed node %s", host), nil
			})
		},
	)
	return a, nil
}

// toggleFollow starts or stops streaming the task logs.
func (a *app) toggleFollow() (tea.Model, tea.Cmd) {
	a.follow = !a.follow
	if a.follow {
		a.setStatus("following logs (f to stop)")
		return a, a.startFollow()
	}
	a.stopFollow()
	a.setStatus("log follow stopped")
	return a, nil
}

// removeVolume asks for confirmation and then deletes a volume.
func (a *app) removeVolume() (tea.Model, tea.Cmd) {
	v := a.currentVolume()
	if v == nil {
		a.setStatus("no volume selected")
		return a, nil
	}
	name := v.Name
	a.confirm.ask(
		fmt.Sprintf("Remove volume %q?", name),
		"Data in the volume is lost. The volume must not be in use.",
		func(a *app) (tea.Model, tea.Cmd) {
			return a, actionCmd(a, func(ctx context.Context) (string, error) {
				if err := a.api.RemoveVolume(ctx, name, false); err != nil {
					return "", err
				}
				return fmt.Sprintf("removed volume %s", name), nil
			})
		},
	)
	return a, nil
}

// removeNetwork asks for confirmation and then deletes a network.
func (a *app) removeNetwork() (tea.Model, tea.Cmd) {
	n := a.currentNetwork()
	if n == nil {
		a.setStatus("no network selected")
		return a, nil
	}
	name := n.Name
	a.confirm.ask(
		fmt.Sprintf("Remove network %q?", name),
		"Attached containers are disconnected. Built-in networks cannot be removed.",
		func(a *app) (tea.Model, tea.Cmd) {
			return a, actionCmd(a, func(ctx context.Context) (string, error) {
				if err := a.api.RemoveNetwork(ctx, name); err != nil {
					return "", err
				}
				return fmt.Sprintf("removed network %s", name), nil
			})
		},
	)
	return a, nil
}

// removeSecret deletes a swarm secret after checking that no service uses it.
func (a *app) removeSecret() (tea.Model, tea.Cmd) {
	idx := a.secrets.Cursor()
	if idx < 0 || idx >= len(a.secretRows) {
		a.setStatus("no secret selected")
		return a, nil
	}
	name := a.secretRows[idx].Name
	a.confirm.ask(
		fmt.Sprintf("Remove secret %q?", name),
		"Secret values are never displayed; deleting is permanent.",
		func(a *app) (tea.Model, tea.Cmd) {
			return a, actionCmd(a, func(ctx context.Context) (string, error) {
				used, err := a.api.SecretUsage(ctx, name)
				if err != nil {
					return "", err
				}
				if len(used) > 0 {
					return "", fmt.Errorf("secret %q is still used by: %s", name, strings.Join(used, ", "))
				}
				if err := a.api.RemoveSecret(ctx, name); err != nil {
					return "", err
				}
				return fmt.Sprintf("removed secret %s", name), nil
			})
		},
	)
	return a, nil
}

// removeConfig deletes a swarm config after checking that no service uses it.
func (a *app) removeConfig() (tea.Model, tea.Cmd) {
	idx := a.configs.Cursor()
	if idx < 0 || idx >= len(a.configRows) {
		a.setStatus("no config selected")
		return a, nil
	}
	name := a.configRows[idx].Name
	a.confirm.ask(
		fmt.Sprintf("Remove config %q?", name),
		"Deleting is permanent.",
		func(a *app) (tea.Model, tea.Cmd) {
			return a, actionCmd(a, func(ctx context.Context) (string, error) {
				used, err := a.api.ConfigUsage(ctx, name)
				if err != nil {
					return "", err
				}
				if len(used) > 0 {
					return "", fmt.Errorf("config %q is still used by: %s", name, strings.Join(used, ", "))
				}
				if err := a.api.RemoveConfig(ctx, name); err != nil {
					return "", err
				}
				return fmt.Sprintf("removed config %s", name), nil
			})
		},
	)
	return a, nil
}
