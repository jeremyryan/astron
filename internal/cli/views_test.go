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
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Values reused across the views tests.
const (
	projWeb = "web"
	podKind = "Pod"
	// testNS is the namespace used for views fixtures across these tests.
	testNS = "astron"
)

// viewsServer returns a test server serving a fixed set of GraphViews from
// /api/views, honoring the projectionName/projectionNamespace query filters the
// way the real read API does.
func viewsServer(t *testing.T) *httptest.Server {
	t.Helper()
	all := []View{
		{Namespace: testNS, Name: "web-only", DisplayName: "Web only",
			ProjectionRef: ViewProjectionRef{Name: "default", Namespace: testNS}},
		{Namespace: testNS, Name: "secrets-hidden",
			ProjectionRef: ViewProjectionRef{Name: "other"}}, // ref ns defaults to view ns (astron)
		{Namespace: "team-a", Name: "team-view",
			ProjectionRef: ViewProjectionRef{Name: "default", Namespace: testNS}},
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
	for _, want := range []string{"NAMESPACE", "NAME", "PROJECTION", "web-only", "team-view", "astron/default"} {
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
		t.Errorf("expected astron views filtered out:\n%s", out)
	}
}

func TestViewsListProjectionFilter(t *testing.T) {
	srv := viewsServer(t)
	defer srv.Close()

	// Views associated with projection "default" in namespace testNS: both the
	// astron "web-only" view and the team-a view (which references astron/default).
	out, err := runCmd(t, "--server", srv.URL, "views", "list",
		"--namespace", testNS, "--projection", "default")
	if err != nil {
		t.Fatalf("views list --projection failed: %v", err)
	}
	if !strings.Contains(out, "web-only") || !strings.Contains(out, "team-view") {
		t.Errorf("expected views referencing astron/default:\n%s", out)
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

func TestBuildDefaultViewVisibleKinds(t *testing.T) {
	compute, ok := lookupDefaultView("compute") // case-insensitive
	if !ok {
		t.Fatal("expected 'compute' to resolve to a default view")
	}
	v := buildDefaultView(testNS, projWeb, compute)
	if v.Name != "web-compute" || v.DisplayName != "Compute" {
		t.Fatalf("unexpected view identity: %+v", v)
	}
	if v.ProjectionRef.Name != projWeb || v.ProjectionRef.Namespace != testNS {
		t.Fatalf("unexpected projectionRef: %+v", v.ProjectionRef)
	}
	// Compute is an allow-list view showing only its own kinds (+ Pod).
	if v.Filters.KindMode != kindModeShow {
		t.Errorf("default views should use allow-list mode, got %q", v.Filters.KindMode)
	}
	if len(v.Filters.HiddenKinds) != 0 {
		t.Errorf("allow-list view should not set hiddenKinds: %v", v.Filters.HiddenKinds)
	}
	visible := map[string]bool{}
	for _, k := range v.Filters.VisibleKinds {
		visible[k] = true
	}
	if !visible[podKind] || !visible["Deployment"] {
		t.Errorf("compute view must show compute kinds: %v", v.Filters.VisibleKinds)
	}
	if visible["Service"] || visible["PersistentVolumeClaim"] || visible["ConfigMap"] {
		t.Errorf("compute view should not show networking/persistence kinds: %v", v.Filters.VisibleKinds)
	}
}

func TestDefaultViewsKeepPodsVisible(t *testing.T) {
	for _, name := range defaultViewNames() {
		cat, ok := lookupDefaultView(name)
		if !ok {
			t.Fatalf("default view %q did not resolve", name)
		}
		visible := map[string]bool{}
		for _, k := range visibleKindsFor(cat) {
			visible[k] = true
		}
		if !visible[podKind] {
			t.Errorf("%s view must keep Pods visible: %v", name, visibleKindsFor(cat))
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

	out, err := runCmd(t, "--server", srv.URL, "views", "add", testNS, projWeb, "Compute", "networking")
	if err != nil {
		t.Fatalf("views add failed: %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("expected 2 views created, got %d", len(created))
	}
	if !strings.Contains(out, "graphview.astron.astron.io/web-compute created in namespace astron") ||
		!strings.Contains(out, "web-networking created") {
		t.Errorf("unexpected confirmation output:\n%s", out)
	}
	for _, v := range created {
		if v.ProjectionRef.Name != projWeb || v.ProjectionRef.Namespace != testNS {
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

	if _, err := runCmd(t, "--server", srv.URL, "views", "add", testNS, projWeb, "compute", "Compute"); err != nil {
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

	_, err := runCmd(t, "--server", srv.URL, "views", "add", testNS, projWeb, "Bogus")
	if err == nil || !strings.Contains(err.Error(), "unknown view") {
		t.Fatalf("expected unknown-view error, got %v", err)
	}
}

func TestViewsListJSON(t *testing.T) {
	srv := viewsServer(t)
	defer srv.Close()

	out, err := runCmd(t, "--server", srv.URL, "-o", "json", "views", "list", "--namespace", testNS)
	if err != nil {
		t.Fatalf("views list -o json failed: %v", err)
	}
	var got []View
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 astron views, got %d: %+v", len(got), got)
	}
}

func TestViewsDefaultsTable(t *testing.T) {
	out, err := runCmd(t, "views", "defaults")
	if err != nil {
		t.Fatalf("views defaults failed: %v", err)
	}
	if !strings.Contains(out, "NAME") || !strings.Contains(out, "DESCRIPTION") || !strings.Contains(out, "KINDS") {
		t.Errorf("missing table header: %q", out)
	}
	// Every built-in view is listed.
	for _, name := range defaultViewNames() {
		if !strings.Contains(out, name) {
			t.Errorf("output missing default view %q:\n%s", name, out)
		}
	}
	// The kinds column reflects what a created GraphView would show, including
	// the always-visible Pod kind in non-compute views.
	if !strings.Contains(out, "Deployment") || !strings.Contains(out, "PersistentVolumeClaim") {
		t.Errorf("output missing expected kinds:\n%s", out)
	}
}

func TestViewsDefaultsJSON(t *testing.T) {
	out, err := runCmd(t, "-o", "json", "views", "defaults")
	if err != nil {
		t.Fatalf("views defaults -o json failed: %v", err)
	}
	var got []struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Kinds       []string `json:"kinds"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if len(got) != len(defaultViewCategories) {
		t.Fatalf("expected %d views, got %d: %+v", len(defaultViewCategories), len(got), got)
	}
	byName := map[string][]string{}
	for _, v := range got {
		if v.Description == "" {
			t.Errorf("view %q has no description", v.Name)
		}
		byName[v.Name] = v.Kinds
	}
	// Networking includes the always-visible Pod kind, matching visibleKindsFor.
	if !slices.Contains(byName["Networking"], podKind) {
		t.Errorf("Networking view should include Pod: %v", byName["Networking"])
	}
	if !slices.Contains(byName["Compute"], "Deployment") {
		t.Errorf("Compute view should include Deployment: %v", byName["Compute"])
	}
}

func TestViewsRm(t *testing.T) {
	var deleted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || !strings.HasPrefix(r.URL.Path, apiViewsPath+"/") {
			http.NotFound(w, r)
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, apiViewsPath+"/")
		if rest == "astron/missing" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"graphviews.astron.astron.io \"missing\" not found"}`))
			return
		}
		deleted = append(deleted, rest)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	// Deleting existing views reports each one and hits the API per name.
	out, err := runCmd(t, "--server", srv.URL, "views", "rm", testNS, "web-compute", "web-networking")
	if err != nil {
		t.Fatalf("views rm failed: %v", err)
	}
	for _, want := range []string{
		"graphview.astron.astron.io/web-compute deleted from namespace astron",
		"graphview.astron.astron.io/web-networking deleted from namespace astron",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if len(deleted) != 2 || deleted[0] != "astron/web-compute" || deleted[1] != "astron/web-networking" {
		t.Errorf("unexpected DELETE requests: %v", deleted)
	}

	// A missing view surfaces the API error but does not stop other deletions.
	deleted = nil
	out, err = runCmd(t, "--server", srv.URL, "views", "rm", testNS, "missing", "web-compute")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
	if !strings.Contains(out, "graphview.astron.astron.io/web-compute deleted") {
		t.Errorf("expected remaining view to still be deleted:\n%s", out)
	}
	if len(deleted) != 1 || deleted[0] != "astron/web-compute" {
		t.Errorf("unexpected DELETE requests: %v", deleted)
	}
}

func TestViewsRmRequiresArgs(t *testing.T) {
	if _, err := runCmd(t, "views", "rm", testNS); err == nil {
		t.Fatal("expected an argument-count error with no view names")
	}
}

func TestViewsGenerateStdout(t *testing.T) {
	out, err := runCmd(t, "views", "generate", testNS, projWeb, "compute", "networking")
	if err != nil {
		t.Fatalf("views generate failed: %v", err)
	}
	if strings.Count(out, "---") != 1 {
		t.Errorf("expected 1 document separator for 2 views, got:\n%s", out)
	}
	for _, want := range []string{
		"kind: GraphView",
		"name: web-compute",
		"name: web-networking",
		"displayName: Compute",
		"namespace: astron",
		"kindMode: show",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestViewsGenerateDefaults(t *testing.T) {
	out, err := runCmd(t, "views", "generate", testNS, projWeb, "defaults")
	if err != nil {
		t.Fatalf("views generate defaults failed: %v", err)
	}
	if got := strings.Count(out, "kind: GraphView"); got != len(defaultViewCategories) {
		t.Errorf("expected %d GraphView documents, got %d:\n%s", len(defaultViewCategories), got, out)
	}
}

func TestViewsGenerateToFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "views.yaml")
	out, err := runCmd(t, "views", "generate", testNS, projWeb, "persistence", "-f", path)
	if err != nil {
		t.Fatalf("views generate -f failed: %v", err)
	}
	if strings.Contains(out, "kind: GraphView") {
		t.Errorf("manifest should not be on stdout when -f is given: %q", out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading output file: %v", err)
	}
	if !strings.Contains(string(data), "name: web-persistence") {
		t.Fatalf("unexpected file contents: %s", data)
	}
}

func TestViewsGenerateErrors(t *testing.T) {
	// Unknown view names are rejected before anything is written.
	if _, err := runCmd(t, "views", "generate", testNS, projWeb, "bogus"); err == nil {
		t.Fatal("expected error for unknown view name")
	}
	// --apply and --output-file are mutually exclusive.
	if _, err := runCmd(t, "views", "generate", testNS, projWeb, "compute", "--apply", "-f", "x.yaml"); err == nil ||
		!strings.Contains(err.Error(), "--apply cannot be combined") {
		t.Fatalf("expected apply/output-file conflict error, got %v", err)
	}
}

func TestAccessControlViewNameNormalized(t *testing.T) {
	cat, ok := lookupDefaultView("Access control")
	if !ok {
		t.Fatal("Access control view not found")
	}
	if got := defaultViewResourceName(projWeb, cat); got != "web-access-control" {
		t.Errorf("resource name = %q, want web-access-control", got)
	}

	// The hyphenated and space forms both resolve to the same view.
	for _, name := range []string{"access-control", "access control", "ACCESS CONTROL", "Access-Control"} {
		got, ok := lookupDefaultView(name)
		if !ok || got.displayName != "Access control" {
			t.Errorf("lookupDefaultView(%q) = %+v, %v; want Access control", name, got, ok)
		}
	}

	// Generated manifests use the normalized name.
	out, err := runCmd(t, "views", "generate", "demo", projWeb, "access-control")
	if err != nil {
		t.Fatalf("views generate access-control failed: %v", err)
	}
	if !strings.Contains(out, "name: web-access-control") {
		t.Errorf("manifest name not normalized:\n%s", out)
	}
	if strings.Contains(out, "name: web-access control") {
		t.Errorf("manifest contains invalid resource name:\n%s", out)
	}
}

func TestViewsNew(t *testing.T) {
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

	out, err := runCmd(t, "--server", srv.URL, "views", "new", testNS, projWeb, "my-view",
		"Pod,Service", "Deployment", "Pod", // mixed comma/space form with a duplicate
		"--display-name", "My view", "--description", "Pods and friends")
	if err != nil {
		t.Fatalf("views new failed: %v", err)
	}
	if !strings.Contains(out, "graphview.astron.astron.io/my-view created in namespace astron") {
		t.Errorf("unexpected confirmation output:\n%s", out)
	}
	if len(created) != 1 {
		t.Fatalf("expected 1 view created, got %d", len(created))
	}
	v := created[0]
	if v.Namespace != testNS || v.Name != "my-view" {
		t.Errorf("unexpected view identity: %s/%s", v.Namespace, v.Name)
	}
	if v.DisplayName != "My view" || v.Description != "Pods and friends" {
		t.Errorf("unexpected metadata: %q %q", v.DisplayName, v.Description)
	}
	if v.ProjectionRef.Name != projWeb || v.ProjectionRef.Namespace != testNS {
		t.Errorf("unexpected projectionRef: %+v", v.ProjectionRef)
	}
	if v.Filters.KindMode != kindModeShow {
		t.Errorf("expected allow-list mode, got %q", v.Filters.KindMode)
	}
	want := []string{"Deployment", "Pod", "Service"}
	if !slices.Equal(v.Filters.VisibleKinds, want) {
		t.Errorf("visibleKinds = %v, want %v", v.Filters.VisibleKinds, want)
	}
}

func TestViewsNewDefaultsDisplayName(t *testing.T) {
	var created View
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&created)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(created)
	}))
	defer srv.Close()

	if _, err := runCmd(t, "--server", srv.URL, "views", "new", testNS, projWeb, "my-view", podKind); err != nil {
		t.Fatalf("views new failed: %v", err)
	}
	if created.DisplayName != "my-view" {
		t.Errorf("displayName should default to the view name, got %q", created.DisplayName)
	}
}

func TestViewsNewRequiresKinds(t *testing.T) {
	// Too few args is a usage error.
	if _, err := runCmd(t, "views", "new", testNS, projWeb, "my-view"); err == nil {
		t.Fatal("expected an argument-count error with no kinds")
	}
	// Kind args that reduce to nothing fail before contacting the server.
	if _, err := runCmd(t, "views", "new", testNS, projWeb, "my-view", ",,"); err == nil ||
		!strings.Contains(err.Error(), "at least one resource kind") {
		t.Fatalf("expected empty-kinds error, got %v", err)
	}
}
