package api

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	ev "github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
)

// Events implements Backend. It subscribes to the daemon's /events stream
// and delivers swarm-relevant events to emit, reconnecting with backoff
// until ctx is cancelled, so a daemon restart does not silently end live
// updates. Stream failures are reported as events of type "error".
func (a *API) Events(ctx context.Context, emit func(Event)) error {
	backoff := 500 * time.Millisecond
	const maxBackoff = 15 * time.Second

	for {
		err := a.streamOnce(ctx, emit)
		if ctx.Err() != nil {
			return nil
		}
		// A plain EOF is the daemon closing the stream (restart, idle
		// timeout); reconnecting quietly is the right response. Anything
		// else is worth telling the user about.
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			emit(Event{Time: time.Now(), Type: "error", Action: "stream", Attrs: map[string]string{"message": err.Error()}})
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

func (a *API) streamOnce(ctx context.Context, emit func(Event)) error {
	msgs, errs := a.cli.Events(ctx, ev.ListOptions{
		Filters: filters.NewArgs(
			filters.Arg("type", "service"),
			filters.Arg("type", "node"),
			filters.Arg("type", "container"),
			filters.Arg("type", "network"),
			filters.Arg("type", "volume"),
			filters.Arg("type", "secret"),
			filters.Arg("type", "config"),
		),
	})
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errs:
			return err
		case m, ok := <-msgs:
			if !ok {
				return nil
			}
			if !a.relevant(m) {
				continue
			}
			emit(toEvent(m))
		}
	}
}

// relevant keeps the events that say something about the swarm. Container
// events of unrelated host containers are dropped, and so is the churn that
// every container causes (mounts, network attachments, exec sessions): it
// would flood the events view and trigger refreshes for nothing.
func (a *API) relevant(m ev.Message) bool {
	action := string(m.Action)
	switch m.Type {
	case "container":
		if strings.HasPrefix(action, "exec_") || action == "attach" || action == "resize" {
			return false
		}
		return a.isSwarmContainer(m.Actor.ID)
	case "network", "volume":
		switch action {
		case "create", "destroy", "remove", "prune":
			return true
		}
		return false
	}
	return true
}

func toEvent(m ev.Message) Event {
	e := Event{
		Type:   string(m.Type),
		Action: string(m.Action),
		ID:     m.Actor.ID,
		Attrs:  m.Actor.Attributes,
	}
	if m.TimeNano != 0 {
		e.Time = time.Unix(0, m.TimeNano)
	} else {
		e.Time = time.Unix(m.Time, 0)
	}
	e.Name = m.Actor.Attributes["name"]
	if e.Name == "" {
		e.Name = ShortID(m.Actor.ID)
	}
	e.Stack = m.Actor.Attributes[StackLabel]
	// Some actions carry a detail ("health_status: healthy"): the verb stays
	// the action and the detail becomes an attribute.
	if verb, detail, ok := strings.Cut(e.Action, ": "); ok {
		e.Action = verb
		e.Attrs = mergeLabels(e.Attrs, map[string]string{"detail": detail})
	}
	return e
}
