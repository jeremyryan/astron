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

	// UpsertNode creates or updates a single node owned by the given projection.
	UpsertNode(ctx context.Context, projection ProjectionID, node Node) error

	// UpsertNodes creates or updates many nodes owned by the given projection in
	// a single transaction.
	UpsertNodes(ctx context.Context, projection ProjectionID, nodes []Node) error

	// DeleteNode removes a node (and its relationships) for the given projection.
	DeleteNode(ctx context.Context, projection ProjectionID, ref Ref) error

	// UpsertRelationship creates or updates a single relationship owned by the
	// given projection. Both endpoints are merged so dangling edges never occur.
	UpsertRelationship(ctx context.Context, projection ProjectionID, rel Relationship) error

	// UpsertRelationships creates or updates many relationships in a single
	// transaction.
	UpsertRelationships(ctx context.Context, projection ProjectionID, rels []Relationship) error

	// DeleteProjection removes all nodes and relationships owned by the given
	// projection. Used when a GraphProjection is deleted.
	DeleteProjection(ctx context.Context, projection ProjectionID) error

	// Counts returns the number of nodes and relationships owned by the given
	// projection.
	Counts(ctx context.Context, projection ProjectionID) (Counts, error)

	// Close releases any resources held by the store (connections, pools).
	Close(ctx context.Context) error
}
