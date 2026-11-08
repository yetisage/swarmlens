package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// HumanDuration renders an age the way kubectl and k9s do: two units at
// most, e.g. 45s, 3m20s, 2h5m, 4d6h.
func HumanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	secs := int64(d.Seconds())
	switch {
	case secs < 120:
		return fmt.Sprintf("%ds", secs)
	case secs < 600:
		return fmt.Sprintf("%dm%ds", secs/60, secs%60)
	case secs < 3*3600:
		return fmt.Sprintf("%dm", secs/60)
	case secs < 8*3600:
		return fmt.Sprintf("%dh%dm", secs/3600, (secs%3600)/60)
	case secs < 48*3600:
		return fmt.Sprintf("%dh", secs/3600)
	case secs < 8*24*3600:
		return fmt.Sprintf("%dd%dh", secs/86400, (secs%86400)/3600)
	case secs < 2*365*24*3600:
		return fmt.Sprintf("%dd", secs/86400)
	default:
		return fmt.Sprintf("%dy%dd", secs/(365*86400), (secs%(365*86400))/86400)
	}
}

// Age renders the time elapsed since t, or "" for the zero time.
func Age(t time.Time, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	return HumanDuration(now.Sub(t))
}

// HumanBytes renders a byte count in binary units.
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// JSONToYAML converts JSON to block-style YAML, keeping the field order of
// the document (the engine emits a meaningful order: ID, Version, Spec, …).
func JSONToYAML(raw []byte) (string, error) {
	var n yaml.Node
	if err := yaml.Unmarshal(raw, &n); err != nil {
		return "", err
	}
	clearStyle(&n)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&n); err != nil {
		return "", err
	}
	return buf.String(), enc.Close()
}

// clearStyle drops the flow/quoted styles a JSON source carries so the
// encoder picks natural block YAML.
func clearStyle(n *yaml.Node) {
	n.Style = 0
	for _, c := range n.Content {
		clearStyle(c)
	}
}

// yamlToJSON converts YAML text to JSON, ready for strict decoding into an
// engine type.
func yamlToJSON(text string) ([]byte, error) {
	var v any
	if err := yaml.Unmarshal([]byte(text), &v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
