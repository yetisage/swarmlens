package api

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// sshDialer returns a dial function for ssh:// endpoints, mirroring the
// behaviour of the docker CLI's ssh helper: it forwards stdio to the remote
// `docker system dial-stdio` command.
func sshDialer(host string) (func(ctx context.Context, network, addr string) (net.Conn, error), error) {
	u, err := url.Parse(host)
	if err != nil {
		return nil, err
	}
	user := u.User.Username()
	if user == "" {
		user = "root"
	}
	addr := u.Host
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, "22")
	}

	auth, err := sshAuth()
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            auth,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // the docker CLI's own ssh helper does the same
		Timeout:         30 * time.Second,
	}

	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		raw, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, err
		}
		cc, chans, reqs, err := ssh.NewClientConn(raw, addr, cfg)
		if err != nil {
			raw.Close()
			return nil, err
		}
		client := ssh.NewClient(cc, chans, reqs)

		sess, err := client.NewSession()
		if err != nil {
			client.Close()
			return nil, err
		}
		stdin, err := sess.StdinPipe()
		if err != nil {
			client.Close()
			return nil, err
		}
		stdout, err := sess.StdoutPipe()
		if err != nil {
			client.Close()
			return nil, err
		}
		if err := sess.Start("docker system dial-stdio"); err != nil {
			client.Close()
			return nil, err
		}
		return &sshConn{client: client, sess: sess, stdin: stdin, stdout: stdout}, nil
	}, nil
}

// sshConn adapts an ssh session's stdio to net.Conn so it can back an
// http.Transport dialer.
type sshConn struct {
	client *ssh.Client
	sess   *ssh.Session
	stdin  io.WriteCloser
	stdout io.Reader
}

func (c *sshConn) Read(p []byte) (int, error)  { return c.stdout.Read(p) }
func (c *sshConn) Write(p []byte) (int, error) { return c.stdin.Write(p) }

func (c *sshConn) Close() error {
	_ = c.sess.Close()
	return c.client.Close()
}

func (c *sshConn) LocalAddr() net.Addr                { return c.client.LocalAddr() }
func (c *sshConn) RemoteAddr() net.Addr               { return c.client.RemoteAddr() }
func (c *sshConn) SetDeadline(t time.Time) error      { return nil }
func (c *sshConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *sshConn) SetWriteDeadline(t time.Time) error { return nil }

// sshAuth assembles SSH auth methods: the agent first, then ~/.docker/id_* keys.
func sshAuth() ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if c, err := net.Dial("unix", sock); err == nil {
			methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(c).Signers))
		}
	}

	keys, err := sshKeyFiles()
	if err == nil && len(keys) > 0 {
		methods = append(methods, ssh.PublicKeys(keys...))
	}

	if len(methods) == 0 {
		return nil, fmt.Errorf("no ssh authentication available (set SSH_AUTH_SOCK or provide ~/.docker/id_rsa)")
	}
	return methods, nil
}

// sshKeyFiles loads the private keys the docker CLI would use.
func sshKeyFiles() ([]ssh.Signer, error) {
	paths := []string{
		os.Getenv("DOCKER_SSH_KEY"),
		filepath.Join(homeDir(), ".docker", "id_rsa"),
		filepath.Join(homeDir(), ".docker", "id_ecdsa"),
		filepath.Join(homeDir(), ".docker", "id_ed25519"),
	}
	var signers []ssh.Signer
	for _, p := range paths {
		if p == "" {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		s, err := ssh.ParsePrivateKey(b)
		if err != nil {
			continue
		}
		signers = append(signers, s)
	}
	return signers, nil
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return os.Getenv("HOME")
}

// endpointSummary renders a short description of the endpoint in use.
func endpointSummary() string {
	host := os.Getenv("DOCKER_HOST")
	if host == "" {
		return "unix:///var/run/docker.sock"
	}
	if i := strings.Index(host, "://"); i > 0 {
		return host[:i] + "://" + maskUser(host[i+3:])
	}
	return host
}

// maskUser hides the username in an ssh:// user@host endpoint.
func maskUser(rest string) string {
	if i := strings.Index(rest, "@"); i >= 0 {
		return rest[i+1:]
	}
	return rest
}
