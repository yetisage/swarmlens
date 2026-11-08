package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
)

// Logs implements Backend. For a service it aggregates the logs of all its
// tasks, the way `docker service logs` does; every line is attributed to
// the task that wrote it. Lines are delivered in batches so a busy service
// does not flood the UI.
func (a *API) Logs(ctx context.Context, t LogTarget, o LogOptions, emit func([]LogLine)) error {
	opts := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Timestamps: true,
		Details:    true, // gives the task id of every line
		Follow:     o.Follow,
		Tail:       "all",
	}
	if o.Tail > 0 {
		opts.Tail = strconv.Itoa(o.Tail)
	}
	if o.Since > 0 {
		opts.Since = o.Since.String()
	}

	var (
		rc  io.ReadCloser
		err error
	)
	switch t.Kind {
	case KindService:
		rc, err = a.cli.ServiceLogs(ctx, t.ID, opts)
	case KindTask:
		rc, err = a.cli.TaskLogs(ctx, t.ID, opts)
	default:
		return fmt.Errorf("no logs for a %s", t.Kind)
	}
	if err != nil {
		return err
	}
	defer rc.Close()

	res := &taskResolver{a: a, ctx: ctx, serviceID: t.ID, names: map[string]taskLabel{}}
	if t.Kind == KindTask {
		res.serviceID = ""
		res.names[t.ID] = taskLabel{name: t.Name}
	} else {
		res.refresh()
	}

	lines := make(chan LogLine, 512)
	var readErr error
	go func() {
		defer close(lines)
		readErr = readLogLines(rc, func(stderr bool, raw string) {
			l := parseLogLine(raw)
			l.Stderr = stderr
			if l.taskID != "" {
				lbl := res.lookup(l.taskID, l.nodeID)
				l.Source, l.Node = lbl.name, lbl.node
			}
			select {
			case lines <- l.LogLine:
			case <-ctx.Done():
			}
		})
	}()

	tick := time.NewTicker(40 * time.Millisecond)
	defer tick.Stop()
	var batch []LogLine
	flush := func() {
		if len(batch) > 0 {
			// Tasks stream independently, so a service's backlog arrives
			// interleaved; put each batch back in time order.
			sort.SliceStable(batch, func(i, j int) bool { return batch[i].Time.Before(batch[j].Time) })
			emit(batch)
			batch = nil
		}
	}
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				flush()
				if ctx.Err() != nil {
					return nil
				}
				return readErr
			}
			batch = append(batch, l)
			if len(batch) >= 200 {
				flush()
			}
		case <-tick.C:
			flush()
		case <-ctx.Done():
			return nil
		}
	}
}

// parsedLine carries the attributes of a log line that are resolved to
// names before the line reaches the UI.
type parsedLine struct {
	LogLine
	taskID string
	nodeID string
}

// parseLogLine splits "<timestamp> <details> <message>". The details are a
// comma separated list of URL-escaped key=value pairs added by the engine
// when details are requested.
func parseLogLine(raw string) parsedLine {
	var p parsedLine
	rest := raw
	if i := strings.IndexByte(rest, ' '); i > 0 {
		if ts, err := time.Parse(time.RFC3339Nano, rest[:i]); err == nil {
			p.Time = ts
			rest = rest[i+1:]
		}
	}
	if i := strings.IndexByte(rest, ' '); i > 0 && strings.Contains(rest[:i], "com.docker.swarm.") {
		for _, kv := range strings.Split(rest[:i], ",") {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				continue
			}
			k, _ = url.QueryUnescape(k)
			v, _ = url.QueryUnescape(v)
			switch k {
			case "com.docker.swarm.task.id":
				p.taskID = v
			case "com.docker.swarm.node.id":
				p.nodeID = v
			}
		}
		rest = rest[i+1:]
	}
	p.Text = rest
	return p
}

// readLogLines reads a log stream and calls onLine for every line. The
// engine multiplexes stdout and stderr into 8-byte-header frames unless the
// task has a TTY, in which case the stream is plain text; both are handled.
func readLogLines(r io.Reader, onLine func(stderr bool, line string)) error {
	br := bufio.NewReaderSize(r, 64*1024)
	head, err := br.Peek(8)
	if err != nil && len(head) == 0 {
		if err == io.EOF {
			return nil
		}
		return err
	}
	if !looksMultiplexed(head) {
		sc := bufio.NewScanner(br)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			onLine(false, strings.TrimRight(sc.Text(), "\r"))
		}
		return sc.Err()
	}

	var pending [3][]byte
	hdr := make([]byte, 8)
	for {
		if _, err := io.ReadFull(br, hdr); err != nil {
			for stream, rest := range pending {
				if len(rest) > 0 {
					onLine(stream == 2, strings.TrimRight(string(rest), "\r"))
				}
			}
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil
			}
			return err
		}
		stream, n := hdr[0], binary.BigEndian.Uint32(hdr[4:8])
		if stream > 2 || n > 8<<20 {
			return fmt.Errorf("corrupt log stream (stream %d, %d bytes)", stream, n)
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(br, payload); err != nil {
			return nil
		}
		pending[stream] = append(pending[stream], payload...)
		for {
			i := bytes.IndexByte(pending[stream], '\n')
			if i < 0 {
				break
			}
			onLine(stream == 2, strings.TrimRight(string(pending[stream][:i]), "\r"))
			pending[stream] = pending[stream][i+1:]
		}
	}
}

func looksMultiplexed(h []byte) bool {
	return len(h) >= 8 && h[0] <= 2 && h[1] == 0 && h[2] == 0 && h[3] == 0
}

// taskLabel is how a task is named in a log prefix.
type taskLabel struct {
	name string
	node string
}

// taskResolver maps the task ids found in log details to readable names.
// Tasks appear while following, so unknown ids trigger a (rate limited)
// refresh.
type taskResolver struct {
	a         *API
	ctx       context.Context
	serviceID string

	mu        sync.Mutex
	names     map[string]taskLabel
	refreshed time.Time
}

func (r *taskResolver) refresh() {
	if r.serviceID == "" {
		return
	}
	tasks, err := r.a.Tasks(r.ctx, TaskQuery{ServiceID: r.serviceID})
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refreshed = time.Now()
	if err != nil {
		return
	}
	for _, t := range tasks {
		r.names[t.ID] = taskLabel{name: t.Name, node: t.Node}
	}
}

func (r *taskResolver) lookup(taskID, nodeID string) taskLabel {
	r.mu.Lock()
	lbl, ok := r.names[taskID]
	stale := time.Since(r.refreshed) > time.Second
	r.mu.Unlock()
	if !ok && stale {
		r.refresh()
		r.mu.Lock()
		lbl, ok = r.names[taskID]
		r.mu.Unlock()
	}
	if !ok {
		lbl = taskLabel{name: ShortID(taskID)}
	}
	if lbl.node == "" && nodeID != "" {
		r.a.mu.RLock()
		lbl.node = r.a.nodeNames[nodeID]
		r.a.mu.RUnlock()
	}
	return lbl
}
