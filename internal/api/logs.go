package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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
	defer func() { _ = rc.Close() }()

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

// maxLogLine bounds one line of a log stream. A task that never ends its
// lines (a progress bar redrawn with \r, a binary dump) must not make us
// buffer without limit: such a line comes out in pieces of this size.
const maxLogLine = 64 << 10

// lineSplitter cuts a byte stream into lines of bounded length.
type lineSplitter struct{ buf []byte }

// cutPoint is where to cut an overlong line: at the limit, without splitting
// a character.
func cutPoint(b []byte) int {
	cut := maxLogLine
	for cut > maxLogLine-utf8.UTFMax && !utf8.RuneStart(b[cut]) {
		cut--
	}
	return cut
}

// write adds data and emits every complete line, and pieces of an overlong one.
func (l *lineSplitter) write(p []byte, emit func(string)) {
	l.buf = append(l.buf, p...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			break
		}
		line := l.buf[:i]
		for len(line) > maxLogLine {
			cut := cutPoint(line)
			emit(string(line[:cut]))
			line = line[cut:]
		}
		emit(strings.TrimRight(string(line), "\r"))
		l.buf = l.buf[i+1:]
	}
	for len(l.buf) > maxLogLine {
		cut := cutPoint(l.buf)
		emit(string(l.buf[:cut]))
		l.buf = l.buf[cut:]
	}
	switch {
	case len(l.buf) == 0:
		l.buf = nil
	case cap(l.buf) > 4*len(l.buf)+4096:
		l.buf = append([]byte(nil), l.buf...) // let go of what was consumed
	}
}

// flush emits what is left when the stream ends.
func (l *lineSplitter) flush(emit func(string)) {
	if len(l.buf) > 0 {
		emit(strings.TrimRight(string(l.buf), "\r"))
		l.buf = nil
	}
}

// readLogLines reads a log stream and calls onLine for every line. The
// engine multiplexes stdout and stderr into 8-byte-header frames unless the
// task has a TTY, in which case the stream is plain text; both are handled.
// A stream that ends in the middle of a frame ends quietly (the daemon went
// away); any other read error is returned.
func readLogLines(r io.Reader, onLine func(stderr bool, line string)) error {
	br := bufio.NewReaderSize(r, 64*1024)
	head, err := br.Peek(8)
	if err != nil && len(head) == 0 {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	if !looksMultiplexed(head) {
		var split lineSplitter
		emit := func(line string) { onLine(false, line) }
		chunk := make([]byte, 32*1024)
		for {
			n, err := br.Read(chunk)
			split.write(chunk[:n], emit)
			if err != nil {
				split.flush(emit)
				if errors.Is(err, io.EOF) {
					return nil
				}
				return err
			}
		}
	}

	var split [3]lineSplitter // by stream: stdin, stdout, stderr
	flushAll := func() {
		for stream := range split {
			split[stream].flush(func(line string) { onLine(stream == 2, line) })
		}
	}
	end := func(err error) error {
		flushAll()
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil
		}
		return err
	}
	hdr := make([]byte, 8)
	for {
		if _, err := io.ReadFull(br, hdr); err != nil {
			return end(err)
		}
		stream, n := hdr[0], binary.BigEndian.Uint32(hdr[4:8])
		if stream > 2 || n > 8<<20 {
			return fmt.Errorf("corrupt log stream (stream %d, %d bytes)", stream, n)
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(br, payload); err != nil {
			return end(err)
		}
		split[stream].write(payload, func(line string) { onLine(stream == 2, line) })
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
