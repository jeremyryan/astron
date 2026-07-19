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
		if r.URL.Path != apiProjectionsPath {
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
			{ID: "pod-1", APIVersion: "v1", Kind: podKind, Namespace: "astron", Name: "web-abc"},
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
	for _, want := range []string{"KIND", "Deployment", podKind, "TYPE", "OWNS", "Deployment astron/web", "Pod astron/web-abc"} {
		if !strings.Contains(out, want) {
			t.Errorf("graph output missing %q:\n%s", want, out)
		}
	}
}

func TestGraphKindFilter(t *testing.T) {
	srv := graphServer(t)
	defer srv.Close()

	out, err := runCmd(t, "--server", srv.URL, "graph", "astron", "default", "--kind", podKind)
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

func TestGraphFormatDOT(t *testing.T) {
	srv := graphServer(t)
	defer srv.Close()

	out, err := runCmd(t, "--server", srv.URL, "graph", "astron", "default", "--format", "dot")
	if err != nil {
		t.Fatalf("graph --format dot failed: %v", err)
	}
	for _, want := range []string{
		"digraph {",
		"rankdir=LR;",
		`"dep-1" [label="Deployment astron/web"];`,
		`"pod-1" [label="Pod astron/web-abc"];`,
		`"dep-1" -> "pod-1" [label="OWNS"];`,
		"}",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dot output missing %q:\n%s", want, out)
		}
	}
}

func TestGraphFormatMermaid(t *testing.T) {
	srv := graphServer(t)
	defer srv.Close()

	out, err := runCmd(t, "--server", srv.URL, "graph", "astron", "default", "--format", "mermaid")
	if err != nil {
		t.Fatalf("graph --format mermaid failed: %v", err)
	}
	for _, want := range []string{
		"graph LR",
		`n0["Deployment astron/web"]`,
		`n1["Pod astron/web-abc"]`,
		"n0 -->|OWNS| n1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("mermaid output missing %q:\n%s", want, out)
		}
	}
}

func TestGraphFormatDOTWithKindFilter(t *testing.T) {
	srv := graphServer(t)
	defer srv.Close()

	// Filtering to Pod drops the Deployment node and the OWNS edge.
	out, err := runCmd(t, "--server", srv.URL, "graph", "astron", "default", "--format", "dot", "--kind", podKind)
	if err != nil {
		t.Fatalf("graph --format dot --kind failed: %v", err)
	}
	if strings.Contains(out, "Deployment") || strings.Contains(out, "OWNS") {
		t.Errorf("filtered dot output should not contain Deployment or OWNS:\n%s", out)
	}
	if !strings.Contains(out, `"pod-1"`) {
		t.Errorf("filtered dot output missing Pod node:\n%s", out)
	}
}

func TestQuoteEscaping(t *testing.T) {
	if got := dotQuote(`a"b\c`); got != `"a\"b\\c"` {
		t.Errorf("dotQuote = %s", got)
	}
	if got := mermaidLabel(`a"b|c`); got != "a'b/c" {
		t.Errorf("mermaidLabel = %s", got)
	}
}

func TestStatusCommand(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/healthz":
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		case apiProjectionsPath:
			_ = json.NewEncoder(w).Encode([]Projection{
				{Namespace: "a", Name: "p1", Phase: "Ready", NodeCount: 10, RelationshipCount: 20},
				{Namespace: "b", Name: "p2", Phase: "Error", NodeCount: 5, RelationshipCount: 1},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	out, err := runCmd(t, "--server", srv.URL, "status")
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	for _, want := range []string{"healthy:     true", "projections: 2 (1 ready)", "15 nodes, 21 edges"} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q:\n%s", want, out)
		}
	}

	// JSON output carries the aggregates.
	out, err = runCmd(t, "--server", srv.URL, "-o", "json", "status")
	if err != nil {
		t.Fatalf("status -o json failed: %v", err)
	}
	var got statusResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if !got.Healthy || got.Projections != 2 || got.Ready != 1 || got.TotalNodes != 15 || got.TotalEdges != 21 {
		t.Errorf("unexpected status result: %+v", got)
	}
}

func TestStatusCommandUnhealthy(t *testing.T) {
	// A reachable server with a failing health endpoint exits non-zero.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, err := runCmd(t, "--server", srv.URL, "status"); err == nil ||
		!strings.Contains(err.Error(), "not healthy") {
		t.Fatalf("expected unhealthy error, got %v", err)
	}

	// An unreachable server also errors.
	if _, err := runCmd(t, "--server", "http://127.0.0.1:1", "status"); err == nil {
		t.Fatal("expected connection error")
	}
}

func TestGraphNamespaceFilter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(Graph{
			Nodes: []Node{
				{ID: "a", Kind: podKind, Namespace: "demo", Name: "p1"},
				{ID: "b", Kind: podKind, Namespace: "other", Name: "p2"},
			},
			Edges: []Edge{{ID: "e", Source: "a", Target: "b", Type: "LINKS"}},
		})
	}))
	defer srv.Close()

	out, err := runCmd(t, "--server", srv.URL, "graph", "astron", "default", "-n", "demo")
	if err != nil {
		t.Fatalf("graph -n failed: %v", err)
	}
	if !strings.Contains(out, "p1") || strings.Contains(out, "p2") {
		t.Errorf("namespace filter not applied:\n%s", out)
	}
	// The cross-namespace edge is dropped with its endpoint.
	if strings.Contains(out, "LINKS") {
		t.Errorf("edge to filtered node should be dropped:\n%s", out)
	}
}

func TestGraphFocus(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/rag/neighborhood") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(Retrieval{Subgraph: Graph{
			Nodes: []Node{
				{ID: "pod-1", Kind: podKind, Namespace: "demo", Name: "web-abc"},
				{ID: "svc-1", Kind: "Service", Namespace: "demo", Name: "web"},
			},
			Edges: []Edge{{ID: "e", Source: "svc-1", Target: "pod-1", Type: "SELECTS"}},
		}})
	}))
	defer srv.Close()

	out, err := runCmd(t, "--server", srv.URL, "graph", "astron", "default",
		"--focus", "Pod/web-abc", "-n", "demo", "--depth", "2")
	if err != nil {
		t.Fatalf("graph --focus failed: %v", err)
	}
	if gotBody["kind"] != podKind || gotBody["name"] != "web-abc" ||
		gotBody["namespace"] != "demo" || gotBody["hops"] != float64(2) {
		t.Errorf("unexpected neighborhood request: %v", gotBody)
	}
	if !strings.Contains(out, "SELECTS") || !strings.Contains(out, "web-abc") {
		t.Errorf("neighborhood output missing content:\n%s", out)
	}
}

func TestGraphFocusErrors(t *testing.T) {
	srv := graphServer(t)
	defer srv.Close()

	// --depth without --focus is rejected.
	if _, err := runCmd(t, "--server", srv.URL, "graph", "astron", "default", "--depth", "2"); err == nil ||
		!strings.Contains(err.Error(), "--depth requires --focus") {
		t.Fatalf("expected depth-requires-focus error, got %v", err)
	}
	// A focus without a Kind/name shape is rejected.
	if _, err := runCmd(t, "--server", srv.URL, "graph", "astron", "default", "--focus", "web-abc"); err == nil ||
		!strings.Contains(err.Error(), "invalid --focus") {
		t.Fatalf("expected invalid-focus error, got %v", err)
	}
}

func TestDocsCommand(t *testing.T) {
	out, err := runCmd(t, "--server", "http://example.com:8082/", "docs")
	if err != nil {
		t.Fatalf("docs failed: %v", err)
	}
	if strings.TrimSpace(out) != "http://example.com:8082/api/docs" {
		t.Errorf("unexpected docs URL: %q", out)
	}
}
