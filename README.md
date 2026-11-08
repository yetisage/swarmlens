# swarmlens

A [k9s](https://k9scli.io)-style terminal UI for **Docker Swarm**. If you live in
k9s, this is the same cockpit pointed at a swarm: the same layout, the same
colours, the same keys, the same `:` prompt. Services are deployments, tasks
are pods, **stacks are namespaces**.

```
Context:  demo [RW]               <0>      all          <e> Edit       <enter> Tasks                       __    __
Swarm:    acme-prod (6/7 ready)   <1>      ci           <?> Help       <y>     YAML                       /  \__/  \  swarm
Node:     manager-1 (leader)      <2>      monitoring   <l> Logs                                          \__/  \__/  lens
SL Rev:   v0.5.0-dirty            <3>      shop         <r> Restart                                       /  \__/  \
Docker:   29.8.2                  <ctrl-d> Delete       <b> Rollback                                      \__/  \__/
Services: 12/14                   <d>      Describe     <s> Scale
Tasks:    28/30
┌─────────────────────────────────────────────────── services(all)[14] ────────────────────────────────────────────────────┐
│ STACK       NAME↑                     MODE            REPLICAS  IMAGE                 PORTS               UPDATE     AGE │
│ ci          ci_backup                 replicated-j…  0/1 (1/1)  acme/backup:3.2                                       9h │
│ ci          ci_legacy                 replicated           0/0  acme/legacy-ci:1.0                                  300d │
│ ci          ci_runner                 replicated           2/3  gitlab/gitlab-runne…                      updating   19d │
│ monitoring  monitoring_cadvisor       global               5/5  gcr.io/cadvisor/cad…                                 77d │
│ monitoring  monitoring_grafana        replicated           1/1  grafana/grafana:11.…  *:3000->3000/tcp               77d │
│ monitoring  monitoring_node-exporter  global               5/5  quay.io/prometheus/…                                 77d │
│ monitoring  monitoring_prometheus     replicated           1/1  prom/prometheus:v2.…  *:9090->9090/tcp               77d │
│             registry                  replicated           1/1  registry:2            *:5000->5000/tcp              200d │
│ shop        shop_api                  replicated           3/3  ghcr.io/acme/shop-a…                                 41d │
│ shop        shop_cache                replicated           1/1  redis:7.4-alpine                                    120d │
└──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┘
  <services>
                                                  😎  Viewing services...
```

Try it without a swarm: `swarmlens --demo` runs against an in-memory swarm with
failing tasks, a rolling update, drained and downed nodes, and three stacks.

## What carries over from k9s

| k9s | swarmlens |
|---|---|
| header: context / cluster / revs / CPU, MEM · key menu · logo | header: context / swarm / node / revs / services / tasks health · key menu · logo |
| framed table, title in the border: `pods(default)[12]` | `services(shop)[5]`, `tasks(shop_web)[4]`, `nodes(all)[7]` … |
| namespace, keys `0`–`9` | stack, keys `0`–`9`, `:svc shop`, `-n shop` |
| `:pods`, `:deploy`, aliases, `ctrl-a` | `:services`, `:svc`, `:nodes`, `:stacks`, … and your own `aliases.yaml` |
| `/regex` filter, `/!inverse` | the same, case-insensitive, live while you type |
| `shift-<col>` sort, `shift-o`, `ctrl-w` wide, `ctrl-z` faults | the same |
| `space` / `ctrl-space` / `ctrl-\` marks, bulk delete | the same, bulk delete / restart / cordon / drain |
| `d` describe, `y` YAML, `e` edit, `l` logs, `s` scale (deployments) / shell (pods), `ctrl-d` delete | all of them: `s` scales a service and opens a shell in a task |
| `enter` drills down: deployment → pods → logs | service → tasks → logs, node → its tasks, stack → its services |
| `[` `]` `-` history, `esc`/`q` back | the same |
| crumbs, flash line, the cow in the `<error>` dialog | the same, with a whale |
| delta colouring: new rows blue, changed rows green-yellow | the same, on every refresh |
| `plugins.yaml`, `aliases.yaml`, `config.yaml` | the same file formats (see below) |
| `--readonly`, `--headless`, `--logoless`, `--crumbsless`, `-c`, `-n`, `-r` | the same flags |
| `:ctx` | `:ctx` — docker contexts |

Rows are coloured by state: aqua is healthy, orange is on its way (starting,
updating, paused or drained), red is broken, gray is history or scaled to zero,
light green and blue are rows that just changed or appeared.

## Install & run

```sh
make build            # or: go build -o swarmlens .
./swarmlens           # the current docker context, like the docker CLI
./swarmlens --demo    # an in-memory swarm
./swarmlens -c nodes  # start in the nodes view
./swarmlens -n shop   # start in the "shop" stack
```

| Flag | |
|---|---|
| `-c, --command CMD` | run a `:` command at startup (`nodes`, `"services shop"`) |
| `-n, --stack NAME` | start in a stack |
| `--context NAME` | docker context to use |
| `-r, --refresh SECS` | refresh rate (default 2) |
| `--readonly` | disable every command that changes the swarm |
| `--headless`, `--logoless`, `--crumbsless`, `--no-icons` | hide parts of the chrome / emoji |
| `--demo` | in-memory demo swarm |
| `--render [--width N --height N]` | print one frame as plain text and exit |
| `--smoke`, `--smoke-mutate` | self-test against the daemon (`--smoke-mutate` creates, scales and removes a throwaway service) |
| `--debug-events` | stream the raw event feed |

**Connection.** swarmlens resolves the daemon like the docker CLI does:
`--context`, then `DOCKER_HOST`, then `DOCKER_CONTEXT`, then the current
context of `~/.docker/config.json`, then the local socket. `tcp://`, `unix://`,
`ssh://` and TLS (`DOCKER_TLS_VERIFY`/`DOCKER_CERT_PATH`, or the TLS material
of a context) work. Point it at a **manager**; workers do not serve the swarm
API and the app says so. `ssh://` endpoints use a built-in ssh client (agent
and `~/.docker/id_*` keys) that does not verify host keys.

## The `:` prompt

`:` opens the prompt (aqua border; `/` opens the green filter prompt). The
completion is shown inline: `tab` or `→` accepts it, `↑`/`↓` cycle through the
candidates, an empty prompt offers your history. `ctrl-u` clears, `esc` closes.

```
:services           :svc            :nodes   :no        :stacks   :st
:tasks              :ps             :networks :net      :volumes  :vol
:secrets            :sec            :configs :cfg       :events   :ev
:ctx                :ctx prod       :alias   ctrl-a     :help     ?
:svc shop           services of one stack      :svc all     every stack
:svc shop /web      … filtered                  :q  :quit  :q!  exit
```

An unknown command gets the `<error>` dialog, with a whale.

## Keys

`?` shows the bindings of the current view, generated from the code, so it is
always right. The header menu lists the ones that matter right now.

**Everywhere**

| | |
|---|---|
| `:` `/` `?` | command, filter, help |
| `esc` `q` | back (`esc` also clears a filter, marks and the faults view); at the root they do nothing — leave with `:q` or `ctrl-c` |
| `[` `]` `-` | history back / forward / the previous command |
| `ctrl-a` | all commands and aliases |
| `ctrl-r` · `ctrl-e` · `ctrl-g` | reload · toggle header · toggle crumbs |
| `j` `k` `g` `G` `ctrl-f` `ctrl-b` | move (arrows, `home`, `end`, page keys too) |

**Tables**

| | |
|---|---|
| `0` … `9` | all stacks / the stack in the menu |
| `shift-n` `shift-a` … | sort by that column, again for the other direction |
| `shift-←` `shift-→` · `shift-o` | pick a column · sort by it |
| `ctrl-w` · `ctrl-z` | wide columns · faults only |
| `space` · `ctrl-space` · `ctrl-\` | mark · mark a range · clear marks |
| `c` · `ctrl-s` | copy the name · save the table as CSV |

**Per resource**

| view | keys |
|---|---|
| services | `enter` tasks · `d` describe · `y` YAML · `e` edit the spec in `$EDITOR` · `l` logs of all its tasks · `s` scale · `r` restart (rolling) · `b` rollback · `ctrl-d` delete |
| tasks | `enter`/`l` logs · `d` · `y` · `s` shell (tasks on the node you are connected to) · `shift-j` jump to the service |
| nodes | `enter` its tasks · `d` · `y` · `c` cordon (pause) · `u` uncordon · `r` drain · `p` promote · `shift-d` demote · `ctrl-d` remove |
| stacks | `enter` its services · `d` · `y` · `ctrl-d` remove the stack |
| networks, volumes, secrets, configs | `d` · `y` · `ctrl-d` (secret values are never fetched; config contents are shown) |
| events | the live event feed of the daemon |
| contexts | `enter` switches the docker context |

**Describe / YAML**: `j`/`k`, `g`/`G`, `h`/`l`, `w` wrap, `f` fullscreen, `/` search with `n`/`N`, `c` copy.
**Logs**: `s` autoscroll, `t` timestamps, `w` wrap, `f` fullscreen, `/` regexp filter, `0` tail, `1`–`5` last 1m/5m/15m/30m/1h, `ctrl-s` save, `c` copy.
A service's logs are aggregated over its tasks, each line prefixed with the task and
coloured per task; container colours (ANSI) are kept.

Destructive actions ask first, and the dialog starts on **Cancel**, so a stray
`enter` deletes nothing. With marks set, an action applies to every marked row.

## Configuration

Optional files in `~/.config/swarmlens/` (or `$SWARMLENS_CONFIG_DIR`). The
formats are k9s'.

```yaml
# config.yaml
swarmlens:
  refreshRate: 2
  readOnly: false
  ui: {headless: false, logoless: false, crumbsless: false, noIcons: false}
  logger: {tail: 100, buffer: 5000, textWrap: false, showTime: false}
```

```yaml
# aliases.yaml
aliases:
  dp: services
  sec: secrets
```

```yaml
# plugins.yaml — the very file k9s reads works, with swarm resource names
plugins:
  svclogs:
    shortCut: Shift-L
    description: Service logs in Zellij
    scopes: [services]
    command: zellij-run
    args: [-f, -n, "Logs: $NAME", -c, "docker service logs -f $NAME"]
```

Plugin arguments can use `$NAME`, `$ID`, `$STACK`, `$CONTEXT`, `$ENDPOINT`,
`$FILTER` and `$COL-<COLUMN>` (e.g. `$COL-IMAGE`). `background: true` detaches
the command, `confirm: true` asks first, `dangerous: true` hides it in
read-only mode.

## How it is built

```
main.go                flags, config, wiring
internal/api/          the Docker Engine API as a typed Backend
  model.go             domain types and the Backend interface
  docker.go            connection (contexts, tcp, ssh, TLS), cluster summary
  list.go inspect.go   listings · describe and YAML
  logs.go events.go    log framing and aggregation · the event stream
  actions.go           scale, restart, rollback, drain, remove, stack removal, spec edit
  contexts.go          reads the docker context store
internal/demo/         an in-memory Backend that simulates a living swarm
internal/config/       config.yaml, aliases.yaml, plugins.yaml
internal/ui/           Bubble Tea program
  canvas.go            the cell grid every frame is drawn on
  app.go view.go       the model, the view stack, key routing
  table.go resources.go  the resource table and the resource definitions
  chrome.go            header, menu, logo, crumbs, flash
  prompt.go command.go the ":" and "/" prompts and the command interpreter
  textview.go logs.go help.go dialog.go  the other views and the modals
  theme.go             the palette (k9s' stock skin)
```

Bubble Tea runs the event loop; **drawing bypasses string layout**. Every frame
is composed on a grid of styled cells, which is what makes the frame
characters with the title in the border, the full-width cursor bar, modal
overlays over a live table and a fully painted background exact. The table
works like k9s' too: columns get their natural width and the spare room is
spread evenly; on narrow terminals columns are squeezed, then the least
useful ones (ports, update state) are dropped.

Live updates: the daemon's `/events` stream triggers a debounced refresh and
feeds the events view; a polling tick (default 2 s) is the fallback. Row
selection is tracked by id, so it survives refreshes that reorder rows.

## Development

```sh
make check                     # vet + tests
go test ./... -race
SWARMLENS_LIVE=1 go test ./internal/api -run Live -v   # read-only, against your daemon
go test ./internal/ui -run Golden -update              # refresh the golden screens
```

* `internal/ui/testdata/*.golden` are full screens of the demo swarm (frozen
  clock). They pin the layout; review their diff when one changes.
* The API tests run against an in-process fake daemon, covering parsing, the
  patch semantics of scale/drain/rollback, stack removal order, log framing and
  attribution, event filtering and reconnection.
* `scripts/termshot.py` runs the app in a pty, sends keys, and renders the
  emulated screen to text, a style dump and a PNG: handy to check colours and
  alignment (`pip install pyte pillow`; see `scripts/tour.script`).

## Docker 29 API notes

- `GET /swarm/watch` (the old live task/node stream) is **gone** (404). Live
  updates come from `GET /events`: swarm-scope `service`, `node`, `network`,
  `volume`, `secret` and `config` events plus `container` events of task
  containers. There are no `task/*` events, hence the polling fallback.
- `GET /services/{id}/tasks` is gone; tasks are listed through
  `GET /tasks?filters={"service":"…"}`.
- Task JSON carries no `Name`; names are derived as `<service>.<slot>`
  (`<service>.<node>` for global services).
- Service logs are multiplexed into 8-byte-header frames (unless the task has a
  TTY) and, with `details=1`, every line is prefixed with the task and node id:
  that is how aggregated logs are attributed.
- `docker service update --replicas=+1` is rejected by the CLI; scaling goes
  through the API with an absolute count.
- The engine normalises image references on decode (`nginx` → `nginx:latest`).
- Bubble Tea v1 asks the terminal for its background colour at start-up; a
  terminal that never answers delays the first frame.

## Not there (yet)

k9s skins, `:pulse` / `:xray`, port-forwarding (a swarm has no equivalent),
mouse support, `docker stack deploy`. swarmlens manages a running swarm; it
does not deploy to it.
