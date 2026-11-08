package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"swarmlens/internal/api"
)

var (
	headerStyle = lipgloss.NewStyle().Bold(true)
	nameStyle   = lipgloss.NewStyle().Bold(false).Foreground(colName)
	plainStyle  = lipgloss.Style{}
)

func serviceColumns() []column {
	return []column{
		{Title: "NAME", Width: 26, Style: nameStyle},
		{Title: "IMAGE", Width: 30, Style: dimStyle},
		{Title: "REPLICAS", Width: 12, Style: plainStyle},
		{Title: "MODE", Width: 16, Style: dimStyle},
		{Title: "UPDATED", Width: 26, Style: plainStyle},
	}
}

func nodeColumns() []column {
	return []column{
		{Title: "HOSTNAME", Width: 24, Style: nameStyle},
		{Title: "ROLE", Width: 10, Style: plainStyle},
		{Title: "AVAILABILITY", Width: 16, Style: plainStyle},
		{Title: "STATUS", Width: 12, Style: plainStyle},
		{Title: "ENGINE", Width: 12, Style: dimStyle},
		{Title: "ADDR", Width: 22, Style: dimStyle},
	}
}

func taskColumns() []column {
	return []column{
		{Title: "TASK", Width: 20, Style: nameStyle},
		{Title: "NODE", Width: 20, Style: dimStyle},
		{Title: "DESIRED", Width: 10, Style: dimStyle},
		{Title: "STATE", Width: 12, Style: plainStyle},
		{Title: "CONTAINER", Width: 14, Style: dimStyle},
		{Title: "SINCE", Width: 10, Style: dimStyle},
	}
}

func volumeColumns() []column {
	return []column{
		{Title: "NAME", Width: 40, Style: nameStyle},
		{Title: "DRIVER", Width: 16, Style: dimStyle},
		{Title: "SCOPE", Width: 12, Style: dimStyle},
		{Title: "CREATED", Width: 26, Style: dimStyle},
	}
}

func networkColumns() []column {
	return []column{
		{Title: "NAME", Width: 40, Style: nameStyle},
		{Title: "DRIVER", Width: 16, Style: dimStyle},
		{Title: "SCOPE", Width: 12, Style: dimStyle},
		{Title: "INTERNAL", Width: 12, Style: plainStyle},
		{Title: "LABELS", Width: 10, Style: dimStyle},
	}
}

func secretColumns() []column {
	return []column{
		{Title: "NAME", Width: 40, Style: nameStyle},
		{Title: "DRIVER", Width: 16, Style: dimStyle},
		{Title: "LABELS", Width: 10, Style: dimStyle},
		{Title: "CREATED", Width: 16, Style: dimStyle},
	}
}

func configColumns() []column {
	return []column{
		{Title: "NAME", Width: 40, Style: nameStyle},
		{Title: "LABELS", Width: 10, Style: dimStyle},
		{Title: "CREATED", Width: 16, Style: dimStyle},
	}
}

// serviceTableRows renders service rows with state colouring.
func serviceTableRows(rows []api.ServiceRow) [][]string {
	out := make([][]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, []string{
			r.Name,
			r.Image,
			replicaStyle(r.Running, r.Desired, r.Mode).Render(r.Replicas),
			r.Mode,
			r.Updated,
		})
	}
	return out
}

func nodeTableRows(rows []api.NodeRow) [][]string {
	out := make([][]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, []string{
			r.Hostname,
			r.Role,
			r.Availability,
			r.Status,
			r.Engine,
			r.Addr,
		})
	}
	return out
}

func taskTableRows(rows []api.TaskRow) [][]string {
	out := make([][]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, []string{
			r.Name,
			r.Node,
			r.Desired,
			r.Current,
			api.ShortID(r.ContainerID),
			r.Since,
		})
	}
	return out
}

func volumeTableRows(rows []api.VolumeRow) [][]string {
	out := make([][]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, []string{r.Name, r.Driver, r.Scope, r.Created})
	}
	return out
}

func networkTableRows(rows []api.NetworkRow) [][]string {
	out := make([][]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, []string{
			r.Name,
			r.Driver,
			r.Scope,
			fmt.Sprintf("%t", r.Internal),
			fmt.Sprintf("%d", r.Labels),
		})
	}
	return out
}

func secretTableRows(rows []api.SecretRow) [][]string {
	out := make([][]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, []string{r.Name, r.Driver, fmt.Sprintf("%d", r.Labels), r.Created})
	}
	return out
}

func configTableRows(rows []api.ConfigRow) [][]string {
	out := make([][]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, []string{r.Name, fmt.Sprintf("%d", r.Labels), r.Created})
	}
	return out
}

// stateStyle maps a swarm state string to a colour.
func stateStyle(state string) lipgloss.Style {
	switch state {
	case "running", "ready", "active", "completed", "healthy":
		return stateOKStyle
	case "updating", "starting", "pending", "preparing", "drain", "pause":
		return stateWarnStyle
	case "failed", "down", "rejected", "shutdown", "orphaned", "unhealthy":
		return stateErrStyle
	case "":
		return stateDimStyle
	default:
		return stateInfoStyle
	}
}

// replicaStyle colours the running/desired column: green when fully
// converged, yellow while converging, red when degraded.
func replicaStyle(running, desired int, mode string) lipgloss.Style {
	if mode == "global" {
		return stateInfoStyle
	}
	switch {
	case desired == 0:
		return stateDimStyle
	case running == desired:
		return stateOKStyle
	case running < desired:
		return stateWarnStyle
	default:
		return stateErrStyle
	}
}

// filterServices applies the current search filter.
func (a *app) filterServices(rows []api.ServiceRow) []api.ServiceRow {
	if a.filter == "" {
		return rows
	}
	q := strings.ToLower(a.filter)
	out := make([]api.ServiceRow, 0, len(rows))
	for _, r := range rows {
		hit := strings.Contains(strings.ToLower(r.Name), q) ||
			strings.Contains(strings.ToLower(r.Image), q) ||
			strings.Contains(strings.ToLower(r.Mode), q)
		if hit != a.inverse {
			out = append(out, r)
		}
	}
	return out
}

func (a *app) visibleServices() []api.ServiceRow {
	return a.filterServices(a.serviceRows)
}

// applyFilter re-renders the current table with the filter applied.
func (a *app) applyFilter() {
	if a.screen == screenServices {
		visible := a.visibleServices()
		a.services.SetRows(serviceTableRows(visible))
		restoreCursor(&a.services, visible, func(r api.ServiceRow) string { return r.ID }, a.selectedServiceID, &a.selectedServiceID)
	}
}
