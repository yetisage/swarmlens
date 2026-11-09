# Security

## Reporting a vulnerability

Please report it privately, not in a public issue: on GitHub use **Security →
Report a vulnerability** on this repository. Say what you found, how to
reproduce it and what you think it allows. Reports are read and answered, and
a confirmed vulnerability is fixed and released.

## Supported versions

The latest release.

## What swarmlens does with your cluster

swarmlens is a client with the credentials you give it, which usually means
full control of a swarm. Knowing what it shows, writes and runs is most of its
threat model.

**Credentials.** It uses what the docker CLI would: `DOCKER_HOST`, docker
contexts, TLS material, and the OpenSSH client for `ssh://`. It stores none
and reads no more than that. TLS verification is on unless a docker context
says `skip-tls-verify`. `ssh://` endpoints are reached through the system ssh,
so host keys are verified by OpenSSH; swarmlens runs it with `BatchMode` and
without a controlling terminal so that it can never ask a question on the
terminal it is drawing on.

**What it shows.** Describe, YAML and the spec editor show the whole service
spec, environment variables included, in clear, as `docker service inspect`
does. Config contents are shown. Secret values are never fetched (the engine
does not return them). Think before sharing a screen.

**What it writes.** Tables and logs you save (`ctrl-s`) go to
`$XDG_STATE_HOME/swarmlens`, readable by you alone (0600 in 0700). The spec
editor writes a temporary file for your editor, also 0600, and removes it.

**What it runs.** The `docker` CLI for shells into tasks, your `$EDITOR`,
the plugins you configure, and `ssh`. Plugin arguments are passed as an
argument vector, not through a shell; if a plugin of yours runs `sh -c`, a value
such as `$COL-ERROR` is text the cluster produced and is yours to quote.

**Untrusted data.** Container output, labels, names and error messages are
data from workloads that may be hostile. Only SGR (colour and style) sequences
reach the terminal: other escape sequences, control characters and the C1
range are dropped, and out-of-range colours are clamped. This is tested with a
table of hostile lines and a fuzz target. Log lines and log buffers are
bounded, so a task cannot make the viewer buffer without limit.

**Read-only.** `--readonly` (or `readOnly: true`) takes away every key that
changes the swarm or what runs in it, the shell included, and the backend
refuses changes however they are asked for (`api.ReadOnly`). Plugins are the
exception: they are commands you wrote, and run in read-only mode unless you
mark them `dangerous: true`.

## Known limits

- `ssh://` needs the OpenSSH client; there is no built-in one.
- The Docker client library, `github.com/moby/moby/client`, is still v0.x: it is
  updated through Dependabot and only when the tests, the real-swarm job
  included, pass. See CONTRIBUTING.md.
