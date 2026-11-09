package api

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/moby/moby/api/types/swarm"
)

// frame builds a multiplexed log frame the way the engine does.
func frame(stream byte, payload string) []byte {
	b := make([]byte, 8+len(payload))
	b[0] = stream
	binary.BigEndian.PutUint32(b[4:8], uint32(len(payload)))
	copy(b[8:], payload)
	return b
}

type got struct {
	stderr bool
	line   string
}

func collect(t *testing.T, data []byte) []got {
	t.Helper()
	var out []got
	if err := readLogLines(bytes.NewReader(data), func(stderr bool, line string) { out = append(out, got{stderr, line}) }); err != nil {
		t.Fatalf("readLogLines: %v", err)
	}
	return out
}

func TestReadLogLinesMultiplexed(t *testing.T) {
	var data []byte
	data = append(data, frame(1, "stdout line\n")...)
	data = append(data, frame(2, "stderr line\n")...)
	data = append(data, frame(1, "second stdout\n")...)

	lines := collect(t, data)
	want := []got{{false, "stdout line"}, {true, "stderr line"}, {false, "second stdout"}}
	if len(lines) != len(want) {
		t.Fatalf("lines = %+v", lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %+v, want %+v", i, lines[i], want[i])
		}
	}
}

// A long line can arrive split over several frames, and a frame can hold
// several lines: lines must be reassembled per stream.
func TestReadLogLinesReassemblesAcrossFrames(t *testing.T) {
	var data []byte
	data = append(data, frame(1, "hel")...)
	data = append(data, frame(2, "err\n")...)
	data = append(data, frame(1, "lo\nsecond\nthi")...)
	data = append(data, frame(1, "rd")...) // unterminated: flushed at EOF

	lines := collect(t, data)
	want := []got{{true, "err"}, {false, "hello"}, {false, "second"}, {false, "third"}}
	if len(lines) != len(want) {
		t.Fatalf("lines = %+v", lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %+v, want %+v", i, lines[i], want[i])
		}
	}
}

// Tasks with a TTY stream plain text with no framing.
func TestReadLogLinesRawTTY(t *testing.T) {
	lines := collect(t, []byte("first\r\nsecond\n"))
	if len(lines) != 2 || lines[0].line != "first" || lines[1].line != "second" {
		t.Fatalf("lines = %+v", lines)
	}
}

func TestReadLogLinesEmptyAndTruncated(t *testing.T) {
	if lines := collect(t, nil); len(lines) != 0 {
		t.Fatalf("empty stream gave %+v", lines)
	}
	data := frame(1, "hello world\n")
	if lines := collect(t, data[:len(data)-4]); len(lines) != 0 {
		t.Fatalf("a truncated frame must not produce a line, got %+v", lines)
	}
}

func TestReadLogLinesRejectsCorruptHeader(t *testing.T) {
	bad := []byte{1, 0, 0, 0, 0xff, 0xff, 0xff, 0xff}
	if err := readLogLines(bytes.NewReader(bad), func(bool, string) {}); err == nil {
		t.Fatal("an absurd frame size must be reported")
	}
}

func TestParseLogLine(t *testing.T) {
	raw := "2026-11-08T15:31:03.978897636Z com.docker.swarm.node.id=nid,com.docker.swarm.service.id=sid,com.docker.swarm.task.id=tid hello  world"
	p := parseLogLine(raw)
	if p.taskID != "tid" || p.nodeID != "nid" {
		t.Errorf("ids = %q %q", p.taskID, p.nodeID)
	}
	if p.Text != "hello  world" {
		t.Errorf("text = %q (spacing in the message must survive)", p.Text)
	}
	if p.Time.Year() != 2026 || p.Time.Nanosecond() != 978897636 {
		t.Errorf("time = %v", p.Time)
	}

	plain := parseLogLine("2026-11-08T15:31:03Z just a message with a=b pair")
	if plain.taskID != "" || plain.Text != "just a message with a=b pair" {
		t.Errorf("plain = %+v", plain)
	}
	if noTime := parseLogLine("no timestamp here"); noTime.Text != "no timestamp here" {
		t.Errorf("noTime = %+v", noTime)
	}
}

func TestServiceLogsAreAttributedToTasks(t *testing.T) {
	details := func(task string) string {
		return "com.docker.swarm.node.id=n1,com.docker.swarm.service.id=svc1,com.docker.swarm.task.id=" + task
	}
	a := newTestAPI(t, route(t, map[string]http.HandlerFunc{
		"/services/svc1/logs": func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			if q.Get("details") != "1" || q.Get("timestamps") != "1" || q.Get("tail") != "50" {
				t.Errorf("unexpected log query %v", q)
			}
			// The client turns the duration into an absolute timestamp.
			since, err := strconv.ParseInt(q.Get("since"), 10, 64)
			if age := time.Now().Unix() - since; err != nil || age < 295 || age > 305 {
				t.Errorf("since = %q (age %ds), want about 5 minutes ago", q.Get("since"), age)
			}
			var buf bytes.Buffer
			buf.Write(frame(1, "2026-11-08T15:00:01Z "+details("t1")+" from one\n"))
			buf.Write(frame(2, "2026-11-08T15:00:02Z "+details("t2")+" from two\n"))
			_, _ = w.Write(buf.Bytes())
		},
		"/tasks": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, []swarm.Task{
				{ID: "t1", ServiceID: "svc1", Slot: 1, NodeID: "n1"},
				{ID: "t2", ServiceID: "svc1", Slot: 2, NodeID: "n1"},
			})
		},
		"/services": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, []swarm.Service{{ID: "svc1", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: "web"}}}})
		},
		"/nodes": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, []swarm.Node{{ID: "n1", Description: swarm.NodeDescription{Hostname: "worker-1"}}})
		},
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var all []LogLine
	err := a.Logs(ctx, LogTarget{Kind: KindService, ID: "svc1", Name: "web"}, LogOptions{Tail: 50, Since: 5 * time.Minute}, func(b []LogLine) { all = append(all, b...) })
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("lines = %+v", all)
	}
	if all[0].Source != "web.1" || all[0].Node != "worker-1" || all[0].Text != "from one" || all[0].Stderr {
		t.Errorf("line 0 = %+v", all[0])
	}
	if all[1].Source != "web.2" || !all[1].Stderr || !strings.HasPrefix(all[1].Time.Format(time.RFC3339), "2026-11-08T15:00:02") {
		t.Errorf("line 1 = %+v", all[1])
	}
}

// A task that never ends its lines must not make the reader buffer without
// limit: an overlong line comes out in pieces, and no piece splits a character.
func TestReadLogLinesBoundsLongLines(t *testing.T) {
	long := strings.Repeat("é", 100_000) // 200 kB of two-byte characters
	for name, stream := range map[string][]byte{
		"multiplexed": append(frame(1, long), frame(1, "\nnext\n")...),
		"tty":         []byte(long + "\nnext\n"),
	} {
		t.Run(name, func(t *testing.T) {
			var pieces []string
			if err := readLogLines(bytes.NewReader(stream), func(_ bool, l string) { pieces = append(pieces, l) }); err != nil {
				t.Fatal(err)
			}
			if len(pieces) < 4 || pieces[len(pieces)-1] != "next" {
				t.Fatalf("got %d lines, last %q", len(pieces), pieces[len(pieces)-1])
			}
			for _, p := range pieces[:len(pieces)-1] {
				if len(p) > maxLogLine {
					t.Errorf("a line of %d bytes, limit %d", len(p), maxLogLine)
				}
				if !utf8.ValidString(p) {
					t.Error("a character was split between two lines")
				}
			}
			if got := strings.Join(pieces[:len(pieces)-1], ""); got != long {
				t.Errorf("text changed: %d bytes in, %d out", len(long), len(got))
			}
		})
	}
}

// 8 MiB without a newline (a progress bar redrawn with \r, say) used to be
// held in memory and delivered as a single line.
func TestReadLogLinesNeverBuffersWithoutLimit(t *testing.T) {
	var data []byte
	chunk := strings.Repeat("x", 1<<20)
	for i := 0; i < 8; i++ {
		data = append(data, frame(1, chunk)...)
	}
	total, longest := 0, 0
	if err := readLogLines(bytes.NewReader(data), func(_ bool, l string) {
		total += len(l)
		longest = max(longest, len(l))
	}); err != nil {
		t.Fatal(err)
	}
	if longest > maxLogLine || total != 8<<20 {
		t.Errorf("longest line %d bytes (limit %d), %d of %d bytes delivered", longest, maxLogLine, total, 8<<20)
	}
}

// failAfter yields its data, then fails with err.
type failAfter struct {
	data []byte
	err  error
}

func (f *failAfter) Read(p []byte) (int, error) {
	if len(f.data) == 0 {
		return 0, f.err
	}
	n := copy(p, f.data)
	f.data = f.data[n:]
	return n, nil
}

// A stream that stops in the middle of a frame is the daemon going away and
// ends quietly; a read error is not, and must reach the caller.
func TestReadLogLinesReportsReadErrors(t *testing.T) {
	boom := errors.New("connection reset by peer")
	frameData := frame(1, "hello world\n")
	for name, c := range map[string]struct {
		data    []byte
		wantErr error
	}{
		"mid payload":   {frameData[:len(frameData)-3], boom},
		"mid header":    {frameData[:5], boom},
		"between lines": {frame(1, "one\n"), boom},
	} {
		t.Run(name, func(t *testing.T) {
			err := readLogLines(&failAfter{data: c.data, err: c.wantErr}, func(bool, string) {})
			if !errors.Is(err, boom) {
				t.Errorf("err = %v, want the read error", err)
			}
		})
	}
	// truncation by EOF stays quiet
	if err := readLogLines(bytes.NewReader(frameData[:len(frameData)-3]), func(bool, string) {}); err != nil {
		t.Errorf("a truncated stream must end quietly, got %v", err)
	}
}
