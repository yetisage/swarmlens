package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"

	"swarmlens/internal/api"
)

// ---- shared bindings ----

// describeAct shows the describe report of the selected object.
func describeAct(v *tableView, kind api.Kind) action {
	return act("d", "Describe", true, func() tea.Cmd {
		r := v.selected()
		if r == nil {
			return nil
		}
		return v.a.openText("describe", r, func(ctx context.Context) (string, error) {
			return v.a.be.Describe(ctx, kind, r.id)
		})
	})
}

// yamlAct shows the object as YAML.
func yamlAct(v *tableView, kind api.Kind) action {
	return act("y", "YAML", true, func() tea.Cmd {
		r := v.selected()
		if r == nil {
			return nil
		}
		return v.a.openText("yaml", r, func(ctx context.Context) (string, error) {
			return v.a.be.YAML(ctx, kind, r.id)
		})
	})
}

// deleteAct removes the selected (or marked) objects after confirmation.
func deleteAct(v *tableView, kind api.Kind, noun string) action {
	return mut("ctrl+d", "Delete", func() tea.Cmd {
		targets := v.targets()
		if len(targets) == 0 {
			return nil
		}
		msg := fmt.Sprintf("Delete %s %q?", noun, targets[0].name)
		done := fmt.Sprintf("Deleted %s %s", noun, targets[0].name)
		if len(targets) > 1 {
			msg = fmt.Sprintf("Delete %d %ss?", len(targets), noun)
			done = fmt.Sprintf("Deleted %d %ss", len(targets), noun)
		}
		switch kind {
		case api.KindStack:
			msg += "\nThis removes its services, networks, secrets and configs. Volumes are kept."
		case api.KindNode:
			msg += "\nThe node is force-removed from the swarm and its tasks are stopped."
		case api.KindVolume:
			msg += "\nThe data in the volume is lost."
		}
		be := v.a.be
		return v.a.confirm("Delete", msg, func() tea.Cmd {
			return v.a.run(done, func(ctx context.Context) error {
				return forEach(ctx, targets, func(ctx context.Context, r *row) error {
					return be.Remove(ctx, kind, r.id)
				})
			})
		})
	})
}

// ---- helpers on the app ----

func (a *app) confirm(title, msg string, onOK func() tea.Cmd) tea.Cmd {
	a.modal = newConfirm(title, msg, onOK)
	return nil
}

// run executes a mutation off the UI thread and reports the outcome.
func (a *app) run(ok string, fn func(ctx context.Context) error) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := fn(ctx); err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{msg: ok}
	}
}

// forEach applies fn to every target. Like `docker service rm a b c` it goes
// on after a failure, so one stale row does not leave the rest untouched, and
// it reports the failures together.
func forEach(ctx context.Context, targets []*row, fn func(ctx context.Context, r *row) error) error {
	var errs []error
	for _, r := range targets {
		if err := fn(ctx, r); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r.name, err))
		}
	}
	switch {
	case len(errs) == 0:
		return nil
	case len(targets) == 1:
		return errs[0]
	}
	return fmt.Errorf("%d of %d failed: %w", len(errs), len(targets), errors.Join(errs...))
}

// bulk applies a mutation to the marked rows, or to the selected one. It asks
// first when more than one object is involved, or, with ask, when the action
// is disruptive enough to deserve the question even for a single object.
func (a *app) bulk(v *tableView, verb, past, ask string, fn func(ctx context.Context, be api.Backend, r *row) error) tea.Cmd {
	targets := v.targets()
	if len(targets) == 0 {
		return nil
	}
	be := a.be
	do := func() tea.Cmd {
		msg := fmt.Sprintf("%s %s", past, targets[0].name)
		if len(targets) > 1 {
			msg = fmt.Sprintf("%s %d %s", past, len(targets), v.res.key)
		}
		return a.run(msg, func(ctx context.Context) error {
			return forEach(ctx, targets, func(ctx context.Context, r *row) error { return fn(ctx, be, r) })
		})
	}
	if ask == "" && len(targets) > 1 {
		ask = fmt.Sprintf("%s %d %s?", verb, len(targets), v.res.key)
	}
	if ask != "" {
		return a.confirm(verb, ask, do)
	}
	return do()
}

// openText pushes a describe or YAML view for a row.
func (a *app) openText(kind string, r *row, load func(ctx context.Context) (string, error)) tea.Cmd {
	a.flash(flashInfo, fmt.Sprintf("%s %s...", map[string]string{"describe": "Describing", "yaml": "Viewing YAML of"}[kind], r.name))
	return a.push(newTextView(a, kind, r.name, load))
}

func (a *app) pushTasks(q loadQuery, sub string) tea.Cmd {
	v := newTableView(a, taskRes, q)
	v.sub = sub
	return a.push(v)
}

func (a *app) pushLogs(t api.LogTarget, stack string) tea.Cmd {
	return a.push(a.newLogView(t, stack))
}

// openStack drills from a stack into its services, making it the active
// stack the way entering a namespace does in k9s.
func (a *app) openStack(name string) tea.Cmd {
	a.applyScope(name)
	return a.runCommand("services", true)
}

// jumpToService opens the services with the owner of a task selected,
// like k9s' "Jump Owner".
func (a *app) jumpToService(t api.Task) tea.Cmd {
	v := newTableView(a, serviceRes, loadQuery{})
	v.selID = t.ServiceID
	return a.push(v)
}

// scaleService asks for a replica count.
func (a *app) scaleService(s api.Service) tea.Cmd {
	d := &dialog{
		title:  "Scale",
		lines:  []string{fmt.Sprintf("Scale service %q?", s.Name)},
		fields: []*dlgField{{label: "Replicas:", digits: true}},
	}
	d.fields[0].in.set(strconv.Itoa(s.Desired))
	d.buttons = []dlgButton{
		{label: "OK", run: func() tea.Cmd {
			n, err := strconv.Atoi(d.value())
			if err != nil {
				return a.flashFor(flashErr, fmt.Sprintf("%q is not a valid replica count", d.value()))
			}
			return a.run(fmt.Sprintf("Scaled %s to %d", s.Name, n), func(ctx context.Context) error {
				return a.be.ScaleService(ctx, s.ID, n)
			})
		}},
		{label: "Cancel"},
	}
	a.modal = d
	return nil
}

// copy puts text on the clipboard: the system one when a helper exists,
// otherwise the terminal's, via OSC 52.
func (a *app) copy(text string) {
	if a.static {
		return
	}
	if err := clipboard.WriteAll(text); err == nil {
		return
	}
	termenv.NewOutput(os.Stdout).Copy(text)
}

// ---- external programs ----

// shell opens a shell in a task's container. Only containers on the node
// the daemon belongs to can be reached.
func (a *app) shell(t api.Task) tea.Cmd {
	switch {
	case a.readOnly:
		a.flash(flashWarn, "Command disabled in read-only mode")
		return nil
	case isSimulated(a.be):
		a.flash(flashWarn, "Shells are not available in demo mode")
		return nil
	case t.ContainerID == "" || t.State != "running":
		a.flash(flashErr, fmt.Sprintf("Task %s is not running", t.Name))
		return nil
	case a.summary != nil && t.NodeID != a.summary.NodeID:
		a.flash(flashErr, fmt.Sprintf("Task %s runs on node %s; shells only reach tasks on the node you are connected to (%s)", t.Name, t.Node, a.summary.NodeName))
		return nil
	}
	args := a.dockerArgs()
	args = append(args, "exec", "-it", t.ContainerID, "sh", "-c", "command -v bash >/dev/null 2>&1 && exec bash || exec sh")
	return tea.ExecProcess(exec.Command("docker", args...), func(err error) tea.Msg {
		return execDoneMsg{what: "shell", err: err}
	})
}

// dockerArgs selects the same daemon for the docker CLI as swarmlens uses.
func (a *app) dockerArgs() []string {
	if name := a.be.ContextName(); name != "" && name != "default" {
		return []string{"--context", name}
	}
	return nil
}

type editMsg struct {
	svc     api.Service
	path    string
	orig    string
	version uint64
	err     error
}

type editDoneMsg struct {
	editMsg
	err error
}

// editService opens the service spec in $EDITOR and applies the result,
// like k9s' edit.
func (a *app) editService(s api.Service) tea.Cmd {
	be := a.be
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		y, version, err := be.ServiceSpec(ctx, s.ID)
		if err != nil {
			return editMsg{svc: s, err: err}
		}
		f, err := os.CreateTemp("", "swarmlens-"+sanitize(s.Name)+"-*.yaml")
		if err != nil {
			return editMsg{svc: s, err: err}
		}
		defer f.Close()
		if _, err := f.WriteString(y); err != nil {
			return editMsg{svc: s, err: err}
		}
		return editMsg{svc: s, path: f.Name(), orig: y, version: version}
	}
}

func editorCommand() []string {
	for _, k := range []string{"SWARMLENS_EDITOR", "EDITOR", "VISUAL"} {
		if v := strings.Fields(os.Getenv(k)); len(v) > 0 {
			return v
		}
	}
	return []string{"vi"}
}

func (a *app) onEdit(m editMsg) tea.Cmd {
	if m.err != nil {
		a.flash(flashErr, m.err.Error())
		return nil
	}
	ed := editorCommand()
	cmd := exec.Command(ed[0], append(ed[1:], m.path)...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return editDoneMsg{editMsg: m, err: err} })
}

func (a *app) onEditDone(m editDoneMsg) tea.Cmd {
	defer os.Remove(m.path)
	if m.err != nil {
		a.flash(flashErr, "Editor failed: "+m.err.Error())
		return nil
	}
	raw, err := os.ReadFile(m.path)
	if err != nil {
		a.flash(flashErr, err.Error())
		return nil
	}
	if string(raw) == m.orig {
		a.flash(flashWarn, fmt.Sprintf("Edit of %s cancelled: no changes", m.svc.Name))
		return nil
	}
	be := a.be
	return a.run("Updated "+m.svc.Name, func(ctx context.Context) error {
		return be.UpdateServiceSpec(ctx, m.svc.ID, m.version, string(raw))
	})
}

func isSimulated(be api.Backend) bool {
	s, ok := be.(api.Simulated)
	return ok && s.Simulated()
}
