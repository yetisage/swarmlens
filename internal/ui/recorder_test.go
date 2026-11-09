package ui

import (
	"context"

	"github.com/yetisage/swarmlens/internal/api"
)

// recorder wraps a backend, remembers the mutations asked of it and can make
// calls on chosen ids fail.
type recorder struct {
	api.Backend
	calls []string
	fail  map[string]error // by id
}

func (r *recorder) note(what, id string) error {
	r.calls = append(r.calls, what+" "+id)
	return r.fail[id]
}

func (r *recorder) SetNodeAvailability(ctx context.Context, id string, a api.NodeAvailability) error {
	if err := r.note("availability:"+string(a), id); err != nil {
		return err
	}
	return r.Backend.SetNodeAvailability(ctx, id, a)
}

func (r *recorder) Remove(ctx context.Context, kind api.Kind, id string) error {
	if err := r.note("remove:"+string(kind), id); err != nil {
		return err
	}
	return r.Backend.Remove(ctx, kind, id)
}
