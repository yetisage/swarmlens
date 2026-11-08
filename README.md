# swarmlens

A k9s-like terminal UI for **Docker Swarm**, written in Go on the
charmbracelet stack (Bubble Tea + Bubbles + Lip Gloss) — the same architecture
k9s uses.

```
 swarmlens · default  node: active  role: manager  unix:///var/run/docker.sock
 NAME                        IMAGE                           REPLICAS      MODE              UPDATED
 demo                        registry:2                      2/2           replicated        none
```

## Why this exists

There is no established TUI for swarm management: `lazydocker` is
host/container-centric (no services/nodes), and the swarm dashboards that do
exist (Portainer, Swarm Visualizer) are web UIs. swarmlens brings the k9s
mental model — resource tables, detail views, streaming logs, keyboard-driven
actions — to `docker service` / `docker node`.

## Features

**Views** — services · nodes · service detail (tasks) · task logs · volumes ·
networks · secrets · configs.

**Live updates** — the daemon's `/events` stream (swarm-scope `service/*`
events plus `container/*` lifecycle events for task containers) drives
debounced table refreshes. The stream **reconnects with backoff**, so a
daemon restart does not silently kill live updates; a 5 s polling tick is the
fallback (and the only source on worker nodes).

**Actions** (all guarded by a confirmation modal where destructive):

| Key | Action |
|---|---|
| `+` / `-` | scale the service up / down by one replica |
| `u` | force a rolling update of the service |
| `R` | roll the service back to its previous spec |
| `x` | remove the service (or the selected node / secret / config) |
| `D` / `A` | drain / activate the selected node |
| `p` / `w` | promote / demote the selected node |

**Navigation** — like k9s, resource types are reached through the **command
palette** (`:`, or `:` plus a short alias such as `svc`, `no`, `vol`, `net`,
`sec`, `cfg`), not through a dedicated key per resource. The letter keys
stay free for actions whose meaning follows the current view.

**Context-sensitive actions** — the same key does different things depending
on the resource view, and the footer shows the keys that apply right now:

| View | Keys |
|---|---|
| services | `d` detail · `+`/`-` scale · `u` update · `R` rollback · `x` remove |
| nodes | `D` drain · `A` activate · `p` promote · `w` demote · `x` remove |
| tasks | `l` logs |
| logs | `f` follow |
| volumes / networks / secrets / configs | `x` remove |

Global: `:` commands · `/` search · `r` refresh · `?` help · `esc`/`b` back ·
`q` quit.

**Robustness** — selection is tracked by **id**, so it survives live
refreshes that reorder rows; the layout is computed from the terminal size;
secret *values* are never fetched or displayed.

## Install & run

```sh
make build          # or: go build -ldflags "-X main.version=$(git describe)" -o swarmlens .
./swarmlens
```

Flags:

| Flag | Purpose |
|---|---|
| `--smoke` | non-interactive self-test (never mutates the swarm) |
| `--smoke-mutate` | self-test that creates and scales a **throwaway** service and cleans it up |
| `--render [--width --height]` | print one static frame and exit |
| `--debug-events` | stream raw `/events` and exit |
| `--version` | print the version |

`DOCKER_HOST` supports `unix://` (default), `tcp://`, `ssh://`, and TLS
(`DOCKER_TLS_VERIFY` + `DOCKER_CERT_PATH`). Point it at a swarm manager to
manage a remote swarm; worker nodes fall back to polling.

## Keys

```
:            command palette (switch resource, run actions)
↑↓           navigate        enter/d  detail        l  logs        f  follow
/            search          r        refresh       ?  help        esc/b back
+ / -        scale           u        update        R  rollback    x  remove
D / A        drain/activate  p / w    promote/demote
```

## Architecture

```
main.go               flags, lifecycle, events→program wiring, self-test
internal/api/         Docker Engine API facade
  client.go           connection (unix/tcp/ssh/TLS), node cache, id helpers
  api.go              services, nodes, tasks
  actions.go          scale, force-update, rollback, remove, node ops
  resources.go        volumes, networks, secrets, configs
  events.go           reconnecting /events stream + relevance filter
  logs.go             log tailing, streaming follow, demultiplexing
  ssh.go              ssh:// endpoint support
internal/ui/          Bubble Tea program
  app.go              root model, screens, layout, key dispatch
  keys.go             global keymap + the viewAction type
  viewactions.go      per-view, context-sensitive actions
  styles.go           adaptive light/dark theme, state colours
  table.go            ANSI-aware table renderer (see note below)
  views.go            table columns and coloured row builders
  commands.go         async loaders, follow, action runner
  actions.go          action handlers (scale, update, drain, …)
  palette.go          command palette + help overlay
  confirm.go          confirmation modal + overlay placement
internal/api/*_test.go  API tests against an in-process fake daemon
internal/ui/*_test.go   model tests (selection stability, filtering, keys)
pty_harness.py          drives the TUI in a pty with keystrokes
Makefile                build / test / vet / smoke targets
.github/workflows/      CI: gofmt, vet, race tests, build
```

### Testing

```sh
make test        # go test ./... -count=1
make check       # vet + test
```

The API tests run against an in-process fake Docker daemon, so they cover
service parsing, task naming, patch semantics for scale/drain, the event
filter, and stream reconnection/stop behaviour without needing a swarm.

`pty_harness.py` drives the real binary in a pty (setting the window size,
sending keystrokes) to verify the interactive views.

## Docker 29 API notes (learned while building this)

- `GET /swarm/watch` (the old live task/node stream) is **gone** — 404.
  Live updates now come from `GET /events`: swarm-scope `service/*` events
  plus `container/*` events for service task containers. There are no
  `task/*` or `node/*` event types anymore, hence the polling fallback for
  node-state changes.
- `GET /services/{id}/tasks` is gone; tasks are listed via the top-level
  `GET /tasks?filters={"service":"…"}` (client: `TaskList`).
- Task JSON no longer carries a `Name` field; display names are derived as
  `<service>.<slot>`.
- `docker service scale name=+1` (relative scaling) is rejected by the
  Docker 29 CLI (`strconv.ParseUint: "+1"`); absolute values work. Scaling is
  done via the API (`ServiceUpdate`) in this codebase.
- Container logs for tasks are multiplexed (no TTY) and need the 8-byte
  std-stream header stripped before display.
- The swarm API normalizes image references on decode (`nginx` → `nginx:latest`).

## Why swarmlens has its own table renderer

`bubbles/table` truncates cell values **byte-wise**. When a cell contains
ANSI colour codes, the escape sequence eats into the column budget and gets
cut in half:

- a date in a 26-wide column rendered as `2025-06…` instead of `2025-06-01`,
  because the 19-byte escape sequence counted against the 26 bytes;
- in narrower columns the escape itself was truncated, producing an invalid
  sequence that left the terminal's colour state set. Plain cells carry no
  escape of their own, so they **inherited** that colour from the previous
  row — which is why some rows rendered dim while the first row and the row
  below the cursor rendered in the default colour.

`internal/ui/table.go` truncates and pads with ANSI-aware operations
(`x/ansi`), so escape sequences survive intact, every cell ends with a reset,
and nothing leaks between rows. The selected row is marked with a background
colour. `table_test.go` locks this behaviour in.

## Roadmap

- TLS/`ssh://` endpoint hardening (key-based auth is implemented; agent and
  `~/.docker/id_*` keys are supported).
- Aggregated logs for all tasks of a service; log filtering.
- Service update history / rollback browser.
- `docker stack` (compose) integration.
- Contexts: switch between multiple swarms from inside the TUI.
- Column sorting and per-column filters.
