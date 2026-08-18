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

	"github.com/project-astron/astron/internal/agent"
)

// registerTools wires up the Astron retrieval tools exposed over MCP.
//
// search_cluster_graph, get_resource_neighborhood, query_graph and
// get_graph_schema come from agent.Catalog() — the same tool definitions the
// in-process chat agent offers — wrapped with the
// projectionNamespace/projectionName parameters this multi-projection,
// external-client transport needs (see agent.ForMultiProjection). This keeps
// their names, descriptions and schemas defined in exactly one place.
// get_resource_yaml is used as-is from the catalog: it already addresses a
// resource directly, with no projection to route to. list_projections and
// answer_question are MCP-specific: the former has no per-projection scope to
// wrap, and the latter is the fixed answer pipeline the chat agent
// deliberately does not expose as a tool to itself.
func (s *Server) registerTools() {
	s.register(tool{
		Name: "list_projections",
		Description: "List the Astron GraphProjections available in the cluster, " +
			"with their namespace, name, phase, and node/edge counts. Use this to " +
			"discover which projection to query.",
		InputSchema: objectSchema(nil, nil),
		handler:     s.toolListProjections,
	})

	s.registerCatalogTool(agent.ToolSearchClusterGraph, s.toolSearch)
	s.registerCatalogTool(agent.ToolGetResourceNeighborhood, s.toolNeighborhood)
	s.registerCatalogTool(agent.ToolQueryGraph, s.toolQuery)
	s.registerCatalogTool(agent.ToolGetGraphSchema, s.toolSchema)

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

	s.registerCatalogToolAsIs(agent.ToolGetResourceYAML, s.toolResourceYAML)
}

// registerCatalogTool registers name from agent.Catalog(), wrapped with
// agent.ForMultiProjection so external MCP clients can name the projection to
// operate on.
func (s *Server) registerCatalogTool(name string, handler toolHandler) {
	spec, ok := agent.CatalogByName(name)
	if !ok {
		panic("mcp: catalog tool not found: " + name)
	}
	scoped := agent.ForMultiProjection(spec)
	s.register(tool{Name: scoped.Name, Description: scoped.Description, InputSchema: scoped.Parameters, handler: handler})
}

// registerCatalogToolAsIs registers name from agent.Catalog() unmodified, for
// tools that don't need projection-routing parameters (get_resource_yaml
// already addresses a specific live resource).
func (s *Server) registerCatalogToolAsIs(name string, handler toolHandler) {
	spec, ok := agent.CatalogByName(name)
	if !ok {
		panic("mcp: catalog tool not found: " + name)
	}
	s.register(tool{Name: spec.Name, Description: spec.Description, InputSchema: spec.Parameters, handler: handler})
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

type schemaArgs struct {
	ProjectionNamespace string `json:"projectionNamespace"`
	ProjectionName      string `json:"projectionName"`
}

func (s *Server) toolSchema(ctx context.Context, raw json.RawMessage) (string, error) {
	var a schemaArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if a.ProjectionNamespace == "" || a.ProjectionName == "" {
		return "", fmt.Errorf("projectionNamespace and projectionName are required")
	}
	out, err := s.api.Schema(ctx, a.ProjectionNamespace, a.ProjectionName)
	if err != nil {
		return "", err
	}
	return string(out), nil
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
//
// These duplicate internal/agent's identical helpers rather than importing
// them, since agent.Catalog() (built with the ones there) is the shared
// source of truth for the four wrapped tools above; these remain for the two
// MCP-specific tool definitions (list_projections, answer_question).

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
