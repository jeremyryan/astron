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

const uidPod = "u-pod"

// retrievalStore is a graph.Store + graph.VectorStore + graph.QueryStore
// returning a fixed graph and a fixed, ordered set of vector hits.
type retrievalStore struct {
	data graph.GraphData
	hits []graph.VectorHit

	// query records the last Cypher passed to ReadOnlyQuery and the rows to
	// return for it.
	lastCypher string
	queryRows  []map[string]any
}

func (s *retrievalStore) Verify(context.Context) error { return nil }
func (s *retrievalStore) Sync(context.Context, graph.ProjectionID, []graph.Node, []graph.Relationship) (graph.Counts, error) {
	return graph.Counts{}, nil
}
func (s *retrievalStore) DeleteProjection(context.Context, graph.ProjectionID) error { return nil }
func (s *retrievalStore) Counts(context.Context, graph.ProjectionID) (graph.Counts, error) {
	return graph.Counts{}, nil
}
func (s *retrievalStore) ReadGraph(context.Context, graph.ProjectionID) (graph.GraphData, error) {
	return s.data, nil
}
func (s *retrievalStore) Close(context.Context) error { return nil }

func (s *retrievalStore) EnsureVectorIndex(context.Context, int, string) error { return nil }
func (s *retrievalStore) UpsertEmbeddings(context.Context, graph.ProjectionID, []graph.NodeEmbedding) error {
	return nil
}
func (s *retrievalStore) VectorSearch(context.Context, graph.ProjectionID, []float32, int, graph.VectorFilter) ([]graph.VectorHit, error) {
	return s.hits, nil
}

func (s *retrievalStore) ReadOnlyQuery(_ context.Context, _ graph.ProjectionID, cypher string, _ map[string]any) ([]map[string]any, error) {
	s.lastCypher = cypher
	return s.queryRows, nil
}

// sampleGraph returns a small graph: Deployment web OWNS Pod web-1, and Service
// web-svc SELECTS Pod web-1; plus an unrelated Pod other-1.
func sampleGraph() graph.GraphData {
	deploy := graph.Node{Ref: graph.Ref{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "shop", Name: "web", UID: "u-deploy"}}
	pod := graph.Node{Ref: graph.Ref{APIVersion: "v1", Kind: "Pod", Namespace: "shop", Name: "web-1", UID: uidPod}}
	svc := graph.Node{Ref: graph.Ref{APIVersion: "v1", Kind: "Service", Namespace: "shop", Name: "web-svc", UID: "u-svc"}}
	other := graph.Node{Ref: graph.Ref{APIVersion: "v1", Kind: "Pod", Namespace: "shop", Name: "other-1", UID: "u-other"}}
	return graph.GraphData{
		Nodes: []graph.Node{deploy, pod, svc, other},
		Relationships: []graph.Relationship{
			{Type: "OWNS", From: graph.Ref{UID: "u-deploy"}, To: graph.Ref{UID: uidPod}},
			{Type: "SELECTS", From: graph.Ref{UID: "u-svc"}, To: graph.Ref{UID: uidPod}},
		},
	}
}

func newRetrievalProjector(store *retrievalStore, embEnabled bool) *Projector {
	opts := Options{ID: "proj-r", Store: store}
	if embEnabled {
		opts.Embedder = rag.NewFakeEmbedder(8)
		opts.VectorStore = store
	}
	return New(opts)
}

//nolint:unparam // uid is fixed (uidPod) in current tests but kept for generality
func hit(uid string, score float64) graph.VectorHit {
	return graph.VectorHit{Node: graph.Node{Ref: graph.Ref{UID: uid}}, Score: score}
}

func idSet(refs ...string) map[string]bool {
	s := map[string]bool{}
	for _, r := range refs {
		s[r] = true
	}
	return s
}

func TestSearchRequiresEmbedding(t *testing.T) {
	p := newRetrievalProjector(&retrievalStore{data: sampleGraph()}, false)
	if _, err := p.Search(context.Background(), "anything", SearchOptions{}); !errors.Is(err, ErrRAGNotEnabled) {
		t.Fatalf("expected ErrRAGNotEnabled, got %v", err)
	}
}

func TestSearchExpandsAroundSeed(t *testing.T) {
	store := &retrievalStore{data: sampleGraph(), hits: []graph.VectorHit{hit(uidPod, 0.9)}}
	p := newRetrievalProjector(store, true)

	r, err := p.Search(context.Background(), "web pod", SearchOptions{TopK: 1, Hops: 1})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(r.Seeds) != 1 || r.Seeds[0].Ref.UID != uidPod || r.Seeds[0].Score != 0.9 {
		t.Fatalf("unexpected seeds: %+v", r.Seeds)
	}
	// 1 hop from the Pod reaches the Deployment and Service, but not other-1.
	gotNodes := nodeUIDs(r.Subgraph)
	want := idSet(uidPod, "u-deploy", "u-svc")
	if !sameSet(gotNodes, want) {
		t.Fatalf("subgraph nodes = %v, want %v", gotNodes, want)
	}
	if len(r.Subgraph.Relationships) != 2 {
		t.Errorf("expected 2 edges in subgraph, got %d", len(r.Subgraph.Relationships))
	}
	if len(r.Cards) != 3 {
		t.Errorf("expected 3 cards, got %d", len(r.Cards))
	}
}

func TestSearchHopsZeroReturnsSeedOnly(t *testing.T) {
	store := &retrievalStore{data: sampleGraph(), hits: []graph.VectorHit{hit(uidPod, 0.9)}}
	p := newRetrievalProjector(store, true)

	r, err := p.Search(context.Background(), "web pod", SearchOptions{TopK: 1, Hops: 0})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := nodeUIDs(r.Subgraph); !sameSet(got, idSet(uidPod)) {
		t.Fatalf("hops=0 subgraph = %v, want just the seed", got)
	}
	if len(r.Subgraph.Relationships) != 0 {
		t.Errorf("hops=0 should yield no edges, got %d", len(r.Subgraph.Relationships))
	}
}

func TestSearchEdgeTypeFilterLimitsExpansion(t *testing.T) {
	store := &retrievalStore{data: sampleGraph(), hits: []graph.VectorHit{hit(uidPod, 0.9)}}
	p := newRetrievalProjector(store, true)

	// Only follow OWNS: from the Pod we should reach the Deployment but not the
	// Service (which is connected via SELECTS).
	r, err := p.Search(context.Background(), "web pod", SearchOptions{TopK: 1, Hops: 1, EdgeTypes: []string{"OWNS"}})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := nodeUIDs(r.Subgraph); !sameSet(got, idSet(uidPod, "u-deploy")) {
		t.Fatalf("OWNS-only subgraph = %v, want pod+deploy", got)
	}
	for _, e := range r.Subgraph.Relationships {
		if e.Type != "OWNS" {
			t.Errorf("unexpected edge type %q in OWNS-filtered result", e.Type)
		}
	}
}

func TestNeighborhoodResolvesByIdentityWithoutEmbedding(t *testing.T) {
	store := &retrievalStore{data: sampleGraph()}
	p := newRetrievalProjector(store, false) // embedding disabled is fine here

	ref := graph.Ref{APIVersion: "v1", Kind: "Pod", Namespace: "shop", Name: "web-1"} // no UID
	r, err := p.Neighborhood(context.Background(), ref, 1, nil)
	if err != nil {
		t.Fatalf("Neighborhood: %v", err)
	}
	if len(r.Seeds) != 1 || r.Seeds[0].Ref.UID != uidPod {
		t.Fatalf("expected the seed resolved to u-pod, got %+v", r.Seeds)
	}
	if got := nodeUIDs(r.Subgraph); !sameSet(got, idSet(uidPod, "u-deploy", "u-svc")) {
		t.Fatalf("neighborhood = %v", got)
	}
}

func TestNeighborhoodUnknownResourceIsEmpty(t *testing.T) {
	store := &retrievalStore{data: sampleGraph()}
	p := newRetrievalProjector(store, false)

	ref := graph.Ref{Kind: "Pod", Namespace: "shop", Name: "ghost"}
	r, err := p.Neighborhood(context.Background(), ref, 1, nil)
	if err != nil {
		t.Fatalf("Neighborhood: %v", err)
	}
	if len(r.Seeds) != 0 || len(r.Subgraph.Nodes) != 0 {
		t.Fatalf("expected empty retrieval for unknown resource, got %+v", r)
	}
}

func nodeUIDs(d graph.GraphData) map[string]bool {
	out := map[string]bool{}
	for _, n := range d.Nodes {
		out[n.Ref.UID] = true
	}
	return out
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
