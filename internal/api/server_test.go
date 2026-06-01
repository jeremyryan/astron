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
	"testing"
	"testing/fstest"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	gamerav1alpha1 "github.com/project-gamera/gamera/api/v1alpha1"
	"github.com/project-gamera/gamera/internal/projector"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := gamerav1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func newTestServer(t *testing.T, objs ...client.Object) *Server {
	t.Helper()
	c := fakeclient.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(objs...).Build()
	mgr := projector.NewManager(nil, nil, nil)
	return NewServer(c, mgr, nil)
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
	proj := &gamerav1alpha1.GraphProjection{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default", UID: types.UID("uid-1")},
		Status: gamerav1alpha1.GraphProjectionStatus{
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
		"index.html":    {Data: []byte("<!doctype html><title>gamera</title>")},
		"assets/app.js": {Data: []byte("console.log('hi')")},
	}
	c := fakeclient.NewClientBuilder().WithScheme(testScheme(t)).Build()
	srv := NewServer(c, projector.NewManager(nil, nil, nil), assets)
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

func TestGraphNotRunningReturnsEmpty(t *testing.T) {
	proj := &gamerav1alpha1.GraphProjection{
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
