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
	"net/url"
	"strings"
	"testing"
)

// linksServer records link requests for the demo/web projection and responds
// the way the real API does.
func linksServer(t *testing.T, lastBody *map[string]any, lastQuery *url.Values, lastMethod *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/projections/demo/web/links" {
			http.NotFound(w, r)
			return
		}
		*lastMethod = r.Method
		*lastQuery = r.URL.Query()
		switch r.Method {
		case http.MethodPost, http.MethodPatch:
			_ = json.NewDecoder(r.Body).Decode(lastBody)
			relType, _ := (*lastBody)["type"].(string)
			if relType == "" {
				relType = "CUSTOM"
			}
			status := http.StatusCreated
			if r.Method == http.MethodPatch {
				status = http.StatusOK
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(Link{
				From: (*lastBody)["from"].(string), To: (*lastBody)["to"].(string), Type: relType,
			})
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
}

// dependsOn is the custom relationship type used across the links tests.
const dependsOn = "DEPENDS_ON"

func TestLinksAdd(t *testing.T) {
	var body map[string]any
	var query url.Values
	var method string
	srv := linksServer(t, &body, &query, &method)
	defer srv.Close()

	// Default type is left to the server (CUSTOM).
	out, err := runCmd(t, "--server", srv.URL, "links", "add", "demo", "web", "n1", "n2")
	if err != nil {
		t.Fatalf("links add failed: %v", err)
	}
	if method != http.MethodPost || body["from"] != "n1" || body["to"] != "n2" {
		t.Errorf("unexpected request: %s %v", method, body)
	}
	if _, present := body["type"]; present {
		t.Errorf("type should be omitted when not set: %v", body)
	}
	if !strings.Contains(out, "link n1 -[CUSTOM]-> n2 created") {
		t.Errorf("unexpected confirmation: %q", out)
	}

	// An explicit type is forwarded.
	out, err = runCmd(t, "--server", srv.URL, "links", "add", "demo", "web", "n1", "n2", "--type", dependsOn)
	if err != nil {
		t.Fatalf("links add --type failed: %v", err)
	}
	if body["type"] != dependsOn || !strings.Contains(out, "-["+dependsOn+"]->") {
		t.Errorf("type not forwarded: %v / %q", body, out)
	}
}

func TestLinksUpdate(t *testing.T) {
	var body map[string]any
	var query url.Values
	var method string
	srv := linksServer(t, &body, &query, &method)
	defer srv.Close()

	out, err := runCmd(t, "--server", srv.URL, "links", "update", "demo", "web", "n1", "n2",
		"--note", "manual dependency", "--type", dependsOn)
	if err != nil {
		t.Fatalf("links update failed: %v", err)
	}
	if method != http.MethodPatch || body["note"] != "manual dependency" || body["type"] != dependsOn {
		t.Errorf("unexpected request: %s %v", method, body)
	}
	if !strings.Contains(out, "updated") {
		t.Errorf("unexpected confirmation: %q", out)
	}
}

func TestLinksRm(t *testing.T) {
	var body map[string]any
	var query url.Values
	var method string
	srv := linksServer(t, &body, &query, &method)
	defer srv.Close()

	out, err := runCmd(t, "--server", srv.URL, "links", "rm", "demo", "web", "n1", "n2", "--type", dependsOn)
	if err != nil {
		t.Fatalf("links rm failed: %v", err)
	}
	if method != http.MethodDelete || query.Get("from") != "n1" || query.Get("to") != "n2" || query.Get("type") != dependsOn {
		t.Errorf("unexpected request: %s %v", method, query)
	}
	if !strings.Contains(out, "link n1 -> n2 deleted") {
		t.Errorf("unexpected confirmation: %q", out)
	}
}

func TestLinksErrorSurfaced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"node n9 not found"}`))
	}))
	defer srv.Close()

	if _, err := runCmd(t, "--server", srv.URL, "links", "add", "demo", "web", "n9", "n2"); err == nil ||
		!strings.Contains(err.Error(), "node n9 not found") {
		t.Fatalf("expected API error, got %v", err)
	}
}
