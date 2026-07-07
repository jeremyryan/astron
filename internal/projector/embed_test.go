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
	"testing"

	"github.com/project-astron/astron/internal/graph"
	"github.com/project-astron/astron/internal/rag"
)

// fakeVectorStore records embedding upserts for assertions.
type fakeVectorStore struct {
	indexCalls   int
	indexDims    int
	upserts      [][]graph.NodeEmbedding // one entry per UpsertEmbeddings call
	current      map[string]graph.NodeEmbedding
	upsertErr    error
	ensureIdxErr error
}

func newFakeVectorStore() *fakeVectorStore {
	return &fakeVectorStore{current: map[string]graph.NodeEmbedding{}}
}

func (f *fakeVectorStore) EnsureVectorIndex(_ context.Context, dims int, _ string) error {
	if f.ensureIdxErr != nil {
		return f.ensureIdxErr
	}
	f.indexCalls++
	f.indexDims = dims
	return nil
}

func (f *fakeVectorStore) UpsertEmbeddings(_ context.Context, _ graph.ProjectionID, es []graph.NodeEmbedding) error {
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.upserts = append(f.upserts, es)
	for _, e := range es {
		f.current[e.Ref.ID()] = e
	}
	return nil
}

func (f *fakeVectorStore) VectorSearch(context.Context, graph.ProjectionID, []float32, int, graph.VectorFilter) ([]graph.VectorHit, error) {
	return nil, nil
}

// lastUpsertCount returns the size of the most recent upsert batch.
func (f *fakeVectorStore) lastUpsertCount() int {
	if len(f.upserts) == 0 {
		return 0
	}
	return len(f.upserts[len(f.upserts)-1])
}

// countingEmbedder wraps a FakeEmbedder and counts how many texts it embeds.
type countingEmbedder struct {
	inner    *rag.FakeEmbedder
	embedded int
}

func newCountingEmbedder(dims int) *countingEmbedder {
	return &countingEmbedder{inner: rag.NewFakeEmbedder(dims)}
}

func (c *countingEmbedder) Embed(ctx context.Context, texts []string) ([]rag.Embedding, error) {
	c.embedded += len(texts)
	return c.inner.Embed(ctx, texts)
}
func (c *countingEmbedder) Dimensions() int { return c.inner.Dimensions() }
func (c *countingEmbedder) Model() string   { return "counting" }

func newEmbeddingProjector(emb rag.Embedder, vs graph.VectorStore) *Projector {
	return New(Options{
		ID:          graph.ProjectionID("proj-embed"),
		Embedder:    emb,
		VectorStore: vs,
	})
}

func podNode(name, uid, status string) graph.Node {
	return graph.Node{
		Ref:        graph.Ref{APIVersion: "v1", Kind: "Pod", Namespace: "shop", Name: name, UID: uid},
		Properties: map[string]any{"status": status},
	}
}

func TestRefreshEmbeddingsDisabledIsNoop(t *testing.T) {
	p := New(Options{ID: "p"}) // no embedder / vector store
	if err := p.refreshEmbeddings(context.Background(), []graph.Node{podNode("a", "u-a", "Running")}, nil); err != nil {
		t.Fatalf("expected no-op, got error: %v", err)
	}
}

func TestRefreshEmbeddingsEmbedsAllThenOnlyChanged(t *testing.T) {
	emb := newCountingEmbedder(8)
	vs := newFakeVectorStore()
	p := newEmbeddingProjector(emb, vs)
	ctx := context.Background()

	nodes := []graph.Node{podNode("a", "u-a", "Running"), podNode("b", "u-b", "Running")}

	// First refresh embeds everything and creates the index once.
	if err := p.refreshEmbeddings(ctx, nodes, nil); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if emb.embedded != 2 {
		t.Errorf("first refresh embedded %d cards, want 2", emb.embedded)
	}
	if vs.indexCalls != 1 || vs.indexDims != 8 {
		t.Errorf("expected one index creation with dims 8, got calls=%d dims=%d", vs.indexCalls, vs.indexDims)
	}
	if vs.lastUpsertCount() != 2 {
		t.Errorf("first upsert wrote %d, want 2", vs.lastUpsertCount())
	}

	// Second refresh with identical content embeds nothing and does not recreate
	// the index.
	if err := p.refreshEmbeddings(ctx, nodes, nil); err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if emb.embedded != 2 {
		t.Errorf("unchanged refresh re-embedded; total embedded=%d, want 2", emb.embedded)
	}
	if vs.indexCalls != 1 {
		t.Errorf("index recreated; calls=%d, want 1", vs.indexCalls)
	}

	// Change one node's status: only that card is re-embedded.
	changed := []graph.Node{podNode("a", "u-a", "CrashLoopBackOff"), podNode("b", "u-b", "Running")}
	if err := p.refreshEmbeddings(ctx, changed, nil); err != nil {
		t.Fatalf("third refresh: %v", err)
	}
	if emb.embedded != 3 {
		t.Errorf("expected exactly one more card embedded (total 3), got %d", emb.embedded)
	}
	if vs.lastUpsertCount() != 1 {
		t.Errorf("third upsert wrote %d, want 1", vs.lastUpsertCount())
	}
	if got := vs.current["u-a"]; got.Card == "" || got.Model != "counting" {
		t.Errorf("upserted embedding missing card/model: %+v", got)
	}
}

func TestRefreshEmbeddingsPrunesDeletedNodesFromCache(t *testing.T) {
	emb := newCountingEmbedder(4)
	p := newEmbeddingProjector(emb, newFakeVectorStore())
	ctx := context.Background()

	both := []graph.Node{podNode("a", "u-a", "Running"), podNode("b", "u-b", "Running")}
	if err := p.refreshEmbeddings(ctx, both, nil); err != nil {
		t.Fatalf("refresh both: %v", err)
	}
	// Remove node b, then bring it back unchanged: because it was pruned from the
	// hash cache, it must be embedded again.
	onlyA := []graph.Node{podNode("a", "u-a", "Running")}
	if err := p.refreshEmbeddings(ctx, onlyA, nil); err != nil {
		t.Fatalf("refresh only a: %v", err)
	}
	if _, present := p.cardHashes["u-b"]; present {
		t.Error("expected deleted node u-b to be pruned from the hash cache")
	}

	before := emb.embedded
	if err := p.refreshEmbeddings(ctx, both, nil); err != nil {
		t.Fatalf("refresh both again: %v", err)
	}
	if emb.embedded != before+1 {
		t.Errorf("expected re-added node to be embedded once, delta=%d", emb.embedded-before)
	}
}

func TestRefreshEmbeddingsUpsertFailureIsRetried(t *testing.T) {
	emb := newCountingEmbedder(4)
	vs := newFakeVectorStore()
	vs.upsertErr = errors.New("boom")
	p := newEmbeddingProjector(emb, vs)
	ctx := context.Background()

	nodes := []graph.Node{podNode("a", "u-a", "Running")}
	if err := p.refreshEmbeddings(ctx, nodes, nil); err == nil {
		t.Fatal("expected upsert error to propagate")
	}
	// Hash must not be recorded on failure, so the next refresh retries.
	if _, present := p.cardHashes["u-a"]; present {
		t.Error("hash recorded despite upsert failure")
	}

	vs.upsertErr = nil
	if err := p.refreshEmbeddings(ctx, nodes, nil); err != nil {
		t.Fatalf("retry refresh: %v", err)
	}
	if vs.lastUpsertCount() != 1 {
		t.Errorf("retry did not re-upsert the node; last upsert=%d", vs.lastUpsertCount())
	}
}
