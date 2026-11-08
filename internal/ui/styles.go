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
	// colName is the emphasised column (resource names). It is explicitly
	// coloured rather than left at the terminal default, so that every row
	// renders identically regardless of the terminal theme.
	colName = lipgloss.AdaptiveColor{Light: "#26262a", Dark: "#f2f2f7"}
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
// (defined in views.go alongside the column definitions)
