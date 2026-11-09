package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// ssh:// endpoints are reached the way the docker CLI reaches them: by running
// the system's ssh client and `docker system dial-stdio` on the far end, and
// speaking HTTP over the pipes. The OpenSSH client brings everything a swarm
// operator has already set up: known_hosts and its host key verification,
// ~/.ssh/config (aliases, ProxyJump, IdentityFile, ControlMaster), agents and
// hardware keys. Nothing here verifies or decides anything about the host.

// sshBinary is the ssh client to run; tests point it at a stand-in.
var sshBinary = "ssh"

// sshSpec is an ssh:// endpoint taken apart.
type sshSpec struct {
	user, host, port string
	path             string // the daemon's socket on the far end, if not the default
}

// parseSSHURL takes ssh://[user@]host[:port][/path/to/docker.sock] apart, with
// the same restrictions as the docker CLI: no password, query or fragment.
func parseSSHURL(raw string) (sshSpec, error) {
	u, err := url.Parse(raw)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Unwrap()
		}
		return sshSpec{}, fmt.Errorf("invalid ssh endpoint: %w", err)
	}
	var s sshSpec
	switch {
	case u.Scheme != "ssh":
		return s, fmt.Errorf("invalid ssh endpoint: scheme %q is not ssh", u.Scheme)
	case u.RawQuery != "":
		return s, errors.New("invalid ssh endpoint: query parameters are not allowed")
	case u.Fragment != "":
		return s, errors.New("invalid ssh endpoint: fragments are not allowed")
	}
	if u.User != nil {
		if _, ok := u.User.Password(); ok {
			return s, errors.New("invalid ssh endpoint: plain-text passwords are not supported (use a key or ssh-agent)")
		}
		s.user = u.User.Username()
	}
	s.host = u.Hostname()
	switch {
	case s.host == "":
		return s, errors.New("invalid ssh endpoint: the host is empty")
	case strings.HasPrefix(s.host, "-") || strings.HasPrefix(s.user, "-"):
		return s, errors.New("invalid ssh endpoint: the host and user cannot start with '-'")
	}
	if s.port = u.Port(); s.port != "" {
		if n, err := strconv.Atoi(s.port); err != nil || n < 1 || n > 65535 {
			return s, fmt.Errorf("invalid ssh endpoint: bad port %q", s.port)
		}
	}
	s.path = u.Path
	return s, nil
}

// args are the arguments of the ssh command.
//
//	-T                  no terminal: the far end is a pipe
//	BatchMode           never ask anything. swarmlens owns the terminal, so a
//	                    passphrase or host key question would land in the UI;
//	                    it fails with ssh's own message instead
//	ConnectTimeout      do not wait for a dead host for ever
//	ServerAlive*        notice a link that died silently, so that the event
//	                    stream reconnects instead of waiting on a dead socket
func (s sshSpec) args() []string {
	var a []string
	if s.user != "" {
		a = append(a, "-l", s.user)
	}
	if s.port != "" {
		a = append(a, "-p", s.port)
	}
	return append(a,
		"-T",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=30",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"--", s.host,
		s.remoteCommand())
}

// remoteCommand is what runs on the far end; ssh hands it to a POSIX shell.
func (s sshSpec) remoteCommand() string {
	if strings.Trim(s.path, "/") == "" {
		return "docker system dial-stdio"
	}
	return "docker --host=" + shellQuote("unix://"+s.path) + " system dial-stdio"
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// sshDialer returns the dial function that makes the HTTP client talk to the
// daemon through ssh. Every connection is a new ssh process (ControlMaster in
// ~/.ssh/config makes those cheap).
func sshDialer(s sshSpec) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// The process is not tied to ctx: that is the context of the request
		// that happened to need a connection, and the connection outlives it.
		return startCmdConn(s.host, sshBinary, s.args()...)
	}
}

// cmdConn is a net.Conn over the standard input and output of a process.
type cmdConn struct {
	host   string
	cmd    *exec.Cmd
	stdin  *os.File // our end of the pipe into the process
	stdout *os.File // our end of the pipe out of it
	stderr *tailBuffer

	done    chan struct{} // closed when the process has exited and been waited for
	waitErr error         // its exit status, valid once done is closed

	closing   atomic.Bool
	closeOnce sync.Once
}

func startCmdConn(host, name string, args ...string) (*cmdConn, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, fmt.Errorf("the %s client was not found: ssh:// endpoints need OpenSSH on the PATH (%w)", name, err)
	}
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return nil, err
	}
	c := &cmdConn{host: host, stdin: inW, stdout: outR, stderr: &tailBuffer{max: 4096}, done: make(chan struct{})}
	c.cmd = exec.Command(path, args...)
	c.cmd.Stdin, c.cmd.Stdout, c.cmd.Stderr = inR, outW, c.stderr
	c.cmd.WaitDelay = 2 * time.Second // a background ssh master must not hold Wait up
	detach(c.cmd)
	err = c.cmd.Start()
	// the process has its ends of the pipes now; ours are the other ones
	inR.Close()
	outW.Close()
	if err != nil {
		inW.Close()
		outR.Close()
		return nil, err
	}
	go func() {
		c.waitErr = c.cmd.Wait()
		close(c.done)
	}()
	return c, nil
}

// explain turns the end of a pipe into what the user needs to know: if the
// process failed, why, in its own words.
func (c *cmdConn) explain(err error) error {
	if err == nil || c.closing.Load() {
		return err
	}
	if !errors.Is(err, io.EOF) && !errors.Is(err, syscall.EPIPE) && !errors.Is(err, os.ErrClosed) {
		return err
	}
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		return fmt.Errorf("ssh %s: the connection ended but the process did not exit: %w", c.host, err)
	}
	if c.waitErr == nil {
		return err
	}
	return &sshError{host: c.host, err: c.waitErr, stderr: strings.TrimSpace(c.stderr.String())}
}

func (c *cmdConn) Read(p []byte) (int, error) {
	n, err := c.stdout.Read(p)
	return n, c.explain(err)
}

func (c *cmdConn) Write(p []byte) (int, error) {
	n, err := c.stdin.Write(p)
	return n, c.explain(err)
}

// Close ends the conversation: the process sees its input close, which ends
// the far end's dial-stdio and so ssh; one that lingers is killed.
func (c *cmdConn) Close() error {
	c.closeOnce.Do(func() {
		c.closing.Store(true)
		_ = c.stdin.Close()
		_ = c.stdout.Close()
		go func() {
			select {
			case <-c.done:
			case <-time.After(2 * time.Second):
				_ = c.cmd.Process.Kill()
				<-c.done
			}
		}()
	})
	return nil
}

func (c *cmdConn) LocalAddr() net.Addr  { return pipeAddr("swarmlens") }
func (c *cmdConn) RemoteAddr() net.Addr { return pipeAddr("ssh " + c.host) }

// Deadlines go to the pipes where the platform supports that, and are
// ignored where it does not.
func (c *cmdConn) SetDeadline(t time.Time) error {
	return errors.Join(ignoreNoDeadline(c.stdin.SetWriteDeadline(t)), ignoreNoDeadline(c.stdout.SetReadDeadline(t)))
}
func (c *cmdConn) SetReadDeadline(t time.Time) error {
	return ignoreNoDeadline(c.stdout.SetReadDeadline(t))
}
func (c *cmdConn) SetWriteDeadline(t time.Time) error {
	return ignoreNoDeadline(c.stdin.SetWriteDeadline(t))
}

func ignoreNoDeadline(err error) error {
	if errors.Is(err, os.ErrNoDeadline) || errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}

type pipeAddr string

func (pipeAddr) Network() string  { return "pipe" }
func (a pipeAddr) String() string { return string(a) }

// tailBuffer keeps the last max bytes written to it: the end of what ssh said
// on its standard error is the part that explains a failure.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = append([]byte(nil), t.b[len(t.b)-t.max:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}

// sshError is a failed ssh run, with what ssh printed and, for the usual
// causes, what to do about it.
type sshError struct {
	host   string
	err    error
	stderr string
}

func (e *sshError) Error() string {
	msg := fmt.Sprintf("ssh %s: %v", e.host, e.err)
	if e.stderr != "" {
		msg += ": " + e.stderr
	}
	if h := sshHint(e.stderr); h != "" {
		msg += " (" + h + ")"
	}
	return msg
}

func (e *sshError) Unwrap() error { return e.err }

// sshHint says what to do about the failures that swarmlens, which cannot
// ask questions on the terminal, runs into most.
func sshHint(stderr string) string {
	switch {
	case strings.Contains(stderr, "Host key verification failed"):
		return "swarmlens cannot ask whether to trust a host: run `ssh` to it once to add it to known_hosts"
	case strings.Contains(stderr, "Permission denied"):
		return "swarmlens cannot ask for a passphrase or password: load your key into ssh-agent"
	case strings.Contains(stderr, "docker: not found"), strings.Contains(stderr, "docker: command not found"):
		return "docker must be on the PATH of non-interactive ssh sessions on the host"
	}
	return ""
}
