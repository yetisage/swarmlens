package api

import (
	"strings"
	"testing"
	"time"
)

func TestHumanDuration(t *testing.T) {
	for in, want := range map[time.Duration]string{
		-time.Second:                   "0s",
		45 * time.Second:               "45s",
		3*time.Minute + 20*time.Second: "3m20s",
		45 * time.Minute:               "45m",
		5*time.Hour + 10*time.Minute:   "5h10m",
		30 * time.Hour:                 "30h",
		4*24*time.Hour + 6*time.Hour:   "4d6h",
		40 * 24 * time.Hour:            "40d",
		800 * 24 * time.Hour:           "2y70d",
	} {
		if got := HumanDuration(in); got != want {
			t.Errorf("HumanDuration(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	for in, want := range map[int64]string{0: "0B", 1023: "1023B", 1024: "1.0KiB", 256 << 20: "256.0MiB", 3 << 30: "3.0GiB"} {
		if got := HumanBytes(in); got != want {
			t.Errorf("HumanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestJSONToYAMLKeepsOrderAndQuoting(t *testing.T) {
	raw := `{"ID":"abc","Version":{"Index":7},"Spec":{"Name":"web","Env":["A=1","B=two words"],"Port":"8080","Replicas":3,"Multi":"line1\nline2"},"Empty":[]}`
	got, err := JSONToYAML([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	// Field order of the document is kept (not sorted alphabetically).
	if strings.Index(got, "ID:") > strings.Index(got, "Version:") || strings.Index(got, "Version:") > strings.Index(got, "Spec:") {
		t.Errorf("order not preserved:\n%s", got)
	}
	for _, want := range []string{"Index: 7", "Replicas: 3", "- A=1", "- B=two words", `Port: "8080"`, "Empty: []", "Multi: |-"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestYAMLToJSON(t *testing.T) {
	raw, err := yamlToJSON("Name: web\nReplicas: 3\nEnv:\n  - A=1\n")
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"Env":["A=1"],"Name":"web","Replicas":3}` {
		t.Errorf("json = %s", raw)
	}
	if _, err := yamlToJSON("a: [unclosed"); err == nil {
		t.Error("invalid YAML must fail")
	}
}

func TestConfigYAMLDecodesData(t *testing.T) {
	// "hello\nworld\n" in base64.
	raw := `{"ID":"c1","Spec":{"Name":"cfg","Data":"aGVsbG8Kd29ybGQK"}}`
	got, err := configYAML([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "hello") || !strings.Contains(got, "world") || strings.Contains(got, "aGVsbG8K") {
		t.Errorf("config data not decoded:\n%s", got)
	}
}
