package api

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	ev "github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"
)

// taskIDLabel is put by swarmkit on every container that runs a task, so it
// is in the attributes of that container's events. It tells task containers
// from the other containers of the host without knowing their ids beforehand:
// a new container's first events (create, start) come before any listing
// could have seen it.
const taskIDLabel = "com.docker.swarm.task.id"

// backoffPolicy paces the reconnects of the event stream.
type backoffPolicy struct {
	initial, max time.Duration
	// healthy is how long a stream that delivered nothing must have stayed
	// up to count as having worked.
	healthy time.Duration
}

var defaultBackoff = backoffPolicy{initial: 500 * time.Millisecond, max: 15 * time.Second, healthy: 10 * time.Second}

// step returns how long to wait before reconnecting and the delay to use
// after that. A stream that worked starts over from the short delay: its drop
// is a new incident, not the continuation of the last one.
func (p backoffPolicy) step(delay time.Duration, worked bool) (wait, next time.Duration) {
	if worked || delay < p.initial {
		delay = p.initial
	}
	return delay, min(delay*2, p.max)
}

// Events implements Backend. It subscribes to the daemon's /events stream
// and delivers swarm-relevant events to emit, reconnecting with backoff
// until ctx is cancelled, so a daemon restart does not silently end live
// updates. Stream failures are reported as events of type "error".
func (a *API) Events(ctx context.Context, emit func(Event)) error {
	policy := a.reconnect
	if policy.initial <= 0 {
		policy = defaultBackoff
	}
	delay := policy.initial
	for {
		started := time.Now()
		received, err := a.streamOnce(ctx, emit)
		if ctx.Err() != nil {
			return nil
		}
		// A plain EOF is the daemon closing the stream (restart, idle
		// timeout); reconnecting quietly is the right response. Anything
		// else is worth telling the user about.
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			emit(Event{Time: time.Now(), Type: "error", Action: "stream", Attrs: map[string]string{"message": err.Error()}})
		}
		worked := received > 0 || time.Since(started) >= policy.healthy
		var wait time.Duration
		wait, delay = policy.step(delay, worked)
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-t.C:
		}
	}
}

// streamOnce runs one connection to the event stream and says how many
// messages it carried.
func (a *API) streamOnce(ctx context.Context, emit func(Event)) (received int, err error) {
	stream := a.cli.Events(ctx, client.EventsListOptions{
		Filters: make(client.Filters).Add("type", "service", "node", "container", "network", "volume", "secret", "config"),
	})
	msgs, errs := stream.Messages, stream.Err
	for {
		select {
		case <-ctx.Done():
			return received, nil
		case err := <-errs:
			return received, err
		case m, ok := <-msgs:
			if !ok {
				return received, nil
			}
			received++
			if !relevant(m) {
				continue
			}
			emit(toEvent(m))
		}
	}
}

// relevant keeps the events that say something about the swarm. Events of
// containers that do not run swarm tasks are dropped, and so is the churn that
// every container causes (mounts, network attachments, exec sessions): it
// would flood the events view and trigger refreshes for nothing.
func relevant(m ev.Message) bool {
	action := string(m.Action)
	switch m.Type {
	case "container":
		if strings.HasPrefix(action, "exec_") || action == "attach" || action == "resize" {
			return false
		}
		return m.Actor.Attributes[taskIDLabel] != ""
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
