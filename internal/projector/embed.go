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
	"fmt"
	"time"

	"github.com/project-gamera/gamera/internal/graph"
	"github.com/project-gamera/gamera/internal/rag"
)

// embeddingEnabled reports whether GraphRAG embedding refresh is configured.
func (p *Projector) embeddingEnabled() bool {
	return p.opts.Embedder != nil && p.opts.VectorStore != nil
}

// EmbeddingStatus reports the current GraphRAG embedding state for status
// reporting: whether embedding is enabled, how many nodes currently have an
// embedding, whether the vector index has been created, and when embeddings
// were last refreshed.
func (p *Projector) EmbeddingStatus() (enabled, indexReady bool, count int, lastRefresh time.Time) {
	if !p.embeddingEnabled() {
		return false, false, 0, time.Time{}
	}
	p.embedMu.Lock()
	defer p.embedMu.Unlock()
	return true, p.vectorIndexReady, len(p.cardHashes), p.lastEmbedTime
}

// refreshEmbeddings brings the projection's node embeddings up to date after a
// sync. It renders each node (plus its immediate edges) into a textual card,
// embeds only the cards whose content changed since the last refresh, and
// upserts the resulting vectors. Unchanged nodes are skipped, which keeps the
// embedding cost proportional to churn rather than graph size.
//
// It is a no-op when embedding is not configured. Errors are returned but are
// not fatal to the surrounding sync: a projection stays correct even if its
// embeddings momentarily lag.
func (p *Projector) refreshEmbeddings(ctx context.Context, nodes []graph.Node, edges []graph.Relationship) error {
	if !p.embeddingEnabled() {
		return nil
	}

	cards := rag.BuildCards(graph.GraphData{Nodes: nodes, Relationships: edges}, p.opts.CardOptions)

	p.embedMu.Lock()
	defer p.embedMu.Unlock()

	// Identify cards whose content changed since they were last embedded, and
	// the set of node IDs still present (to prune deleted nodes from the cache).
	present := make(map[string]bool, len(cards))
	var changed []rag.Card
	for _, c := range cards {
		id := c.Ref.ID()
		present[id] = true
		if p.cardHashes[id] != c.Hash {
			changed = append(changed, c)
		}
	}
	for id := range p.cardHashes {
		if !present[id] {
			delete(p.cardHashes, id)
		}
	}

	if len(changed) == 0 {
		return nil
	}

	texts := make([]string, len(changed))
	for i, c := range changed {
		texts[i] = c.Text
	}
	vectors, err := rag.EmbedBatched(ctx, p.opts.Embedder, texts, p.opts.EmbeddingBatchSize)
	if err != nil {
		return fmt.Errorf("embedding %d changed cards: %w", len(changed), err)
	}
	if len(vectors) != len(changed) {
		return fmt.Errorf("embedder returned %d vectors for %d cards", len(vectors), len(changed))
	}

	model := p.opts.Embedder.Model()
	embeddings := make([]graph.NodeEmbedding, len(changed))
	for i, c := range changed {
		embeddings[i] = graph.NodeEmbedding{
			Ref:      c.Ref,
			Vector:   vectors[i],
			Card:     c.Text,
			CardHash: c.Hash,
			Model:    model,
		}
	}

	if err := p.ensureVectorIndex(ctx, len(vectors[0])); err != nil {
		return err
	}
	if err := p.opts.VectorStore.UpsertEmbeddings(ctx, p.opts.ID, embeddings); err != nil {
		return fmt.Errorf("upserting %d embeddings: %w", len(embeddings), err)
	}

	// Record the new hashes only after a successful upsert, so a failed refresh
	// is retried on the next sync.
	for _, c := range changed {
		p.cardHashes[c.Ref.ID()] = c.Hash
	}
	p.lastEmbedTime = time.Now()
	return nil
}

// ensureVectorIndex lazily creates the vector index on first use. The dimension
// is taken from the embedder when it reports one, otherwise from the length of
// an actual embedding (fallbackDims). Must be called with embedMu held.
func (p *Projector) ensureVectorIndex(ctx context.Context, fallbackDims int) error {
	if p.vectorIndexReady {
		return nil
	}
	dims := p.opts.Embedder.Dimensions()
	if dims <= 0 {
		dims = fallbackDims
	}
	if err := p.opts.VectorStore.EnsureVectorIndex(ctx, dims, p.opts.VectorSimilarity); err != nil {
		return fmt.Errorf("ensuring vector index: %w", err)
	}
	p.vectorIndexReady = true
	return nil
}
