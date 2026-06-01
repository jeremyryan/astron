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

// resourceLabel is applied to every node materialized from a Kubernetes
// resource, in addition to a per-kind label.
const resourceLabel = "K8sResource"

// projectionProperty stores the owning ProjectionID on every node and
// relationship so a projection's data can be tracked and removed independently.
const projectionProperty = "_projection"

// Neo4jConfig holds the connection parameters for a Neo4J store.
type Neo4jConfig struct {
	// URI is the bolt/neo4j connection URI.
	URI string
	// Username for authentication.
	Username string
	// Password for authentication.
	Password string
	// Database is the target database name (defaults to "neo4j").
	Database string
}

// Neo4jStore is a Store backed by a Neo4J database.
type Neo4jStore struct {
	driver   neo4j.DriverWithContext
	database string
}

// compile-time assertion that Neo4jStore satisfies Store.
var _ Store = (*Neo4jStore)(nil)

// NewNeo4jStore constructs a Neo4jStore from the given configuration. It opens
// the driver but does not verify connectivity; call Verify for that.
func NewNeo4jStore(cfg Neo4jConfig) (*Neo4jStore, error) {
	if cfg.URI == "" {
		return nil, fmt.Errorf("neo4j URI is required")
	}
	driver, err := neo4j.NewDriverWithContext(cfg.URI, neo4j.BasicAuth(cfg.Username, cfg.Password, ""))
	if err != nil {
		return nil, fmt.Errorf("creating neo4j driver: %w", err)
	}
	database := cfg.Database
	if database == "" {
		database = "neo4j"
	}
	return &Neo4jStore{driver: driver, database: database}, nil
}

// Verify checks connectivity and authentication.
func (s *Neo4jStore) Verify(ctx context.Context) error {
	if err := s.driver.VerifyConnectivity(ctx); err != nil {
		return fmt.Errorf("verifying neo4j connectivity: %w", err)
	}
	if err := s.driver.VerifyAuthentication(ctx, nil); err != nil {
		return fmt.Errorf("verifying neo4j authentication: %w", err)
	}
	return nil
}

// session opens a new session bound to the configured database.
func (s *Neo4jStore) session(ctx context.Context) neo4j.SessionWithContext {
	return s.driver.NewSession(ctx, neo4j.SessionConfig{DatabaseName: s.database})
}

// nodeParams builds the parameter map for a node merge.
func nodeParams(projection ProjectionID, node Node) map[string]any {
	props := map[string]any{}
	for k, v := range node.Properties {
		props[k] = v
	}
	props["apiVersion"] = node.Ref.APIVersion
	props["kind"] = node.Ref.Kind
	props["namespace"] = node.Ref.Namespace
	props["name"] = node.Ref.Name
	props["uid"] = node.Ref.UID
	props[projectionProperty] = string(projection)

	return map[string]any{
		"key":   nodeKey(projection, node.Ref),
		"props": props,
	}
}

// nodeKey returns the stable merge key for a node. The UID is preferred; when
// it is unavailable a composite of identifying fields is used.
func nodeKey(projection ProjectionID, ref Ref) string {
	if ref.UID != "" {
		return fmt.Sprintf("%s|%s", projection, ref.UID)
	}
	return fmt.Sprintf("%s|%s|%s|%s|%s", projection, ref.APIVersion, ref.Kind, ref.Namespace, ref.Name)
}

// mergeNodeCypher is the Cypher used to upsert a node. The node is identified
// by a synthetic _key so it is stable across updates.
const mergeNodeCypher = `
MERGE (n:` + resourceLabel + ` {_key: $key})
SET n += $props`

// UpsertNode creates or updates a single node.
func (s *Neo4jStore) UpsertNode(ctx context.Context, projection ProjectionID, node Node) error {
	return s.UpsertNodes(ctx, projection, []Node{node})
}

// UpsertNodes creates or updates many nodes in a single write transaction.
func (s *Neo4jStore) UpsertNodes(ctx context.Context, projection ProjectionID, nodes []Node) error {
	if len(nodes) == 0 {
		return nil
	}
	sess := s.session(ctx)
	defer sess.Close(ctx)

	_, err := sess.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		for _, node := range nodes {
			if _, err := tx.Run(ctx, mergeNodeCypher, nodeParams(projection, node)); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		return fmt.Errorf("upserting %d node(s): %w", len(nodes), err)
	}
	return nil
}

const deleteNodeCypher = `
MATCH (n:` + resourceLabel + ` {_key: $key})
DETACH DELETE n`

// DeleteNode removes a node and its relationships.
func (s *Neo4jStore) DeleteNode(ctx context.Context, projection ProjectionID, ref Ref) error {
	sess := s.session(ctx)
	defer sess.Close(ctx)

	_, err := sess.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		return tx.Run(ctx, deleteNodeCypher, map[string]any{"key": nodeKey(projection, ref)})
	})
	if err != nil {
		return fmt.Errorf("deleting node %s: %w", ref, err)
	}
	return nil
}

// relParams builds the parameter map for a relationship merge.
func relParams(projection ProjectionID, rel Relationship) map[string]any {
	props := map[string]any{}
	for k, v := range rel.Properties {
		props[k] = v
	}
	props[projectionProperty] = string(projection)

	return map[string]any{
		"fromKey": nodeKey(projection, rel.From),
		"toKey":   nodeKey(projection, rel.To),
		"props":   props,
	}
}

// mergeRelCypher upserts a relationship of a parameterized type. The type is
// validated/sanitized before being interpolated (Cypher cannot parameterize
// relationship types).
func mergeRelCypher(relType string) string {
	return fmt.Sprintf(`
MATCH (from:%s {_key: $fromKey})
MATCH (to:%s {_key: $toKey})
MERGE (from)-[r:%s]->(to)
SET r += $props`, resourceLabel, resourceLabel, relType)
}

// UpsertRelationship creates or updates a single relationship.
func (s *Neo4jStore) UpsertRelationship(ctx context.Context, projection ProjectionID, rel Relationship) error {
	return s.UpsertRelationships(ctx, projection, []Relationship{rel})
}

// UpsertRelationships creates or updates many relationships in a single
// write transaction.
func (s *Neo4jStore) UpsertRelationships(ctx context.Context, projection ProjectionID, rels []Relationship) error {
	if len(rels) == 0 {
		return nil
	}
	sess := s.session(ctx)
	defer sess.Close(ctx)

	_, err := sess.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		for _, rel := range rels {
			relType, err := sanitizeRelType(rel.Type)
			if err != nil {
				return nil, err
			}
			if _, err := tx.Run(ctx, mergeRelCypher(relType), relParams(projection, rel)); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		return fmt.Errorf("upserting %d relationship(s): %w", len(rels), err)
	}
	return nil
}

const deleteProjectionCypher = `
MATCH (n:` + resourceLabel + ` {` + projectionProperty + `: $projection})
DETACH DELETE n`

// DeleteProjection removes all data owned by a projection.
func (s *Neo4jStore) DeleteProjection(ctx context.Context, projection ProjectionID) error {
	sess := s.session(ctx)
	defer sess.Close(ctx)

	_, err := sess.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		return tx.Run(ctx, deleteProjectionCypher, map[string]any{"projection": string(projection)})
	})
	if err != nil {
		return fmt.Errorf("deleting projection %q: %w", projection, err)
	}
	return nil
}

const countsCypher = `
MATCH (n:` + resourceLabel + ` {` + projectionProperty + `: $projection})
WITH count(n) AS nodes
OPTIONAL MATCH (:` + resourceLabel + `)-[r {` + projectionProperty + `: $projection}]->()
RETURN nodes, count(r) AS rels`

// Counts returns the node and relationship counts for a projection.
func (s *Neo4jStore) Counts(ctx context.Context, projection ProjectionID) (Counts, error) {
	sess := s.session(ctx)
	defer sess.Close(ctx)

	result, err := sess.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		res, err := tx.Run(ctx, countsCypher, map[string]any{"projection": string(projection)})
		if err != nil {
			return nil, err
		}
		rec, err := res.Single(ctx)
		if err != nil {
			return nil, err
		}
		return rec, nil
	})
	if err != nil {
		return Counts{}, fmt.Errorf("counting projection %q: %w", projection, err)
	}

	rec := result.(*neo4j.Record)
	nodes, _ := rec.Get("nodes")
	rels, _ := rec.Get("rels")
	return Counts{
		Nodes:         asInt64(nodes),
		Relationships: asInt64(rels),
	}, nil
}

// Close releases the driver and its connection pool.
func (s *Neo4jStore) Close(ctx context.Context) error {
	return s.driver.Close(ctx)
}

func asInt64(v any) int64 {
	if i, ok := v.(int64); ok {
		return i
	}
	return 0
}
