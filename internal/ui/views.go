package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"

	"swarmlens/internal/api"
)

// navKeyMap keeps only arrow/page navigation, leaving single-letter keys
// (d, l, b, s, n, …) free for view navigation and actions.
func navKeyMap() table.KeyMap {
	return table.KeyMap{
		LineUp:       key.NewBinding(key.WithKeys("up"), key.WithHelp("↑", "up")),
		LineDown:     key.NewBinding(key.WithKeys("down"), key.WithHelp("↓", "down")),
		PageUp:       key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "page up")),
		PageDown:     key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdn", "page down")),
		HalfPageUp:   key.NewBinding(key.WithKeys("ctrl+u"), key.WithHelp("ctrl+u", "½ up")),
		HalfPageDown: key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("ctrl+d", "½ down")),
		GotoTop:      key.NewBinding(key.WithKeys("home"), key.WithHelp("home", "start")),
		GotoBottom:   key.NewBinding(key.WithKeys("end"), key.WithHelp("end", "end")),
	}
}

func newNavTable(cols ...table.Column) table.Model {
	styles := table.DefaultStyles()
	styles.Header = styles.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(colDim).
		BorderBottom(true).
		Bold(true)
	styles.Selected = lipgloss.NewStyle().Bold(true).Foreground(colTitle)

	return table.New(
		table.WithColumns(cols),
		table.WithFocused(true),
		table.WithKeyMap(navKeyMap()),
		table.WithStyles(styles),
	)
}

func serviceColumns() []table.Column {
	return []table.Column{
		{Title: "NAME", Width: 26},
		{Title: "IMAGE", Width: 30},
		{Title: "REPLICAS", Width: 12},
		{Title: "MODE", Width: 16},
		{Title: "UPDATED", Width: 26},
	}
}

func nodeColumns() []table.Column {
	return []table.Column{
		{Title: "HOSTNAME", Width: 24},
		{Title: "ROLE", Width: 10},
		{Title: "AVAILABILITY", Width: 16},
		{Title: "STATUS", Width: 12},
		{Title: "ENGINE", Width: 12},
		{Title: "ADDR", Width: 22},
	}
}

func taskColumns() []table.Column {
	return []table.Column{
		{Title: "TASK", Width: 20},
		{Title: "NODE", Width: 20},
		{Title: "DESIRED", Width: 10},
		{Title: "STATE", Width: 12},
		{Title: "CONTAINER", Width: 14},
		{Title: "SINCE", Width: 10},
	}
}

func volumeColumns() []table.Column {
	return []table.Column{
		{Title: "NAME", Width: 34},
		{Title: "DRIVER", Width: 16},
		{Title: "SCOPE", Width: 12},
		{Title: "CREATED", Width: 26},
	}
}

func networkColumns() []table.Column {
	return []table.Column{
		{Title: "NAME", Width: 34},
		{Title: "DRIVER", Width: 16},
		{Title: "SCOPE", Width: 12},
		{Title: "INTERNAL", Width: 12},
		{Title: "LABELS", Width: 10},
	}
}

func secretColumns() []table.Column {
	return []table.Column{
		{Title: "NAME", Width: 40},
		{Title: "DRIVER", Width: 16},
		{Title: "LABELS", Width: 10},
		{Title: "CREATED", Width: 16},
	}
}

func configColumns() []table.Column {
	return []table.Column{
		{Title: "NAME", Width: 40},
		{Title: "LABELS", Width: 10},
		{Title: "CREATED", Width: 16},
	}
}

// serviceTableRows renders service rows with state colouring.
func serviceTableRows(rows []api.ServiceRow) []table.Row {
	out := make([]table.Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, table.Row{
			r.Name,
			dimStyle.Render(r.Image),
			replicaStyle(r.Running, r.Desired, r.Mode).Render(r.Replicas),
			dimStyle.Render(r.Mode),
			stateStyle(r.Updated).Render(r.Updated),
		})
	}
	return out
}

func nodeTableRows(rows []api.NodeRow) []table.Row {
	out := make([]table.Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, table.Row{
			r.Hostname,
			roleStyle(r.Role).Render(r.Role),
			availabilityStyle(r.Availability).Render(r.Availability),
			stateStyle(r.Status).Render(r.Status),
			dimStyle.Render(r.Engine),
			dimStyle.Render(r.Addr),
		})
	}
	return out
}

func taskTableRows(rows []api.TaskRow) []table.Row {
	out := make([]table.Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, table.Row{
			r.Name,
			dimStyle.Render(r.Node),
			dimStyle.Render(r.Desired),
			stateStyle(r.Current).Render(r.Current),
			dimStyle.Render(api.ShortID(r.ContainerID)),
			dimStyle.Render(r.Since),
		})
	}
	return out
}

func volumeTableRows(rows []api.VolumeRow) []table.Row {
	out := make([]table.Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, table.Row{
			r.Name,
			dimStyle.Render(r.Driver),
			dimStyle.Render(r.Scope),
			dimStyle.Render(r.Created),
		})
	}
	return out
}

func networkTableRows(rows []api.NetworkRow) []table.Row {
	out := make([]table.Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, table.Row{
			r.Name,
			dimStyle.Render(r.Driver),
			dimStyle.Render(r.Scope),
			dimStyle.Render(fmt.Sprintf("%t", r.Internal)),
			dimStyle.Render(fmt.Sprintf("%d", r.Labels)),
		})
	}
	return out
}

func secretTableRows(rows []api.SecretRow) []table.Row {
	out := make([]table.Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, table.Row{
			r.Name,
			dimStyle.Render(r.Driver),
			dimStyle.Render(fmt.Sprintf("%d", r.Labels)),
			dimStyle.Render(r.Created),
		})
	}
	return out
}

func configTableRows(rows []api.ConfigRow) []table.Row {
	out := make([]table.Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, table.Row{
			r.Name,
			dimStyle.Render(fmt.Sprintf("%d", r.Labels)),
			dimStyle.Render(r.Created),
		})
	}
	return out
}

func roleStyle(role string) lipgloss.Style {
	if role == "manager" {
		return lipgloss.NewStyle().Foreground(colTitle)
	}
	return stateDimStyle
}

func availabilityStyle(avail string) lipgloss.Style {
	switch avail {
	case "active":
		return stateOKStyle
	case "drain":
		return stateWarnStyle
	case "pause":
		return stateInfoStyle
	default:
		return stateDimStyle
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
		if strings.Contains(strings.ToLower(r.Name), q) ||
			strings.Contains(strings.ToLower(r.Image), q) ||
			strings.Contains(strings.ToLower(r.Mode), q) {
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
