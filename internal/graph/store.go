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
	"time"
)

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

	// SetManualLinkNote sets (or, when note is empty, clears) the free-text note
	// property on a manual relationship of relType between the two nodes within
	// the projection. Only links flagged manual are affected. The note is
	// surfaced on the edge in ReadGraph and fed into GraphRAG cards.
	SetManualLinkNote(ctx context.Context, projection ProjectionID, fromID, toID, relType, note string) error

	// ManualLinks returns the projection's user-created relationships (with their
	// properties, e.g. a note). It lets the projector fold manual links into the
	// GraphRAG cards it builds from the derived graph.
	ManualLinks(ctx context.Context, projection ProjectionID) ([]Relationship, error)
}

// SnapshotInfo describes one snapshot: an immutable point-in-time copy of a
// projection's graph that is not kept in sync with the cluster.
type SnapshotInfo struct {
	// ID uniquely identifies the snapshot within its projection.
	ID string
	// Name is the user-provided display name.
	Name string
	// CreatedAt is when the snapshot was taken.
	CreatedAt time.Time
	// Nodes and Relationships are the sizes of the copied subgraph.
	Nodes         int64
	Relationships int64
}

// SnapshotStore is an optional capability for stores that support snapshots:
// point-in-time copies of a projection's nodes and relationships that live in
// the same database but are never touched by Sync's mark-and-sweep, so they
// preserve the graph as it was even after the cluster moves on. It is kept
// separate from Store so the feature stays additive.
type SnapshotStore interface {
	// CreateSnapshot copies the projection's current nodes and relationships
	// into a new snapshot with the given display name and returns its metadata.
	// When nodeIDs is non-empty only the nodes with those IDs (as returned by
	// ReadGraph) and the relationships between them are copied; when empty the
	// entire projection is copied.
	CreateSnapshot(ctx context.Context, projection ProjectionID, name string, nodeIDs []string) (SnapshotInfo, error)

	// ListSnapshots returns the projection's snapshots, newest first.
	ListSnapshots(ctx context.Context, projection ProjectionID) ([]SnapshotInfo, error)

	// ReadSnapshot returns the nodes and relationships captured by a snapshot,
	// in the same shape as ReadGraph. It returns ErrSnapshotNotFound when the
	// snapshot does not exist.
	ReadSnapshot(ctx context.Context, projection ProjectionID, snapshotID string) (GraphData, error)

	// RenameSnapshot updates a snapshot's display name. It returns
	// ErrSnapshotNotFound when the snapshot does not exist.
	RenameSnapshot(ctx context.Context, projection ProjectionID, snapshotID, name string) error

	// DeleteSnapshot removes a snapshot and all of its copied data. Deleting a
	// snapshot that does not exist is not an error.
	DeleteSnapshot(ctx context.Context, projection ProjectionID, snapshotID string) error

	// AddSnapshotLink, DeleteSnapshotLink, and SetSnapshotLinkNote manage
	// user-created links between a snapshot's node copies, mirroring the
	// LinkStore operations on live nodes. Snapshot links live only on the
	// snapshot; they are never synced, embedded, or reflected back to the
	// live graph.
	AddSnapshotLink(ctx context.Context, projection ProjectionID, snapshotID, fromID, toID, relType string) error
	DeleteSnapshotLink(ctx context.Context, projection ProjectionID, snapshotID, fromID, toID, relType string) error
	SetSnapshotLinkNote(ctx context.Context, projection ProjectionID, snapshotID, fromID, toID, relType, note string) error
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

// ResourceSchema is one CRD version's rendered documentation (typically
// produced by rag.RenderCRDSchemas), as written and read via SchemaStore.
// Unlike every other type in this file, it is cluster-wide, not scoped to any
// projection: a CRD's schema is a cluster fact, not projection-owned state.
// See docs/crd-schema-design.md.
type ResourceSchema struct {
	// Group, Version and Kind identify this CRD version, e.g.
	// ("example.com", "v1", "Widget"). Together they form the node's key (see
	// ResourceSchemaKey).
	Group, Version, Kind string
	// Scope is "Namespaced" or "Cluster".
	Scope string
	// CRDName is the owning CustomResourceDefinition object's name (e.g.
	// "widgets.example.com").
	CRDName string
	// Storage reports whether Version is the CRD's storage version. Only the
	// storage version is intended to carry an embedding (see Embedding below);
	// every version's FullDoc is stored regardless.
	Storage bool

	// Overview is the compact, embeddable summary (rag.CRDSchema.Overview).
	Overview string
	// OverviewHash is a content hash of Overview. ExistingResourceSchemaHashes
	// lets a caller compare against this before re-embedding unchanged schemas.
	OverviewHash string
	// FullDoc is the complete rendered schema for this version
	// (rag.CRDSchema.Full).
	FullDoc string

	// Embedding and EmbeddingModel carry a (new) embedding to write for this
	// entry. Leave Embedding nil to upsert/update the documents only, without
	// touching any previously stored embedding — the normal case for a
	// non-storage version, or a storage version whose Overview is unchanged.
	Embedding      []float32
	EmbeddingModel string
}

// ResourceSchemaKey returns the stable identity key for one CRD version, used
// as the node's merge key and as the key type for
// ExistingResourceSchemaHashes/DeleteResourceSchemas.
func ResourceSchemaKey(group, version, kind string) string {
	return group + "/" + version + "/" + kind
}

// ResourceSchemaHit is a CRD overview returned by SearchResourceSchemas, with
// its similarity score. It intentionally omits FullDoc (fetch it separately
// via ReadResourceSchema once a specific kind is of interest) so a batch of
// search hits stays small.
type ResourceSchemaHit struct {
	Group, Version, Kind string
	Scope                string
	Overview             string
	Score                float64
}

// SchemaStore is an optional capability for stores that support CRD schema
// documentation for RAG (see docs/crd-schema-design.md). Unlike every other
// capability in this file, schema data is not scoped to a projection — one
// CRD's schema is a cluster fact, not projection-owned state — so no method
// here takes a ProjectionID. It is kept separate from Store so the feature
// stays additive.
type SchemaStore interface {
	// EnsureResourceSchemaVectorIndex creates (if absent) the vector index over
	// CRD overview embeddings. dimensions is the embedding length; similarity is
	// the metric, either "cosine" or "euclidean". It is idempotent.
	EnsureResourceSchemaVectorIndex(ctx context.Context, dimensions int, similarity string) error

	// ExistingResourceSchemaHashes returns the currently stored OverviewHash for
	// each of the given keys (ResourceSchemaKey); keys with no stored node are
	// simply absent from the result. Callers use this to compute candidate
	// overviews first and only embed and write the ones that actually changed.
	ExistingResourceSchemaHashes(ctx context.Context, keys []string) (map[string]string, error)

	// UpsertResourceSchemas creates or updates schema nodes. It is safe (if
	// wasteful) to pass unchanged entries; ExistingResourceSchemaHashes lets a
	// caller avoid that.
	UpsertResourceSchemas(ctx context.Context, schemas []ResourceSchema) error

	// DeleteResourceSchemas removes schema nodes by key. Deleting a key that
	// does not exist is not an error.
	DeleteResourceSchemas(ctx context.Context, keys []string) error

	// ReadResourceSchema returns one CRD's full rendered document for the given
	// kind. version selects a specific served version; empty selects the CRD's
	// storage version. ok is false when no matching schema is captured.
	ReadResourceSchema(ctx context.Context, kind, version string) (doc string, ok bool, err error)

	// SearchResourceSchemas performs vector search over CRD overview cards.
	SearchResourceSchemas(ctx context.Context, query []float32, topK int) ([]ResourceSchemaHit, error)
}
