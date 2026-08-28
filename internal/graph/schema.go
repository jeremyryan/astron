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
	"fmt"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// resourceSchemaLabel is applied to CRD schema nodes. The distinct label (and
// the complete absence of a projectionProperty on these nodes) is what keeps
// them invisible to Sync's pruning, ReadGraph, the UI, and the existing
// resource vector index, all of which match resourceLabel — see
// docs/crd-schema-design.md.
const resourceSchemaLabel = "ResourceSchema"

// resourceSchemaVectorIndexName is the fixed name of the CRD schema overview
// vector index. It is a second, separate index from vectorIndexName because
// Neo4J vector indexes are defined per label.
const resourceSchemaVectorIndexName = "resource_schema_embedding"

// compile-time assertion that Neo4jStore satisfies SchemaStore.
var _ SchemaStore = (*Neo4jStore)(nil)

// resourceSchemaVectorIndexCypher builds the CREATE VECTOR INDEX statement for
// CRD schema overviews, mirroring vectorIndexCypher for the (differently
// labeled) resource vector index.
func resourceSchemaVectorIndexCypher(dimensions int, similarity string) (string, error) {
	if dimensions <= 0 {
		return "", fmt.Errorf("vector index dimensions must be positive, got %d", dimensions)
	}
	if !validSimilarities[similarity] {
		return "", fmt.Errorf("invalid vector similarity %q (want one of cosine, euclidean)", similarity)
	}
	return fmt.Sprintf(`
CREATE VECTOR INDEX %s IF NOT EXISTS
FOR (n:%s) ON (n.%s)
OPTIONS { indexConfig: {
  `+"`vector.dimensions`"+`: %d,
  `+"`vector.similarity_function`"+`: '%s'
} }`, resourceSchemaVectorIndexName, resourceSchemaLabel, embeddingProperty, dimensions, similarity), nil
}

// EnsureResourceSchemaVectorIndex creates the CRD schema overview vector index
// if it does not already exist.
func (s *Neo4jStore) EnsureResourceSchemaVectorIndex(ctx context.Context, dimensions int, similarity string) error {
	cypher, err := resourceSchemaVectorIndexCypher(dimensions, similarity)
	if err != nil {
		return err
	}
	sess := s.session(ctx)
	defer func() { _ = sess.Close(ctx) }()

	_, err = sess.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		return tx.Run(ctx, cypher, nil)
	})
	if err != nil {
		return fmt.Errorf("ensuring resource schema vector index: %w", err)
	}
	return nil
}

// existingResourceSchemaHashesCypher looks up the currently stored
// overviewHash for each requested key, in one batched read.
const existingResourceSchemaHashesCypher = `
UNWIND $keys AS key
MATCH (n:` + resourceSchemaLabel + ` {_key: key})
RETURN n._key AS key, n.overviewHash AS hash`

// ExistingResourceSchemaHashes returns the currently stored overviewHash for
// each of the given keys, so a caller can compute candidate overviews first
// and only embed and write the ones that actually changed. Keys with no
// stored node are absent from the result.
func (s *Neo4jStore) ExistingResourceSchemaHashes(ctx context.Context, keys []string) (map[string]string, error) {
	if len(keys) == 0 {
		return map[string]string{}, nil
	}
	sess := s.session(ctx)
	defer func() { _ = sess.Close(ctx) }()

	result, err := sess.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		res, err := tx.Run(ctx, existingResourceSchemaHashesCypher, map[string]any{"keys": toAnySlice(keys)})
		if err != nil {
			return nil, err
		}
		return res.Collect(ctx)
	})
	if err != nil {
		return nil, fmt.Errorf("reading existing resource schema hashes: %w", err)
	}

	records := result.([]*neo4j.Record)
	out := make(map[string]string, len(records))
	for _, rec := range records {
		key, _ := rec.Get("key")
		hash, _ := rec.Get("hash")
		if k := asString(key); k != "" {
			out[k] = asString(hash)
		}
	}
	return out, nil
}

// resourceSchemaRow builds the per-schema parameter map for the document
// upsert (identity and documents only — the embedding, when present, is
// written separately; see UpsertResourceSchemas).
func resourceSchemaRow(sc ResourceSchema) map[string]any {
	return map[string]any{
		"key":          ResourceSchemaKey(sc.Group, sc.Version, sc.Kind),
		"group":        sc.Group,
		"version":      sc.Version,
		"kind":         sc.Kind,
		"scope":        sc.Scope,
		"crdName":      sc.CRDName,
		"storage":      sc.Storage,
		"overview":     sc.Overview,
		"overviewHash": sc.OverviewHash,
		"fullDoc":      sc.FullDoc,
	}
}

// upsertResourceSchemaDocsCypher creates/updates a schema node's identity and
// documents. It never touches the embedding property, so a document-only
// update (no new embedding) leaves any previously stored embedding intact.
const upsertResourceSchemaDocsCypher = `
UNWIND $rows AS row
MERGE (n:` + resourceSchemaLabel + ` {_key: row.key})
SET n.group = row.group,
    n.version = row.version,
    n.kind = row.kind,
    n.scope = row.scope,
    n.crdName = row.crdName,
    n.storage = row.storage,
    n.overview = row.overview,
    n.overviewHash = row.overviewHash,
    n.fullDoc = row.fullDoc`

// upsertResourceSchemaEmbeddingsCypher sets the embedding on schema nodes that
// already exist (written by upsertResourceSchemaDocsCypher first, in the same
// transaction). db.create.setNodeVectorProperty requires a real vector value,
// so this only runs over the subset of rows that actually carry one.
const upsertResourceSchemaEmbeddingsCypher = `
UNWIND $rows AS row
MATCH (n:` + resourceSchemaLabel + ` {_key: row.key})
CALL db.create.setNodeVectorProperty(n, '` + embeddingProperty + `', row.embedding)
SET n.` + embeddingModelProperty + ` = row.model`

// UpsertResourceSchemas creates or updates schema nodes: their identity and
// rendered documents unconditionally, and (only for entries with a non-empty
// Embedding) their embedding vector and model. Both writes happen in one
// transaction.
func (s *Neo4jStore) UpsertResourceSchemas(ctx context.Context, schemas []ResourceSchema) error {
	if len(schemas) == 0 {
		return nil
	}
	docRows := make([]any, 0, len(schemas))
	var embedRows []any
	for _, sc := range schemas {
		docRows = append(docRows, resourceSchemaRow(sc))
		if len(sc.Embedding) > 0 {
			embedRows = append(embedRows, map[string]any{
				"key":       ResourceSchemaKey(sc.Group, sc.Version, sc.Kind),
				"embedding": sc.Embedding,
				"model":     sc.EmbeddingModel,
			})
		}
	}

	sess := s.session(ctx)
	defer func() { _ = sess.Close(ctx) }()

	_, err := sess.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		if _, err := tx.Run(ctx, upsertResourceSchemaDocsCypher, map[string]any{"rows": docRows}); err != nil {
			return nil, err
		}
		if len(embedRows) == 0 {
			return nil, nil
		}
		return tx.Run(ctx, upsertResourceSchemaEmbeddingsCypher, map[string]any{"rows": embedRows})
	})
	if err != nil {
		return fmt.Errorf("upserting resource schemas: %w", err)
	}
	return nil
}

// deleteResourceSchemasCypher removes schema nodes by key.
const deleteResourceSchemasCypher = `
UNWIND $keys AS key
MATCH (n:` + resourceSchemaLabel + ` {_key: key})
DETACH DELETE n`

// DeleteResourceSchemas removes schema nodes by key. Deleting a key that does
// not exist is not an error.
func (s *Neo4jStore) DeleteResourceSchemas(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	sess := s.session(ctx)
	defer func() { _ = sess.Close(ctx) }()

	_, err := sess.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		return tx.Run(ctx, deleteResourceSchemasCypher, map[string]any{"keys": toAnySlice(keys)})
	})
	if err != nil {
		return fmt.Errorf("deleting resource schemas: %w", err)
	}
	return nil
}

// readResourceSchemaVersionCypher fetches the full document for one specific,
// named version of a kind.
const readResourceSchemaVersionCypher = `
MATCH (n:` + resourceSchemaLabel + ` {kind: $kind, version: $version})
RETURN n.fullDoc AS doc
LIMIT 1`

// readResourceSchemaStorageCypher fetches the full document for a kind's
// storage version (there is exactly one per CRD), used when no version is
// requested.
const readResourceSchemaStorageCypher = `
MATCH (n:` + resourceSchemaLabel + ` {kind: $kind, storage: true})
RETURN n.fullDoc AS doc
LIMIT 1`

// ReadResourceSchema returns one CRD's full rendered document for the given
// kind. version selects a specific served version; empty selects the CRD's
// storage version. ok is false when no matching schema is captured.
func (s *Neo4jStore) ReadResourceSchema(ctx context.Context, kind, version string) (string, bool, error) {
	cypher := readResourceSchemaStorageCypher
	params := map[string]any{"kind": kind}
	if version != "" {
		cypher = readResourceSchemaVersionCypher
		params["version"] = version
	}

	sess := s.session(ctx)
	defer func() { _ = sess.Close(ctx) }()

	result, err := sess.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		res, err := tx.Run(ctx, cypher, params)
		if err != nil {
			return nil, err
		}
		return res.Collect(ctx)
	})
	if err != nil {
		return "", false, fmt.Errorf("reading resource schema for kind %q: %w", kind, err)
	}

	records := result.([]*neo4j.Record)
	if len(records) == 0 {
		return "", false, nil
	}
	doc, _ := records[0].Get("doc")
	docStr := asString(doc)
	return docStr, docStr != "", nil
}

// searchResourceSchemasCypher performs vector search over CRD overview cards.
// Unlike VectorSearch (which is projection-scoped and supports a kind/
// namespace filter), schema search has neither concern: there is exactly one
// (cluster-wide) set of schema nodes and no projection to filter by.
const searchResourceSchemasCypher = `
CALL db.index.vector.queryNodes($index, $topK, $query) YIELD node, score
RETURN node.group AS group, node.version AS version, node.kind AS kind,
       node.scope AS scope, node.overview AS overview, score
ORDER BY score DESC
LIMIT $topK`

// SearchResourceSchemas returns up to topK CRD overviews most similar to
// query.
func (s *Neo4jStore) SearchResourceSchemas(ctx context.Context, query []float32, topK int) ([]ResourceSchemaHit, error) {
	if topK <= 0 {
		return nil, nil
	}
	params := map[string]any{
		"index": resourceSchemaVectorIndexName,
		"topK":  topK,
		"query": query,
	}

	sess := s.session(ctx)
	defer func() { _ = sess.Close(ctx) }()

	result, err := sess.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		res, err := tx.Run(ctx, searchResourceSchemasCypher, params)
		if err != nil {
			return nil, err
		}
		return res.Collect(ctx)
	})
	if err != nil {
		return nil, fmt.Errorf("searching resource schemas: %w", err)
	}

	records := result.([]*neo4j.Record)
	hits := make([]ResourceSchemaHit, 0, len(records))
	for _, rec := range records {
		group, _ := rec.Get("group")
		version, _ := rec.Get("version")
		kind, _ := rec.Get("kind")
		scope, _ := rec.Get("scope")
		overview, _ := rec.Get("overview")
		score, _ := rec.Get("score")
		hits = append(hits, ResourceSchemaHit{
			Group:    asString(group),
			Version:  asString(version),
			Kind:     asString(kind),
			Scope:    asString(scope),
			Overview: asString(overview),
			Score:    asFloat64(score),
		})
	}
	return hits, nil
}
