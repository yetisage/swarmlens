package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runArgs runs the program in a scratch config directory.
func runArgs(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	t.Setenv("SWARMLENS_CONFIG_DIR", t.TempDir())
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestVersionAndHelp(t *testing.T) {
	old := version
	version = "v1.2.3"
	defer func() { version = old }()
	if code, out, _ := runArgs(t, "--version"); code != 0 || out != "swarmlens v1.2.3\n" {
		t.Errorf("--version: %d %q", code, out)
	}
	for _, flag := range []string{"-h", "--help"} {
		if code, out, _ := runArgs(t, flag); code != 0 || !strings.Contains(out, "usage: swarmlens") {
			t.Errorf("%s: %d %q", flag, code, out)
		}
	}
}

func TestMistakesOnTheCommandLine(t *testing.T) {
	for _, args := range [][]string{{"--no-such-flag"}, {"services"}, {"--demo", "extra"}, {"-r"}} {
		code, out, errOut := runArgs(t, args...)
		if code != 2 || out != "" || !strings.Contains(errOut, "usage: swarmlens") {
			t.Errorf("%v: code %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
	if _, _, errOut := runArgs(t, "stray"); !strings.Contains(errOut, `unexpected argument "stray"`) {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestBadRefreshRateIsAFailure(t *testing.T) {
	code, _, errOut := runArgs(t, "--demo", "-r", "soon")
	if code != 1 || !strings.Contains(errOut, `invalid refresh rate "soon"`) {
		t.Errorf("code %d, stderr %q", code, errOut)
	}
}

func TestParseRefresh(t *testing.T) {
	for in, want := range map[string]time.Duration{"": 0, "2": 2 * time.Second, "0.5": 500 * time.Millisecond, "500ms": 500 * time.Millisecond, "1m": time.Minute} {
		if got, err := parseRefresh(in); err != nil || got != want {
			t.Errorf("parseRefresh(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"0", "-1", "abc", "0s", "-5s"} {
		if _, err := parseRefresh(in); err == nil {
			t.Errorf("parseRefresh(%q) must fail", in)
		}
	}
}

func TestRenderDemo(t *testing.T) {
	code, out, _ := runArgs(t, "--demo", "--render", "--width", "120", "--height", "30")
	if code != 0 || !strings.Contains(out, "services(all)[14]") || !strings.Contains(out, "Context:  demo") {
		t.Errorf("code %d, frame:\n%s", code, out)
	}
	// a start command of -c reaches the frame, and so does a typo's dialog
	if _, out, _ := runArgs(t, "--demo", "--render", "-c", "nodes"); !strings.Contains(out, "nodes(all)[7]") {
		t.Errorf("-c nodes:\n%s", out)
	}
	if _, out, _ := runArgs(t, "--demo", "--render", "-c", "nosuchcommand"); !strings.Contains(out, "command not found") || !strings.Contains(out, "services(all)") {
		t.Errorf("-c nosuchcommand:\n%s", out)
	}
}

func TestSmokeAgainstTheDemo(t *testing.T) {
	smokeEventWindow = 100 * time.Millisecond
	defer func() { smokeEventWindow = 0 }()

	code, out, _ := runArgs(t, "--demo", "--smoke")
	if code != 0 || !strings.Contains(out, "PASSED") || strings.Contains(out, "mutating checks") {
		t.Errorf("--smoke: code %d\n%s", code, out)
	}
	code, out, _ = runArgs(t, "--demo", "--smoke-mutate", "--smoke-image", "example/img:1")
	for _, want := range []string{"mutating checks", "✓ create swarmlens-smoke-", "✓ scale to 2", "✓ restart", "✓ remove swarmlens-smoke-", "PASSED"} {
		if !strings.Contains(out, want) {
			t.Errorf("--smoke-mutate: %q missing from\n%s", want, out)
		}
	}
	if code != 0 {
		t.Errorf("--smoke-mutate: code %d", code)
	}
}

// --readonly must hold for the self-test too: it has nothing to create with.
func TestReadOnlySmokeMutateCreatesNothing(t *testing.T) {
	smokeEventWindow = 100 * time.Millisecond
	defer func() { smokeEventWindow = 0 }()
	code, out, _ := runArgs(t, "--demo", "--readonly", "--smoke-mutate")
	if code != 0 || !strings.Contains(out, "skipped") || strings.Contains(out, "✓ create") {
		t.Errorf("code %d\n%s", code, out)
	}
}

func TestDebugEventsAgainstTheDemo(t *testing.T) {
	debugEventsWindow = 150 * time.Millisecond
	defer func() { debugEventsWindow = 30 * time.Second }()
	code, out, _ := runArgs(t, "--demo", "--debug-events")
	if code != 0 || !strings.Contains(out, "streaming /events") || !strings.Contains(out, "stream ended") {
		t.Errorf("code %d\n%s", code, out)
	}
}

func TestMalformedConfigIsAFailureThatNamesTheFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("swarmlens: [this is not a mapping\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SWARMLENS_CONFIG_DIR", dir)
	var out, errOut bytes.Buffer
	if code := run([]string{"--demo", "--render"}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "config.yaml") {
		t.Errorf("code %d, stderr %q", code, errOut.String())
	}
}
