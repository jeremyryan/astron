/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// runCmd executes the root command with the given args and returns stdout.
func runCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestVersionCommand(t *testing.T) {
	out, err := runCmd(t, "version")
	if err != nil {
		t.Fatalf("version command failed: %v", err)
	}
	if !strings.Contains(out, "astron") {
		t.Fatalf("version output missing program name: %q", out)
	}
}

func TestDefaultServerURL(t *testing.T) {
	// Without the env var, the built-in default is used.
	t.Setenv(serverEnvVar, "")
	if got := defaultServerURL(); got != defaultServer {
		t.Errorf("defaultServerURL() = %q, want %q", got, defaultServer)
	}

	// The env var overrides the built-in default.
	t.Setenv(serverEnvVar, "http://env-server:9000")
	if got := defaultServerURL(); got != "http://env-server:9000" {
		t.Errorf("defaultServerURL() = %q, want env value", got)
	}

	// The env var becomes the --server flag default.
	cmd := newRootCmd()
	if got := cmd.PersistentFlags().Lookup("server").DefValue; got != "http://env-server:9000" {
		t.Errorf("--server default = %q, want env value", got)
	}
}

func TestServerFlagOverridesEnv(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{})
	}))
	defer srv.Close()

	// Point the env var at a dead address; the flag must win.
	t.Setenv(serverEnvVar, "http://127.0.0.1:1")
	if _, err := runCmd(t, "--server", srv.URL, "projections", "list"); err != nil {
		t.Fatalf("expected --server to override %s: %v", serverEnvVar, err)
	}
}

func TestInvalidOutputFlag(t *testing.T) {
	_, err := runCmd(t, "--output", "yaml", "version")
	if err == nil {
		t.Fatal("expected error for invalid --output, got nil")
	}
}

func TestProjectionsListTable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/projections" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]Projection{
			{Namespace: "astron", Name: "default", Phase: "Ready", NodeCount: 3, RelationshipCount: 2},
		})
	}))
	defer srv.Close()

	out, err := runCmd(t, "--server", srv.URL, "projections", "list")
	if err != nil {
		t.Fatalf("projections list failed: %v", err)
	}
	for _, want := range []string{"NAMESPACE", "default", "Ready"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
}

// graphServer returns a test server serving a small fixed graph for any
// projection graph request.
func graphServer(t *testing.T) *httptest.Server {
	t.Helper()
	g := Graph{
		Nodes: []Node{
			{ID: "dep-1", APIVersion: "apps/v1", Kind: "Deployment", Namespace: "astron", Name: "web"},
			{ID: "pod-1", APIVersion: "v1", Kind: "Pod", Namespace: "astron", Name: "web-abc"},
		},
		Edges: []Edge{
			{ID: "e1", Source: "dep-1", Target: "pod-1", Type: "OWNS"},
		},
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/projections/") || !strings.HasSuffix(r.URL.Path, "/graph") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(g)
	}))
}

func TestGraphTable(t *testing.T) {
	srv := graphServer(t)
	defer srv.Close()

	out, err := runCmd(t, "--server", srv.URL, "graph", "astron", "default")
	if err != nil {
		t.Fatalf("graph failed: %v", err)
	}
	for _, want := range []string{"KIND", "Deployment", "Pod", "TYPE", "OWNS", "Deployment astron/web", "Pod astron/web-abc"} {
		if !strings.Contains(out, want) {
			t.Errorf("graph output missing %q:\n%s", want, out)
		}
	}
}

func TestGraphKindFilter(t *testing.T) {
	srv := graphServer(t)
	defer srv.Close()

	out, err := runCmd(t, "--server", srv.URL, "graph", "astron", "default", "--kind", "Pod")
	if err != nil {
		t.Fatalf("graph --kind failed: %v", err)
	}
	if strings.Contains(out, "Deployment") {
		t.Errorf("expected Deployment to be filtered out:\n%s", out)
	}
	if !strings.Contains(out, "web-abc") {
		t.Errorf("expected Pod to remain:\n%s", out)
	}
	// The OWNS edge touches a filtered-out node, so it must be dropped.
	if strings.Contains(out, "OWNS") {
		t.Errorf("expected dangling edge to be dropped:\n%s", out)
	}
}

func TestGraphMutuallyExclusiveFlags(t *testing.T) {
	srv := graphServer(t)
	defer srv.Close()

	_, err := runCmd(t, "--server", srv.URL, "graph", "astron", "default", "--edges-only", "--nodes-only")
	if err == nil {
		t.Fatal("expected error for --edges-only with --nodes-only")
	}
}

func TestGraphJSON(t *testing.T) {
	srv := graphServer(t)
	defer srv.Close()

	out, err := runCmd(t, "--server", srv.URL, "-o", "json", "graph", "astron", "default")
	if err != nil {
		t.Fatalf("graph -o json failed: %v", err)
	}
	var got Graph
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if len(got.Nodes) != 2 || len(got.Edges) != 1 {
		t.Fatalf("unexpected graph payload: %+v", got)
	}
}

func TestGraphFormatJSON(t *testing.T) {
	srv := graphServer(t)
	defer srv.Close()

	out, err := runCmd(t, "--server", srv.URL, "graph", "astron", "default", "--format", "json")
	if err != nil {
		t.Fatalf("graph --format json failed: %v", err)
	}
	var got Graph
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if len(got.Nodes) != 2 || len(got.Edges) != 1 {
		t.Fatalf("unexpected graph payload: %+v", got)
	}
}

func TestGraphFormatTableOverridesOutput(t *testing.T) {
	srv := graphServer(t)
	defer srv.Close()

	// Explicit --format table wins over a global -o json.
	out, err := runCmd(t, "--server", srv.URL, "-o", "json", "graph", "astron", "default", "--format", "table")
	if err != nil {
		t.Fatalf("graph --format table failed: %v", err)
	}
	if !strings.Contains(out, "KIND") || !strings.Contains(out, "TYPE") {
		t.Errorf("expected table output, got:\n%s", out)
	}
}

func TestGraphFormatInvalid(t *testing.T) {
	srv := graphServer(t)
	defer srv.Close()

	_, err := runCmd(t, "--server", srv.URL, "graph", "astron", "default", "--format", "yaml")
	if err == nil {
		t.Fatal("expected error for invalid --format value")
	}
}

func TestProjectionsListJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]Projection{
			{Namespace: "astron", Name: "default", NodeCount: 1},
		})
	}))
	defer srv.Close()

	out, err := runCmd(t, "--server", srv.URL, "-o", "json", "projections", "list")
	if err != nil {
		t.Fatalf("projections list -o json failed: %v", err)
	}
	var got []Projection
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if len(got) != 1 || got[0].Name != "default" {
		t.Fatalf("unexpected JSON payload: %+v", got)
	}
}
