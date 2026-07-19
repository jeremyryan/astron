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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ragServer returns a test server for a projection's rag endpoints, recording
// each decoded request body into got (keyed by verb: search, answer, query).
func ragServer(t *testing.T, got map[string]map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/api/projections/demo/web/rag/"
		if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		verb := strings.TrimPrefix(r.URL.Path, prefix)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		got[verb] = body

		w.Header().Set("Content-Type", "application/json")
		switch verb {
		case "search":
			_ = json.NewEncoder(w).Encode(Retrieval{
				Query: body["query"].(string),
				Seeds: []Seed{
					{ID: "n1", Kind: "Pod", Name: "web-1", Score: 0.91},
					{ID: "n2", Kind: "Service", Name: "web", Score: 0.85},
				},
				Cards: []Card{
					{ID: "n1", Kind: "Pod", Namespace: "demo", Name: "web-1", Text: "Pod web-1 runs nginx."},
				},
			})
		case "answer":
			_ = json.NewEncoder(w).Encode(Answer{
				Question: body["question"].(string),
				Answer:   "The web deployment has 3 replicas.",
				Retrieval: Retrieval{
					Seeds: []Seed{{ID: "n1", Kind: "Deployment", Name: "web", Score: 0.88}},
				},
			})
		case "query":
			_ = json.NewEncoder(w).Encode(QueryResult{
				Question: body["question"].(string),
				Cypher:   "MATCH (p:Pod) RETURN p.name AS name, p.restarts AS restarts",
				Rows: []map[string]any{
					{"name": "web-1", "restarts": float64(2)},
					{"name": "web-2", "restarts": float64(0)},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestSearchCommand(t *testing.T) {
	got := map[string]map[string]any{}
	srv := ragServer(t, got)
	defer srv.Close()

	// Multi-word queries are joined; filter flags are forwarded.
	out, err := runCmd(t, "--server", srv.URL, "search", "demo", "web",
		"nginx", "pods", "--top-k", "5", "--hops", "2", "--kinds", "Pod,Service")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	req := got["search"]
	if req["query"] != "nginx pods" {
		t.Errorf("query = %v, want %q", req["query"], "nginx pods")
	}
	if req["topK"] != float64(5) || req["hops"] != float64(2) {
		t.Errorf("unexpected topK/hops: %v / %v", req["topK"], req["hops"])
	}
	if kinds, _ := req["kinds"].([]any); len(kinds) != 2 || kinds[0] != "Pod" {
		t.Errorf("unexpected kinds: %v", req["kinds"])
	}
	for _, want := range []string{"KIND", "SCORE", "Pod", "web-1", "0.910"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "runs nginx") {
		t.Errorf("cards should not print without --show-cards:\n%s", out)
	}

	// --show-cards appends the card text; negative hops omits the field.
	out, err = runCmd(t, "--server", srv.URL, "search", "demo", "web", "nginx", "--show-cards")
	if err != nil {
		t.Fatalf("search --show-cards failed: %v", err)
	}
	if !strings.Contains(out, "Pod web-1 runs nginx.") || !strings.Contains(out, "demo/web-1") {
		t.Errorf("card output missing:\n%s", out)
	}
	if _, present := got["search"]["hops"]; present {
		t.Errorf("hops should be omitted when not set: %v", got["search"])
	}
}

func TestSearchCommandJSON(t *testing.T) {
	srv := ragServer(t, map[string]map[string]any{})
	defer srv.Close()

	out, err := runCmd(t, "--server", srv.URL, "-o", "json", "search", "demo", "web", "nginx")
	if err != nil {
		t.Fatalf("search -o json failed: %v", err)
	}
	var r Retrieval
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if len(r.Seeds) != 2 || len(r.Cards) != 1 {
		t.Fatalf("unexpected retrieval: %+v", r)
	}
}

func TestAskCommand(t *testing.T) {
	got := map[string]map[string]any{}
	srv := ragServer(t, got)
	defer srv.Close()

	out, err := runCmd(t, "--server", srv.URL, "ask", "demo", "web",
		"how", "many", "replicas?", "--model", "gpt-4o-mini")
	if err != nil {
		t.Fatalf("ask failed: %v", err)
	}
	if strings.TrimSpace(out) != "The web deployment has 3 replicas." {
		t.Errorf("unexpected answer output: %q", out)
	}
	req := got["answer"]
	if req["question"] != "how many replicas?" || req["model"] != "gpt-4o-mini" {
		t.Errorf("unexpected request: %v", req)
	}

	// --show-context appends the grounding table.
	out, err = runCmd(t, "--server", srv.URL, "ask", "demo", "web", "replicas?", "--show-context")
	if err != nil {
		t.Fatalf("ask --show-context failed: %v", err)
	}
	if !strings.Contains(out, "Grounded in:") || !strings.Contains(out, "Deployment") {
		t.Errorf("grounding context missing:\n%s", out)
	}
}

func TestQueryCommand(t *testing.T) {
	got := map[string]map[string]any{}
	srv := ragServer(t, got)
	defer srv.Close()

	out, err := runCmd(t, "--server", srv.URL, "query", "demo", "web", "pod", "restarts")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if got["query"]["question"] != "pod restarts" {
		t.Errorf("unexpected question: %v", got["query"])
	}
	// Rows render as a table with sorted, uppercased columns and integer cells.
	for _, want := range []string{"NAME", "RESTARTS", "web-1", "web-2"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "2") || strings.Contains(out, "MATCH") {
		t.Errorf("unexpected output (cypher should be hidden by default):\n%s", out)
	}

	// --show-cypher prints the generated query before the rows.
	out, err = runCmd(t, "--server", srv.URL, "query", "demo", "web", "restarts", "--show-cypher")
	if err != nil {
		t.Fatalf("query --show-cypher failed: %v", err)
	}
	if !strings.Contains(out, "MATCH (p:Pod)") {
		t.Errorf("cypher missing with --show-cypher:\n%s", out)
	}
}

func TestQueryCommandNoRows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(QueryResult{Question: "q", Cypher: "MATCH ..."})
	}))
	defer srv.Close()

	out, err := runCmd(t, "--server", srv.URL, "query", "demo", "web", "anything")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if !strings.Contains(out, "(no rows)") {
		t.Errorf("expected '(no rows)', got:\n%s", out)
	}
}

func TestRAGServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"GraphRAG chat is not enabled for this projection"}`))
	}))
	defer srv.Close()

	_, err := runCmd(t, "--server", srv.URL, "ask", "demo", "web", "anything")
	if err == nil || !strings.Contains(err.Error(), "chat is not enabled") {
		t.Fatalf("expected API error to surface, got %v", err)
	}
}

func TestCellString(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, ""},
		{"x", "x"},
		{float64(3), "3"},
		{2.5, "2.5"},
		{true, "true"},
	}
	for _, c := range cases {
		if got := cellString(c.in); got != c.want {
			t.Errorf("cellString(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
