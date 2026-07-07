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
	"maps"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// resourceLabel is applied to every node materialized from a Kubernetes
// resource, in addition to a per-kind label.
const resourceLabel = "K8sResource"

// projectionProperty stores the owning ProjectionID on every node and
// relationship so a projection's data can be tracked and removed independently.
const projectionProperty = "_projection"

// syncTokenProperty stamps each node/relationship with the token of the sync
// that last wrote it, enabling mark-and-sweep pruning of stale data.
const syncTokenProperty = "_syncToken"

// manualProperty marks a relationship as user-created (added via the UI) so it
// is excluded from the projector's mark-and-sweep pruning and persists across
// re-syncs.
const manualProperty = "_manual"

// syncCounter guarantees unique, monotonically increasing sync tokens even when
// two syncs occur within the same nanosecond.
var syncCounter atomic.Uint64

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

// compile-time assertion that Neo4jStore satisfies Store and LinkStore.
var (
	_ Store     = (*Neo4jStore)(nil)
	_ LinkStore = (*Neo4jStore)(nil)
)

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

// newSyncToken returns a unique token for a single Sync invocation.
func newSyncToken() string {
	return strconv.FormatInt(time.Now().UnixNano(), 10) + "-" + strconv.FormatUint(syncCounter.Add(1), 10)
}

// nodeKey returns the stable merge key for a node. The UID is preferred; when
// it is unavailable a composite of identifying fields is used.
func nodeKey(projection ProjectionID, ref Ref) string {
	if ref.UID != "" {
		return fmt.Sprintf("%s|%s", projection, ref.UID)
	}
	return fmt.Sprintf("%s|%s|%s|%s|%s", projection, ref.APIVersion, ref.Kind, ref.Namespace, ref.Name)
}

// nodeRow builds the per-node parameter map used by the UNWIND merge.
func nodeRow(projection ProjectionID, node Node) map[string]any {
	props := map[string]any{}
	maps.Copy(props, node.Properties)
	props["apiVersion"] = node.Ref.APIVersion
	props["kind"] = node.Ref.Kind
	props["namespace"] = node.Ref.Namespace
	props["name"] = node.Ref.Name
	props["uid"] = node.Ref.UID
	props[projectionProperty] = string(projection)

	return map[string]any{"key": nodeKey(projection, node.Ref), "props": props}
}

// relRow builds the per-relationship parameter map used by the UNWIND merge.
func relRow(projection ProjectionID, rel Relationship) map[string]any {
	props := map[string]any{}
	maps.Copy(props, rel.Properties)
	props[projectionProperty] = string(projection)

	return map[string]any{
		"fromKey": nodeKey(projection, rel.From),
		"toKey":   nodeKey(projection, rel.To),
		"props":   props,
	}
}

const upsertNodesCypher = `
UNWIND $rows AS row
MERGE (n:` + resourceLabel + ` {_key: row.key})
SET n += row.props, n.` + syncTokenProperty + ` = $token`

func upsertRelsCypher(relType string) string {
	return fmt.Sprintf(`
UNWIND $rows AS row
MATCH (from:%s {_key: row.fromKey})
MATCH (to:%s {_key: row.toKey})
MERGE (from)-[r:%s]->(to)
SET r += row.props, r.%s = $token`, resourceLabel, resourceLabel, relType, syncTokenProperty)
}

const pruneNodesCypher = `
MATCH (n:` + resourceLabel + ` {` + projectionProperty + `: $projection})
WHERE n.` + syncTokenProperty + ` <> $token
DETACH DELETE n`

const pruneRelsCypher = `
MATCH (:` + resourceLabel + `)-[r {` + projectionProperty + `: $projection}]->()
WHERE r.` + syncTokenProperty + ` <> $token AND coalesce(r.` + manualProperty + `, false) = false
DELETE r`

// Sync upserts all nodes and relationships under a fresh token, then prunes any
// data of the projection not stamped with that token.
func (s *Neo4jStore) Sync(ctx context.Context, projection ProjectionID, nodes []Node, rels []Relationship) (Counts, error) {
	token := newSyncToken()
	sess := s.session(ctx)
	defer func() { _ = sess.Close(ctx) }()

	_, err := sess.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		if len(nodes) > 0 {
			rows := make([]any, 0, len(nodes))
			for _, n := range nodes {
				rows = append(rows, nodeRow(projection, n))
			}
			if _, err := tx.Run(ctx, upsertNodesCypher, map[string]any{"rows": rows, "token": token}); err != nil {
				return nil, fmt.Errorf("upserting nodes: %w", err)
			}
		}

		// Relationship types cannot be parameterized, so group rows by sanitized
		// type and run one UNWIND merge per type.
		byType := map[string][]any{}
		for _, rel := range rels {
			relType, err := sanitizeRelType(rel.Type)
			if err != nil {
				return nil, err
			}
			byType[relType] = append(byType[relType], relRow(projection, rel))
		}
		for relType, rows := range byType {
			if _, err := tx.Run(ctx, upsertRelsCypher(relType), map[string]any{"rows": rows, "token": token}); err != nil {
				return nil, fmt.Errorf("upserting %q relationships: %w", relType, err)
			}
		}

		// Mark-and-sweep: remove stale relationships first, then stale nodes.
		params := map[string]any{"projection": string(projection), "token": token}
		if _, err := tx.Run(ctx, pruneRelsCypher, params); err != nil {
			return nil, fmt.Errorf("pruning relationships: %w", err)
		}
		if _, err := tx.Run(ctx, pruneNodesCypher, params); err != nil {
			return nil, fmt.Errorf("pruning nodes: %w", err)
		}
		return nil, nil
	})
	if err != nil {
		return Counts{}, fmt.Errorf("syncing projection %q: %w", projection, err)
	}

	return s.Counts(ctx, projection)
}

const deleteProjectionCypher = `
MATCH (n:` + resourceLabel + ` {` + projectionProperty + `: $projection})
DETACH DELETE n`

// ManualLinkType is the default relationship type used for user-created links.
const ManualLinkType = "CUSTOM"

// addManualLinkCypher matches both endpoints by their UID within the projection,
// merges the relationship, and flags it manual. The relationship type is
// interpolated (it cannot be parameterized) after validation by the caller.
func addManualLinkCypher(relType string) string {
	return fmt.Sprintf(`
MATCH (from:%s {%s: $projection, uid: $fromID})
MATCH (to:%s {%s: $projection, uid: $toID})
MERGE (from)-[r:%s]->(to)
SET r.%s = $projection, r.%s = true, r.%s = $token
RETURN count(r) AS c`,
		resourceLabel, projectionProperty,
		resourceLabel, projectionProperty,
		relType,
		projectionProperty, manualProperty, syncTokenProperty)
}

// AddManualLink merges a user-created relationship between two existing nodes of
// a projection, identified by their node IDs (UIDs). The link is flagged manual
// so it survives Sync pruning. It errors when either endpoint is not found.
func (s *Neo4jStore) AddManualLink(ctx context.Context, projection ProjectionID, fromID, toID, relType string) error {
	if fromID == "" || toID == "" {
		return fmt.Errorf("both endpoint ids are required")
	}
	if fromID == toID {
		return fmt.Errorf("cannot link a node to itself")
	}
	rt, err := sanitizeRelType(relType)
	if err != nil {
		return err
	}

	sess := s.session(ctx)
	defer func() { _ = sess.Close(ctx) }()

	created, err := sess.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		res, err := tx.Run(ctx, addManualLinkCypher(rt), map[string]any{
			"projection": string(projection),
			"fromID":     fromID,
			"toID":       toID,
			"token":      newSyncToken(),
		})
		if err != nil {
			return int64(0), err
		}
		// When either MATCH finds nothing, the MERGE never runs and the query
		// produces no rows; treat that as "endpoint(s) not found".
		rec, err := res.Single(ctx)
		if err != nil {
			return int64(0), nil
		}
		c, _ := rec.Get("c")
		return asInt64(c), nil
	})
	if err != nil {
		return fmt.Errorf("adding manual link for projection %q: %w", projection, err)
	}
	if created.(int64) == 0 {
		return fmt.Errorf("one or both nodes were not found in projection %q", projection)
	}
	return nil
}

// deleteManualLinkCypher matches a manual relationship of the given type between
// two nodes (by UID) within a projection and deletes it. The type is matched
// via type(r), so it needs no interpolation.
const deleteManualLinkCypher = `
MATCH (from:` + resourceLabel + ` {` + projectionProperty + `: $projection, uid: $fromID})-[r {` + projectionProperty + `: $projection}]->(to:` + resourceLabel + ` {` + projectionProperty + `: $projection, uid: $toID})
WHERE type(r) = $relType AND coalesce(r.` + manualProperty + `, false) = true
DELETE r`

// DeleteManualLink removes a user-created relationship of relType between two
// nodes of a projection. Only manual links are affected. It is idempotent.
func (s *Neo4jStore) DeleteManualLink(ctx context.Context, projection ProjectionID, fromID, toID, relType string) error {
	if fromID == "" || toID == "" {
		return fmt.Errorf("both endpoint ids are required")
	}
	if relType == "" {
		return fmt.Errorf("relationship type is required")
	}
	sess := s.session(ctx)
	defer func() { _ = sess.Close(ctx) }()

	_, err := sess.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		return tx.Run(ctx, deleteManualLinkCypher, map[string]any{
			"projection": string(projection),
			"fromID":     fromID,
			"toID":       toID,
			"relType":    relType,
		})
	})
	if err != nil {
		return fmt.Errorf("deleting manual link for projection %q: %w", projection, err)
	}
	return nil
}

// manualNoteProperty is the free-text note a user can attach to a manual link.
// It is a normal (non-underscored) property, so it flows through ReadGraph like
// any other relationship data and can be surfaced in the UI and GraphRAG cards.
const manualNoteProperty = "note"

// setManualLinkNoteCypher sets the note property on a manual relationship of the
// given type between two nodes (by UID) within a projection.
const setManualLinkNoteCypher = `
MATCH (from:` + resourceLabel + ` {` + projectionProperty + `: $projection, uid: $fromID})-[r {` + projectionProperty + `: $projection}]->(to:` + resourceLabel + ` {` + projectionProperty + `: $projection, uid: $toID})
WHERE type(r) = $relType AND coalesce(r.` + manualProperty + `, false) = true
SET r.` + manualNoteProperty + ` = $note`

// clearManualLinkNoteCypher removes the note property (used when the note is set
// to empty) so the edge carries no empty-string clutter.
const clearManualLinkNoteCypher = `
MATCH (from:` + resourceLabel + ` {` + projectionProperty + `: $projection, uid: $fromID})-[r {` + projectionProperty + `: $projection}]->(to:` + resourceLabel + ` {` + projectionProperty + `: $projection, uid: $toID})
WHERE type(r) = $relType AND coalesce(r.` + manualProperty + `, false) = true
REMOVE r.` + manualNoteProperty

// SetManualLinkNote sets or clears the note on a user-created relationship of
// relType between two nodes of a projection. Only manual links are affected. It
// is idempotent and does not error when the link is absent.
func (s *Neo4jStore) SetManualLinkNote(ctx context.Context, projection ProjectionID, fromID, toID, relType, note string) error {
	if fromID == "" || toID == "" {
		return fmt.Errorf("both endpoint ids are required")
	}
	if relType == "" {
		return fmt.Errorf("relationship type is required")
	}
	cypher := setManualLinkNoteCypher
	if note == "" {
		cypher = clearManualLinkNoteCypher
	}

	sess := s.session(ctx)
	defer func() { _ = sess.Close(ctx) }()

	_, err := sess.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		return tx.Run(ctx, cypher, map[string]any{
			"projection": string(projection),
			"fromID":     fromID,
			"toID":       toID,
			"relType":    relType,
			"note":       note,
		})
	})
	if err != nil {
		return fmt.Errorf("setting manual link note for projection %q: %w", projection, err)
	}
	return nil
}

// manualLinksCypher returns every manual relationship of a projection with its
// endpoints (by node key) and properties, in the same row shape as ReadGraph so
// relFromProps can decode it.
const manualLinksCypher = `
MATCH (a:` + resourceLabel + ` {` + projectionProperty + `: $projection})-[r {` + projectionProperty + `: $projection}]->(b:` + resourceLabel + ` {` + projectionProperty + `: $projection})
WHERE coalesce(r.` + manualProperty + `, false) = true
RETURN collect({type: type(r), fromKey: a._key, toKey: b._key, props: properties(r)}) AS rels`

// ManualLinks returns the user-created relationships of a projection, including
// their properties (e.g. a note).
func (s *Neo4jStore) ManualLinks(ctx context.Context, projection ProjectionID) ([]Relationship, error) {
	sess := s.session(ctx)
	defer func() { _ = sess.Close(ctx) }()

	result, err := sess.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		res, err := tx.Run(ctx, manualLinksCypher, map[string]any{"projection": string(projection)})
		if err != nil {
			return nil, err
		}
		return res.Single(ctx)
	})
	if err != nil {
		return nil, fmt.Errorf("reading manual links for projection %q: %w", projection, err)
	}

	var out []Relationship
	rec := result.(*neo4j.Record)
	rawRels, _ := rec.Get("rels")
	if rels, ok := rawRels.([]any); ok {
		for _, r := range rels {
			rel, ok := r.(map[string]any)
			if !ok || rel["type"] == nil {
				continue
			}
			out = append(out, relFromProps(rel))
		}
	}
	return out, nil
}

// DeleteProjection removes all data owned by a projection.
func (s *Neo4jStore) DeleteProjection(ctx context.Context, projection ProjectionID) error {
	sess := s.session(ctx)
	defer func() { _ = sess.Close(ctx) }()

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
	defer func() { _ = sess.Close(ctx) }()

	result, err := sess.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		res, err := tx.Run(ctx, countsCypher, map[string]any{"projection": string(projection)})
		if err != nil {
			return nil, err
		}
		return res.Single(ctx)
	})
	if err != nil {
		return Counts{}, fmt.Errorf("counting projection %q: %w", projection, err)
	}

	rec := result.(*neo4j.Record)
	nodes, _ := rec.Get("nodes")
	rels, _ := rec.Get("rels")
	return Counts{Nodes: asInt64(nodes), Relationships: asInt64(rels)}, nil
}

const readGraphCypher = `
MATCH (n:` + resourceLabel + ` {` + projectionProperty + `: $projection})
WITH collect(n) AS nodes
OPTIONAL MATCH (a:` + resourceLabel + `)-[r {` + projectionProperty + `: $projection}]->(b:` + resourceLabel + `)
RETURN nodes, collect({type: type(r), fromKey: a._key, toKey: b._key, props: properties(r)}) AS rels`

// ReadGraph returns all nodes and relationships owned by a projection.
func (s *Neo4jStore) ReadGraph(ctx context.Context, projection ProjectionID) (GraphData, error) {
	sess := s.session(ctx)
	defer func() { _ = sess.Close(ctx) }()

	result, err := sess.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		res, err := tx.Run(ctx, readGraphCypher, map[string]any{"projection": string(projection)})
		if err != nil {
			return nil, err
		}
		return res.Single(ctx)
	})
	if err != nil {
		return GraphData{}, fmt.Errorf("reading graph for projection %q: %w", projection, err)
	}

	rec := result.(*neo4j.Record)
	data := GraphData{}

	rawNodes, _ := rec.Get("nodes")
	if nodes, ok := rawNodes.([]any); ok {
		for _, n := range nodes {
			node, ok := n.(neo4j.Node)
			if !ok {
				continue
			}
			data.Nodes = append(data.Nodes, nodeFromProps(node.Props))
		}
	}

	rawRels, _ := rec.Get("rels")
	if rels, ok := rawRels.([]any); ok {
		for _, r := range rels {
			rel, ok := r.(map[string]any)
			if !ok || rel["type"] == nil {
				continue
			}
			data.Relationships = append(data.Relationships, relFromProps(rel))
		}
	}
	return data, nil
}

// nodeFromProps reconstructs a Node from stored Neo4J node properties.
func nodeFromProps(props map[string]any) Node {
	ref := Ref{
		APIVersion: asString(props["apiVersion"]),
		Kind:       asString(props["kind"]),
		Namespace:  asString(props["namespace"]),
		Name:       asString(props["name"]),
		UID:        asString(props["uid"]),
	}
	userProps := map[string]any{}
	for k, v := range props {
		switch k {
		case "apiVersion", "kind", "namespace", "name", "uid", "_key", projectionProperty, syncTokenProperty,
			// Vector/GraphRAG bookkeeping: large or internal, not for graph reads.
			embeddingProperty, cardProperty, cardHashProperty, embeddingModelProperty:
			continue
		default:
			userProps[k] = v
		}
	}
	return Node{Ref: ref, Properties: userProps}
}

// relFromProps reconstructs a Relationship from a read row. The endpoint keys
// are the synthetic node keys "<projection>|<uid>"; the UID suffix is used as
// the endpoint identity.
func relFromProps(row map[string]any) Relationship {
	props := map[string]any{}
	manual := false
	if p, ok := row["props"].(map[string]any); ok {
		for k, v := range p {
			if k == manualProperty {
				if b, ok := v.(bool); ok {
					manual = b
				}
			}
			if k == projectionProperty || k == syncTokenProperty || k == manualProperty {
				continue
			}
			props[k] = v
		}
	}
	return Relationship{
		Type:       asString(row["type"]),
		From:       Ref{UID: keyToUID(asString(row["fromKey"]))},
		To:         Ref{UID: keyToUID(asString(row["toKey"]))},
		Properties: props,
		Manual:     manual,
	}
}

// keyToUID extracts the identity suffix from a synthetic node key. For
// UID-based keys ("<projection>|<uid>") it returns the uid; otherwise the whole
// remainder after the first separator.
func keyToUID(key string) string {
	if _, after, found := strings.Cut(key, "|"); found {
		return after
	}
	return key
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
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
