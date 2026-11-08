package ui

import (
	"strings"
)

// viewHistory keeps the stack of visited resource views so the user can
// walk back and forward, like k9s' cmdHistory ("[" back, "]" forward and
// "-" for the last view).
//
// It follows k9s' model: a flat list of commands with a cursor. Pushing
// does not truncate the entries ahead of the cursor, and walking the
// history does not record a new entry.
type viewHistory struct {
	entries    []string
	currentIdx int
}

func (h *viewHistory) push(entry string) {
	if entry == "" {
		return
	}
	if len(h.entries) > 0 && h.entries[len(h.entries)-1] == entry {
		h.currentIdx = len(h.entries) - 1
		return
	}
	h.entries = append(h.entries, entry)
	if len(h.entries) > 50 {
		h.entries = h.entries[len(h.entries)-50:]
	}
	h.currentIdx = len(h.entries) - 1
}

func (h *viewHistory) back() (string, bool) {
	if len(h.entries) == 0 || h.currentIdx <= 0 {
		return "", false
	}
	h.currentIdx--
	return h.entries[h.currentIdx], true
}

func (h *viewHistory) forward() (string, bool) {
	if len(h.entries) == 0 {
		return "", false
	}
	h.currentIdx++
	if h.currentIdx >= len(h.entries) {
		h.currentIdx = len(h.entries) - 1
		return "", false
	}
	return h.entries[h.currentIdx], true
}

func (h *viewHistory) last() (string, bool) {
	if len(h.entries) == 0 {
		return "", false
	}
	h.currentIdx = len(h.entries) - 1
	return h.entries[h.currentIdx], true
}

// crumbs renders the navigation trail, the swarmlens equivalent of k9s'
// breadcrumb bar: <services> <demo> <task>.
func (a *app) crumbs() string {
	var parts []string
	switch a.screen {
	case screenServices:
		parts = append(parts, "services")
	case screenNodes:
		parts = append(parts, "nodes")
	case screenVolumes:
		parts = append(parts, "volumes")
	case screenNetworks:
		parts = append(parts, "networks")
	case screenSecrets:
		parts = append(parts, "secrets")
	case screenConfigs:
		parts = append(parts, "configs")
	case screenDetail, screenLogs:
		parts = append(parts, "services")
		if s := a.currentService(); s != nil {
			parts = append(parts, s.Name)
		}
		if a.screen == screenLogs {
			if t := a.currentTask(); t != nil {
				parts = append(parts, t.Name)
			}
		}
	}

	var b strings.Builder
	for i, p := range parts {
		if i > 0 {
			b.WriteString(dimStyle.Render(" › "))
		}
		if i == len(parts)-1 {
			b.WriteString(selectedStyle.Render(p))
		} else {
			b.WriteString(dimStyle.Render(p))
		}
	}
	return b.String() + "\n"
}
