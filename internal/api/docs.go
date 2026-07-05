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
	_ "embed"
	"net/http"
)

// The Redoc API-reference page and its (locally served, so it works offline)
// standalone bundle. Redoc renders the document served at /api/openapi.json.
var (
	//go:embed redoc/index.html
	redocHTML []byte
	//go:embed redoc/redoc.standalone.js
	redocBundle []byte
)

// handleDocs serves the Redoc API-reference page at /api/docs.
func (s *Server) handleDocs(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(redocHTML)
}

// handleRedocBundle serves the embedded Redoc JavaScript bundle.
func (s *Server) handleRedocBundle(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	// The bundle is versioned and immutable; allow caching.
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(redocBundle)
}
