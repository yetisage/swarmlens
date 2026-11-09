package api

import (
	"context"
	"errors"
)

// ErrReadOnly is what every mutation of a read-only backend returns.
var ErrReadOnly = errors.New("read-only mode: the swarm cannot be changed")

// ReadOnly wraps b so that it can be read and followed but not changed,
// whoever asks. The UI also hides the keys that change things in read-only
// mode; this is the guarantee behind that, and it does not depend on every
// action remembering to say that it changes something.
func ReadOnly(b Backend) Backend {
	if r, ok := b.(*readOnly); ok {
		return r
	}
	return &readOnly{Reader: b, Streamer: b, inner: b}
}

type readOnly struct {
	Reader
	Streamer
	inner Backend
}

var (
	_ Backend   = (*readOnly)(nil)
	_ Switcher  = (*readOnly)(nil)
	_ Simulated = (*readOnly)(nil)
)

func (*readOnly) ScaleService(context.Context, string, int) error { return ErrReadOnly }
func (*readOnly) RestartService(context.Context, string) error    { return ErrReadOnly }
func (*readOnly) RollbackService(context.Context, string) error   { return ErrReadOnly }
func (*readOnly) SetNodeAvailability(context.Context, string, NodeAvailability) error {
	return ErrReadOnly
}
func (*readOnly) SetNodeRole(context.Context, string, NodeRole) error { return ErrReadOnly }
func (*readOnly) Remove(context.Context, Kind, string) error          { return ErrReadOnly }
func (*readOnly) RemoveStack(context.Context, string) error           { return ErrReadOnly }
func (*readOnly) UpdateServiceSpec(context.Context, string, uint64, string) error {
	return ErrReadOnly
}

// Close releases the wrapped backend.
func (r *readOnly) Close() error { return r.inner.Close() }

// Contexts passes the optional Switcher capability through.
func (r *readOnly) Contexts() ([]ContextInfo, error) {
	if s, ok := r.inner.(Switcher); ok {
		return s.Contexts()
	}
	return nil, errors.New("this backend has no docker contexts")
}

// Simulated passes the optional Simulated capability through.
func (r *readOnly) Simulated() bool {
	s, ok := r.inner.(Simulated)
	return ok && s.Simulated()
}
