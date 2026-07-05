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

// Values reused across the views tests.
const (
	projWeb = "web"
	podKind = "Pod"
)

// viewsServer returns a test server serving a fixed set of GraphViews from
// /api/views, honoring the projectionName/projectionNamespace query filters the
// way the real read API does.
func viewsServer(t *testing.T) *httptest.Server {
	t.Helper()
	all := []View{
		{Namespace: "gamera", Name: "web-only", DisplayName: "Web only",
			ProjectionRef: ViewProjectionRef{Name: "default", Namespace: "gamera"}},
		{Namespace: "gamera", Name: "secrets-hidden",
			ProjectionRef: ViewProjectionRef{Name: "other"}}, // ref ns defaults to view ns (gamera)
		{Namespace: "team-a", Name: "team-view",
			ProjectionRef: ViewProjectionRef{Name: "default", Namespace: "gamera"}},
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != apiViewsPath {
			http.NotFound(w, r)
			return
		}
		wantName := r.URL.Query().Get("projectionName")
		wantNS := r.URL.Query().Get("projectionNamespace")
		out := make([]View, 0, len(all))
		for _, v := range all {
			if wantName != "" && v.ProjectionRef.Name != wantName {
				continue
			}
			refNS := v.ProjectionRef.Namespace
			if refNS == "" {
				refNS = v.Namespace
			}
			if wantNS != "" && refNS != wantNS {
				continue
			}
			out = append(out, v)
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
}

func TestViewsListTable(t *testing.T) {
	srv := viewsServer(t)
	defer srv.Close()

	out, err := runCmd(t, "--server", srv.URL, "views", "list")
	if err != nil {
		t.Fatalf("views list failed: %v", err)
	}
	for _, want := range []string{"NAMESPACE", "NAME", "PROJECTION", "web-only", "team-view", "gamera/default"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
}

func TestViewsListNamespaceFilter(t *testing.T) {
	srv := viewsServer(t)
	defer srv.Close()

	out, err := runCmd(t, "--server", srv.URL, "views", "list", "--namespace", "team-a")
	if err != nil {
		t.Fatalf("views list -n failed: %v", err)
	}
	if !strings.Contains(out, "team-view") {
		t.Errorf("expected team-a view retained:\n%s", out)
	}
	if strings.Contains(out, "web-only") || strings.Contains(out, "secrets-hidden") {
		t.Errorf("expected gamera views filtered out:\n%s", out)
	}
}

func TestViewsListProjectionFilter(t *testing.T) {
	srv := viewsServer(t)
	defer srv.Close()

	// Views associated with projection "default" in namespace "gamera": both the
	// gamera "web-only" view and the team-a view (which references gamera/default).
	out, err := runCmd(t, "--server", srv.URL, "views", "list",
		"--namespace", "gamera", "--projection", "default")
	if err != nil {
		t.Fatalf("views list --projection failed: %v", err)
	}
	if !strings.Contains(out, "web-only") || !strings.Contains(out, "team-view") {
		t.Errorf("expected views referencing gamera/default:\n%s", out)
	}
	if strings.Contains(out, "secrets-hidden") {
		t.Errorf("expected view referencing 'other' to be excluded:\n%s", out)
	}
}

func TestViewsListProjectionRequiresNamespace(t *testing.T) {
	srv := viewsServer(t)
	defer srv.Close()

	_, err := runCmd(t, "--server", srv.URL, "views", "list", "--projection", "default")
	if err == nil {
		t.Fatal("expected error when --projection is used without --namespace")
	}
}

func TestBuildDefaultViewHiddenKinds(t *testing.T) {
	compute, ok := lookupDefaultView("compute") // case-insensitive
	if !ok {
		t.Fatal("expected 'compute' to resolve to a default view")
	}
	v := buildDefaultView("gamera", projWeb, compute)
	if v.Name != "web-compute" || v.DisplayName != "Compute" {
		t.Fatalf("unexpected view identity: %+v", v)
	}
	if v.ProjectionRef.Name != projWeb || v.ProjectionRef.Namespace != "gamera" {
		t.Fatalf("unexpected projectionRef: %+v", v.ProjectionRef)
	}
	hidden := map[string]bool{}
	for _, k := range v.Filters.HiddenKinds {
		hidden[k] = true
	}
	// Compute view hides networking + persistence kinds, not its own.
	if hidden[podKind] || hidden["Deployment"] {
		t.Errorf("compute view must not hide compute kinds: %v", v.Filters.HiddenKinds)
	}
	if !hidden["Service"] || !hidden["PersistentVolumeClaim"] || !hidden["ConfigMap"] {
		t.Errorf("compute view should hide networking/persistence kinds: %v", v.Filters.HiddenKinds)
	}
}

func TestDefaultViewsKeepPodsVisible(t *testing.T) {
	for _, name := range defaultViewNames() {
		cat, ok := lookupDefaultView(name)
		if !ok {
			t.Fatalf("default view %q did not resolve", name)
		}
		for _, k := range hiddenKindsFor(cat) {
			if k == podKind {
				t.Errorf("%s view must keep Pods visible, but hides them: %v", name, hiddenKindsFor(cat))
			}
		}
	}
}

func TestViewsAddCreatesViews(t *testing.T) {
	var created []View
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != apiViewsPath {
			http.NotFound(w, r)
			return
		}
		var in View
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		created = append(created, in)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(in)
	}))
	defer srv.Close()

	out, err := runCmd(t, "--server", srv.URL, "views", "add", "gamera", projWeb, "Compute", "networking")
	if err != nil {
		t.Fatalf("views add failed: %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("expected 2 views created, got %d", len(created))
	}
	if !strings.Contains(out, "graphview.gamera.gamera.io/web-compute created in namespace gamera") ||
		!strings.Contains(out, "web-networking created") {
		t.Errorf("unexpected confirmation output:\n%s", out)
	}
	for _, v := range created {
		if v.ProjectionRef.Name != projWeb || v.ProjectionRef.Namespace != "gamera" {
			t.Errorf("unexpected projectionRef on created view: %+v", v.ProjectionRef)
		}
	}
}

func TestViewsAddDeduplicates(t *testing.T) {
	count := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		var in View
		_ = json.NewDecoder(r.Body).Decode(&in)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(in)
	}))
	defer srv.Close()

	if _, err := runCmd(t, "--server", srv.URL, "views", "add", "gamera", projWeb, "compute", "Compute"); err != nil {
		t.Fatalf("views add failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected duplicate view names to create once, got %d", count)
	}
}

func TestViewsAddUnknownView(t *testing.T) {
	// The server should never be hit; an unknown name fails validation first.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be called for an invalid view name")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	_, err := runCmd(t, "--server", srv.URL, "views", "add", "gamera", projWeb, "Bogus")
	if err == nil || !strings.Contains(err.Error(), "unknown view") {
		t.Fatalf("expected unknown-view error, got %v", err)
	}
}

func TestViewsListJSON(t *testing.T) {
	srv := viewsServer(t)
	defer srv.Close()

	out, err := runCmd(t, "--server", srv.URL, "-o", "json", "views", "list", "--namespace", "gamera")
	if err != nil {
		t.Fatalf("views list -o json failed: %v", err)
	}
	var got []View
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 gamera views, got %d: %+v", len(got), got)
	}
}
