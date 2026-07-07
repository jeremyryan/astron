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
	"sync"

	"github.com/swaggest/openapi-go"
	"github.com/swaggest/openapi-go/openapi3"
	"sigs.k8s.io/yaml"

	"github.com/project-astron/astron/internal/projector"
)

//go:generate go run ../../hack/openapi -o ../../docs/openapi.yaml

// openAPIVersion is the API version reported in the generated document.
const openAPIVersion = "v1alpha1"

// --- Response envelopes used only for documentation. ---

// errorResponse is the JSON body returned for error responses.
type errorResponse struct {
	Error string `json:"error"`
}

// healthResponse is the body of the health endpoint.
type healthResponse struct {
	Status string `json:"status"`
}

// linkResponse is the body returned when a manual link is created or updated.
type linkResponse struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
	Note string `json:"note,omitempty"`
}

// --- Request shapes (path + query + body) used only for documentation. ---

// projectionPath carries the namespace/name path parameters shared by the
// projection-scoped endpoints.
type projectionPath struct {
	Namespace string `path:"namespace" description:"Projection namespace"`
	Name      string `path:"name" description:"Projection name"`
}

type ragSearchReq struct {
	projectionPath
	ragSearchRequest
}

type ragNeighborhoodReq struct {
	projectionPath
	ragNeighborhoodRequest
}

type ragQuestionReq struct {
	projectionPath
	ragQuestionRequest
}

type createLinkReq struct {
	projectionPath
	linkRequest
}

type updateLinkReq struct {
	projectionPath
	linkRequest
}

type deleteLinkReq struct {
	Namespace string `path:"namespace" description:"Projection namespace"`
	Name      string `path:"name" description:"Projection name"`
	From      string `query:"from" required:"true" description:"Source node id"`
	To        string `query:"to" required:"true" description:"Target node id"`
	Type      string `query:"type" description:"Relationship type (defaults to the manual-link type)"`
}

type resourceReq struct {
	APIVersion string `query:"apiVersion" required:"true" description:"Resource apiVersion, e.g. apps/v1"`
	Kind       string `query:"kind" required:"true" description:"Resource kind, e.g. Deployment"`
	Name       string `query:"name" required:"true" description:"Resource name"`
	Namespace  string `query:"namespace" description:"Resource namespace (namespaced kinds only)"`
}

type listViewsReq struct {
	ProjectionNamespace string `query:"projectionNamespace" description:"Filter to views referencing this projection namespace"`
	ProjectionName      string `query:"projectionName" description:"Filter to views referencing this projection name"`
}

type updateViewReq struct {
	PathNamespace string `path:"namespace" description:"View namespace"`
	PathName      string `path:"name" description:"View name"`
	viewDTO
}

type viewPath struct {
	Namespace string `path:"namespace" description:"View namespace"`
	Name      string `path:"name" description:"View name"`
}

// endpoint describes one operation for the generated spec.
type endpoint struct {
	method   string
	path     string
	id       string
	tag      string
	summary  string
	req      any    // request structure (path/query/body); nil for none
	resp     any    // success response body; nil for no content
	status   int    // success status code
	respMIME string // success response content type (default application/json)
	errors   []int  // documented error status codes (errorResponse body)
}

// apiEndpoints enumerates the HTTP API. It mirrors the routes registered in
// Server.Handler and is the single source of truth for the OpenAPI document.
func apiEndpoints() []endpoint {
	return []endpoint{
		{
			method: http.MethodGet, path: "/api/healthz", id: "getHealth", tag: "system",
			summary: "Liveness/readiness probe", resp: new(healthResponse), status: http.StatusOK,
		},
		{
			method: http.MethodGet, path: "/api/projections", id: "listProjections", tag: "projections",
			summary: "List all GraphProjections", resp: new([]projectionDTO), status: http.StatusOK,
		},
		{
			method: http.MethodGet, path: "/api/projections/{namespace}/{name}/graph", id: "getGraph",
			tag: "projections", summary: "Get a projection's full node/edge graph",
			req: new(projectionPath), resp: new(graphDTO), status: http.StatusOK,
			errors: []int{http.StatusNotFound, http.StatusInternalServerError},
		},
		{
			method: http.MethodPost, path: "/api/projections/{namespace}/{name}/rag/search", id: "ragSearch",
			tag: "graphrag", summary: "Hybrid (vector + graph) retrieval for a query",
			req: new(ragSearchReq), resp: new(retrievalDTO), status: http.StatusOK,
			errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusServiceUnavailable, http.StatusInternalServerError},
		},
		{
			method: http.MethodPost, path: "/api/projections/{namespace}/{name}/rag/neighborhood", id: "ragNeighborhood",
			tag: "graphrag", summary: "Structural retrieval: subgraph around a named resource",
			req: new(ragNeighborhoodReq), resp: new(retrievalDTO), status: http.StatusOK,
			errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusServiceUnavailable, http.StatusInternalServerError},
		},
		{
			method: http.MethodPost, path: "/api/projections/{namespace}/{name}/rag/query", id: "ragQuery",
			tag: "graphrag", summary: "Text-to-Cypher: answer a question with a guarded read-only query",
			req: new(ragQuestionReq), resp: new(projector.QueryResult), status: http.StatusOK,
			errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusServiceUnavailable, http.StatusInternalServerError},
		},
		{
			method: http.MethodPost, path: "/api/projections/{namespace}/{name}/rag/answer", id: "ragAnswer",
			tag: "graphrag", summary: "Retrieval-augmented answer to a natural-language question",
			req: new(ragQuestionReq), resp: new(answerDTO), status: http.StatusOK,
			errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusServiceUnavailable, http.StatusInternalServerError},
		},
		{
			method: http.MethodPost, path: "/api/projections/{namespace}/{name}/links", id: "createLink",
			tag: "links", summary: "Create a user-defined edge between two nodes",
			req: new(createLinkReq), resp: new(linkResponse), status: http.StatusCreated,
			errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusServiceUnavailable},
		},
		{
			method: http.MethodPatch, path: "/api/projections/{namespace}/{name}/links", id: "updateLink",
			tag: "links", summary: "Set or clear the note on a user-defined edge",
			req: new(updateLinkReq), resp: new(linkResponse), status: http.StatusOK,
			errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusServiceUnavailable},
		},
		{
			method: http.MethodDelete, path: "/api/projections/{namespace}/{name}/links", id: "deleteLink",
			tag: "links", summary: "Delete a user-defined edge between two nodes",
			req: new(deleteLinkReq), status: http.StatusNoContent,
			errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusServiceUnavailable},
		},
		{
			method: http.MethodGet, path: "/api/resource", id: "getResourceYAML", tag: "resources",
			summary: "Fetch a live cluster resource as YAML",
			req:     new(resourceReq), resp: new(string), status: http.StatusOK, respMIME: "application/yaml",
			errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusServiceUnavailable, http.StatusInternalServerError},
		},
		{
			method: http.MethodGet, path: "/api/views", id: "listViews", tag: "views",
			summary: "List saved GraphViews, optionally filtered by projection",
			req:     new(listViewsReq), resp: new([]viewDTO), status: http.StatusOK,
			errors: []int{http.StatusInternalServerError},
		},
		{
			method: http.MethodPost, path: "/api/views", id: "createView", tag: "views",
			summary: "Create a GraphView (saved filter set)",
			req:     new(viewDTO), resp: new(viewDTO), status: http.StatusCreated,
			errors: []int{http.StatusBadRequest, http.StatusConflict, http.StatusInternalServerError},
		},
		{
			method: http.MethodPut, path: "/api/views/{namespace}/{name}", id: "updateView", tag: "views",
			summary: "Replace an existing GraphView's spec",
			req:     new(updateViewReq), resp: new(viewDTO), status: http.StatusOK,
			errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError},
		},
		{
			method: http.MethodDelete, path: "/api/views/{namespace}/{name}", id: "deleteView", tag: "views",
			summary: "Delete a GraphView", req: new(viewPath), status: http.StatusNoContent,
			errors: []int{http.StatusNotFound, http.StatusInternalServerError},
		},
	}
}

// buildOpenAPIReflector constructs the OpenAPI 3 document for the HTTP API by
// reflecting the request/response Go types.
func buildOpenAPIReflector() (*openapi3.Reflector, error) {
	r := openapi3.NewReflector()
	r.SpecEns().Info.
		WithTitle("Astron API").
		WithVersion(openAPIVersion).
		WithDescription("HTTP API for Project Astron: read the projected Kubernetes " +
			"resource graph, run GraphRAG retrieval, manage manual links and saved views.")

	for _, e := range apiEndpoints() {
		oc, err := r.NewOperationContext(e.method, e.path)
		if err != nil {
			return nil, err
		}
		oc.SetID(e.id)
		oc.SetSummary(e.summary)
		if e.tag != "" {
			oc.SetTags(e.tag)
		}
		if e.req != nil {
			oc.AddReqStructure(e.req)
		}
		if e.resp != nil {
			opts := []openapi.ContentOption{openapi.WithHTTPStatus(e.status)}
			if e.respMIME != "" {
				opts = append(opts, openapi.WithContentType(e.respMIME))
			}
			oc.AddRespStructure(e.resp, opts...)
		} else {
			oc.AddRespStructure(nil, openapi.WithHTTPStatus(e.status))
		}
		for _, code := range e.errors {
			oc.AddRespStructure(new(errorResponse), openapi.WithHTTPStatus(code))
		}
		if err := r.AddOperation(oc); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// OpenAPIJSON returns the generated OpenAPI 3 document as indented JSON.
func OpenAPIJSON() ([]byte, error) {
	r, err := buildOpenAPIReflector()
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(r.SpecEns(), "", "  ")
}

// OpenAPIYAML returns the generated OpenAPI 3 document as YAML.
func OpenAPIYAML() ([]byte, error) {
	j, err := OpenAPIJSON()
	if err != nil {
		return nil, err
	}
	return yaml.JSONToYAML(j)
}

// The document is deterministic, so build it once and reuse it.
var (
	openAPIOnce  sync.Once
	openAPIBytes []byte
	openAPIErr   error
)

func cachedOpenAPIJSON() ([]byte, error) {
	openAPIOnce.Do(func() {
		openAPIBytes, openAPIErr = OpenAPIJSON()
	})
	return openAPIBytes, openAPIErr
}

// handleOpenAPI serves the generated OpenAPI 3 document.
func (s *Server) handleOpenAPI(w http.ResponseWriter, _ *http.Request) {
	spec, err := cachedOpenAPIJSON()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(spec)
}
