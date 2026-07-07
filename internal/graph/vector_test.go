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

package graph

import (
	"context"
	"os"
	"strings"
	"testing"
)

const (
	kindPod = "Pod"
	nameWeb = "web"
)

func TestVectorIndexCypherValidation(t *testing.T) {
	if _, err := vectorIndexCypher(0, "cosine"); err == nil {
		t.Error("expected error for non-positive dimensions")
	}
	if _, err := vectorIndexCypher(1536, "manhattan"); err == nil {
		t.Error("expected error for unsupported similarity function")
	}

	cypher, err := vectorIndexCypher(1536, "cosine")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"CREATE VECTOR INDEX " + vectorIndexName,
		"FOR (n:" + resourceLabel + ")",
		"ON (n." + embeddingProperty + ")",
		"`vector.dimensions`: 1536",
		"`vector.similarity_function`: 'cosine'",
		"IF NOT EXISTS",
	} {
		if !strings.Contains(cypher, want) {
			t.Errorf("index cypher missing %q:\n%s", want, cypher)
		}
	}
}

func TestEmbeddingRowKeyAndParams(t *testing.T) {
	e := NodeEmbedding{
		Ref:      Ref{UID: "u1", Kind: kindPod, Name: nameWeb},
		Vector:   []float32{0.1, 0.2, 0.3},
		Card:     "Pod web ...",
		CardHash: "deadbeef",
		Model:    "text-embedding-3-small",
	}
	row := embeddingRow("proj-a", e)

	if row["key"] != "proj-a|u1" {
		t.Errorf("embedding row key = %v, want proj-a|u1", row["key"])
	}
	if v, ok := row["vector"].([]float32); !ok || len(v) != 3 {
		t.Errorf("embedding row vector wrong: %#v", row["vector"])
	}
	if row["card"] != "Pod web ..." || row["hash"] != "deadbeef" || row["model"] != "text-embedding-3-small" {
		t.Errorf("embedding row metadata wrong: %#v", row)
	}
}

func TestVectorSearchCypherFilters(t *testing.T) {
	plain := vectorSearchCypher(VectorFilter{})
	if strings.Contains(plain, "node.kind IN") || strings.Contains(plain, "node.namespace IN") {
		t.Errorf("unfiltered search should have no kind/namespace clauses:\n%s", plain)
	}
	for _, want := range []string{
		"db.index.vector.queryNodes($index, $limit, $query)",
		"node." + projectionProperty + " = $projection",
		"ORDER BY score DESC",
		"LIMIT $topK",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("search cypher missing %q:\n%s", want, plain)
		}
	}

	filtered := vectorSearchCypher(VectorFilter{Kinds: []string{kindPod}, Namespaces: []string{"shop"}})
	if !strings.Contains(filtered, "node.kind IN $kinds") {
		t.Errorf("expected kind filter clause:\n%s", filtered)
	}
	if !strings.Contains(filtered, "node.namespace IN $namespaces") {
		t.Errorf("expected namespace filter clause:\n%s", filtered)
	}
}

func TestNodeFromPropsHidesVectorBookkeeping(t *testing.T) {
	props := map[string]any{
		"apiVersion":           "v1",
		"kind":                 kindPod,
		"name":                 nameWeb,
		"phase":                "Running",
		embeddingProperty:      []float32{0.1, 0.2},
		cardProperty:           "Pod web is Running.",
		cardHashProperty:       "abc123",
		embeddingModelProperty: "fake",
	}
	node := nodeFromProps(props)

	if node.Ref.Kind != kindPod || node.Ref.Name != nameWeb {
		t.Errorf("identity not reconstructed: %+v", node.Ref)
	}
	if node.Properties["phase"] != "Running" {
		t.Errorf("expected user property phase to survive, got %#v", node.Properties)
	}
	for _, hidden := range []string{embeddingProperty, cardProperty, cardHashProperty, embeddingModelProperty} {
		if _, present := node.Properties[hidden]; present {
			t.Errorf("expected %q to be excluded from node properties", hidden)
		}
	}
}

// TestVectorStoreIntegration exercises the real Neo4J vector path. It is skipped
// unless ASTRON_NEO4J_TEST_URI is set, e.g.:
//
//	ASTRON_NEO4J_TEST_URI=neo4j://localhost:7687 \
//	ASTRON_NEO4J_TEST_PASSWORD=password go test ./internal/graph/ -run Integration
func TestVectorStoreIntegration(t *testing.T) {
	uri := os.Getenv("ASTRON_NEO4J_TEST_URI")
	if uri == "" {
		t.Skip("set ASTRON_NEO4J_TEST_URI to run the Neo4J vector integration test")
	}
	user := envOr("ASTRON_NEO4J_TEST_USERNAME", "neo4j")
	pass := envOr("ASTRON_NEO4J_TEST_PASSWORD", "password")

	store, err := NewNeo4jStore(Neo4jConfig{URI: uri, Username: user, Password: pass})
	if err != nil {
		t.Fatalf("NewNeo4jStore: %v", err)
	}
	ctx := context.Background()
	defer func() { _ = store.Close(ctx) }()
	if err := store.Verify(ctx); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	const proj ProjectionID = "vector-itest"
	defer func() { _ = store.DeleteProjection(ctx, proj) }()

	// Seed two nodes, attach 3-dim embeddings, then search near the first.
	pod := Node{Ref: Ref{APIVersion: "v1", Kind: kindPod, Namespace: "shop", Name: nameWeb, UID: "u-web"}}
	svc := Node{Ref: Ref{APIVersion: "v1", Kind: "Service", Namespace: "shop", Name: "api", UID: "u-api"}}
	if _, err := store.Sync(ctx, proj, []Node{pod, svc}, nil); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if err := store.EnsureVectorIndex(ctx, 3, "cosine"); err != nil {
		t.Fatalf("EnsureVectorIndex: %v", err)
	}
	embeddings := []NodeEmbedding{
		{Ref: pod.Ref, Vector: []float32{1, 0, 0}, Card: "Pod web", CardHash: "h1", Model: "test"},
		{Ref: svc.Ref, Vector: []float32{0, 1, 0}, Card: "Service api", CardHash: "h2", Model: "test"},
	}
	if err := store.UpsertEmbeddings(ctx, proj, embeddings); err != nil {
		t.Fatalf("UpsertEmbeddings: %v", err)
	}

	hits, err := store.VectorSearch(ctx, proj, []float32{1, 0, 0}, 1, VectorFilter{})
	if err != nil {
		t.Fatalf("VectorSearch: %v", err)
	}
	if len(hits) != 1 || hits[0].Node.Ref.Name != nameWeb {
		t.Fatalf("expected nearest hit to be Pod web, got %+v", hits)
	}

	// Vector bookkeeping must not leak into ordinary graph reads.
	data, err := store.ReadGraph(ctx, proj)
	if err != nil {
		t.Fatalf("ReadGraph: %v", err)
	}
	for _, n := range data.Nodes {
		if _, ok := n.Properties[embeddingProperty]; ok {
			t.Errorf("embedding leaked into ReadGraph properties for %s", n.Ref)
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
