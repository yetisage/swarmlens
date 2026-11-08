package demo

import (
	"context"
	"fmt"
	"hash/fnv"
	"math/rand"
	"sort"
	"time"

	"swarmlens/internal/api"
)

// Logs implements api.Backend with generated, service-flavoured output: a
// backlog first, then (when following) a few lines per tick.
func (b *Backend) Logs(ctx context.Context, t api.LogTarget, o api.LogOptions, emit func([]api.LogLine)) error {
	b.mu.Lock()
	b.advance()
	var tasks []task
	kind := "plain"
	for _, tk := range b.tasks {
		switch {
		case t.Kind == api.KindTask && tk.ID == t.ID:
			tasks = append(tasks, *tk)
		case t.Kind == api.KindService && tk.ServiceID == t.ID && tk.State == "running":
			tasks = append(tasks, *tk)
		}
		if tk.ServiceID != "" && (tk.ID == t.ID || tk.ServiceID == t.ID) {
			if s := b.svcByID(tk.ServiceID); s != nil {
				kind = s.logKind
			}
		}
	}
	now := b.clock()
	b.mu.Unlock()

	if len(tasks) == 0 {
		if !o.Follow {
			return nil
		}
		<-ctx.Done()
		return nil
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Name < tasks[j].Name })

	h := fnv.New64a()
	_, _ = h.Write([]byte(t.ID))
	rng := rand.New(rand.NewSource(int64(h.Sum64())))

	// Backlog.
	n := o.Tail
	if n <= 0 {
		n = 300
	}
	step := 900 * time.Millisecond
	if o.Since > 0 {
		n = min(int(o.Since/step), 600)
	}
	lines := make([]api.LogLine, 0, n)
	for i := n; i > 0; i-- {
		tk := tasks[rng.Intn(len(tasks))]
		lines = append(lines, genLine(rng, kind, tk.Task, now.Add(-time.Duration(i)*step)))
	}
	if len(lines) > 0 {
		emit(lines)
	}
	if !o.Follow {
		return nil
	}

	tick := time.NewTicker(350 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			at := b.clock()
			var batch []api.LogLine
			for i, k := 0, 1+rng.Intn(3); i < k; i++ {
				tk := tasks[rng.Intn(len(tasks))]
				batch = append(batch, genLine(rng, kind, tk.Task, at))
			}
			emit(batch)
		}
	}
}

var paths = []string{"/", "/api/products", "/api/products/42", "/api/cart", "/static/app.js", "/static/app.css", "/healthz", "/api/orders?limit=20", "/login", "/favicon.ico"}

func pick[T any](rng *rand.Rand, xs ...T) T { return xs[rng.Intn(len(xs))] }

func genLine(rng *rand.Rand, kind string, t api.Task, at time.Time) api.LogLine {
	l := api.LogLine{Time: at, Source: t.Name, Node: t.Node}
	ts := at.UTC()
	switch kind {
	case "nginx":
		status := pick(rng, 200, 200, 200, 200, 200, 304, 404, 500)
		l.Text = fmt.Sprintf(`10.0.1.%d - - [%s] "GET %s HTTP/1.1" %d %d "-" "Mozilla/5.0"`,
			20+rng.Intn(200), ts.Format("02/Jan/2006:15:04:05 -0700"), pick(rng, paths...), status, 200+rng.Intn(9000))
		l.Stderr = status >= 500
	case "json":
		lvl := pick(rng, "info", "info", "info", "info", "warn", "error")
		l.Text = fmt.Sprintf(`{"level":"%s","ts":"%s","msg":"%s","path":"%s","status":%d,"dur_ms":%d}`,
			lvl, ts.Format(time.RFC3339), pick(rng, "request completed", "cache miss", "upstream slow", "request completed"),
			pick(rng, paths...), pick(rng, 200, 200, 201, 404, 502), 2+rng.Intn(180))
		l.Stderr = lvl == "error"
	case "ansi":
		switch rng.Intn(6) {
		case 0:
			l.Text = fmt.Sprintf("\x1b[33mWARN\x1b[0m  job %d took longer than expected (\x1b[1m%dms\x1b[0m)", 4000+rng.Intn(900), 800+rng.Intn(2000))
		case 1:
			l.Text = fmt.Sprintf("\x1b[31mERROR\x1b[0m job %d failed: connection reset by peer", 4000+rng.Intn(900))
			l.Stderr = true
		default:
			l.Text = fmt.Sprintf("\x1b[32mINFO\x1b[0m  processed job %d in \x1b[36m%dms\x1b[0m", 4000+rng.Intn(900), 5+rng.Intn(300))
		}
	case "postgres":
		l.Text = fmt.Sprintf("%s UTC [%d] %s", ts.Format("2006-01-02 15:04:05.000"), 60+rng.Intn(40),
			pick(rng, "LOG:  checkpoint starting: time", "LOG:  checkpoint complete: wrote 42 buffers (0.3%)", "LOG:  automatic vacuum of table \"shop.public.orders\": index scans: 1", "LOG:  connection received: host=10.0.2.7"))
	default:
		l.Text = fmt.Sprintf(`time="%s" level=%s msg="%s"`, ts.Format(time.RFC3339),
			pick(rng, "info", "info", "info", "warning"), pick(rng, "scrape finished", "compacting blocks", "listening on [::]:9090", "health check ok"))
	}
	return l
}
