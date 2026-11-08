package api

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"strings"

	"github.com/docker/docker/api/types/container"
)

// TaskLogs fetches the tail of a task's container logs (demultiplexed).
func (a *API) TaskLogs(ctx context.Context, taskID, tail string) (string, error) {
	rc, err := a.cli.TaskLogs(ctx, taskID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Timestamps: true,
		Tail:       tail,
	})
	if err != nil {
		return "", err
	}
	data, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		return "", err
	}
	return DemuxLogs(data), nil
}

// FollowTaskLogs streams a task's logs, calling onLine for each line as it
// arrives. It blocks until the stream ends, ctx is canceled, or the reader
// errors.
func (a *API) FollowTaskLogs(ctx context.Context, taskID, tail string, onLine func(string)) error {
	rc, err := a.cli.TaskLogs(ctx, taskID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Timestamps: true,
		Follow:     true,
		Tail:       tail,
	})
	if err != nil {
		return err
	}
	defer rc.Close()

	pr, pw := io.Pipe()
	go func() {
		_, _ = io.Copy(pw, rc)
		pw.Close()
	}()

	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		onLine(sc.Text())
	}
	return sc.Err()
}

// DemuxLogs strips the 8-byte std stream multiplexing header that the
// engine prepends to each frame when a container has no TTY.
func DemuxLogs(data []byte) string {
	if len(data) < 8 || (data[0] != 1 && data[0] != 2) {
		return string(data)
	}
	var b strings.Builder
	for len(data) >= 8 {
		typ, n := data[0], int(binary.BigEndian.Uint32(data[4:8]))
		if typ != 1 && typ != 2 {
			b.Write(data)
			break
		}
		if 8+n > len(data) {
			b.Write(data)
			break
		}
		b.Write(data[8 : 8+n])
		data = data[8+n:]
	}
	return b.String()
}
