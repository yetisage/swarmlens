package api

import (
	"os"
	"path/filepath"
	"testing"
)

// writeContext lays a docker context out on disk the way the CLI does.
func writeContext(t *testing.T, dir, name, host string) {
	t.Helper()
	metaDir := filepath.Join(dir, "contexts", "meta", contextID(name))
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := `{"Name":"` + name + `","Metadata":{"Description":"the ` + name + ` swarm"},"Endpoints":{"docker":{"Host":"` + host + `","SkipTLSVerify":false}}}`
	if err := os.WriteFile(filepath.Join(metaDir, "meta.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestContextResolution(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", dir)
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("DOCKER_CONTEXT", "")
	t.Setenv("DOCKER_TLS_VERIFY", "")
	writeContext(t, dir, "prod", "ssh://deploy@swarm.example.com")
	writeContext(t, dir, "lab", "tcp://10.0.0.5:2375")
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"currentContext":"prod"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ep, err := resolveEndpoint(ConnectOptions{})
	if err != nil || ep.name != "prod" || ep.host != "ssh://deploy@swarm.example.com" {
		t.Fatalf("current context: %+v %v", ep, err)
	}
	if ep.display() != "ssh://swarm.example.com" {
		t.Errorf("display = %q, the ssh user must be hidden", ep.display())
	}

	ep, err = resolveEndpoint(ConnectOptions{Context: "lab"})
	if err != nil || ep.host != "tcp://10.0.0.5:2375" {
		t.Fatalf("explicit context: %+v %v", ep, err)
	}

	t.Setenv("DOCKER_HOST", "tcp://1.2.3.4:2376")
	ep, _ = resolveEndpoint(ConnectOptions{})
	if ep.host != "tcp://1.2.3.4:2376" || ep.name != "default" {
		t.Errorf("DOCKER_HOST must override the current context: %+v", ep)
	}

	ep, _ = resolveEndpoint(ConnectOptions{Host: "unix:///tmp/x.sock"})
	if ep.host != "unix:///tmp/x.sock" {
		t.Errorf("explicit host: %+v", ep)
	}

	if _, err := resolveEndpoint(ConnectOptions{Context: "ghost"}); err == nil {
		t.Error("an unknown context must fail")
	}
}

func TestListContexts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", dir)
	t.Setenv("DOCKER_HOST", "")
	writeContext(t, dir, "prod", "ssh://deploy@swarm.example.com")
	writeContext(t, dir, "lab", "tcp://10.0.0.5:2375")

	a := &API{ctxName: "lab"}
	list, err := a.Contexts()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[0].Name != "default" || list[1].Name != "lab" || list[2].Name != "prod" {
		t.Fatalf("contexts = %+v", list)
	}
	if !list[1].Current || list[0].Current || list[2].Current {
		t.Errorf("current flag wrong: %+v", list)
	}
	if list[2].Description != "the prod swarm" || list[2].Endpoint != "ssh://swarm.example.com" {
		t.Errorf("prod = %+v", list[2])
	}
}

func TestListContextsWithoutStore(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	a := &API{ctxName: "default"}
	list, err := a.Contexts()
	if err != nil || len(list) != 1 || !list[0].Current {
		t.Fatalf("list=%+v err=%v", list, err)
	}
}
