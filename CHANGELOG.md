# Changelog

All notable changes are listed here, newest first. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

Everything since v0.6.0. Several changes are breaking; they are marked.

### Breaking

- **The Go module is `github.com/yetisage/swarmlens`.** It was called just
  `swarmlens`, so `go install github.com/yetisage/swarmlens@latest` failed and
  the v0.6.0 tag could not be used as a Go module at all.
- **`ssh://` endpoints run the system's OpenSSH client**, as the docker CLI
  does, instead of a built-in client that did not verify host keys. Host keys
  are now verified, and `~/.ssh/config` (aliases, `ProxyJump`, `IdentityFile`,
  `ControlMaster`), agents and hardware keys work. In return: `ssh` must be on
  the `PATH`, swarmlens cannot ask questions (it runs ssh with `BatchMode`, so
  use `ssh-agent` and trust the host once with `ssh <host>`), and the user is
  no longer defaulted to `root`.
- **Config files reject unknown keys.** `config.yaml`, `aliases.yaml` and
  `plugins.yaml` used to ignore them, so `readonly: true` instead of
  `readOnly: true` left the session read-write without a word. The error names
  the line and suggests the nearest real key. `plugins.yaml` still accepts the
  k9s-only fields `override`, `pipes` and `overwriteOutput` (and ignores them).
- **`--readonly` also takes away the shell** into a task's container, which
  changes whatever it is used for, and the backend itself refuses every change
  (`api.ReadOnly`), however it is asked for.

### Fixed

- A log line with an out-of-range colour (`ESC[38;2;-5;0;0m`) crashed the whole
  UI, and again every time the logs were reopened while it was in view. Any
  container could cause it. SGR colours are clamped, incomplete ones skipped,
  and the colon forms (`38:2::r:g:b`) understood.
- `swarmlens -c help` and `-c aliases` panicked at start-up, and a mistyped
  `-c foo` left an empty screen that panicked on the first tick. Start commands
  that cannot be the root of the view stack now open on top of the default
  view, as k9s falls back to its default.
- Marking several nodes and draining them did nothing: confirming "Drain 2
  nodes?" opened a second dialog that the key handler closed again. It asks
  once and drains every marked node.
- Bulk actions and bulk delete stopped silently at the first error. They go on
  (like `docker service rm a b c`) and report "N of M failed: …".
- The log view crashed on 32-bit builds (linux/arm, 386).
- The event stream's reconnect delay never came back down after a drop: after a
  few blips every reconnect waited 15 s. It starts over after a stream that
  worked.
- The create and start events of new task containers were dropped (task
  containers were recognised by ids learned from listings). They are recognised
  by the `com.docker.swarm.task.id` label, from their first event, and the set
  of ids that grew for ever is gone.
- One new event flagged all 500 rows of the events view as changed once the
  buffer was full, and moved the selection.
- `--context default` and `:ctx default` ignored `DOCKER_HOST` while the
  contexts table showed it.
- A task that never ended its lines made the log reader buffer without limit
  (48 MiB into one line in a test); lines are bounded (64 KiB, cut between
  characters) and a log view keeps at most 32 MiB. Read errors are reported
  instead of being taken for a clean end.
- Lines still in flight from a restarted log stream no longer end up in the new
  one.
- Removing a stack lists everything before removing anything, so a failing
  listing no longer leaves it half removed, and an object that is already gone
  counts as removed.
- Plugin variables are expanded in one pass: a value containing `$NAME` is not
  expanded again.
- A describe or YAML view that failed to load removed whatever view was on top
  instead of itself.
- `DOCKER_TLS_VERIFY` no longer requires a client certificate: the CA is
  required, `cert.pem` and `key.pem` are used if they are there.
- `--smoke-mutate` printed its cleanup after PASSED and a failed cleanup did not
  count.
- Saved tables and logs were readable by every user; they are 0600 in 0700
  directories now.

### Added

- `--smoke-image` chooses the image of the `--smoke-mutate` service.
- Releases: tagging `vX.Y.Z` builds archives for Linux, macOS, Windows and
  FreeBSD (amd64, arm64, armv7, 386) with checksums, a build attestation and the
  third-party license notices.
- CI: static checks (gofmt, tidy, vet, golangci-lint), the suite with the race
  detector and in random order, the suite on 32-bit and macOS, cross-builds for
  nine platforms, and a job that runs the code against a real swarm. A weekly
  `govulncheck`. Dependabot for modules and actions.
- `SECURITY.md`, `CONTRIBUTING.md`, a Makefile that mirrors CI (`make ci`).

### Changed

- `api.Backend` is `Reader` + `Streamer` + `Mutator` + `Close`. `api.ReadOnly`
  wraps a backend and refuses every `Mutator` method; adding one stops it from
  compiling until it says what to do with it.
- `main` is a thin, tested `run()`; `--smoke` and `--debug-events` live in
  `internal/selfcheck`.
- Test coverage: `internal/api` 46% → 86%, `main` 0% → 89%, `internal/ui`
  85% → 89%. The real describe/YAML reports are golden files made from API
  responses recorded from a Docker 29.9.2 swarm.
- golangci-lint is clean; dead code is gone.
- `golang.org/x/crypto` is no longer a dependency.

### Not done, on purpose

- Moving from `github.com/docker/docker` (v28.5.2, the last release on that
  path) to `github.com/moby/moby/client`: it is still v0.x, its API is a
  different shape from top to bottom (63 call sites, 31 methods) and moving
  between its minor versions may break, while the current client works with
  Docker 29 through API negotiation. See CONTRIBUTING.md for when to revisit.

## [0.6.0]

The k9s-style redesign: framed tables, the `:` prompt, crumbs, logs, YAML and
describe views, marks and bulk actions, plugins, aliases, contexts, a demo
swarm.
