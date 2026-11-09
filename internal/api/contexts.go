package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// The docker CLI keeps contexts on disk:
//
//	$DOCKER_CONFIG/config.json                          {"currentContext": "name"}
//	$DOCKER_CONFIG/contexts/meta/<sha256(name)>/meta.json
//	$DOCKER_CONFIG/contexts/tls/<sha256(name)>/docker/{ca,cert,key}.pem
//
// Reading that layout directly keeps swarmlens free of the docker CLI
// module while honouring `docker context use`.

type contextMeta struct {
	Name     string
	Metadata struct {
		Description string
	}
	Endpoints map[string]struct {
		Host          string
		SkipTLSVerify bool
	}
}

func dockerConfigDir() string {
	if d := os.Getenv("DOCKER_CONFIG"); d != "" {
		return d
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".docker")
	}
	return ".docker"
}

func contextID(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])
}

// currentContextName returns DOCKER_CONTEXT or the context selected with
// `docker context use`. It returns "" for the implicit default context.
func currentContextName() string {
	if c := os.Getenv("DOCKER_CONTEXT"); c != "" {
		return c
	}
	raw, err := os.ReadFile(filepath.Join(dockerConfigDir(), "config.json"))
	if err != nil {
		return ""
	}
	var cfg struct {
		CurrentContext string `json:"currentContext"`
	}
	if json.Unmarshal(raw, &cfg) != nil {
		return ""
	}
	return cfg.CurrentContext
}

func readContextMeta(dir string) (contextMeta, error) {
	var m contextMeta
	raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(raw, &m)
}

func contextEndpoint(name string) (endpoint, error) {
	id := contextID(name)
	m, err := readContextMeta(filepath.Join(dockerConfigDir(), "contexts", "meta", id))
	if err != nil {
		return endpoint{}, fmt.Errorf("docker context %q: %w", name, err)
	}
	dk, ok := m.Endpoints["docker"]
	if !ok || dk.Host == "" {
		return endpoint{}, fmt.Errorf("docker context %q has no docker endpoint", name)
	}
	ep := endpoint{name: name, host: dk.Host, skipTLSVerify: dk.SkipTLSVerify}
	tlsDir := filepath.Join(dockerConfigDir(), "contexts", "tls", id, "docker")
	for file, dst := range map[string]*string{"ca.pem": &ep.caFile, "cert.pem": &ep.certFile, "key.pem": &ep.keyFile} {
		p := filepath.Join(tlsDir, file)
		if _, err := os.Stat(p); err == nil {
			*dst = p
		}
	}
	return ep, nil
}

// Contexts lists the docker contexts, the implicit "default" one first.
func (a *API) Contexts() ([]ContextInfo, error) {
	cur := a.ctxName
	out := []ContextInfo{{
		Name:        "default",
		Description: "Current DOCKER_HOST based configuration",
		Endpoint:    defaultEndpoint().display(),
		Current:     cur == "default",
	}}

	entries, err := os.ReadDir(filepath.Join(dockerConfigDir(), "contexts", "meta"))
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, err
	}
	var rest []ContextInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, err := readContextMeta(filepath.Join(dockerConfigDir(), "contexts", "meta", e.Name()))
		if err != nil || m.Name == "" {
			continue
		}
		ci := ContextInfo{Name: m.Name, Description: m.Metadata.Description, Current: m.Name == cur}
		if dk, ok := m.Endpoints["docker"]; ok {
			ci.Endpoint = endpoint{host: dk.Host}.display()
		}
		rest = append(rest, ci)
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i].Name < rest[j].Name })
	return append(out, rest...), nil
}
