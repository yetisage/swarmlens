package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/swarm"
)

func TestDroppedFields(t *testing.T) {
	for _, tc := range []struct {
		name, orig, back string
		want             []string
	}{
		{"nothing is lost", `{"a":1,"b":{"c":"x"}}`, `{"a":1,"b":{"c":"x"}}`, nil},
		{"a field is missing", `{"a":1,"Networks":[{"Target":"n"}]}`, `{"a":1}`, []string{".Networks"}},
		{"a nested field is missing", `{"T":{"C":{"New":"v","Old":"w"}}}`, `{"T":{"C":{"Old":"w"}}}`, []string{".T.C.New"}},
		{"empty values do not count", `{"a":"","b":0,"c":false,"d":[],"e":{},"f":null}`, `{}`, nil},
		{"an array element is missing", `{"L":[{"x":1},{"y":2}]}`, `{"L":[{"x":1}]}`, []string{".L[1]"}},
		{"a field of an element is missing", `{"L":[{"x":1,"z":3}]}`, `{"L":[{"x":1}]}`, []string{".L[0].z"}},
		{"more in the result is fine", `{"a":1}`, `{"a":1,"b":2}`, nil},
		{"a changed value is not a drop", `{"a":1}`, `{"a":2}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := droppedFields([]byte(tc.orig), []byte(tc.back))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("dropped = %v, want %v", got, tc.want)
			}
		})
	}
}

// What the engine sends for a service must survive the client's types: scale
// and restart build their update from them. This is the check that an update of
// the client library has to pass; if it fails, the library no longer knows
// something that the engine still says.
func TestRecordedServicesSurviveTheirTypes(t *testing.T) {
	var list []json.RawMessage
	if err := json.Unmarshal(fixture(t, "services.json"), &list); err != nil {
		t.Fatal(err)
	}
	list = append(list, fixture(t, "service.json"))
	for _, raw := range list {
		var svc swarm.Service
		if err := json.Unmarshal(raw, &svc); err != nil {
			t.Fatal(err)
		}
		if err := keepsEverything(raw, svc.Spec); err != nil {
			t.Errorf("service %s: %v", svc.Spec.Name, err)
		}
	}
}

// A service with a field that the types lack is not updated: the update would
// remove the field.
func TestScaleAndRestartRefuseToDropFields(t *testing.T) {
	const withLegacyField = `{"ID":"svc1","Version":{"Index":3},"Spec":{"Name":"web",
		"TaskTemplate":{"ContainerSpec":{"Image":"nginx"}},
		"Mode":{"Replicated":{"Replicas":2}},
		"Networks":[{"Target":"legacy"}]}}`
	updated := false
	a := newTestAPI(t, route(t, map[string]http.HandlerFunc{
		"/services/svc1": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(withLegacyField))
		},
		"/services/svc1/update": func(w http.ResponseWriter, r *http.Request) { updated = true },
	}))

	for name, do := range map[string]func() error{
		"scale":   func() error { return a.ScaleService(t.Context(), "svc1", 5) },
		"restart": func() error { return a.RestartService(t.Context(), "svc1") },
	} {
		err := do()
		if err == nil {
			t.Fatalf("%s: expected a refusal", name)
		}
		for _, want := range []string{`"web"`, ".Networks", "docker CLI"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: error %q should mention %s", name, err, want)
			}
		}
	}
	if updated {
		t.Error("an update was sent for a service whose fields could not be kept")
	}
}
