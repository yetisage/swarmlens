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

**Navigation** — following k9s, the `:` prompt is a **command interpreter for
navigation**, not a menu of actions. It switches resource views and can
carry a filter; every action lives on a key instead:

```
:service       :svc         switch to services
:node          :no          switch to nodes
:volume        :vol         switch to volumes
:network       :net         switch to networks
:secret        :sec         switch to secrets
:config        :cfg         switch to configs
:service /web  switch to services filtered by "web"
:service /!web keep everything that does NOT match
```

The prompt uses k9s' fish-style **inline suggestions**: typing `:vol` shows
`ume` as ghost text, `tab` (or `→`/`ctrl+f`) accepts it and `↑`/`↓` cycle
through the candidates. An empty prompt offers the command history.
`ctrl+w`/`ctrl+u` clear it, `enter` or `ctrl+e` runs it.

**Context-sensitive actions** — the same key does different things depending
on the resource view. The bottom bar shows the keys that apply right now,
in k9s' `<key> Description` format (k9s' "active keyboard mnemonics"):

| View | Keys |
|---|---|
| services | `d` describe · `s` scale · `u` update · `R` rollback · `ctrl+d` delete |
| nodes | `D` drain · `A` activate · `p` promote · `w` demote · `ctrl+d` delete |
| tasks | `l` logs |
| logs | `f` follow |
| volumes / networks / secrets / configs | `ctrl+d` delete |

The bindings follow k9s' conventions: `ctrl-d` deletes, `ctrl+r` refreshes,
`d` describes, `l` shows logs. Destructive actions open a confirmation
dialog whose focus starts on **Cancel**, so a stray `enter` cannot delete
anything (`tab` moves the focus, `y`/`n` also work).

**Chrome** — a breadcrumb trail under the header (`services › demo › task`)
shows where you are; `ctrl+g` toggles it. `[` goes back, `]` goes forward
and `-` returns to the newest view, walking the same history k9s keeps in
its command buffer.

Global: `:` command · `/` filter · `ctrl+r` refresh · `ctrl+a` aliases ·
`ctrl+u` clear filter · `ctrl+g` crumbs · `[` `]` `-` history · `?` help ·
`esc`/`b` back · `q` quit.

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
:            command prompt (switch resource, e.g. :svc, :node /filter)
↑↓           navigate        d        describe      l  logs        f  follow
/            filter          s        scale         u  update      R  rollback
ctrl+d       delete          ctrl+r   refresh       ctrl+a aliases
D / A        drain/activate  p / w    promote/demote
[ ] -        history         ctrl+u   clear filter  ctrl+g crumbs
esc/b        back            ?        help          q  quit
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
  command.go          the ":" command interpreter + resource alias table
  commandview.go      the command prompt and its key handling
  crumbs.go           breadcrumb trail + the view history
  navigate.go         history navigation (back / forward / last)
  aliases.go          the alias table overlay (ctrl+a)
  prompt.go           value prompt (scale) + confirmation dialog
  help.go             the contextual help overlay
  styles.go           adaptive light/dark theme, state colours
  table.go            ANSI-aware table renderer (see note below)
  views.go            table columns and coloured row builders
  commands.go         async loaders, follow, action runner
  actions.go          action handlers (scale, update, drain, …)
  confirm.go          overlay placement helpers
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
