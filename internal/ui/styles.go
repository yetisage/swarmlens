package ui

import (
	"github.com/charmbracelet/lipgloss"
)

// Adaptive colours so the UI works on light and dark terminals.
var (
	colTitle   = lipgloss.AdaptiveColor{Light: "#8700af", Dark: "#d787ff"}
	colDim     = lipgloss.AdaptiveColor{Light: "#6c6c6c", Dark: "#949494"}
	colKey     = lipgloss.AdaptiveColor{Light: "#00875f", Dark: "#5fd7af"}
	colOK      = lipgloss.AdaptiveColor{Light: "#008700", Dark: "#5fd75f"}
	colWarn    = lipgloss.AdaptiveColor{Light: "#af8700", Dark: "#ffd75f"}
	colErr     = lipgloss.AdaptiveColor{Light: "#af0000", Dark: "#ff5f5f"}
	colInfo    = lipgloss.AdaptiveColor{Light: "#005faf", Dark: "#5fafff"}
	colDimBG   = lipgloss.AdaptiveColor{Light: "#eeeeee", Dark: "#262626"}
	colSelBG   = lipgloss.AdaptiveColor{Light: "#dddddd", Dark: "#303030"}
	colOverlay = lipgloss.AdaptiveColor{Light: "#f5f5f5", Dark: "#1c1c1c"}
)

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(colTitle)
	dimStyle    = lipgloss.NewStyle().Foreground(colDim)
	keyStyle    = lipgloss.NewStyle().Foreground(colDim)
	statusStyle = lipgloss.NewStyle().Background(colDimBG)
	errStyle    = lipgloss.NewStyle().Bold(true).Foreground(colErr)
	okStyle     = lipgloss.NewStyle().Bold(true).Foreground(colOK)
	warnStyle   = lipgloss.NewStyle().Bold(true).Foreground(colWarn)
	infoStyle   = lipgloss.NewStyle().Foreground(colInfo)

	selectedStyle = lipgloss.NewStyle().Bold(true)

	// state colours used in table cells
	stateOKStyle   = lipgloss.NewStyle().Foreground(colOK)
	stateWarnStyle = lipgloss.NewStyle().Foreground(colWarn)
	stateErrStyle  = lipgloss.NewStyle().Foreground(colErr)
	stateDimStyle  = lipgloss.NewStyle().Foreground(colDim)
	stateInfoStyle = lipgloss.NewStyle().Foreground(colInfo)

	// overlay (palette / confirm) styling
	overlayBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colTitle).
			Padding(0, 1)

	helpBoxStyle = lipgloss.NewStyle().
			Foreground(colDim).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colDim)
)

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
