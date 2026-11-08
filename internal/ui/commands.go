package ui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// load*Cmds are the async data-loading commands Bubble Tea runs in
// goroutines; each returns a msg consumed in Update.

func loadServicesCmd(a *app) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		rows, err := a.api.Services(ctx)
		return servicesMsg{rows, err}
	}
}

func loadNodesCmd(a *app) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		rows, err := a.api.Nodes(ctx)
		return nodesMsg{rows, err}
	}
}

func loadTasksCmd(a *app) tea.Cmd {
	id, name := "", ""
	if s := a.currentService(); s != nil {
		id, name = s.ID, s.Name
	}
	if id == "" {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		rows, err := a.api.Tasks(ctx, id, name)
		return tasksMsg{rows, err}
	}
}

func loadLogsCmd(a *app) tea.Cmd {
	id := a.logsTask
	if id == "" {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		content, err := a.api.TaskLogs(ctx, id, "300")
		return logsMsg{content, err}
	}
}

func loadVolumesCmd(a *app) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		rows, err := a.api.Volumes(ctx)
		return volumesMsg{rows, err}
	}
}

func loadNetworksCmd(a *app) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		rows, err := a.api.Networks(ctx)
		return networksMsg{rows, err}
	}
}

func loadSecretsCmd(a *app) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		rows, err := a.api.Secrets(ctx)
		return secretsMsg{rows, err}
	}
}

func loadConfigsCmd(a *app) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		rows, err := a.api.Configs(ctx)
		return configsMsg{rows, err}
	}
}

// startFollow begins streaming logs for the selected task.
func (a *app) startFollow() tea.Cmd {
	id := a.logsTask
	if id == "" {
		return nil
	}
	p := a.program
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		a.followCancel = cancel
		go func() {
			err := a.api.FollowTaskLogs(ctx, id, "50", func(line string) {
				if p != nil {
					p.Send(logLineMsg{line: line})
				}
			})
			if err != nil && ctx.Err() == nil {
				p.Send(logLineMsg{err: err})
			}
		}()
		return nil
	}
}

// actionCmd runs a mutating API call and reports the outcome.
func actionCmd(a *app, fn func(ctx context.Context) (string, error)) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		msg, err := fn(ctx)
		return actionMsg{message: msg, err: err}
	}
}
