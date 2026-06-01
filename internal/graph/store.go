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

	// Close releases any resources held by the store (connections, pools).
	Close(ctx context.Context) error
}
