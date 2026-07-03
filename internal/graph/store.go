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

import "context"

// Counts is a summary of how much data a projection currently has materialized
// in the graph.
type Counts struct {
	Nodes         int64
	Relationships int64
}

// Store abstracts the graph database operations needed to project Kubernetes
// resources. Implementations must be safe for concurrent use.
type Store interface {
	// Verify checks that the backing graph database is reachable and the
	// credentials are valid.
	Verify(ctx context.Context) error

	// Sync reconciles the complete desired graph for a projection. It upserts
	// every given node and relationship, then prunes any nodes or relationships
	// previously owned by the projection that are no longer present (mark and
	// sweep). It returns the resulting counts. Sync is the primary write path
	// used by the resource graph watchers, which rebuild the full desired state
	// on each (debounced) change.
	Sync(ctx context.Context, projection ProjectionID, nodes []Node, rels []Relationship) (Counts, error)

	// DeleteProjection removes all nodes and relationships owned by the given
	// projection. Used when a GraphProjection is deleted.
	DeleteProjection(ctx context.Context, projection ProjectionID) error

	// Counts returns the number of nodes and relationships owned by the given
	// projection.
	Counts(ctx context.Context, projection ProjectionID) (Counts, error)

	// ReadGraph returns the full set of nodes and relationships owned by the
	// given projection, for read-only consumption by the API/UI.
	ReadGraph(ctx context.Context, projection ProjectionID) (GraphData, error)

	// Close releases any resources held by the store (connections, pools).
	Close(ctx context.Context) error
}

// LinkStore is an optional capability for stores that support user-created
// ("manual") links between two existing nodes. Manual links are flagged so the
// projector's Sync mark-and-sweep pruning does not remove them on the next
// reconcile. It is kept separate from Store so the feature stays additive.
type LinkStore interface {
	// AddManualLink merges a relationship of relType from the node identified by
	// fromID to the node identified by toID (the node IDs as returned by
	// ReadGraph), within the projection. The link is marked manual so periodic
	// re-syncs do not prune it. It returns an error if either endpoint does not
	// exist in the projection.
	AddManualLink(ctx context.Context, projection ProjectionID, fromID, toID, relType string) error

	// DeleteManualLink removes a manual relationship of relType between the two
	// nodes (by node ID) within the projection. Only links flagged manual are
	// removed, so projector-derived edges are never affected. It is idempotent:
	// deleting a link that is absent is not an error.
	DeleteManualLink(ctx context.Context, projection ProjectionID, fromID, toID, relType string) error
}

// NodeEmbedding pairs a node's identity with the embedding vector derived from
// its textual "resource card", plus the metadata needed to detect staleness.
type NodeEmbedding struct {
	// Ref identifies the node the embedding belongs to. It must match a node
	// already materialized by Sync; the embedding is attached to that node.
	Ref Ref
	// Vector is the dense embedding of the node's card.
	Vector []float32
	// Card is the natural-language text that was embedded. It is stored on the
	// node so retrieval can return it without re-rendering.
	Card string
	// CardHash is a content hash of Card, used to skip re-embedding unchanged
	// nodes on subsequent syncs.
	CardHash string
	// Model identifies the embedding model, so vectors produced by a different
	// model can be detected and refreshed.
	Model string
}

// VectorFilter optionally narrows a vector search to certain kinds and/or
// namespaces. Empty fields impose no constraint.
type VectorFilter struct {
	// Kinds restricts results to these resource kinds (e.g. "Pod").
	Kinds []string
	// Namespaces restricts results to these namespaces.
	Namespaces []string
}

// VectorHit is a single node returned by a vector similarity search, with its
// similarity score.
type VectorHit struct {
	// Node is the matched node.
	Node Node
	// Score is the similarity score (higher is more similar).
	Score float64
}

// VectorStore is an optional capability, implemented by stores that support
// vector (embedding) storage and similarity search for GraphRAG retrieval. It
// is kept separate from Store so that GraphRAG remains an additive, optional
// feature: code can type-assert a Store to VectorStore and degrade gracefully
// when the backend does not support it.
type VectorStore interface {
	// EnsureVectorIndex creates (if absent) the vector index over node
	// embeddings. dimensions is the embedding length; similarity is the metric,
	// either "cosine" or "euclidean". It is idempotent.
	EnsureVectorIndex(ctx context.Context, dimensions int, similarity string) error

	// UpsertEmbeddings attaches the given embeddings to nodes already owned by
	// the projection, keyed by Ref. Embeddings for refs with no matching node are
	// ignored. It is incremental: only the supplied nodes are touched.
	UpsertEmbeddings(ctx context.Context, projection ProjectionID, embeddings []NodeEmbedding) error

	// VectorSearch returns up to topK nodes owned by the projection whose
	// embeddings are most similar to query, optionally constrained by filter.
	VectorSearch(ctx context.Context, projection ProjectionID, query []float32, topK int, filter VectorFilter) ([]VectorHit, error)
}
