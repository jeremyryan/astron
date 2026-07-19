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
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestParseTypeRef(t *testing.T) {
	cases := []struct {
		in         string
		apiVersion string
		kind       string
		wantErr    bool
	}{
		{"v1/Pod", "v1", "Pod", false},
		{"apps/v1/Deployment", "apps/v1", "Deployment", false},
		{"gateway.networking.k8s.io/v1/HTTPRoute", "gateway.networking.k8s.io/v1", "HTTPRoute", false},
		{"Pod", "", "", true},
		{"v1/", "", "", true},
		{"/Pod", "", "", true},
	}
	for _, c := range cases {
		av, k, err := parseTypeRef(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseTypeRef(%q): expected error", c.in)
			}
			continue
		}
		if err != nil || av != c.apiVersion || k != c.kind {
			t.Errorf("parseTypeRef(%q) = %q, %q, %v; want %q, %q", c.in, av, k, err, c.apiVersion, c.kind)
		}
	}
}

func TestResourceCommand(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/resource" {
			http.NotFound(w, r)
			return
		}
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write([]byte("apiVersion: v1\nkind: Pod\nmetadata:\n  name: web-abc\n"))
	}))
	defer srv.Close()

	out, err := runCmd(t, "--server", srv.URL, "resource", "v1/Pod", "web-abc", "-n", "demo")
	if err != nil {
		t.Fatalf("resource failed: %v", err)
	}
	if gotQuery.Get("apiVersion") != "v1" || gotQuery.Get("kind") != "Pod" ||
		gotQuery.Get("name") != "web-abc" || gotQuery.Get("namespace") != "demo" {
		t.Errorf("unexpected query: %v", gotQuery)
	}
	if !strings.Contains(out, "kind: Pod") || !strings.Contains(out, "name: web-abc") {
		t.Errorf("unexpected YAML output:\n%s", out)
	}

	// Cluster-scoped: the namespace parameter is omitted entirely.
	if _, err := runCmd(t, "--server", srv.URL, "resource", "rbac.authorization.k8s.io/v1/ClusterRole", "admin"); err != nil {
		t.Fatalf("cluster-scoped resource failed: %v", err)
	}
	if _, present := gotQuery["namespace"]; present {
		t.Errorf("namespace should be omitted when not set: %v", gotQuery)
	}
	if gotQuery.Get("apiVersion") != "rbac.authorization.k8s.io/v1" {
		t.Errorf("unexpected apiVersion: %v", gotQuery.Get("apiVersion"))
	}
}

func TestResourceCommandErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"pods \"nope\" not found"}`))
	}))
	defer srv.Close()

	// API errors surface with the server's message.
	if _, err := runCmd(t, "--server", srv.URL, "resource", "v1/Pod", "nope", "-n", "demo"); err == nil ||
		!strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}

	// A bare Kind without a version is rejected before contacting the server.
	if _, err := runCmd(t, "--server", srv.URL, "resource", "Pod", "web"); err == nil ||
		!strings.Contains(err.Error(), "invalid resource type") {
		t.Fatalf("expected type parse error, got %v", err)
	}
}
