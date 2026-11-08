package ui

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

// Context-sensitive bindings. The same key means different things in
// different views, which is how k9s keeps a small, consistent vocabulary.
var (
	bindDetail    = key.NewBinding(key.WithKeys("enter", "d"), key.WithHelp("d", "detail"))
	bindLogs      = key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "logs"))
	bindScaleUp   = key.NewBinding(key.WithKeys("+", "="), key.WithHelp("+", "scale up"))
	bindScaleDown = key.NewBinding(key.WithKeys("-"), key.WithHelp("-", "scale down"))
	bindUpdate    = key.NewBinding(key.WithKeys("u"), key.WithHelp("u", "update"))
	bindRollback  = key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "rollback"))
	bindRemove    = key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "remove"))
	bindDrain     = key.NewBinding(key.WithKeys("D"), key.WithHelp("D", "drain"))
	bindActivate  = key.NewBinding(key.WithKeys("A"), key.WithHelp("A", "activate"))
	bindPromote   = key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "promote"))
	bindDemote    = key.NewBinding(key.WithKeys("w"), key.WithHelp("w", "demote"))
	bindFollow    = key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "follow"))
)

// viewActions returns the actions available in the current view.
func (a *app) viewActions() []viewAction {
	switch a.screen {
	case screenServices:
		return a.serviceActions()
	case screenNodes:
		return a.nodeActions()
	case screenVolumes:
		return a.volumeActions()
	case screenNetworks:
		return a.networkActions()
	case screenSecrets:
		return a.secretActions()
	case screenConfigs:
		return a.configActions()
	case screenDetail:
		return a.detailActions()
	case screenLogs:
		return a.logActions()
	}
	return nil
}

func (a *app) serviceActions() []viewAction {
	return []viewAction{
		{
			binding: bindDetail,
			label:   "d detail",
			desc:    "open the task list",
			enabled: func(a *app) bool { return a.currentService() != nil },
			run:     func(a *app) (tea.Model, tea.Cmd) { return a.openDetail() },
		},
		{
			binding: bindScaleUp,
			label:   "+ scale",
			desc:    "add one replica",
			enabled: func(a *app) bool { return a.currentService() != nil && a.currentService().Mode == "replicated" },
			run:     func(a *app) (tea.Model, tea.Cmd) { return a.scaleService(+1) },
		},
		{
			binding: bindScaleDown,
			label:   "- scale",
			desc:    "remove one replica",
			enabled: func(a *app) bool { return a.currentService() != nil && a.currentService().Mode == "replicated" },
			run:     func(a *app) (tea.Model, tea.Cmd) { return a.scaleService(-1) },
		},
		{
			binding: bindUpdate,
			label:   "u update",
			desc:    "force a rolling update of every task",
			enabled: func(a *app) bool { return a.currentService() != nil },
			run:     func(a *app) (tea.Model, tea.Cmd) { return a.forceUpdateService() },
		},
		{
			binding: bindRollback,
			label:   "R rollback",
			desc:    "restore the previous spec",
			enabled: func(a *app) bool { return a.currentService() != nil && a.currentService().HasPrev },
			run:     func(a *app) (tea.Model, tea.Cmd) { return a.rollbackService() },
		},
		{
			binding: bindRemove,
			label:   "x remove",
			desc:    "delete the service and its tasks",
			enabled: func(a *app) bool { return a.currentService() != nil },
			run:     func(a *app) (tea.Model, tea.Cmd) { return a.removeService() },
		},
	}
}

func (a *app) nodeActions() []viewAction {
	return []viewAction{
		{
			binding: bindDrain,
			label:   "D drain",
			desc:    "stop scheduling new tasks on the node",
			enabled: func(a *app) bool { return a.currentNode() != nil },
			run:     func(a *app) (tea.Model, tea.Cmd) { return a.setNodeAvailability("drain") },
		},
		{
			binding: bindActivate,
			label:   "A activate",
			desc:    "resume scheduling on the node",
			enabled: func(a *app) bool { return a.currentNode() != nil },
			run:     func(a *app) (tea.Model, tea.Cmd) { return a.setNodeAvailability("active") },
		},
		{
			binding: bindPromote,
			label:   "p promote",
			desc:    "make the node a manager",
			enabled: func(a *app) bool { return a.currentNode() != nil && a.currentNode().Role != "manager" },
			run:     func(a *app) (tea.Model, tea.Cmd) { return a.setNodeRole("manager") },
		},
		{
			binding: bindDemote,
			label:   "w demote",
			desc:    "make the node a worker",
			enabled: func(a *app) bool { return a.currentNode() != nil && a.currentNode().Role != "worker" },
			run:     func(a *app) (tea.Model, tea.Cmd) { return a.setNodeRole("worker") },
		},
		{
			binding: bindRemove,
			label:   "x remove",
			desc:    "force-remove the node from the swarm",
			enabled: func(a *app) bool { return a.currentNode() != nil },
			run:     func(a *app) (tea.Model, tea.Cmd) { return a.removeNode() },
		},
	}
}

func (a *app) volumeActions() []viewAction {
	return []viewAction{
		{
			binding: bindRemove,
			label:   "x remove",
			desc:    "delete the volume",
			enabled: func(a *app) bool { return a.currentVolume() != nil },
			run:     func(a *app) (tea.Model, tea.Cmd) { return a.removeVolume() },
		},
	}
}

func (a *app) networkActions() []viewAction {
	return []viewAction{
		{
			binding: bindRemove,
			label:   "x remove",
			desc:    "delete the network",
			enabled: func(a *app) bool { return a.currentNetwork() != nil },
			run:     func(a *app) (tea.Model, tea.Cmd) { return a.removeNetwork() },
		},
	}
}

func (a *app) secretActions() []viewAction {
	return []viewAction{
		{
			binding: bindRemove,
			label:   "x remove",
			desc:    "delete the secret (refused while a service uses it)",
			enabled: func(a *app) bool { return len(a.secretRows) > 0 },
			run:     func(a *app) (tea.Model, tea.Cmd) { return a.removeSecret() },
		},
	}
}

func (a *app) configActions() []viewAction {
	return []viewAction{
		{
			binding: bindRemove,
			label:   "x remove",
			desc:    "delete the config (refused while a service uses it)",
			enabled: func(a *app) bool { return len(a.configRows) > 0 },
			run:     func(a *app) (tea.Model, tea.Cmd) { return a.removeConfig() },
		},
	}
}

func (a *app) detailActions() []viewAction {
	return []viewAction{
		{
			binding: bindLogs,
			label:   "l logs",
			desc:    "open the logs of the selected task",
			enabled: func(a *app) bool { return a.currentTask() != nil },
			run:     func(a *app) (tea.Model, tea.Cmd) { return a.openLogs() },
		},
	}
}

func (a *app) logActions() []viewAction {
	return []viewAction{
		{
			binding: bindFollow,
			label:   "f follow",
			desc:    "stream new log lines",
			run:     func(a *app) (tea.Model, tea.Cmd) { return a.toggleFollow() },
		},
	}
}
