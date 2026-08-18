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
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	astronv1alpha1 "github.com/project-astron/astron/api/v1alpha1"
	"github.com/project-astron/astron/internal/projector"
	"github.com/project-astron/astron/internal/rag"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := astronv1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func newTestServer(t *testing.T, objs ...client.Object) *Server {
	t.Helper()
	c := fakeclient.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(objs...).Build()
	mgr := projector.NewManager(nil, nil, nil)
	return NewServer(c, mgr, nil, nil, nil)
}

func TestHealth(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestListProjections(t *testing.T) {
	proj := &astronv1alpha1.GraphProjection{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default", UID: types.UID("uid-1")},
		Status: astronv1alpha1.GraphProjectionStatus{
			Phase: "Ready", NodeCount: 5, RelationshipCount: 3,
		},
	}
	srv := newTestServer(t, proj)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projections", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var got []projectionDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "demo" || got[0].NodeCount != 5 || got[0].Phase != "Ready" {
		t.Fatalf("unexpected projections: %+v", got)
	}
	if got[0].ChatEnabled {
		t.Fatalf("chatEnabled = true for projection without GraphRAG chat")
	}
}

func TestListProjectionsChatEnabled(t *testing.T) {
	// chatEnabled must be reported only when GraphRAG and its chat model are
	// both enabled.
	objs := []client.Object{
		&astronv1alpha1.GraphProjection{
			ObjectMeta: metav1.ObjectMeta{Name: "chat", Namespace: "default", UID: types.UID("u1")},
			Spec: astronv1alpha1.GraphProjectionSpec{
				GraphRAG: &astronv1alpha1.GraphRAGSpec{
					Enabled: true,
					Chat:    &astronv1alpha1.ChatModelConfig{Enabled: true, Provider: "fake"},
				},
			},
		},
		&astronv1alpha1.GraphProjection{
			ObjectMeta: metav1.ObjectMeta{Name: "no-chat", Namespace: "default", UID: types.UID("u2")},
			Spec: astronv1alpha1.GraphProjectionSpec{
				GraphRAG: &astronv1alpha1.GraphRAGSpec{Enabled: true},
			},
		},
		&astronv1alpha1.GraphProjection{
			ObjectMeta: metav1.ObjectMeta{Name: "rag-disabled", Namespace: "default", UID: types.UID("u3")},
			Spec: astronv1alpha1.GraphProjectionSpec{
				GraphRAG: &astronv1alpha1.GraphRAGSpec{
					Chat: &astronv1alpha1.ChatModelConfig{Enabled: true},
				},
			},
		},
	}
	srv := newTestServer(t, objs...)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projections", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var got []projectionDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"chat": true, "no-chat": false, "rag-disabled": false}
	if len(got) != len(want) {
		t.Fatalf("got %d projections, want %d", len(got), len(want))
	}
	for _, p := range got {
		if p.ChatEnabled != want[p.Name] {
			t.Errorf("projection %q chatEnabled = %v, want %v", p.Name, p.ChatEnabled, want[p.Name])
		}
	}
}

func TestListProjectionsChatEnabledViaProviderChats(t *testing.T) {
	// A controller-wide chat provider enables chat for any GraphRAG-embedding
	// projection, even without a per-projection chat model. Projections without
	// GraphRAG stay chat-disabled (answering still needs retrieval).
	objs := []client.Object{
		&astronv1alpha1.GraphProjection{
			ObjectMeta: metav1.ObjectMeta{Name: "rag", Namespace: "default", UID: types.UID("u1")},
			Spec: astronv1alpha1.GraphProjectionSpec{
				GraphRAG: &astronv1alpha1.GraphRAGSpec{Enabled: true},
			},
		},
		&astronv1alpha1.GraphProjection{
			ObjectMeta: metav1.ObjectMeta{Name: "plain", Namespace: "default", UID: types.UID("u2")},
			Spec:       astronv1alpha1.GraphProjectionSpec{},
		},
	}
	c := fakeclient.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(objs...).Build()
	mgr := projector.NewManager(nil, nil, nil)
	mgr.SetProviderChats(map[string]rag.Chat{"fake": rag.NewFakeChat("")})
	srv := NewServer(c, mgr, nil, nil, nil)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projections", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got []projectionDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"rag": true, "plain": false}
	for _, p := range got {
		if p.ChatEnabled != want[p.Name] {
			t.Errorf("projection %q chatEnabled = %v, want %v", p.Name, p.ChatEnabled, want[p.Name])
		}
	}
}

func TestListProjectionsSorted(t *testing.T) {
	// Provide projections out of order; the API must return them sorted by
	// (namespace, name) regardless of input order.
	objs := []client.Object{
		&astronv1alpha1.GraphProjection{ObjectMeta: metav1.ObjectMeta{Name: "zeta", Namespace: "b", UID: types.UID("u1")}},
		&astronv1alpha1.GraphProjection{ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "b", UID: types.UID("u2")}},
		&astronv1alpha1.GraphProjection{ObjectMeta: metav1.ObjectMeta{Name: "mid", Namespace: "a", UID: types.UID("u3")}},
	}
	srv := newTestServer(t, objs...)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projections", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var got []projectionDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := []struct{ ns, name string }{{"a", "mid"}, {"b", "alpha"}, {"b", "zeta"}}
	if len(got) != len(want) {
		t.Fatalf("got %d projections, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Namespace != w.ns || got[i].Name != w.name {
			t.Fatalf("projection[%d] = %s/%s, want %s/%s", i, got[i].Namespace, got[i].Name, w.ns, w.name)
		}
	}
}

func TestCreateLink(t *testing.T) {
	proj := &astronv1alpha1.GraphProjection{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default", UID: types.UID("uid-1")},
	}
	srv := newTestServer(t, proj)

	post := func(path, body string) int {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		return rec.Code
	}

	// Missing endpoints -> 400.
	if code := post("/api/projections/default/demo/links", `{"from":"","to":""}`); code != http.StatusBadRequest {
		t.Fatalf("empty endpoints: status = %d, want 400", code)
	}
	// Unknown projection -> 404.
	if code := post("/api/projections/default/missing/links", `{"from":"a","to":"b"}`); code != http.StatusNotFound {
		t.Fatalf("missing projection: status = %d, want 404", code)
	}
	// Known projection but no running projector -> 503.
	if code := post("/api/projections/default/demo/links", `{"from":"a","to":"b"}`); code != http.StatusServiceUnavailable {
		t.Fatalf("not running: status = %d, want 503", code)
	}
}

func TestDeleteLink(t *testing.T) {
	proj := &astronv1alpha1.GraphProjection{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default", UID: types.UID("uid-1")},
	}
	srv := newTestServer(t, proj)

	del := func(path string) int {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, path, nil))
		return rec.Code
	}

	// Missing endpoints -> 400.
	if code := del("/api/projections/default/demo/links"); code != http.StatusBadRequest {
		t.Fatalf("missing params: status = %d, want 400", code)
	}
	// Unknown projection -> 404.
	if code := del("/api/projections/default/missing/links?from=a&to=b"); code != http.StatusNotFound {
		t.Fatalf("missing projection: status = %d, want 404", code)
	}
	// Known projection but no running projector -> 503.
	if code := del("/api/projections/default/demo/links?from=a&to=b"); code != http.StatusServiceUnavailable {
		t.Fatalf("not running: status = %d, want 503", code)
	}
}

func TestGraphNotFound(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projections/default/missing/graph", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestSPAServesAssetsAndFallsBack(t *testing.T) {
	assets := fstest.MapFS{
		"index.html":    {Data: []byte("<!doctype html><title>astron</title>")},
		"assets/app.js": {Data: []byte("console.log('hi')")},
	}
	c := fakeclient.NewClientBuilder().WithScheme(testScheme(t)).Build()
	srv := NewServer(c, projector.NewManager(nil, nil, nil), nil, nil, assets)
	handler := srv.Handler()

	// Existing asset is served directly.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "console.log('hi')" {
		t.Fatalf("asset not served: code=%d body=%q", rec.Code, rec.Body.String())
	}

	// Unknown client-side route falls back to index.html.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/projections/default/demo", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") == "application/json" {
		t.Fatalf("expected SPA fallback, got code=%d", rec.Code)
	}

	// API routes still take precedence over the SPA handler.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("api route shadowed by SPA: code=%d", rec.Code)
	}
}

func TestResourceYAML(t *testing.T) {
	scheme := testScheme(t)
	cm := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":          "demo",
			"namespace":     "default",
			"managedFields": []any{map[string]any{"manager": "kubectl"}},
			"annotations": map[string]any{
				"kubectl.kubernetes.io/last-applied-configuration": "{}",
				"keep-me": "yes",
			},
		},
		"data": map[string]any{"key": "value"},
	}}
	dc := dynamicfake.NewSimpleDynamicClient(scheme, cm)
	mapper := meta.NewDefaultRESTMapper(nil)
	mapper.Add(schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}, meta.RESTScopeNamespace)

	c := fakeclient.NewClientBuilder().WithScheme(scheme).Build()
	srv := NewServer(c, projector.NewManager(nil, nil, nil), dc, mapper, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/resource?apiVersion=v1&kind=ConfigMap&namespace=default&name=demo", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "kind: ConfigMap") || !strings.Contains(body, "key: value") {
		t.Fatalf("unexpected YAML body:\n%s", body)
	}
	if strings.Contains(body, "managedFields") {
		t.Fatalf("managedFields should have been stripped:\n%s", body)
	}
	if strings.Contains(body, "last-applied-configuration") {
		t.Fatalf("last-applied-configuration should have been stripped:\n%s", body)
	}
	if !strings.Contains(body, "keep-me:") {
		t.Fatalf("expected remaining annotation to be kept:\n%s", body)
	}
}

func TestResourceYAMLMissingParams(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/resource?kind=Pod", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func doJSON(t *testing.T, srv *Server, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = strings.NewReader(string(b))
	} else {
		rdr = strings.NewReader("")
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(method, target, rdr))
	return rec
}

func TestViewsCRUD(t *testing.T) {
	srv := newTestServer(t)

	// Create.
	create := map[string]any{
		"name":          "team-a",
		"displayName":   "Team A",
		"projectionRef": map[string]any{"name": "demo", "namespace": "default"},
		"filters": map[string]any{
			"hiddenKinds": []string{"Secret"},
			"labelMode":   "all",
		},
	}
	rec := doJSON(t, srv, http.MethodPost, "/api/views", create)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created viewDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Name != "team-a" || created.Namespace != "default" || created.DisplayName != "Team A" {
		t.Fatalf("unexpected created view: %+v", created)
	}

	// List (no filter).
	rec = doJSON(t, srv, http.MethodGet, "/api/views", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", rec.Code)
	}
	var list []viewDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "team-a" {
		t.Fatalf("unexpected list: %+v", list)
	}

	// List filtered by a different projection -> empty.
	rec = doJSON(t, srv, http.MethodGet, "/api/views?projectionName=other", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("expected no views for projection 'other', got %+v", list)
	}

	// Update.
	update := map[string]any{
		"name":          "team-a",
		"displayName":   "Team A (edited)",
		"projectionRef": map[string]any{"name": "demo", "namespace": "default"},
		"filters":       map[string]any{"groupByNamespace": false},
	}
	rec = doJSON(t, srv, http.MethodPut, "/api/views/default/team-a", update)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var updated viewDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.DisplayName != "Team A (edited)" || updated.Filters.GroupByNamespace == nil || *updated.Filters.GroupByNamespace {
		t.Fatalf("update not applied: %+v", updated)
	}

	// Delete.
	rec = doJSON(t, srv, http.MethodDelete, "/api/views/default/team-a", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", rec.Code)
	}
	rec = doJSON(t, srv, http.MethodGet, "/api/views", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 0 {
		t.Fatalf("expected no views after delete, got %+v", list)
	}
}

func TestCreateViewValidation(t *testing.T) {
	srv := newTestServer(t)
	rec := doJSON(t, srv, http.MethodPost, "/api/views", map[string]any{"name": "x"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestUpdateViewNotFound(t *testing.T) {
	srv := newTestServer(t)
	body := map[string]any{"projectionRef": map[string]any{"name": "demo"}}
	rec := doJSON(t, srv, http.MethodPut, "/api/views/default/missing", body)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestGraphNotRunningReturnsEmpty(t *testing.T) {
	proj := &astronv1alpha1.GraphProjection{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default", UID: types.UID("uid-1")},
	}
	srv := newTestServer(t, proj)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projections/default/demo/graph", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var got graphDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Nodes) != 0 || len(got.Edges) != 0 {
		t.Fatalf("expected empty graph, got %+v", got)
	}
}

func TestHandleRAGSchema(t *testing.T) {
	proj := &astronv1alpha1.GraphProjection{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default", UID: types.UID("uid-1")},
	}
	srv := newTestServer(t, proj)

	// Unknown projection -> 404.
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projections/default/missing/rag/schema", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing projection: status = %d, want 404", rec.Code)
	}

	// Known projection but no running projector -> 503 (it requires a
	// materialized graph, like the snapshot endpoints).
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projections/default/demo/rag/schema", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("not running: status = %d, want 503", rec.Code)
	}
}

func TestHandleRAGAgent(t *testing.T) {
	proj := &astronv1alpha1.GraphProjection{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default", UID: types.UID("uid-1")},
	}
	srv := newTestServer(t, proj)

	do := func(path, body string) int {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		return rec.Code
	}

	// Invalid JSON body -> 400.
	if code := do("/api/projections/default/demo/rag/agent", `not json`); code != http.StatusBadRequest {
		t.Fatalf("invalid JSON: status = %d, want 400", code)
	}
	// Empty question -> 400, before the projection is even resolved.
	if code := do("/api/projections/default/demo/rag/agent", `{"question":"  "}`); code != http.StatusBadRequest {
		t.Fatalf("empty question: status = %d, want 400", code)
	}
	// Unsupported history role -> 400.
	badHistory := `{"question":"why?","history":[{"role":"system","content":"x"}]}`
	if code := do("/api/projections/default/demo/rag/agent", badHistory); code != http.StatusBadRequest {
		t.Fatalf("unsupported history role: status = %d, want 400", code)
	}
	// Unknown projection -> 404.
	if code := do("/api/projections/default/missing/rag/agent", `{"question":"why?"}`); code != http.StatusNotFound {
		t.Fatalf("missing projection: status = %d, want 404", code)
	}
	// Known projection but no running projector -> 503.
	if code := do("/api/projections/default/demo/rag/agent", `{"question":"why?"}`); code != http.StatusServiceUnavailable {
		t.Fatalf("not running: status = %d, want 503", code)
	}
	// A valid history entry (user/assistant) is accepted at the validation
	// stage — it still 503s for the same not-running reason above.
	goodHistory := `{"question":"why?","history":[{"role":"user","content":"hi"},{"role":"assistant","content":"hello"}]}`
	if code := do("/api/projections/default/demo/rag/agent", goodHistory); code != http.StatusServiceUnavailable {
		t.Fatalf("valid history, not running: status = %d, want 503", code)
	}
}

func TestToRAGMessages(t *testing.T) {
	msgs, err := toRAGMessages([]ragAgentMessage{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	})
	if err != nil {
		t.Fatalf("toRAGMessages: %v", err)
	}
	if len(msgs) != 2 || msgs[0].Role != rag.RoleUser || msgs[1].Role != rag.RoleAssistant {
		t.Fatalf("unexpected messages: %+v", msgs)
	}

	if _, err := toRAGMessages([]ragAgentMessage{{Role: "tool", Content: "x"}}); err == nil {
		t.Fatal("expected an error for an unsupported role")
	}

	// Empty history is fine and yields an empty (non-nil) slice.
	msgs, err = toRAGMessages(nil)
	if err != nil || len(msgs) != 0 {
		t.Fatalf("toRAGMessages(nil) = %+v, %v", msgs, err)
	}
}

func TestSnapshotEndpoints(t *testing.T) {
	proj := &astronv1alpha1.GraphProjection{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default", UID: types.UID("uid-1")},
	}
	srv := newTestServer(t, proj)

	do := func(method, path, body string) int {
		rec := httptest.NewRecorder()
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(method, path, rd))
		return rec.Code
	}

	// Missing name -> 400 (validated before the projection is even resolved).
	if code := do(http.MethodPost, "/api/projections/default/demo/snapshots", `{"name":"  "}`); code != http.StatusBadRequest {
		t.Fatalf("empty name: status = %d, want 400", code)
	}
	// Unknown projection -> 404.
	if code := do(http.MethodPost, "/api/projections/default/missing/snapshots", `{"name":"before-upgrade"}`); code != http.StatusNotFound {
		t.Fatalf("missing projection: status = %d, want 404", code)
	}
	// Known projection but no running projector -> 503.
	if code := do(http.MethodPost, "/api/projections/default/demo/snapshots", `{"name":"before-upgrade"}`); code != http.StatusServiceUnavailable {
		t.Fatalf("create, not running: status = %d, want 503", code)
	}
	if code := do(http.MethodGet, "/api/projections/default/demo/snapshots", ""); code != http.StatusServiceUnavailable {
		t.Fatalf("list, not running: status = %d, want 503", code)
	}
	if code := do(http.MethodDelete, "/api/projections/default/demo/snapshots/abc", ""); code != http.StatusServiceUnavailable {
		t.Fatalf("delete, not running: status = %d, want 503", code)
	}
}

func TestListProviders(t *testing.T) {
	// An empty registry (no providers configured) returns empty lists, not null.
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/providers", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var empty providersDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &empty); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if empty.EmbeddingProviders == nil || empty.ChatProviders == nil {
		t.Fatalf("expected empty (non-null) lists, got %+v", empty)
	}
	if len(empty.EmbeddingProviders) != 0 || len(empty.ChatProviders) != 0 {
		t.Fatalf("expected no providers, got %+v", empty)
	}

	// With providers configured, they are listed in declaration order with only
	// the descriptive (non-secret) fields.
	dir := t.TempDir()
	file := filepath.Join(dir, "providers.yaml")
	content := `
embeddingProviders:
  - name: openai-small
    provider: openai
    model: text-embedding-3-small
    apiKeySecret:
      name: k
chatProviders:
  - name: gpt4o
    provider: openai
    model: gpt-4o-mini
`
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := rag.LoadProvidersConfig(file)
	if err != nil {
		t.Fatalf("LoadProvidersConfig: %v", err)
	}
	mgr := projector.NewManager(nil, nil, nil)
	mgr.SetProviders(reg)
	c := fakeclient.NewClientBuilder().WithScheme(testScheme(t)).Build()
	srv = NewServer(c, mgr, nil, nil, nil)

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/providers", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got providersDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(got.EmbeddingProviders) != 1 || got.EmbeddingProviders[0].Name != "openai-small" ||
		got.EmbeddingProviders[0].Model != "text-embedding-3-small" {
		t.Fatalf("unexpected embedding providers: %+v", got.EmbeddingProviders)
	}
	if len(got.ChatProviders) != 1 || got.ChatProviders[0].Name != "gpt4o" {
		t.Fatalf("unexpected chat providers: %+v", got.ChatProviders)
	}
}
