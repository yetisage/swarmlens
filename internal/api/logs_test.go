package api

import (
	"encoding/binary"
	"testing"
)

// frame builds a multiplexed log frame the way the engine does.
func frame(stream byte, payload string) []byte {
	b := make([]byte, 8+len(payload))
	b[0] = stream
	binary.BigEndian.PutUint32(b[4:8], uint32(len(payload)))
	copy(b[8:], payload)
	return b
}

func TestDemuxLogsPlainText(t *testing.T) {
	// No TTY: the payload arrives raw.
	got := DemuxLogs([]byte("hello\nworld\n"))
	if got != "hello\nworld\n" {
		t.Fatalf("got %q, want %q", got, "hello\nworld\n")
	}
}

func TestDemuxLogsMultiplexed(t *testing.T) {
	var data []byte
	data = append(data, frame(1, "stdout line\n")...)
	data = append(data, frame(2, "stderr line\n")...)
	data = append(data, frame(1, "second stdout\n")...)

	got := DemuxLogs(data)
	want := "stdout line\nstderr line\nsecond stdout\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDemuxLogsTruncatedFrame(t *testing.T) {
	// A frame header claiming more bytes than are present must not panic
	// and must not lose data.
	data := frame(1, "hello")
	data = data[:len(data)-2]
	got := DemuxLogs(data)
	if got == "" {
		t.Fatal("expected partial payload to be preserved")
	}
}

func TestDemuxLogsShortInput(t *testing.T) {
	if got := DemuxLogs([]byte{1, 2}); got != "\x01\x02" {
		t.Fatalf("short input mangled: %q", got)
	}
	if got := DemuxLogs(nil); got != "" {
		t.Fatalf("nil input should be empty, got %q", got)
	}
}
