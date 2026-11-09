package ui

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/yetisage/swarmlens/internal/api"
)

// editedService opens the spec of shop_web the way the "e" key does and
// returns the message that names the temp file the editor would be given.
func editedService(t *testing.T, a *app) (api.Service, editMsg) {
	t.Helper()
	a.command("svc /shop_web")
	svc := a.view().(*tableView).selected().obj.(api.Service)
	msg, ok := a.editService(svc)().(editMsg)
	if !ok || msg.err != nil {
		t.Fatalf("editService gave %#v", msg)
	}
	t.Cleanup(func() { _ = os.Remove(msg.path) })
	return svc, msg
}

func imageOf(t *testing.T, a *app, name string) string {
	t.Helper()
	svcs, err := a.be.Services(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range svcs {
		if s.Name == name {
			return s.Image
		}
	}
	t.Fatalf("no service %s", name)
	return ""
}

func TestEditingAServiceAppliesTheSavedSpec(t *testing.T) {
	a := newTestApp(t, 140, 38)
	_, msg := editedService(t, a)

	// the spec may hold secrets: the file is the user's alone
	if info, err := os.Stat(msg.path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("temp file: %v, %v; want mode 0600", info, err)
	}
	if raw, _ := os.ReadFile(msg.path); string(raw) != msg.orig {
		t.Error("the editor must be given the spec that was read")
	}

	edited := strings.Replace(msg.orig, "nginx:1.27-alpine", "nginx:1.28-alpine", 1)
	if edited == msg.orig {
		t.Fatalf("the spec has no image to change:\n%s", msg.orig)
	}
	if err := os.WriteFile(msg.path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	a.drain(a.onEditDone(editDoneMsg{editMsg: msg}))

	if got := imageOf(t, a, "shop_web"); got != "nginx:1.28-alpine" {
		t.Errorf("image = %q, the edit was not applied", got)
	}
	mustContain(t, a.screenText(), "Updated shop_web")
	if _, err := os.Stat(msg.path); !os.IsNotExist(err) {
		t.Error("the temp file must be removed")
	}
}

func TestLeavingTheEditorWithoutChangesDoesNothing(t *testing.T) {
	opt := testOpts()
	rec := &recorder{Backend: opt.Backend}
	opt.Backend = rec
	a := newTestAppOpts(t, opt, 140, 38)
	_, msg := editedService(t, a)

	if cmd := a.onEditDone(editDoneMsg{editMsg: msg}); cmd != nil {
		t.Error("no change must not call the backend")
	}
	mustContain(t, a.screenText(), "no changes")
	if _, err := os.Stat(msg.path); !os.IsNotExist(err) {
		t.Error("the temp file must be removed")
	}
}

func TestAnEditThatCannotBeAppliedIsReported(t *testing.T) {
	a := newTestApp(t, 140, 38)
	_, msg := editedService(t, a)
	if err := os.WriteFile(msg.path, []byte("name: [this is not closed"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.drain(a.onEditDone(editDoneMsg{editMsg: msg}))
	if got := a.flashMsg; got.level != flashErr || !strings.Contains(got.text, "invalid YAML") {
		t.Errorf("flash = %+v, want the YAML error", got)
	}
	if got := imageOf(t, a, "shop_web"); got != "nginx:1.27-alpine" {
		t.Errorf("image = %q: a rejected edit must change nothing", got)
	}
	if _, err := os.Stat(msg.path); !os.IsNotExist(err) {
		t.Error("the temp file must be removed")
	}
}

func TestAnEditorThatFailsIsReported(t *testing.T) {
	a := newTestApp(t, 140, 38)
	_, msg := editedService(t, a)
	if cmd := a.onEditDone(editDoneMsg{editMsg: msg, err: errors.New("exit status 1")}); cmd != nil {
		t.Error("a failed editor must not apply anything")
	}
	if got := a.flashMsg; got.level != flashErr || !strings.Contains(got.text, "Editor failed") || !strings.Contains(got.text, "exit status 1") {
		t.Errorf("flash = %+v", got)
	}
	if _, err := os.Stat(msg.path); !os.IsNotExist(err) {
		t.Error("the temp file must be removed")
	}
}

func TestEditingAServiceThatIsGoneIsReported(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.command("svc /shop_web")
	gone := a.view().(*tableView).selected().obj.(api.Service)
	gone.ID, gone.Name = "no-such-service", "ghost"
	msg, ok := a.editService(gone)().(editMsg)
	if !ok || msg.err == nil {
		t.Fatalf("editService = %#v, want an error", msg)
	}
	a.drain(a.onEdit(msg))
	if a.flashMsg.level != flashErr {
		t.Errorf("flash = %+v", a.flashMsg)
	}
}

func TestTheEditorIsChosenLikeEveryoneElseDoes(t *testing.T) {
	for _, c := range []struct {
		swarmlens, editor, visual string
		want                      []string
	}{
		{"", "", "", []string{"vi"}},
		{"", "", "code --wait", []string{"code", "--wait"}},
		{"", "nano -w", "code", []string{"nano", "-w"}},
		{"hx", "nano", "code", []string{"hx"}},
		{"  ", "nano", "", []string{"nano"}},
	} {
		t.Setenv("SWARMLENS_EDITOR", c.swarmlens)
		t.Setenv("EDITOR", c.editor)
		t.Setenv("VISUAL", c.visual)
		if got := editorCommand(); strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("SWARMLENS_EDITOR=%q EDITOR=%q VISUAL=%q: %v, want %v", c.swarmlens, c.editor, c.visual, got, c.want)
		}
	}
}

// notSimulated hides what marks a backend as a demo, so that the guards of the
// shell that come after "not in demo mode" can be reached.
type notSimulated struct{ api.Backend }

func TestShellOnlyOpensWhereItCanWork(t *testing.T) {
	opt := testOpts()
	opt.Backend = notSimulated{opt.Backend}
	a := newTestAppOpts(t, opt, 140, 38)
	tasks, err := a.be.Tasks(context.Background(), api.TaskQuery{})
	if err != nil {
		t.Fatal(err)
	}
	var local, remote, stopped api.Task
	for _, tk := range tasks {
		switch {
		case tk.State == "running" && tk.ContainerID != "" && tk.NodeID == a.summary.NodeID:
			local = tk
		case tk.State == "running" && tk.NodeID != a.summary.NodeID:
			remote = tk
		case tk.State != "running":
			stopped = tk
		}
	}
	if local.ID == "" || remote.ID == "" || stopped.ID == "" {
		t.Fatalf("the demo must offer a running task here, one elsewhere and a stopped one: %+v %+v %+v", local, remote, stopped)
	}

	if cmd := a.shell(stopped); cmd != nil || !strings.Contains(a.flashMsg.text, "is not running") {
		t.Errorf("a stopped task: cmd %v, flash %q", cmd != nil, a.flashMsg.text)
	}
	if cmd := a.shell(remote); cmd != nil || !strings.Contains(a.flashMsg.text, "shells only reach tasks on the node you are connected to") {
		t.Errorf("a task on another node: cmd %v, flash %q", cmd != nil, a.flashMsg.text)
	}
	if cmd := a.shell(local); cmd == nil {
		t.Errorf("a running task on this node must open a shell, flash %q", a.flashMsg.text)
	}
}

func TestDemoHasNoShell(t *testing.T) {
	a := newTestApp(t, 140, 38)
	a.command("tasks")
	a.press("s")
	mustContain(t, a.screenText(), "Shells are not available in demo mode")
}

// The shell talks to the same daemon swarmlens does.
func TestDockerCLIIsPointedAtTheSameDaemon(t *testing.T) {
	a := newTestApp(t, 140, 38)
	if got := a.dockerArgs(); len(got) != 2 || got[0] != "--context" || got[1] != "demo" {
		t.Errorf("dockerArgs = %v, want --context demo", got)
	}
	// the default context is whatever the environment says, which the docker
	// CLI reads the same way
	a.command("ctx lab")
	if got := a.dockerArgs(); len(got) != 2 || got[1] != "lab" {
		t.Errorf("after :ctx lab, dockerArgs = %v", got)
	}
}
