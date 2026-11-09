package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/moby/moby/client"
	"github.com/moby/moby/client/pkg/versions"
)

// daemonOfVersion is a fake daemon that says it speaks the given API version
// and records every path it is asked for.
type daemonOfVersion struct {
	mu   sync.Mutex
	seen []string
}

func (d *daemonOfVersion) paths() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.seen...)
}

// newNegotiatingAPI connects an API to a fake daemon of the given API version,
// with the client left to negotiate, as it does against a real daemon.
func newNegotiatingAPI(t *testing.T, version string) (*API, *daemonOfVersion) {
	t.Helper()
	d := &daemonOfVersion{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		d.seen = append(d.seen, r.URL.Path)
		d.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("Api-Version", version)
			w.Header().Set("Ostype", "linux")
			if r.Method != http.MethodHead {
				_, _ = w.Write([]byte("OK"))
			}
		case strings.HasSuffix(r.URL.Path, "/services"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[]"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	cli, err := client.New(client.WithHost("tcp://" + srv.Listener.Addr().String()))
	if err != nil {
		t.Fatal(err)
	}
	return NewWithClient(cli), d
}

// requestedPrefix is the version prefix of the path asked for services.
func requestedPrefix(d *daemonOfVersion) string {
	for _, p := range d.paths() {
		if strings.HasSuffix(p, "/services") {
			return strings.TrimSuffix(p, "services")
		}
	}
	return ""
}

func TestSpeaksTheVersionOfTheDaemon(t *testing.T) {
	newest := "/v" + client.MaxAPIVersion + "/"
	for _, tc := range []struct{ daemon, want string }{
		{"1.41", "/v1.41/"}, // the oldest daemon that is supported
		{"1.44", "/v1.44/"}, // an older one: the client asks for what it knows
		{"1.51", "/v1.51/"}, // what the clients before it asked of Docker 29
		{client.MaxAPIVersion, newest},
		{"1.99", newest}, // a daemon newer than the client: the client's own newest
	} {
		t.Run("daemon "+tc.daemon, func(t *testing.T) {
			a, d := newNegotiatingAPI(t, tc.daemon)
			if _, err := a.Services(t.Context()); err != nil {
				t.Fatalf("Services: %v", err)
			}
			if got := requestedPrefix(d); got != tc.want {
				t.Errorf("asked the daemon of API %s with %q, want %q", tc.daemon, got, tc.want)
			}
		})
	}
}

func TestRefusesADaemonOlderThanTheMinimum(t *testing.T) {
	// 1.40 is below the minimum of swarmlens but not of the client: the engine
	// has no service status yet, so every service would read "0 running".
	for _, version := range []string{"1.39", "1.40"} {
		t.Run("daemon "+version, func(t *testing.T) {
			a, d := newNegotiatingAPI(t, version)
			_, err := a.Summary(t.Context())
			if err == nil {
				t.Fatal("expected an error from a daemon older than the minimum")
			}
			for _, want := range []string{version, minAPIVersion, "Docker Engine " + minDockerEngine} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the error should say %q: %v", want, err)
				}
			}
			for _, p := range d.paths() {
				if !strings.HasSuffix(p, "/_ping") {
					t.Errorf("asked a daemon that is too old for %s", p)
				}
			}
		})
	}
}

// The README and the changelog promise Docker Engine 20.10 or newer, which is
// API 1.41. When an update of the client raises its own floor above that this
// fails: either accept it on purpose (minAPIVersion, README, CHANGELOG, this
// test) or hold the update back.
func TestTheMinimumDaemonIsWhatTheDocsPromise(t *testing.T) {
	if minAPIVersion != "1.41" || minDockerEngine != "20.10" {
		t.Fatalf("the minimum is API %s, Docker Engine %s: the docs say 1.41 and 20.10", minAPIVersion, minDockerEngine)
	}
	if versions.LessThan(minAPIVersion, client.MinAPIVersion) {
		t.Fatalf("the client no longer goes below API %s, and swarmlens promises API %s", client.MinAPIVersion, minAPIVersion)
	}
}
