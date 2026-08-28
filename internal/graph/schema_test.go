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

// kindWidget is the synthetic CRD kind used throughout this file's fixtures.
const kindWidget = "Widget"

func TestResourceSchemaKey(t *testing.T) {
	if got := ResourceSchemaKey("example.com", "v1", kindWidget); got != "example.com/v1/Widget" {
		t.Errorf("ResourceSchemaKey = %q", got)
	}
	if got := ResourceSchemaKey("", "v1", "Pod"); got != "/v1/Pod" {
		t.Errorf("ResourceSchemaKey (empty group) = %q", got)
	}
}

func TestResourceSchemaVectorIndexCypherValidation(t *testing.T) {
	if _, err := resourceSchemaVectorIndexCypher(0, "cosine"); err == nil {
		t.Error("expected error for non-positive dimensions")
	}
	if _, err := resourceSchemaVectorIndexCypher(1536, "manhattan"); err == nil {
		t.Error("expected error for unsupported similarity function")
	}

	cypher, err := resourceSchemaVectorIndexCypher(1536, "cosine")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"CREATE VECTOR INDEX " + resourceSchemaVectorIndexName,
		"FOR (n:" + resourceSchemaLabel + ")",
		"ON (n." + embeddingProperty + ")",
		"`vector.dimensions`: 1536",
		"`vector.similarity_function`: 'cosine'",
		"IF NOT EXISTS",
	} {
		if !strings.Contains(cypher, want) {
			t.Errorf("index cypher missing %q:\n%s", want, cypher)
		}
	}
	// A distinct index from the resource vector index (same property name is
	// fine; it's a different label).
	if resourceSchemaVectorIndexName == vectorIndexName {
		t.Fatal("resource schema vector index must be distinct from the resource vector index")
	}
}

func TestResourceSchemaRowParams(t *testing.T) {
	sc := ResourceSchema{
		Group: "example.com", Version: "v1", Kind: kindWidget, Scope: "Namespaced",
		CRDName: "widgets.example.com", Storage: true,
		Overview: "Kind: Widget...", OverviewHash: "deadbeef", FullDoc: "Widget\n...",
	}
	row := resourceSchemaRow(sc)

	if row["key"] != "example.com/v1/Widget" {
		t.Errorf("row key = %v", row["key"])
	}
	if row["group"] != "example.com" || row["version"] != "v1" || row["kind"] != kindWidget {
		t.Errorf("row identity wrong: %#v", row)
	}
	if row["scope"] != "Namespaced" || row["crdName"] != "widgets.example.com" || row["storage"] != true {
		t.Errorf("row metadata wrong: %#v", row)
	}
	if row["overview"] != "Kind: Widget..." || row["overviewHash"] != "deadbeef" || row["fullDoc"] != "Widget\n..." {
		t.Errorf("row documents wrong: %#v", row)
	}
	// The embedding is deliberately never part of the docs row (it's written
	// by a separate Cypher statement; see UpsertResourceSchemas).
	if _, present := row["embedding"]; present {
		t.Error("docs row must not carry an embedding field")
	}
}

func TestSearchResourceSchemasCypherShape(t *testing.T) {
	for _, want := range []string{
		"db.index.vector.queryNodes($index, $topK, $query)",
		"node.group AS group",
		"node.version AS version",
		"node.kind AS kind",
		"node.scope AS scope",
		"node.overview AS overview",
		"ORDER BY score DESC",
		"LIMIT $topK",
	} {
		if !strings.Contains(searchResourceSchemasCypher, want) {
			t.Errorf("search cypher missing %q:\n%s", want, searchResourceSchemasCypher)
		}
	}
	// Deliberately no projection scoping, unlike vectorSearchCypher.
	if strings.Contains(searchResourceSchemasCypher, projectionProperty) {
		t.Errorf("resource schema search must not be projection-scoped:\n%s", searchResourceSchemasCypher)
	}
}

func TestUpsertResourceSchemasNoOpOnEmpty(t *testing.T) {
	// No store/network required: an empty slice must short-circuit before
	// ever building a session. A nil *Neo4jStore would panic on s.session(),
	// so reaching a nil-pointer-free return proves the short-circuit works.
	var s *Neo4jStore
	if err := s.UpsertResourceSchemas(context.Background(), nil); err != nil {
		t.Fatalf("UpsertResourceSchemas(nil): %v", err)
	}
	if err := s.DeleteResourceSchemas(context.Background(), nil); err != nil {
		t.Fatalf("DeleteResourceSchemas(nil): %v", err)
	}
	got, err := s.ExistingResourceSchemaHashes(context.Background(), nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("ExistingResourceSchemaHashes(nil) = %v, %v", got, err)
	}
}

// TestSchemaStoreIntegration exercises the real Neo4J CRD schema path,
// including that schema data stays invisible to ordinary projection reads
// and deletion. It is skipped unless ASTRON_NEO4J_TEST_URI is set, e.g.:
//
//	ASTRON_NEO4J_TEST_URI=neo4j://localhost:7687 \
//	ASTRON_NEO4J_TEST_PASSWORD=password go test ./internal/graph/ -run SchemaStoreIntegration
func TestSchemaStoreIntegration(t *testing.T) {
	uri := os.Getenv("ASTRON_NEO4J_TEST_URI")
	if uri == "" {
		t.Skip("set ASTRON_NEO4J_TEST_URI to run the Neo4J schema store integration test")
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

	widgetV1 := ResourceSchemaKey("example.com", "v1", kindWidget)
	widgetV1beta1 := ResourceSchemaKey("example.com", "v1beta1", kindWidget)
	defer func() { _ = store.DeleteResourceSchemas(ctx, []string{widgetV1, widgetV1beta1}) }()

	if err := store.EnsureResourceSchemaVectorIndex(ctx, 3, "cosine"); err != nil {
		t.Fatalf("EnsureResourceSchemaVectorIndex: %v", err)
	}

	t.Run("before upsert", func(t *testing.T) {
		schemaStoreIntegrationBeforeUpsert(t, ctx, store, widgetV1)
	})

	// Upsert the storage version (with an embedding) and a non-storage
	// version (without one).
	err = store.UpsertResourceSchemas(ctx, []ResourceSchema{
		{
			Group: "example.com", Version: "v1", Kind: kindWidget, Scope: "Namespaced",
			CRDName: "widgets.example.com", Storage: true,
			Overview: "Kind: Widget\n...", OverviewHash: "hash-v1", FullDoc: "Widget v1 full doc",
			Embedding: []float32{1, 0, 0}, EmbeddingModel: "test",
		},
		{
			Group: "example.com", Version: "v1beta1", Kind: kindWidget, Scope: "Namespaced",
			CRDName: "widgets.example.com", Storage: false,
			Overview: "Kind: Widget (v1beta1)\n...", OverviewHash: "hash-v1beta1", FullDoc: "Widget v1beta1 full doc",
		},
	})
	if err != nil {
		t.Fatalf("UpsertResourceSchemas: %v", err)
	}

	t.Run("hashes after upsert", func(t *testing.T) {
		schemaStoreIntegrationHashesAfterUpsert(t, ctx, store, widgetV1, widgetV1beta1)
	})
	t.Run("read resource schema", func(t *testing.T) {
		schemaStoreIntegrationReadResourceSchema(t, ctx, store)
	})
	t.Run("search resource schemas", func(t *testing.T) {
		schemaStoreIntegrationSearch(t, ctx, store)
	})
	t.Run("invisible to unrelated projection", func(t *testing.T) {
		schemaStoreIntegrationInvisibleToProjection(t, ctx, store)
	})
	t.Run("delete", func(t *testing.T) {
		schemaStoreIntegrationDelete(t, ctx, store, widgetV1, widgetV1beta1)
	})
}

func schemaStoreIntegrationBeforeUpsert(t *testing.T, ctx context.Context, store *Neo4jStore, widgetV1 string) {
	t.Helper()
	if _, ok, err := store.ReadResourceSchema(ctx, kindWidget, ""); err != nil || ok {
		t.Fatalf("ReadResourceSchema before upsert: ok=%v err=%v", ok, err)
	}
	hashes, err := store.ExistingResourceSchemaHashes(ctx, []string{widgetV1})
	if err != nil || len(hashes) != 0 {
		t.Fatalf("ExistingResourceSchemaHashes before upsert: %v, %v", hashes, err)
	}
}

func schemaStoreIntegrationHashesAfterUpsert(t *testing.T, ctx context.Context, store *Neo4jStore, widgetV1, widgetV1beta1 string) {
	t.Helper()
	// The non-storage version was written too, without an embedding
	// (verified separately by schemaStoreIntegrationSearch).
	hashes, err := store.ExistingResourceSchemaHashes(ctx, []string{widgetV1, widgetV1beta1, "no/such/Key"})
	if err != nil {
		t.Fatalf("ExistingResourceSchemaHashes: %v", err)
	}
	if hashes[widgetV1] != "hash-v1" || hashes[widgetV1beta1] != "hash-v1beta1" {
		t.Fatalf("unexpected hashes: %+v", hashes)
	}
	if _, ok := hashes["no/such/Key"]; ok {
		t.Errorf("unexpected entry for a key with no stored node: %+v", hashes)
	}
}

func schemaStoreIntegrationReadResourceSchema(t *testing.T, ctx context.Context, store *Neo4jStore) {
	t.Helper()
	// Empty version resolves to the storage version; an explicit version
	// resolves to that version's own document.
	doc, ok, err := store.ReadResourceSchema(ctx, kindWidget, "")
	if err != nil || !ok || doc != "Widget v1 full doc" {
		t.Fatalf("ReadResourceSchema(storage): doc=%q ok=%v err=%v", doc, ok, err)
	}
	doc, ok, err = store.ReadResourceSchema(ctx, kindWidget, "v1beta1")
	if err != nil || !ok || doc != "Widget v1beta1 full doc" {
		t.Fatalf("ReadResourceSchema(v1beta1): doc=%q ok=%v err=%v", doc, ok, err)
	}
	if _, ok, err := store.ReadResourceSchema(ctx, "NoSuchKind", ""); err != nil || ok {
		t.Fatalf("ReadResourceSchema(missing kind): ok=%v err=%v", ok, err)
	}
}

func schemaStoreIntegrationSearch(t *testing.T, ctx context.Context, store *Neo4jStore) {
	t.Helper()
	hits, err := store.SearchResourceSchemas(ctx, []float32{1, 0, 0}, 5)
	if err != nil {
		t.Fatalf("SearchResourceSchemas: %v", err)
	}
	found := false
	for _, h := range hits {
		if h.Kind == kindWidget && h.Version == "v1" {
			found = true
			if h.Overview != "Kind: Widget\n..." {
				t.Errorf("hit overview = %q", h.Overview)
			}
		}
		if h.Version == "v1beta1" {
			t.Errorf("non-storage version must not have an embedding to be found by search: %+v", h)
		}
	}
	if !found {
		t.Fatalf("expected to find the Widget v1 overview, got %+v", hits)
	}
}

// schemaStoreIntegrationInvisibleToProjection verifies schema data is
// invisible to an unrelated projection's ordinary reads and deletion: it
// carries no _projection property and a distinct label, so
// Sync/ReadGraph/DeleteProjection never touch it.
func schemaStoreIntegrationInvisibleToProjection(t *testing.T, ctx context.Context, store *Neo4jStore) {
	t.Helper()
	const proj ProjectionID = "schema-itest"
	defer func() { _ = store.DeleteProjection(ctx, proj) }()
	pod := Node{Ref: Ref{APIVersion: "v1", Kind: "Pod", Namespace: "shop", Name: "web", UID: "u-web"}}
	if _, err := store.Sync(ctx, proj, []Node{pod}, nil); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	data, err := store.ReadGraph(ctx, proj)
	if err != nil {
		t.Fatalf("ReadGraph: %v", err)
	}
	for _, n := range data.Nodes {
		if n.Ref.Kind == kindWidget {
			t.Errorf("schema node leaked into ReadGraph: %+v", n)
		}
	}
	if err := store.DeleteProjection(ctx, proj); err != nil {
		t.Fatalf("DeleteProjection: %v", err)
	}
	if doc, ok, err := store.ReadResourceSchema(ctx, kindWidget, ""); err != nil || !ok || doc == "" {
		t.Fatalf("schema data should survive an unrelated projection's deletion: doc=%q ok=%v err=%v", doc, ok, err)
	}
}

func schemaStoreIntegrationDelete(t *testing.T, ctx context.Context, store *Neo4jStore, widgetV1, widgetV1beta1 string) {
	t.Helper()
	// DeleteResourceSchemas removes both versions; deleting again is a no-op.
	if err := store.DeleteResourceSchemas(ctx, []string{widgetV1, widgetV1beta1}); err != nil {
		t.Fatalf("DeleteResourceSchemas: %v", err)
	}
	if _, ok, err := store.ReadResourceSchema(ctx, kindWidget, ""); err != nil || ok {
		t.Fatalf("ReadResourceSchema after delete: ok=%v err=%v", ok, err)
	}
	if err := store.DeleteResourceSchemas(ctx, []string{widgetV1, widgetV1beta1}); err != nil {
		t.Fatalf("DeleteResourceSchemas (repeat): %v", err)
	}
}
