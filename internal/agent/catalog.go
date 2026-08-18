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

package agent

import (
	"maps"

	"github.com/project-astron/astron/internal/rag"
)

// Tool names in the phase-1 catalog, exported so callers (ToolSet builders,
// the API layer, tests) can reference them without repeating string literals.
const (
	ToolSearchClusterGraph      = "search_cluster_graph"
	ToolGetResourceNeighborhood = "get_resource_neighborhood"
	ToolQueryGraph              = "query_graph"
	ToolGetGraphSchema          = "get_graph_schema"
	ToolGetResourceYAML         = "get_resource_yaml"
)

// Catalog returns the canonical rag.ToolSpecs for the phase-1 chat agent.
// It is the single source of truth for these tools' names, descriptions and
// parameter schemas.
//
// The agent is scoped to a single, already-selected projection (the caller
// binds each Tool's Invoke to that projection when building a ToolSet), so
// unlike the equivalent tools the MCP server exposes to external clients,
// these schemas carry no projectionNamespace/projectionName parameters. A
// future refactor of internal/mcp/tools.go can wrap these same specs with
// routing parameters so both surfaces stay defined in one place.
func Catalog() []rag.ToolSpec {
	return []rag.ToolSpec{
		{
			Name: ToolSearchClusterGraph,
			Description: "Semantically search the projection's Kubernetes resource graph " +
				"for a natural-language query and return the most relevant resources " +
				"together with the connecting subgraph (owners, mounts, selectors, etc.). " +
				"Best for open-ended questions like 'why is the web deployment unhealthy?'.",
			Parameters: objectSchema(map[string]any{
				"query":     stringProp("The natural-language search query."),
				"topK":      intProp("Maximum number of seed resources to return (default 5)."),
				"hops":      intProp("How far to expand the graph around each seed (default 1)."),
				"edgeTypes": stringArrayProp("Restrict expansion to these relationship types (e.g. OWNS, SELECTS, MOUNTS)."),
			}, []string{"query"}),
		},
		{
			Name: ToolGetResourceNeighborhood,
			Description: "Return the subgraph within a number of hops of a specific Kubernetes " +
				"resource (its 'blast radius': owners, owned objects, mounted config, selecting " +
				"services, etc.). Does not require embeddings. Best when you already know the " +
				"exact resource.",
			Parameters: objectSchema(map[string]any{
				"kind":       stringProp("Kind of the resource, e.g. 'Pod' or 'Deployment'."),
				"name":       stringProp("Name of the resource."),
				"namespace":  stringProp("Namespace of the resource (omit for cluster-scoped)."),
				"apiVersion": stringProp("API version of the resource, e.g. 'apps/v1' (optional)."),
				"hops":       intProp("How far to expand around the resource (default 1)."),
				"edgeTypes":  stringArrayProp("Restrict expansion to these relationship types."),
			}, []string{"kind", "name"}),
		},
		{
			Name: ToolQueryGraph,
			Description: "Answer a precise or aggregate question (counts, filters, joins) by " +
				"generating and running a guarded, read-only Cypher query over the projection's " +
				"graph. Returns the generated Cypher and the result rows.",
			Parameters: objectSchema(map[string]any{
				"question": stringProp("The natural-language question to translate to Cypher."),
			}, []string{"question"}),
		},
		{
			Name: ToolGetGraphSchema,
			Description: "Return a summary of the projection's current graph schema: the " +
				"resource kinds present and the relationship types between them. Cheap " +
				"orientation to help form a query_graph question or check whether a resource " +
				"kind exists before searching for it.",
			Parameters: objectSchema(nil, nil),
		},
		{
			Name: ToolGetResourceYAML,
			Description: "Fetch the live YAML manifest of a single Kubernetes resource from the " +
				"cluster (server-managed noise stripped). Use to inspect a resource surfaced by " +
				"the other tools in full detail.",
			Parameters: objectSchema(map[string]any{
				"apiVersion": stringProp("API version, e.g. 'v1' or 'apps/v1'."),
				"kind":       stringProp("Kind, e.g. 'ConfigMap'."),
				"name":       stringProp("Resource name."),
				"namespace":  stringProp("Namespace (omit for cluster-scoped resources)."),
			}, []string{"apiVersion", "kind", "name"}),
		},
	}
}

// CatalogByName returns the catalog's ToolSpec for name, if any.
func CatalogByName(name string) (rag.ToolSpec, bool) {
	for _, spec := range Catalog() {
		if spec.Name == name {
			return spec, true
		}
	}
	return rag.ToolSpec{}, false
}

// ForMultiProjection adapts a catalog ToolSpec for transports that address
// more than one projection — like the MCP server, which serves external
// clients rather than operating within a single, already-selected
// projection — by adding the projectionNamespace/projectionName parameters
// the in-process agent doesn't need. The original spec is left unmodified.
func ForMultiProjection(spec rag.ToolSpec) rag.ToolSpec {
	props := map[string]any{
		"projectionNamespace": stringProp("Namespace of the GraphProjection."),
		"projectionName":      stringProp("Name of the GraphProjection."),
	}
	if orig, ok := spec.Parameters["properties"].(map[string]any); ok {
		maps.Copy(props, orig)
	}
	required := []string{"projectionNamespace", "projectionName"}
	if orig, ok := spec.Parameters["required"].([]string); ok {
		required = append(required, orig...)
	}
	spec.Parameters = objectSchema(props, required)
	return spec
}

// --- JSON Schema helpers ---
//
// These mirror the helpers in internal/mcp/tools.go; a later refactor can
// share one copy once the MCP tool definitions are rebuilt on this catalog.

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
