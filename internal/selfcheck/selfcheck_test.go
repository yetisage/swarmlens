package selfcheck

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yetisage/swarmlens/internal/api"
	"github.com/yetisage/swarmlens/internal/demo"
)

var frozen = time.Date(2026, 11, 8, 15, 0, 0, 0, time.UTC)

func smoke(t *testing.T, be api.Backend, o Options) (int, string) {
	t.Helper()
	if o.EventWindow == 0 {
		o.EventWindow = 50 * time.Millisecond
	}
	var out bytes.Buffer
	code := Smoke(context.Background(), &out, be, o)
	return code, out.String()
}

func TestSmokePassesOnAHealthySwarm(t *testing.T) {
	code, out := smoke(t, demo.NewStatic(frozen), Options{})
	if code != 0 || !strings.Contains(out, "PASSED") {
		t.Fatalf("code %d\n%s", code, out)
	}
	for _, want := range []string{"swarm: active role=manager", "✓ services", "✓ nodes", "✓ describe ", "✓ yaml ", "✓ logs ", "stream closed cleanly"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing from\n%s", want, out)
		}
	}
	if strings.Contains(out, "mutating") {
		t.Error("nothing may be changed without --smoke-mutate")
	}
}

func TestSmokeMutateLeavesNothingBehind(t *testing.T) {
	be := demo.NewStatic(frozen)
	before, _ := be.Services(context.Background())
	code, out := smoke(t, be, Options{Mutate: true, Image: "example/img:1", Now: func() time.Time { return frozen }})
	if code != 0 {
		t.Fatalf("code %d\n%s", code, out)
	}
	for _, want := range []string{"✓ create swarmlens-smoke-", "✓ scale to 2", "✓ restart", "✓ remove swarmlens-smoke-"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing from\n%s", want, out)
		}
	}
	// the removal comes before the verdict, not after it
	if strings.Index(out, "✓ remove") > strings.Index(out, "PASSED") {
		t.Error("the cleanup must be reported before the verdict")
	}
	after, _ := be.Services(context.Background())
	if len(after) != len(before) {
		t.Errorf("%d services before, %d after: the throwaway service was left behind", len(before), len(after))
	}
}

// failing makes chosen calls fail.
type failing struct {
	api.Backend
	services, remove error
	summary          *api.Summary
	summaryErr       error
}

func (f failing) Services(ctx context.Context) ([]api.Service, error) {
	if f.services != nil {
		return nil, f.services
	}
	return f.Backend.Services(ctx)
}

func (f failing) Remove(ctx context.Context, k api.Kind, id string) error {
	if f.remove != nil {
		return f.remove
	}
	return f.Backend.Remove(ctx, k, id)
}

func (f failing) Summary(ctx context.Context) (*api.Summary, error) {
	if f.summaryErr != nil || f.summary != nil {
		return f.summary, f.summaryErr
	}
	return f.Backend.Summary(ctx)
}

func (f failing) CreateService(ctx context.Context, name, image string, n int) error {
	return f.Backend.(api.ServiceCreator).CreateService(ctx, name, image, n)
}

func TestSmokeFailsWhenACheckFails(t *testing.T) {
	be := failing{Backend: demo.NewStatic(frozen), services: errors.New("boom")}
	code, out := smoke(t, be, Options{})
	if code != 1 || !strings.Contains(out, "✗ services") || !strings.Contains(out, "FAILED: 1 check(s)") || strings.Contains(out, "PASSED") {
		t.Errorf("code %d\n%s", code, out)
	}
}

// A throwaway service that cannot be removed must fail the run: it used to
// be reported after PASSED, and not count.
func TestSmokeFailsWhenTheCleanupFails(t *testing.T) {
	be := failing{Backend: demo.NewStatic(frozen), remove: errors.New("no permission")}
	code, out := smoke(t, be, Options{Mutate: true})
	if code != 1 || !strings.Contains(out, "✗ remove swarmlens-smoke-") || strings.Contains(out, "PASSED") {
		t.Errorf("code %d\n%s", code, out)
	}
}

func TestSmokeMutateIsSkippedWhereServicesCannotBeCreated(t *testing.T) {
	code, out := smoke(t, api.ReadOnly(demo.NewStatic(frozen)), Options{Mutate: true})
	if code != 0 || !strings.Contains(out, "skipped") || strings.Contains(out, "✓ create") {
		t.Errorf("code %d\n%s", code, out)
	}
}

func TestSmokeReportsAnUnreachableDaemonAndANonManager(t *testing.T) {
	code, out := smoke(t, failing{Backend: demo.NewStatic(frozen), summaryErr: errors.New("connection refused")}, Options{})
	if code != 1 || !strings.Contains(out, "FAIL: connection refused") {
		t.Errorf("unreachable: code %d\n%s", code, out)
	}
	worker := &api.Summary{Context: "w", SwarmState: "active", Role: "worker"}
	code, out = smoke(t, failing{Backend: demo.NewStatic(frozen), summary: worker}, Options{})
	if code != 0 || !strings.Contains(out, "not a swarm manager") || strings.Contains(out, "API checks") {
		t.Errorf("worker: code %d\n%s", code, out)
	}
}

func TestDebugEventsStreamsWhatHappens(t *testing.T) {
	be := demo.NewStatic(frozen)
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = be.RestartService(context.Background(), "shop_web")
	}()
	var out bytes.Buffer
	if code := DebugEvents(context.Background(), &out, be, 600*time.Millisecond); code != 0 {
		t.Errorf("code %d", code)
	}
	for _, want := range []string{"streaming /events", "service", "update", "shop_web", "stream ended: 1 events"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("%q missing from\n%s", want, out.String())
		}
	}
}
