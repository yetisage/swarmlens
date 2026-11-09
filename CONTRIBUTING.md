# Contributing

## The one design rule

swarmlens is k9s for Docker Swarm. **Where there is a k9s behaviour, mirror it**
(keys, layout, colours, the `:` prompt, config file formats), **unless it is
plainly worse**: a start command that crashes or leaves a dead screen, a bulk
action that stops silently at the first error, a config typo that is silently
ignored, a read-only mode with a shell in it. In those cases do the better
thing and say so in the commit.

## Getting set up

You need Go 1.27.1 or newer (`go.mod` says which; a `go` that is older fetches
the right toolchain by itself) and, to try it against something real, Docker.

```sh
make build        # the binary
make demo         # run it against the in-memory swarm
make check        # vet + tests: the quick gate
make ci           # everything CI runs, except the real-swarm job
```

`make ci` is gofmt, `go mod tidy -diff`, `go vet`, golangci-lint, the suite with
the race detector in random order, the suite on 32-bit (`GOARCH=386`, where
`int` is 32 bits wide) and a build and vet for the nine platforms the releases
cover. golangci-lint is built with the Go version of `go.mod`: a prebuilt one
compiled with an older Go refuses to analyse the module.

## Tests

- `internal/ui` drives the real model with key presses against the demo swarm
  (`helpers_test.go`: `press`, `command`, `screenText`). The golden screens in
  `internal/ui/testdata` pin the layout: refresh them with
  `go test ./internal/ui -run Golden -update` and **read the diff**.
- `internal/api` runs against an in-process fake daemon, and against API
  responses recorded from a real Docker 29.9.2 swarm
  (`internal/api/testdata/docker-29.9.2`, see the README there). The describe
  and YAML reports are golden files: `go test ./internal/api -run Recorded
  -update`, then read the diff.
- The ssh transport is tested with stand-in `ssh` programs and a helper process
  that plays `docker system dial-stdio`.
- Input that comes from containers is hostile: when you touch what turns
  container output into cells (`ansiSegs`, `painter.text`), add the case to
  `hostileLines` in `internal/ui/text_test.go`.
- A bug fix comes with a test that fails without it.
- `make live` (or `SWARMLENS_LIVE=1 go test ./internal/api -run 'TestLive$'`)
  runs the read-only API tests against the daemon you point `DOCKER_HOST` at.
  CI does it against a throwaway swarm, and then `swarmlens --smoke
  --smoke-mutate`, which creates, scales and removes a service.

## Commits

Conventional commit subjects (`fix(ui): …`, `feat(api): …`, `test: …`,
`ci: …`), with a body that says *why*. A breaking change gets a `!` and a
`BREAKING CHANGE:` footer: the changelog and the release notes are made from
them. Add what users will notice to `CHANGELOG.md`.

## Releasing

Tag `vX.Y.Z` on `main`. `.github/workflows/release.yml` runs the tests, builds
the archives with GoReleaser (`.goreleaser.yaml`), publishes the release with
checksums and attests where the files came from. To try it without publishing:

```sh
goreleaser release --snapshot --clean --skip=publish
```

## Decisions worth knowing

**The Docker client is `github.com/moby/moby/client`**, with the API types of
`github.com/moby/moby/api`. It replaced `github.com/docker/docker`, whose module
path ends at v28.5.2: the vulnerability database lists Moby daemon advisories
against that path, for the whole module and with no fixed version ever, so every
scan of swarmlens failed on bugs of a daemon that it does not contain. The new
client is still v0.x, so its minor releases may break its API. What contains
that:

- Only `internal/api` imports it; the UI sees `api.Backend`.
- The tests are written against the HTTP API, not the client: recorded responses
  of a real Docker 29.9.2 swarm (API 1.56), golden reports, TLS and ssh against
  fakes, and the real-swarm CI job. They tell you if an update changes behaviour.
- Scale and restart rebuild the update from the client's types, which would
  drop a field the types do not have. `keepsEverything` refuses to send such an
  update, and `TestRecordedServicesSurviveTheirTypes` fails an update of the
  client whose types no longer cover what the engine sends.
- The oldest daemon is pinned in `minAPIVersion`: API 1.41, Docker Engine 20.10,
  because the engine reports how many tasks of a service run from there on (the
  client itself goes down to 1.40). `TestTheMinimumDaemonIsWhatTheDocsPromise`
  fails when an update raises the client's own floor above it.
- Dependabot updates `github.com/moby/moby/*` together, apart from the other
  modules.

To update it by hand: `go get github.com/moby/moby/client@vX
github.com/moby/moby/api@vY`, `go mod tidy`, `make ci` and, with a daemon at
hand, `make live`. When a newer API adds something swarmlens shows, record the
fixtures again (the README in `internal/api/testdata/docker-29.9.2`).

**`ssh://` goes through the system `ssh`.** A built-in client has to reimplement
host key verification, `~/.ssh/config`, agents, hardware keys and passphrases,
and the first version of it skipped host key verification. OpenSSH does all of
that, and it is what the docker CLI does.

**Read-only is enforced by the backend.** `api.ReadOnly` refuses every method of
`api.Mutator`, so a new action cannot forget to say that it changes something.
Hiding the keys is the UX; the wrapper is the guarantee.

**Resources are package-level values wired in `init()`** (`internal/ui/resources.go`)
because the actions of one resource drill into another, which Go would reject as
an initialisation cycle. It works and is tested; moving the registry onto the
app would let tests run in parallel, and has not been worth the churn yet.
