package api

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// spy is a backend that counts what changes.
type spy struct {
	Backend
	mutations int
	closed    bool
}

func (s *spy) ContextName() string { return "spy" }
func (s *spy) Close() error        { s.closed = true; return nil }
func (s *spy) ScaleService(context.Context, string, int) error {
	s.mutations++
	return nil
}
func (s *spy) RestartService(context.Context, string) error {
	s.mutations++
	return nil
}
func (s *spy) RollbackService(context.Context, string) error {
	s.mutations++
	return nil
}
func (s *spy) SetNodeAvailability(context.Context, string, NodeAvailability) error {
	s.mutations++
	return nil
}
func (s *spy) SetNodeRole(context.Context, string, NodeRole) error {
	s.mutations++
	return nil
}
func (s *spy) Remove(context.Context, Kind, string) error {
	s.mutations++
	return nil
}
func (s *spy) RemoveStack(context.Context, string) error {
	s.mutations++
	return nil
}
func (s *spy) UpdateServiceSpec(context.Context, string, uint64, string) error {
	s.mutations++
	return nil
}

// Every method of Mutator, found by reflection so that one added later is
// covered without anyone having to remember this test.
func TestReadOnlyRefusesEveryMutation(t *testing.T) {
	inner := &spy{}
	ro := ReadOnly(inner)

	mt := reflect.TypeOf((*Mutator)(nil)).Elem()
	rv := reflect.ValueOf(ro)
	if mt.NumMethod() == 0 {
		t.Fatal("no mutators found")
	}
	for i := 0; i < mt.NumMethod(); i++ {
		m := mt.Method(i)
		args := make([]reflect.Value, m.Type.NumIn())
		for j := range args {
			args[j] = reflect.Zero(m.Type.In(j))
		}
		out := rv.MethodByName(m.Name).Call(args)
		if err, _ := out[0].Interface().(error); !errors.Is(err, ErrReadOnly) {
			t.Errorf("%s returned %v, want ErrReadOnly", m.Name, err)
		}
	}
	if inner.mutations != 0 {
		t.Errorf("%d mutations reached the wrapped backend", inner.mutations)
	}
}

func TestReadOnlyPassesReadsAndClosesThrough(t *testing.T) {
	inner := &spy{}
	ro := ReadOnly(inner)
	if ro.ContextName() != "spy" {
		t.Error("reads must reach the wrapped backend")
	}
	if ReadOnly(ro) != ro {
		t.Error("wrapping twice must not stack wrappers")
	}
	if err := ro.Close(); err != nil || !inner.closed {
		t.Errorf("Close = %v, wrapped backend closed = %v", err, inner.closed)
	}
}

// The wrapper keeps the optional capabilities of what it wraps: the UI asks
// for them with type assertions on the backend it holds.
func TestReadOnlyKeepsOptionalCapabilities(t *testing.T) {
	ro := ReadOnly(&spy{})
	sw, ok := ro.(Switcher)
	if !ok {
		t.Fatal("the wrapper must be a Switcher")
	}
	if _, err := sw.Contexts(); err == nil {
		t.Error("a backend without contexts must say so")
	}
	if s, ok := ro.(Simulated); !ok || s.Simulated() {
		t.Errorf("a real backend must not look simulated (ok=%v)", ok)
	}
}
