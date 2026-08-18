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

package projector

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/project-astron/astron/internal/agent"
	"github.com/project-astron/astron/internal/graph"
	"github.com/project-astron/astron/internal/rag"
)

// toolSet builds the agent.ToolSet exposing this projector's retrieval and
// live-read capabilities to a tool-calling chat model, binding each of
// agent.Catalog's tool specs to a handler that operates on this projection.
// model is the chat model name in use for the run; it is threaded through to
// query_graph so its Cypher generation shares the same resolved chat and
// allowedModels policy as the rest of the conversation.
//
// Tools whose capability isn't available for this projection (e.g. no chat
// configured, so query_graph would always fail) are still included: their
// handler returns an error, which the agent.Runner turns into an observation
// telling the model the capability is unavailable, rather than omitting the
// tool and confusing the model about why it can't be found.
func (p *Projector) toolSet(model string) agent.ToolSet {
	handlers := map[string]func(ctx context.Context, args json.RawMessage) (string, error){
		agent.ToolSearchClusterGraph:      p.toolSearch,
		agent.ToolGetResourceNeighborhood: p.toolNeighborhood,
		agent.ToolQueryGraph: func(ctx context.Context, args json.RawMessage) (string, error) {
			return p.toolQuery(ctx, args, model)
		},
		agent.ToolGetGraphSchema:  p.toolSchema,
		agent.ToolGetResourceYAML: p.toolResourceYAML,
	}

	catalog := agent.Catalog()
	tools := make(agent.ToolSet, 0, len(catalog))
	for _, spec := range catalog {
		if h, ok := handlers[spec.Name]; ok {
			tools = append(tools, agent.Tool{Spec: spec, Invoke: h})
		}
	}
	return tools
}

// searchToolArgs is the search_cluster_graph tool's argument shape, matching
// agent.Catalog's schema for it.
type searchToolArgs struct {
	Query     string   `json:"query"`
	TopK      int      `json:"topK"`
	Hops      *int     `json:"hops"`
	EdgeTypes []string `json:"edgeTypes"`
}

func (p *Projector) toolSearch(ctx context.Context, raw json.RawMessage) (string, error) {
	var a searchToolArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if a.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	opts := SearchOptions{TopK: a.TopK, Hops: defaultSearchHops, EdgeTypes: a.EdgeTypes}
	if a.Hops != nil {
		opts.Hops = *a.Hops
	}
	retrieval, err := p.Search(ctx, a.Query, opts)
	if err != nil {
		return "", err
	}
	return marshalRetrieval(retrieval)
}

// neighborhoodToolArgs is the get_resource_neighborhood tool's argument
// shape, matching agent.Catalog's schema for it.
type neighborhoodToolArgs struct {
	Kind       string   `json:"kind"`
	Name       string   `json:"name"`
	Namespace  string   `json:"namespace"`
	APIVersion string   `json:"apiVersion"`
	Hops       *int     `json:"hops"`
	EdgeTypes  []string `json:"edgeTypes"`
}

func (p *Projector) toolNeighborhood(ctx context.Context, raw json.RawMessage) (string, error) {
	var a neighborhoodToolArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if a.Kind == "" || a.Name == "" {
		return "", fmt.Errorf("kind and name are required")
	}
	hops := defaultSearchHops
	if a.Hops != nil {
		hops = *a.Hops
	}
	ref := graph.Ref{APIVersion: a.APIVersion, Kind: a.Kind, Namespace: a.Namespace, Name: a.Name}
	retrieval, err := p.Neighborhood(ctx, ref, hops, a.EdgeTypes)
	if err != nil {
		return "", err
	}
	return marshalRetrieval(retrieval)
}

// queryToolArgs is the query_graph tool's argument shape, matching
// agent.Catalog's schema for it.
type queryToolArgs struct {
	Question string `json:"question"`
}

func (p *Projector) toolQuery(ctx context.Context, raw json.RawMessage, model string) (string, error) {
	var a queryToolArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if a.Question == "" {
		return "", fmt.Errorf("question is required")
	}
	result, err := p.Query(ctx, a.Question, model)
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("encoding query result: %w", err)
	}
	return string(out), nil
}

// get_graph_schema takes no arguments.
func (p *Projector) toolSchema(ctx context.Context, _ json.RawMessage) (string, error) {
	data, err := p.ReadGraph(ctx)
	if err != nil {
		return "", fmt.Errorf("reading graph: %w", err)
	}
	return rag.SchemaSummary(data), nil
}

// resourceYAMLToolArgs is the get_resource_yaml tool's argument shape,
// matching agent.Catalog's schema for it.
type resourceYAMLToolArgs struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
}

func (p *Projector) toolResourceYAML(ctx context.Context, raw json.RawMessage) (string, error) {
	var a resourceYAMLToolArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if a.APIVersion == "" || a.Kind == "" || a.Name == "" {
		return "", fmt.Errorf("apiVersion, kind and name are required")
	}
	out, err := p.ResourceYAML(ctx, a.APIVersion, a.Kind, a.Namespace, a.Name)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// toolRetrievalObservation is the compact JSON shape fed back to the model
// for search_cluster_graph and get_resource_neighborhood: the cards
// describing every resource in the retrieved subgraph, plus short
// relationship sentences connecting them — the same context Answer grounds
// itself with.
type toolRetrievalObservation struct {
	Query         string          `json:"query"`
	Cards         []toolCardEntry `json:"cards"`
	Relationships []string        `json:"relationships,omitempty"`
}

// toolCardEntry is one resource's natural-language card in a tool observation.
type toolCardEntry struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	Text      string `json:"text"`
}

func marshalRetrieval(r Retrieval) (string, error) {
	obs := toolRetrievalObservation{
		Query:         r.Query,
		Cards:         make([]toolCardEntry, 0, len(r.Cards)),
		Relationships: relationshipSentences(r.Subgraph),
	}
	for _, c := range r.Cards {
		obs.Cards = append(obs.Cards, toolCardEntry{
			Kind:      c.Ref.Kind,
			Namespace: c.Ref.Namespace,
			Name:      c.Ref.Name,
			Text:      c.Text,
		})
	}
	out, err := json.Marshal(obs)
	if err != nil {
		return "", fmt.Errorf("encoding retrieval result: %w", err)
	}
	return string(out), nil
}
