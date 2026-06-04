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

// Package api exposes a read-only HTTP API over the projected resource graph,
// and serves the web UI. It is consumed by the Gamera frontend.
package api

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	gamerav1alpha1 "github.com/project-gamera/gamera/api/v1alpha1"
	"github.com/project-gamera/gamera/internal/graph"
	"github.com/project-gamera/gamera/internal/projector"
)

// Server serves the Gamera read API and (optionally) the embedded web UI.
type Server struct {
	client     client.Client
	projectors *projector.Manager
	// dyn and mapper are used to fetch arbitrary live resources (for the YAML
	// view) without going through the controller-runtime cache. They may be nil,
	// in which case the resource endpoint is disabled.
	dyn    dynamic.Interface
	mapper meta.RESTMapper
	assets fs.FS
}

// NewServer builds a Server. assets may be nil to disable static UI serving
// (e.g. in tests or API-only deployments). dyn and mapper may be nil to disable
// the live-resource (YAML) endpoint.
func NewServer(c client.Client, projectors *projector.Manager, dyn dynamic.Interface, mapper meta.RESTMapper, assets fs.FS) *Server {
	return &Server{client: c, projectors: projectors, dyn: dyn, mapper: mapper, assets: assets}
}

// Handler returns the HTTP handler for the API and UI.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/healthz", s.handleHealth)
	mux.HandleFunc("GET /api/projections", s.handleListProjections)
	mux.HandleFunc("GET /api/projections/{namespace}/{name}/graph", s.handleGraph)
	mux.HandleFunc("GET /api/resource", s.handleResourceYAML)

	if s.assets != nil {
		mux.Handle("/", s.spaHandler())
	}
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleListProjections(w http.ResponseWriter, r *http.Request) {
	var list gamerav1alpha1.GraphProjectionList
	if err := s.client.List(r.Context(), &list); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	out := make([]projectionDTO, 0, len(list.Items))
	for _, p := range list.Items {
		out = append(out, projectionToDTO(p))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	namespace := r.PathValue("namespace")
	name := r.PathValue("name")

	var projection gamerav1alpha1.GraphProjection
	key := client.ObjectKey{Namespace: namespace, Name: name}
	if err := s.client.Get(r.Context(), key, &projection); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	data, err := s.projectors.ReadGraph(r.Context(), graph.ProjectionID(projection.UID))
	if err != nil {
		if errors.Is(err, projector.ErrNotRunning) {
			// The projection exists but its projector is not (yet) running.
			writeJSON(w, http.StatusOK, graphDTO{Nodes: []nodeDTO{}, Edges: []edgeDTO{}})
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, graphToDTO(data))
}

// handleResourceYAML fetches a single live resource from the cluster and returns
// its YAML representation. The resource is identified by query parameters:
// apiVersion, kind, name and (for namespaced kinds) namespace.
func (s *Server) handleResourceYAML(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	apiVersion := q.Get("apiVersion")
	kind := q.Get("kind")
	name := q.Get("name")
	namespace := q.Get("namespace")
	if apiVersion == "" || kind == "" || name == "" {
		writeError(w, http.StatusBadRequest, errors.New("apiVersion, kind and name query parameters are required"))
		return
	}
	if s.dyn == nil || s.mapper == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("live resource fetching is not configured"))
		return
	}

	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	gvk := gv.WithKind(kind)
	mapping, err := s.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	var ri dynamic.ResourceInterface
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		ri = s.dyn.Resource(mapping.Resource).Namespace(namespace)
	} else {
		ri = s.dyn.Resource(mapping.Resource)
	}

	obj, err := ri.Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	// Strip noisy server-managed fields for a readable manifest.
	unstructured.RemoveNestedField(obj.Object, "metadata", "managedFields")
	if ann := obj.GetAnnotations(); ann != nil {
		delete(ann, "kubectl.kubernetes.io/last-applied-configuration")
		if len(ann) == 0 {
			unstructured.RemoveNestedField(obj.Object, "metadata", "annotations")
		} else {
			obj.SetAnnotations(ann)
		}
	}

	out, err := yaml.Marshal(obj.Object)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

// spaHandler serves the embedded single-page app, falling back to index.html
// for client-side routes (any path that is not an existing asset).
func (s *Server) spaHandler() http.Handler {
	fileServer := http.FileServer(http.FS(s.assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if upath == "" {
			upath = "index.html"
		}
		if _, err := fs.Stat(s.assets, upath); err != nil {
			// Not a real asset: serve index.html so the SPA router can handle it.
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			http.ServeFileFS(w, r2, s.assets, "index.html")
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
