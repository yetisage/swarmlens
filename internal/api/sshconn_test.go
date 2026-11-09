package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestParseSSHURL(t *testing.T) {
	ok := map[string]sshSpec{
		"ssh://swarm.example.com":             {host: "swarm.example.com"},
		"ssh://deploy@swarm.example.com":      {user: "deploy", host: "swarm.example.com"},
		"ssh://deploy@swarm.example.com:2222": {user: "deploy", host: "swarm.example.com", port: "2222"},
		"ssh://mgr/run/user/1000/docker.sock": {host: "mgr", path: "/run/user/1000/docker.sock"},
		"ssh://root@[2001:db8::1]:22":         {user: "root", host: "2001:db8::1", port: "22"},
		"ssh://prod-alias":                    {host: "prod-alias"}, // an alias from ~/.ssh/config
		"ssh://u%40corp@h":                    {user: "u@corp", host: "h"},
		"ssh://h/":                            {host: "h", path: "/"},
	}
	for in, want := range ok {
		got, err := parseSSHURL(in)
		if err != nil || got != want {
			t.Errorf("parseSSHURL(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{
		"tcp://host:2375",
		"ssh://",
		"ssh://user@",
		"ssh://user:secret@host",
		"ssh://host?x=1",
		"ssh://host#frag",
		"ssh://host:0",
		"ssh://host:99999",
		"ssh://host:abc",
		"ssh://-oProxyCommand=evil",
		"ssh://-evil@host",
		"ssh://%zz",
	} {
		if got, err := parseSSHURL(in); err == nil {
			t.Errorf("parseSSHURL(%q) = %+v, want an error", in, got)
		}
	}
	// the password must not be echoed back in the message
	if _, err := parseSSHURL("ssh://user:hunter2@host"); err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Errorf("error = %v", err)
	}
}

func TestSSHArgs(t *testing.T) {
	got := strings.Join(sshSpec{user: "deploy", host: "swarm", port: "2222"}.args(), " ")
	want := "-l deploy -p 2222 -T -o BatchMode=yes -o ConnectTimeout=30 -o ServerAliveInterval=15 -o ServerAliveCountMax=3 -- swarm docker system dial-stdio"
	if got != want {
		t.Errorf("args =\n  %s\nwant\n  %s", got, want)
	}
	// the host comes after "--", whatever it looks like, and the remote
	// command is one argument
	a := sshSpec{host: "h", path: "/run/it's here/docker.sock"}.args()
	if a[len(a)-3] != "--" || a[len(a)-2] != "h" {
		t.Errorf("args = %q", a)
	}
	if last := a[len(a)-1]; last != `docker --host='unix:///run/it'\''s here/docker.sock' system dial-stdio` {
		t.Errorf("remote command = %s", last)
	}
	if strings.Contains(strings.Join(sshSpec{host: "h", path: "/"}.args(), " "), "--host") {
		t.Error("the default socket needs no --host")
	}
}

// fakeSSH installs a stand-in for ssh: a shell script.
func fakeSSH(t *testing.T, script string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	path := filepath.Join(t.TempDir(), "ssh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := sshBinary
	sshBinary = path
	t.Cleanup(func() { sshBinary = old })
}

func dialFake(t *testing.T) *cmdConn {
	t.Helper()
	conn, err := sshDialer(sshSpec{host: "swarm.example.com"})(context.Background(), "tcp", "ignored")
	if err != nil {
		t.Fatal(err)
	}
	c := conn.(*cmdConn)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func exited(c *cmdConn, within time.Duration) bool {
	select {
	case <-c.done:
		return true
	case <-time.After(within):
		return false
	}
}

func TestCmdConnCarriesBytesBothWays(t *testing.T) {
	fakeSSH(t, "exec cat")
	c := dialFake(t)
	if _, err := io.WriteString(c, "hello\n"); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 6)
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "hello\n" {
		t.Fatalf("read %q, %v", buf, err)
	}
	_ = c.Close()
	if !exited(c, 5*time.Second) {
		t.Error("the process must end when the connection is closed")
	}
	if _, err := c.Read(buf); err == nil {
		t.Error("a closed connection must not read")
	}
}

// A failing ssh must explain itself in its own words, with a hint where
// there is one: the user cannot answer a prompt, so the message is all they get.
func TestCmdConnExplainsWhyTheProcessFailed(t *testing.T) {
	fakeSSH(t, `echo "Host key verification failed." >&2; exit 255`)
	c := dialFake(t)
	_, err := c.Read(make([]byte, 1))
	var se *sshError
	if !errors.As(err, &se) {
		t.Fatalf("err = %T %v, want an *sshError", err, err)
	}
	for _, want := range []string{"swarm.example.com", "255", "Host key verification failed", "known_hosts"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q is missing from %q", want, err.Error())
		}
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 255 {
		t.Errorf("the exit status must be reachable with errors.As, got %v", err)
	}
	// the write side explains it the same way
	if _, err := c.Write([]byte("x")); err == nil || !strings.Contains(err.Error(), "Host key verification failed") {
		t.Errorf("write error = %v", err)
	}
}

func TestSSHHints(t *testing.T) {
	for stderr, want := range map[string]string{
		"Host key verification failed.":                      "known_hosts",
		"deploy@host: Permission denied (publickey).":        "ssh-agent",
		"bash: docker: command not found":                    "PATH",
		"sh: 1: docker: not found":                           "PATH",
		"ssh: connect to host x port 22: Connection refused": "",
	} {
		if got := sshHint(stderr); (want == "") != (got == "") || !strings.Contains(got, want) {
			t.Errorf("sshHint(%q) = %q, want it to mention %q", stderr, got, want)
		}
	}
}

func TestCmdConnCleanExitIsAPlainEOF(t *testing.T) {
	fakeSSH(t, "exit 0")
	c := dialFake(t)
	if _, err := c.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Errorf("err = %v, want io.EOF", err)
	}
}

func TestCmdConnCloseKillsAProcessThatIgnoresIt(t *testing.T) {
	fakeSSH(t, "exec sleep 60")
	c := dialFake(t)
	_ = c.Close()
	if !exited(c, 6*time.Second) {
		t.Fatal("a process that outlives Close must be killed")
	}
	_ = c.Close() // closing twice is fine
}

func TestSSHClientMissing(t *testing.T) {
	old := sshBinary
	sshBinary = "swarmlens-no-such-ssh-client"
	defer func() { sshBinary = old }()
	_, err := sshDialer(sshSpec{host: "h"})(context.Background(), "tcp", "x")
	if err == nil || !strings.Contains(err.Error(), "OpenSSH") {
		t.Errorf("err = %v, want it to say that OpenSSH is needed", err)
	}
}

func TestSSHDialRespectsAnEndedContext(t *testing.T) {
	fakeSSH(t, "exec cat")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sshDialer(sshSpec{host: "h"})(ctx, "tcp", "x"); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// ---- the whole path, with a Go process standing in for the far end ----

// TestHelperSSHBridge is not a test: it is the "far end" the fake ssh runs,
// the way `docker system dial-stdio` would: it joins its standard input and
// output to the daemon.
func TestHelperSSHBridge(t *testing.T) {
	addr := os.Getenv("SWARMLENS_HELPER_ADDR")
	if addr == "" {
		t.Skip("only runs as the helper process of the ssh tests")
	}
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "helper:", err)
		os.Exit(3)
	}
	go func() {
		_, _ = io.Copy(conn, os.Stdin)
		_ = conn.(*net.TCPConn).CloseWrite()
	}()
	_, _ = io.Copy(os.Stdout, conn)
	os.Exit(0)
}

func fakeDaemon(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(route(t, map[string]http.HandlerFunc{
		"/_ping": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("API-Version", "1.51")
			_, _ = io.WriteString(w, "OK")
		},
		"/info": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, map[string]any{"Name": "manager-1", "ServerVersion": "29.0.0", "Swarm": map[string]any{"LocalNodeState": "inactive"}})
		},
		"/version": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, map[string]any{"Version": "29.0.0", "ApiVersion": "1.51"})
		},
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSSHEndpointSpeaksHTTPThroughTheProcess(t *testing.T) {
	srv := fakeDaemon(t)
	argsFile := filepath.Join(t.TempDir(), "args")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SWARMLENS_HELPER_ADDR", srv.Listener.Addr().String())
	t.Setenv("SWARMLENS_TEST_BIN", exe)
	t.Setenv("SWARMLENS_ARGS_FILE", argsFile)
	fakeSSH(t, `printf '%s\n' "$@" > "$SWARMLENS_ARGS_FILE"
exec "$SWARMLENS_TEST_BIN" -test.run='^TestHelperSSHBridge$'`)

	a, err := Connect(ConnectOptions{Host: "ssh://deploy@swarm.example.com:2222"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	if a.Endpoint() != "ssh://swarm.example.com:2222" {
		t.Errorf("endpoint shown as %q: the user name stays out of the header", a.Endpoint())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// twice: the second request may reuse the connection or open another
	for i := 0; i < 2; i++ {
		sum, err := a.Summary(ctx)
		if err != nil {
			t.Fatalf("Summary #%d: %v", i, err)
		}
		if sum.ServerVersion != "29.0.0" || sum.NodeName != "manager-1" {
			t.Errorf("summary = %+v", sum)
		}
	}
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if strings.Join(args[:4], " ") != "-l deploy -p 2222" || args[len(args)-1] != "docker system dial-stdio" || args[len(args)-3] != "--" || args[len(args)-2] != "swarm.example.com" {
		t.Errorf("ssh was run with %q", args)
	}
}

// What ssh said must still be readable after the HTTP client has wrapped it.
func TestSSHFailureReachesTheCaller(t *testing.T) {
	fakeSSH(t, `echo "deploy@swarm.example.com: Permission denied (publickey)." >&2; exit 255`)
	a, err := Connect(ConnectOptions{Host: "ssh://deploy@swarm.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err = a.Summary(ctx)
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"Permission denied (publickey)", "ssh-agent"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q is missing from %q", want, err.Error())
		}
	}
}

func TestSSHEndpointWithABadURLFailsAtConnect(t *testing.T) {
	if _, err := Connect(ConnectOptions{Host: "ssh://user:pw@host"}); err == nil || !strings.Contains(err.Error(), "passwords") {
		t.Errorf("err = %v", err)
	}
}
