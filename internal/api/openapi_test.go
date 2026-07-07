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

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestOpenAPIJSONBuilds verifies the document generates and covers every route.
func TestOpenAPIJSONBuilds(t *testing.T) {
	raw, err := OpenAPIJSON()
	if err != nil {
		t.Fatalf("OpenAPIJSON: %v", err)
	}

	var doc struct {
		OpenAPI string                          `json:"openapi"`
		Info    struct{ Title, Version string } `json:"info"`
		Paths   map[string]map[string]any       `json:"paths"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("spec is not valid JSON: %v", err)
	}
	if doc.OpenAPI == "" {
		t.Error("missing openapi version")
	}
	if doc.Info.Title == "" || doc.Info.Version == "" {
		t.Errorf("missing info title/version: %+v", doc.Info)
	}

	// Every documented endpoint must be present as an operation.
	wantOps := map[string]string{} // "METHOD path" -> present
	for _, e := range apiEndpoints() {
		wantOps[e.method+" "+e.path] = ""
	}
	got := 0
	for path, ops := range doc.Paths {
		for method := range ops {
			key := methodUpper(method) + " " + path
			if _, ok := wantOps[key]; ok {
				got++
			}
		}
	}
	if got != len(wantOps) {
		t.Errorf("expected %d documented operations, matched %d", len(wantOps), got)
	}
}

func methodUpper(m string) string {
	switch m {
	case "get":
		return http.MethodGet
	case "post":
		return http.MethodPost
	case "put":
		return http.MethodPut
	case "patch":
		return http.MethodPatch
	case "delete":
		return http.MethodDelete
	default:
		return m
	}
}

// TestHandleOpenAPI verifies the /api/openapi.json endpoint serves the document.
func TestHandleOpenAPI(t *testing.T) {
	srv := NewServer(nil, nil, nil, nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/openapi.json", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	var doc struct {
		Paths map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if _, ok := doc.Paths["/api/projections"]; !ok {
		t.Error("served spec is missing the /api/projections path")
	}
}

// TestOpenAPIYAMLInSync fails when docs/openapi.yaml is stale, i.e. the API
// types changed but the checked-in spec was not regenerated with `make openapi`.
func TestOpenAPIYAMLInSync(t *testing.T) {
	want, err := OpenAPIYAML()
	if err != nil {
		t.Fatalf("OpenAPIYAML: %v", err)
	}
	path := filepath.Join("..", "..", "docs", "openapi.yaml")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if string(got) != string(want) {
		t.Errorf("%s is out of date; run `make openapi` to regenerate", path)
	}
}
