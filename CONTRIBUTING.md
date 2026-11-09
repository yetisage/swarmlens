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

**The Docker client is `github.com/docker/docker` v28.5.2.** That is the last
release on that path; the maintained client is `github.com/moby/moby/client`.
It was not adopted because it is still v0.x (eight releases in a short time, so
minor versions can break), its API has a different shape from top to bottom
(every call takes an options struct and returns a result struct; 63 call sites
across 31 methods here), and the current client works with Docker 29 through API
version negotiation, which the recorded fixtures and the real-swarm CI job
exercise. Revisit it when `moby/moby/client` reaches v1.0, when the old module
needs a security fix, or when a Docker API change cannot be handled by
negotiation. The tests are written against the HTTP API, not the client, so
they will tell you if the move changes behaviour.

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
