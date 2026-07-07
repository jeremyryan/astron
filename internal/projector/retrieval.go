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
	"errors"
	"fmt"

	"github.com/project-astron/astron/internal/graph"
	"github.com/project-astron/astron/internal/rag"
)

// ErrRAGNotEnabled indicates that a projection cannot serve semantic (vector)
// retrieval because no embedder/vector store is configured for it. Structural
// retrieval (Neighborhood) does not require embeddings and is unaffected.
var ErrRAGNotEnabled = errors.New("graphrag is not enabled for this projection")

// ErrChatNotEnabled indicates that natural-language answering / text-to-Cypher
// is not configured (no chat model) for a projection.
var ErrChatNotEnabled = errors.New("natural-language answering is not enabled for this projection")

// Default retrieval parameters.
const (
	defaultSearchTopK = 5
	defaultSearchHops = 1
)

// SearchOptions parameterizes a hybrid (vector + graph) retrieval.
type SearchOptions struct {
	// TopK bounds the number of vector seed nodes. Defaults to 5.
	TopK int
	// Hops is how far to expand the graph around each seed. 0 returns seeds
	// only; defaults to 1.
	Hops int
	// EdgeTypes optionally restricts expansion (and the returned subgraph) to
	// these relationship types. Empty means all types.
	EdgeTypes []string
	// Filter optionally constrains seed selection by kind/namespace.
	Filter graph.VectorFilter
}

// Seed is a retrieval entry point: a node and the score that selected it. For
// vector search this is the similarity score; for neighborhood retrieval it is
// 1.
type Seed struct {
	Ref   graph.Ref
	Score float64
}

// Retrieval is the assembled context returned to a GraphRAG caller: the seed
// nodes, the connected subgraph around them, and the natural-language cards for
// every node in that subgraph (for grounding an LLM with provenance).
type Retrieval struct {
	Query    string
	Seeds    []Seed
	Cards    []rag.Card
	Subgraph graph.GraphData
}

// Search performs hybrid retrieval: it embeds the query, finds the most similar
// seed nodes via the vector index, then expands the graph around them and
// assembles the connecting subgraph and its cards. It requires embedding to be
// enabled.
func (p *Projector) Search(ctx context.Context, query string, opts SearchOptions) (Retrieval, error) {
	if !p.embeddingEnabled() {
		return Retrieval{}, ErrRAGNotEnabled
	}
	topK := opts.TopK
	if topK <= 0 {
		topK = defaultSearchTopK
	}

	vecs, err := p.opts.Embedder.Embed(ctx, []string{query})
	if err != nil {
		return Retrieval{}, fmt.Errorf("embedding query: %w", err)
	}
	if len(vecs) != 1 {
		return Retrieval{}, fmt.Errorf("embedder returned %d vectors for the query", len(vecs))
	}

	hits, err := p.opts.VectorStore.VectorSearch(ctx, p.opts.ID, vecs[0], topK, opts.Filter)
	if err != nil {
		return Retrieval{}, fmt.Errorf("vector search: %w", err)
	}

	seeds := make([]Seed, 0, len(hits))
	seedIDs := make(map[string]bool, len(hits))
	for _, h := range hits {
		seeds = append(seeds, Seed{Ref: h.Node.Ref, Score: h.Score})
		seedIDs[h.Node.Ref.ID()] = true
	}

	data, err := p.opts.Store.ReadGraph(ctx, p.opts.ID)
	if err != nil {
		return Retrieval{}, fmt.Errorf("reading graph: %w", err)
	}
	return p.assemble(query, seeds, seedIDs, data, opts.Hops, opts.EdgeTypes), nil
}

// Neighborhood performs pure structural retrieval: it locates the named
// resource in the graph and returns the subgraph within hops of it. It does not
// require embeddings.
func (p *Projector) Neighborhood(ctx context.Context, ref graph.Ref, hops int, edgeTypes []string) (Retrieval, error) {
	data, err := p.opts.Store.ReadGraph(ctx, p.opts.ID)
	if err != nil {
		return Retrieval{}, fmt.Errorf("reading graph: %w", err)
	}

	id, resolved, ok := findNode(data, ref)
	if !ok {
		// The resource is not in the projected graph: return an empty result
		// rather than an error, mirroring how the graph endpoint behaves.
		return Retrieval{Query: ref.String(), Subgraph: graph.GraphData{}}, nil
	}
	seeds := []Seed{{Ref: resolved, Score: 1}}
	seedIDs := map[string]bool{id: true}
	return p.assemble(ref.String(), seeds, seedIDs, data, hops, edgeTypes), nil
}

// assemble expands the seed set over the graph and builds the resulting
// subgraph and per-node cards.
func (p *Projector) assemble(query string, seeds []Seed, seedIDs map[string]bool, data graph.GraphData, hops int, edgeTypes []string) Retrieval {
	if hops < 0 {
		hops = 0
	}
	allowed := typeSet(edgeTypes)
	included := expand(data, seedIDs, hops, allowed)

	sub := graph.GraphData{}
	for _, n := range data.Nodes {
		if included[n.Ref.ID()] {
			sub.Nodes = append(sub.Nodes, n)
		}
	}
	for _, r := range data.Relationships {
		if included[r.From.ID()] && included[r.To.ID()] && allowed.has(r.Type) {
			sub.Relationships = append(sub.Relationships, r)
		}
	}

	// Build cards over the full graph so each card reflects the node's complete
	// set of relationships, then keep only those for included nodes.
	var cards []rag.Card
	for _, c := range rag.BuildCards(data, p.opts.CardOptions) {
		if included[c.Ref.ID()] {
			cards = append(cards, c)
		}
	}

	return Retrieval{Query: query, Seeds: seeds, Cards: cards, Subgraph: sub}
}

// expand performs a breadth-first expansion from the seed node IDs over the
// undirected graph induced by the allowed edge types, up to the given number of
// hops. It returns the set of reachable node IDs (including the seeds).
func expand(data graph.GraphData, seedIDs map[string]bool, hops int, allowed typeFilter) map[string]bool {
	included := make(map[string]bool, len(seedIDs))
	for id := range seedIDs {
		included[id] = true
	}
	if hops == 0 {
		return included
	}

	adj := map[string][]string{}
	for _, r := range data.Relationships {
		if !allowed.has(r.Type) {
			continue
		}
		f, t := r.From.ID(), r.To.ID()
		adj[f] = append(adj[f], t)
		adj[t] = append(adj[t], f)
	}

	frontier := make([]string, 0, len(seedIDs))
	for id := range seedIDs {
		frontier = append(frontier, id)
	}
	for h := 0; h < hops && len(frontier) > 0; h++ {
		var next []string
		for _, id := range frontier {
			for _, nb := range adj[id] {
				if !included[nb] {
					included[nb] = true
					next = append(next, nb)
				}
			}
		}
		frontier = next
	}
	return included
}

// findNode locates a node in the graph by identity (kind, namespace, name; or
// by ID when a UID is supplied) and returns its graph ID and full Ref.
func findNode(data graph.GraphData, ref graph.Ref) (string, graph.Ref, bool) {
	wantID := ref.ID()
	for _, n := range data.Nodes {
		if ref.UID != "" {
			if n.Ref.ID() == wantID {
				return n.Ref.ID(), n.Ref, true
			}
			continue
		}
		if n.Ref.Kind == ref.Kind && n.Ref.Namespace == ref.Namespace && n.Ref.Name == ref.Name {
			return n.Ref.ID(), n.Ref, true
		}
	}
	return "", graph.Ref{}, false
}

// typeFilter is a set of allowed relationship types. A nil/empty filter allows
// every type.
type typeFilter map[string]bool

func typeSet(types []string) typeFilter {
	if len(types) == 0 {
		return nil
	}
	s := make(typeFilter, len(types))
	for _, t := range types {
		s[t] = true
	}
	return s
}

func (f typeFilter) has(t string) bool {
	if len(f) == 0 {
		return true
	}
	return f[t]
}
