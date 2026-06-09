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

// Vector-related node property and index names. These are stored on the same
// K8sResource node as the resource's identity, so retrieval can return a node's
// card and score without a second lookup.
const (
	// vectorIndexName is the fixed name of the node vector index.
	vectorIndexName = "k8s_resource_embedding"
	// embeddingProperty holds the dense embedding vector.
	embeddingProperty = "embedding"
	// cardProperty holds the natural-language card the embedding was built from.
	cardProperty = "card"
	// cardHashProperty holds the content hash of the card (for staleness checks).
	cardHashProperty = "cardHash"
	// embeddingModelProperty records which model produced the embedding.
	embeddingModelProperty = "embeddingModel"
)

// compile-time assertion that Neo4jStore satisfies VectorStore.
var _ VectorStore = (*Neo4jStore)(nil)

// validSimilarities is the set of vector index similarity functions Neo4J
// supports. The value is interpolated into the index definition (it cannot be
// parameterized), so it must be validated against this allow-list.
var validSimilarities = map[string]bool{"cosine": true, "euclidean": true}

// vectorIndexCypher builds the CREATE VECTOR INDEX statement. The dimension and
// similarity function are interpolated (index option maps cannot be
// parameterized in Cypher), so both are validated by the caller first.
func vectorIndexCypher(dimensions int, similarity string) (string, error) {
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
} }`, vectorIndexName, resourceLabel, embeddingProperty, dimensions, similarity), nil
}

// EnsureVectorIndex creates the node embedding vector index if it does not
// already exist.
func (s *Neo4jStore) EnsureVectorIndex(ctx context.Context, dimensions int, similarity string) error {
	cypher, err := vectorIndexCypher(dimensions, similarity)
	if err != nil {
		return err
	}
	sess := s.session(ctx)
	defer func() { _ = sess.Close(ctx) }()

	_, err = sess.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		return tx.Run(ctx, cypher, nil)
	})
	if err != nil {
		return fmt.Errorf("ensuring vector index: %w", err)
	}
	return nil
}

// embeddingRow builds the per-embedding parameter map used by the UNWIND merge
// in UpsertEmbeddings. The node is addressed by its projection-scoped merge key.
func embeddingRow(projection ProjectionID, e NodeEmbedding) map[string]any {
	return map[string]any{
		"key":    nodeKey(projection, e.Ref),
		"vector": e.Vector,
		"card":   e.Card,
		"hash":   e.CardHash,
		"model":  e.Model,
	}
}

// upsertEmbeddingsCypher attaches embeddings to existing nodes. The vector is
// written via db.create.setNodeVectorProperty so it is stored in the form the
// vector index requires. Nodes absent from the graph are simply not matched.
const upsertEmbeddingsCypher = `
UNWIND $rows AS row
MATCH (n:` + resourceLabel + ` {_key: row.key})
CALL db.create.setNodeVectorProperty(n, '` + embeddingProperty + `', row.vector)
SET n.` + cardProperty + ` = row.card,
    n.` + cardHashProperty + ` = row.hash,
    n.` + embeddingModelProperty + ` = row.model`

// UpsertEmbeddings attaches the given embeddings to their nodes. It is
// incremental: only the supplied nodes are touched, leaving other nodes (and
// their existing embeddings) unchanged.
func (s *Neo4jStore) UpsertEmbeddings(ctx context.Context, projection ProjectionID, embeddings []NodeEmbedding) error {
	if len(embeddings) == 0 {
		return nil
	}
	rows := make([]any, 0, len(embeddings))
	for _, e := range embeddings {
		rows = append(rows, embeddingRow(projection, e))
	}

	sess := s.session(ctx)
	defer func() { _ = sess.Close(ctx) }()

	_, err := sess.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		return tx.Run(ctx, upsertEmbeddingsCypher, map[string]any{"rows": rows})
	})
	if err != nil {
		return fmt.Errorf("upserting embeddings for projection %q: %w", projection, err)
	}
	return nil
}

// vectorSearchParams holds the parameters for a vector search query, alongside
// the over-fetch factor applied when filters are present (the vector index
// returns its top results before WHERE filtering, so we fetch extra and trim).
const filterOverFetch = 10

// vectorSearchCypher builds the vector search query. When the filter constrains
// kinds or namespaces, an over-fetch limit and post-filter clauses are added.
func vectorSearchCypher(filter VectorFilter) string {
	cypher := `
CALL db.index.vector.queryNodes($index, $limit, $query) YIELD node, score
WHERE node.` + projectionProperty + ` = $projection`
	if len(filter.Kinds) > 0 {
		cypher += `
  AND node.kind IN $kinds`
	}
	if len(filter.Namespaces) > 0 {
		cypher += `
  AND node.namespace IN $namespaces`
	}
	cypher += `
RETURN node, score
ORDER BY score DESC
LIMIT $topK`
	return cypher
}

// VectorSearch returns up to topK nodes most similar to query within the
// projection, honoring the optional kind/namespace filter.
func (s *Neo4jStore) VectorSearch(ctx context.Context, projection ProjectionID, query []float32, topK int, filter VectorFilter) ([]VectorHit, error) {
	if topK <= 0 {
		return nil, nil
	}
	// Over-fetch from the index when filtering, since filters are applied after
	// the index returns its nearest neighbors.
	limit := topK
	if len(filter.Kinds) > 0 || len(filter.Namespaces) > 0 {
		limit = topK * filterOverFetch
	}

	params := map[string]any{
		"index":      vectorIndexName,
		"limit":      limit,
		"topK":       topK,
		"query":      query,
		"projection": string(projection),
	}
	if len(filter.Kinds) > 0 {
		params["kinds"] = toAnySlice(filter.Kinds)
	}
	if len(filter.Namespaces) > 0 {
		params["namespaces"] = toAnySlice(filter.Namespaces)
	}

	cypher := vectorSearchCypher(filter)
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
		return nil, fmt.Errorf("vector search for projection %q: %w", projection, err)
	}

	records := result.([]*neo4j.Record)
	hits := make([]VectorHit, 0, len(records))
	for _, rec := range records {
		rawNode, _ := rec.Get("node")
		node, ok := rawNode.(neo4j.Node)
		if !ok {
			continue
		}
		rawScore, _ := rec.Get("score")
		hits = append(hits, VectorHit{
			Node:  nodeFromProps(node.Props),
			Score: asFloat64(rawScore),
		})
	}
	return hits, nil
}

func toAnySlice(in []string) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}

func asFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}
