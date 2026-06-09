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
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	gamerav1alpha1 "github.com/project-gamera/gamera/api/v1alpha1"
)

func ragRequest(t *testing.T, srv *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestRAGSearchEmptyQueryIsBadRequest(t *testing.T) {
	proj := &gamerav1alpha1.GraphProjection{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default", UID: types.UID("uid-1")},
	}
	srv := newTestServer(t, proj)
	rec := ragRequest(t, srv, "/api/projections/default/demo/rag/search", `{"query":"  "}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestRAGSearchMissingProjectionIsNotFound(t *testing.T) {
	srv := newTestServer(t)
	rec := ragRequest(t, srv, "/api/projections/default/missing/rag/search", `{"query":"web"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// With a Manager that has no running projector, search resolves the projection
// but finds it not running, yielding an empty 200 (consistent with the graph
// endpoint's behavior).
func TestRAGSearchNotRunningReturnsEmpty(t *testing.T) {
	proj := &gamerav1alpha1.GraphProjection{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default", UID: types.UID("uid-1")},
	}
	srv := newTestServer(t, proj)
	rec := ragRequest(t, srv, "/api/projections/default/demo/rag/search", `{"query":"web"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var got retrievalDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Query != "web" || len(got.Seeds) != 0 || len(got.Subgraph.Nodes) != 0 {
		t.Fatalf("expected empty retrieval echoing the query, got %+v", got)
	}
}

func TestRAGNeighborhoodRequiresKindAndName(t *testing.T) {
	proj := &gamerav1alpha1.GraphProjection{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default", UID: types.UID("uid-1")},
	}
	srv := newTestServer(t, proj)
	rec := ragRequest(t, srv, "/api/projections/default/demo/rag/neighborhood", `{"name":"web"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestRAGNeighborhoodNotRunningReturnsEmpty(t *testing.T) {
	proj := &gamerav1alpha1.GraphProjection{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default", UID: types.UID("uid-1")},
	}
	srv := newTestServer(t, proj)
	rec := ragRequest(t, srv, "/api/projections/default/demo/rag/neighborhood",
		`{"kind":"Pod","namespace":"shop","name":"web-1","hops":2}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var got retrievalDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Subgraph.Nodes) != 0 {
		t.Fatalf("expected empty subgraph, got %+v", got)
	}
}

func TestRAGSearchRejectsMalformedBody(t *testing.T) {
	proj := &gamerav1alpha1.GraphProjection{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default", UID: types.UID("uid-1")},
	}
	srv := newTestServer(t, proj)
	rec := ragRequest(t, srv, "/api/projections/default/demo/rag/search", `{not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestRAGQueryEmptyQuestionIsBadRequest(t *testing.T) {
	proj := &gamerav1alpha1.GraphProjection{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default", UID: types.UID("uid-1")},
	}
	srv := newTestServer(t, proj)
	rec := ragRequest(t, srv, "/api/projections/default/demo/rag/query", `{"question":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// With no running projector, text-to-Cypher and answer report the capability as
// unavailable (503), since they require a configured chat model.
func TestRAGQueryNotRunningIsUnavailable(t *testing.T) {
	proj := &gamerav1alpha1.GraphProjection{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default", UID: types.UID("uid-1")},
	}
	srv := newTestServer(t, proj)
	rec := ragRequest(t, srv, "/api/projections/default/demo/rag/query", `{"question":"how many pods?"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestRAGAnswerNotRunningIsUnavailable(t *testing.T) {
	proj := &gamerav1alpha1.GraphProjection{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default", UID: types.UID("uid-1")},
	}
	srv := newTestServer(t, proj)
	rec := ragRequest(t, srv, "/api/projections/default/demo/rag/answer", `{"question":"why is web down?"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestRAGAnswerMissingProjectionIsNotFound(t *testing.T) {
	srv := newTestServer(t)
	rec := ragRequest(t, srv, "/api/projections/default/missing/rag/answer", `{"question":"q"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
