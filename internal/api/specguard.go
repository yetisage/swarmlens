package api

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/moby/moby/api/types/swarm"
)

// keepsEverything checks that spec, which an update is about to send back,
// carries everything the engine said about the service in raw (the service as
// JSON). An update replaces the spec, and the types of the client decide what a
// spec is: a field they do not have, an old one the engine still sends or a new
// one the client has not learned, would be taken away from the service without
// a sign. Refusing is better than that.
func keepsEverything(raw json.RawMessage, spec swarm.ServiceSpec) error {
	var env struct{ Spec json.RawMessage }
	if err := json.Unmarshal(raw, &env); err != nil {
		return err
	}
	back, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	dropped, err := droppedFields(env.Spec, back)
	if err != nil {
		return err
	}
	if len(dropped) == 0 {
		return nil
	}
	shown := dropped
	if len(shown) > 3 {
		shown = append(append([]string{}, shown[:3]...), fmt.Sprintf("and %d more", len(dropped)-3))
	}
	return fmt.Errorf("it has fields that swarmlens would drop when updating it (%s); change it with the docker CLI", strings.Join(shown, ", "))
}

// droppedFields lists the paths that hold a value in the JSON document orig and
// are missing from the document back, which is orig after a trip through the
// client's types. Empty values (null, "", 0, false, [] and {}) do not count: the
// types leave them out on purpose.
func droppedFields(orig, back []byte) ([]string, error) {
	var a, b any
	if err := json.Unmarshal(orig, &a); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(back, &b); err != nil {
		return nil, err
	}
	var out []string
	collectDropped("", a, b, &out)
	sort.Strings(out)
	return out, nil
}

func collectDropped(path string, a, b any, out *[]string) {
	switch x := a.(type) {
	case map[string]any:
		y, _ := b.(map[string]any)
		for k, v := range x {
			w, ok := y[k]
			if !ok {
				if !emptyJSON(v) {
					*out = append(*out, path+"."+k)
				}
				continue
			}
			collectDropped(path+"."+k, v, w, out)
		}
	case []any:
		y, _ := b.([]any)
		for i, v := range x {
			p := fmt.Sprintf("%s[%d]", path, i)
			if i >= len(y) {
				if !emptyJSON(v) {
					*out = append(*out, p)
				}
				continue
			}
			collectDropped(p, v, y[i], out)
		}
	}
}

func emptyJSON(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case bool:
		return !x
	case float64:
		return x == 0
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}
