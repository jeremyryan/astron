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

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// registerTools wires up the Astron retrieval tools exposed over MCP.
func (s *Server) registerTools() {
	s.register(tool{
		Name: "list_projections",
		Description: "List the Astron GraphProjections available in the cluster, " +
			"with their namespace, name, phase, and node/edge counts. Use this to " +
			"discover which projection to query.",
		InputSchema: objectSchema(nil, nil),
		handler:     s.toolListProjections,
	})

	s.register(tool{
		Name: "search_cluster_graph",
		Description: "Semantically search a projection's Kubernetes resource graph " +
			"for a natural-language query and return the most relevant resources " +
			"together with the connecting subgraph (owners, mounts, selectors, etc.) " +
			"and natural-language descriptions of each. Best for open-ended questions " +
			"like 'why is the web deployment unhealthy?'.",
		InputSchema: objectSchema(map[string]any{
			"projectionNamespace": stringProp("Namespace of the GraphProjection to search."),
			"projectionName":      stringProp("Name of the GraphProjection to search."),
			"query":               stringProp("The natural-language search query."),
			"topK":                intProp("Maximum number of seed resources to return (default 5)."),
			"hops":                intProp("How far to expand the graph around each seed (default 1)."),
			"edgeTypes":           stringArrayProp("Restrict expansion to these relationship types (e.g. OWNS, SELECTS, MOUNTS)."),
		}, []string{"projectionNamespace", "projectionName", "query"}),
		handler: s.toolSearch,
	})

	s.register(tool{
		Name: "get_resource_neighborhood",
		Description: "Return the subgraph within a number of hops of a specific " +
			"Kubernetes resource in a projection (its 'blast radius': owners, owned " +
			"objects, mounted config, selecting services, etc.). Does not require " +
			"embeddings. Best when you already know the exact resource.",
		InputSchema: objectSchema(map[string]any{
			"projectionNamespace": stringProp("Namespace of the GraphProjection."),
			"projectionName":      stringProp("Name of the GraphProjection."),
			"kind":                stringProp("Kind of the resource, e.g. 'Pod' or 'Deployment'."),
			"name":                stringProp("Name of the resource."),
			"namespace":           stringProp("Namespace of the resource (omit for cluster-scoped)."),
			"apiVersion":          stringProp("API version of the resource, e.g. 'apps/v1' (optional)."),
			"hops":                intProp("How far to expand around the resource (default 1)."),
			"edgeTypes":           stringArrayProp("Restrict expansion to these relationship types."),
		}, []string{"projectionNamespace", "projectionName", "kind", "name"}),
		handler: s.toolNeighborhood,
	})

	s.register(tool{
		Name: "answer_question",
		Description: "Ask a natural-language question about a projection's cluster " +
			"and get a grounded answer synthesized from the relevant resources and " +
			"their relationships, with citations. Requires a configured chat model.",
		InputSchema: objectSchema(map[string]any{
			"projectionNamespace": stringProp("Namespace of the GraphProjection."),
			"projectionName":      stringProp("Name of the GraphProjection."),
			"question":            stringProp("The natural-language question."),
			"topK":                intProp("Maximum number of seed resources to retrieve (default 5)."),
			"hops":                intProp("How far to expand the graph around each seed (default 1)."),
		}, []string{"projectionNamespace", "projectionName", "question"}),
		handler: s.toolAnswer,
	})

	s.register(tool{
		Name: "query_cluster",
		Description: "Answer a precise or aggregate question (counts, filters, joins) " +
			"by generating and running a guarded, read-only Cypher query over a " +
			"projection's graph. Returns the generated Cypher and the result rows. " +
			"Requires a configured chat model.",
		InputSchema: objectSchema(map[string]any{
			"projectionNamespace": stringProp("Namespace of the GraphProjection."),
			"projectionName":      stringProp("Name of the GraphProjection."),
			"question":            stringProp("The natural-language question to translate to Cypher."),
		}, []string{"projectionNamespace", "projectionName", "question"}),
		handler: s.toolQuery,
	})

	s.register(tool{
		Name: "get_resource_yaml",
		Description: "Fetch the live YAML manifest of a single Kubernetes resource " +
			"from the cluster (server-managed noise stripped). Use to inspect a " +
			"resource surfaced by the other tools in full detail.",
		InputSchema: objectSchema(map[string]any{
			"apiVersion": stringProp("API version, e.g. 'v1' or 'apps/v1'."),
			"kind":       stringProp("Kind, e.g. 'ConfigMap'."),
			"name":       stringProp("Resource name."),
			"namespace":  stringProp("Namespace (omit for cluster-scoped resources)."),
		}, []string{"apiVersion", "kind", "name"}),
		handler: s.toolResourceYAML,
	})
}

// --- handlers ---

func (s *Server) toolListProjections(ctx context.Context, _ json.RawMessage) (string, error) {
	raw, err := s.api.ListProjections(ctx)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

type searchArgs struct {
	ProjectionNamespace string   `json:"projectionNamespace"`
	ProjectionName      string   `json:"projectionName"`
	Query               string   `json:"query"`
	TopK                int      `json:"topK"`
	Hops                *int     `json:"hops"`
	EdgeTypes           []string `json:"edgeTypes"`
}

func (s *Server) toolSearch(ctx context.Context, raw json.RawMessage) (string, error) {
	var a searchArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if a.ProjectionNamespace == "" || a.ProjectionName == "" || a.Query == "" {
		return "", fmt.Errorf("projectionNamespace, projectionName and query are required")
	}
	body := map[string]any{"query": a.Query, "topK": a.TopK, "edgeTypes": a.EdgeTypes}
	if a.Hops != nil {
		body["hops"] = *a.Hops
	}
	out, err := s.api.Search(ctx, a.ProjectionNamespace, a.ProjectionName, body)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

type neighborhoodArgs struct {
	ProjectionNamespace string   `json:"projectionNamespace"`
	ProjectionName      string   `json:"projectionName"`
	APIVersion          string   `json:"apiVersion"`
	Kind                string   `json:"kind"`
	Name                string   `json:"name"`
	Namespace           string   `json:"namespace"`
	Hops                *int     `json:"hops"`
	EdgeTypes           []string `json:"edgeTypes"`
}

func (s *Server) toolNeighborhood(ctx context.Context, raw json.RawMessage) (string, error) {
	var a neighborhoodArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if a.ProjectionNamespace == "" || a.ProjectionName == "" || a.Kind == "" || a.Name == "" {
		return "", fmt.Errorf("projectionNamespace, projectionName, kind and name are required")
	}
	body := map[string]any{
		"apiVersion": a.APIVersion,
		"kind":       a.Kind,
		"name":       a.Name,
		"namespace":  a.Namespace,
		"edgeTypes":  a.EdgeTypes,
	}
	if a.Hops != nil {
		body["hops"] = *a.Hops
	}
	out, err := s.api.Neighborhood(ctx, a.ProjectionNamespace, a.ProjectionName, body)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

type questionArgs struct {
	ProjectionNamespace string `json:"projectionNamespace"`
	ProjectionName      string `json:"projectionName"`
	Question            string `json:"question"`
	TopK                int    `json:"topK"`
	Hops                *int   `json:"hops"`
}

func (s *Server) toolAnswer(ctx context.Context, raw json.RawMessage) (string, error) {
	a, err := parseQuestionArgs(raw)
	if err != nil {
		return "", err
	}
	body := map[string]any{"question": a.Question, "topK": a.TopK}
	if a.Hops != nil {
		body["hops"] = *a.Hops
	}
	out, err := s.api.Answer(ctx, a.ProjectionNamespace, a.ProjectionName, body)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (s *Server) toolQuery(ctx context.Context, raw json.RawMessage) (string, error) {
	a, err := parseQuestionArgs(raw)
	if err != nil {
		return "", err
	}
	out, err := s.api.Query(ctx, a.ProjectionNamespace, a.ProjectionName, map[string]any{"question": a.Question})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func parseQuestionArgs(raw json.RawMessage) (questionArgs, error) {
	var a questionArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return questionArgs{}, fmt.Errorf("invalid arguments: %w", err)
	}
	if a.ProjectionNamespace == "" || a.ProjectionName == "" || a.Question == "" {
		return questionArgs{}, fmt.Errorf("projectionNamespace, projectionName and question are required")
	}
	return a, nil
}

type resourceYAMLArgs struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
}

func (s *Server) toolResourceYAML(ctx context.Context, raw json.RawMessage) (string, error) {
	var a resourceYAMLArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if a.APIVersion == "" || a.Kind == "" || a.Name == "" {
		return "", fmt.Errorf("apiVersion, kind and name are required")
	}
	q := url.Values{}
	q.Set("apiVersion", a.APIVersion)
	q.Set("kind", a.Kind)
	q.Set("name", a.Name)
	if a.Namespace != "" {
		q.Set("namespace", a.Namespace)
	}
	out, err := s.api.ResourceYAML(ctx, q)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// --- JSON Schema helpers ---

func objectSchema(properties map[string]any, required []string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func stringProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func intProp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

func stringArrayProp(desc string) map[string]any {
	return map[string]any{
		"type":        "array",
		"items":       map[string]any{"type": "string"},
		"description": desc,
	}
}
