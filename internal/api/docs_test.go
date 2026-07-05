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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleDocs(t *testing.T) {
	srv := NewServer(nil, nil, nil, nil, nil)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/docs = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type = %q, want text/html", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<redoc") || !strings.Contains(body, "/api/openapi.json") {
		t.Error("docs page does not reference redoc / the spec url")
	}
	if !strings.Contains(body, "/api/redoc.standalone.js") {
		t.Error("docs page does not reference the local redoc bundle")
	}
}

func TestHandleRedocBundle(t *testing.T) {
	srv := NewServer(nil, nil, nil, nil, nil)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/redoc.standalone.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/redoc.standalone.js = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/javascript") {
		t.Errorf("content-type = %q, want application/javascript", ct)
	}
	if rec.Body.Len() < 1000 {
		t.Errorf("redoc bundle too small (%d bytes); embed missing?", rec.Body.Len())
	}
}
