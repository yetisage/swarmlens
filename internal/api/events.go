package api

import (
	"context"
	"errors"
	"time"

	ev "github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
)

// Event is one engine event.
type Event = ev.Message

// EventFilter decides whether an event is interesting for the UI.
type EventFilter func(Event) bool

// EventStream subscribes to the daemon's /events stream and delivers
// relevant events to onEvent. It reconnects with backoff until stop is
// closed or ctx is canceled, so a daemon restart does not silently end
// live updates.
func (a *API) EventStream(ctx context.Context, stop <-chan struct{}, filter EventFilter, onEvent func(Event)) error {
	backoff := 500 * time.Millisecond
	const maxBackoff = 15 * time.Second

	for {
		err := a.streamOnce(ctx, stop, filter, onEvent)
		if ctx.Err() != nil {
			return nil
		}
		select {
		case <-stop:
			return nil
		case <-ctx.Done():
			return nil
		default:
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			// Report the failure but keep retrying.
			onEvent(Event{Type: "error", Action: "stream", Actor: ev.Actor{Attributes: map[string]string{"message": err.Error()}}})
		}
		select {
		case <-stop:
			return nil
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

// streamOnce runs a single subscription to the events endpoint. It returns
// as soon as stop is closed, even while the stream is healthy.
func (a *API) streamOnce(ctx context.Context, stop <-chan struct{}, filter EventFilter, onEvent func(Event)) error {
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		select {
		case <-stop:
			cancel()
		case <-ctx.Done():
			cancel()
		case <-cctx.Done():
		}
	}()

	msgs, errs := a.cli.Events(cctx, ev.ListOptions{
		Filters: filters.NewArgs(
			filters.Arg("type", "service"),
			filters.Arg("type", "container"),
			filters.Arg("type", "node"),
		),
	})
	for {
		select {
		case <-cctx.Done():
			return nil
		case err := <-errs:
			if err == nil {
				return nil
			}
			return err
		case msg, ok := <-msgs:
			if !ok {
				return nil
			}
			if filter != nil && !filter(msg) {
				continue
			}
			onEvent(msg)
		}
	}
}

// DefaultFilter keeps swarm-scope service events and container events that
// belong to known swarm task containers, ignoring unrelated host activity.
func (a *API) DefaultFilter() EventFilter {
	return func(msg Event) bool {
		switch msg.Type {
		case "service":
			return true
		case "node":
			return true
		case "container":
			// Only surface containers we know are swarm task containers.
			return a.isSwarmContainer(msg.Actor.ID)
		default:
			return false
		}
	}
}
