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
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// ErrSnapshotNotFound indicates the requested snapshot does not exist.
var ErrSnapshotNotFound = errors.New("snapshot not found")

// snapshotResourceLabel is applied to node copies belonging to a snapshot. The
// distinct label keeps snapshot data invisible to Sync's pruning, ReadGraph,
// and the vector index, all of which match resourceLabel.
const snapshotResourceLabel = "SnapshotResource"

// snapshotMetaLabel is applied to the one metadata node per snapshot.
const snapshotMetaLabel = "Snapshot"

// snapshotProperty stores the owning snapshot ID on every copied node and
// relationship.
const snapshotProperty = "_snapshot"

// origKeyProperty preserves the copied node's live _key so relationships can
// be rewired between the copies (and so a future diff view can correlate
// snapshot nodes with their live counterparts).
const origKeyProperty = "_origKey"

// compile-time assertion that Neo4jStore satisfies SnapshotStore.
var _ SnapshotStore = (*Neo4jStore)(nil)

// newSnapshotID returns a random, URL-safe snapshot identifier.
func newSnapshotID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating snapshot id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// createSnapshotMetaCypher records the snapshot's metadata node.
const createSnapshotMetaCypher = `
CREATE (s:` + snapshotMetaLabel + ` {
  id: $id,
  ` + projectionProperty + `: $projection,
  name: $name,
  createdAt: $createdAt,
  nodes: 0,
  relationships: 0
})`

// copySnapshotNodesCypher copies the projection's live nodes into the
// snapshot, stripping the sync and GraphRAG bookkeeping so copies are inert.
// When $all is false only nodes whose id (uid) is in $ids are copied, scoping
// the snapshot to e.g. the currently visible subset.
const copySnapshotNodesCypher = `
MATCH (n:` + resourceLabel + ` {` + projectionProperty + `: $projection})
WHERE $all OR n.uid IN $ids
CREATE (c:` + snapshotResourceLabel + `)
SET c = properties(n),
    c.` + snapshotProperty + ` = $id,
    c.` + origKeyProperty + ` = n._key,
    c._key = $id + '|' + n._key
REMOVE c.` + syncTokenProperty + `, c.` + embeddingProperty + `, c.` + cardProperty + `, c.` + cardHashProperty + `
RETURN count(c) AS c`

// snapshotRelTypesCypher lists the distinct relationship types present between
// the projection's nodes, so each type can be copied (Cypher cannot
// parameterize relationship types).
const snapshotRelTypesCypher = `
MATCH (:` + resourceLabel + ` {` + projectionProperty + `: $projection})-[r]->(:` + resourceLabel + ` {` + projectionProperty + `: $projection})
RETURN DISTINCT type(r) AS t`

// copySnapshotRelsCypher copies all relationships of one type between the
// snapshot's node copies, wiring endpoints through origKeyProperty.
func copySnapshotRelsCypher(relType string) string {
	return fmt.Sprintf(`
MATCH (a:%[1]s {%[2]s: $projection})-[r:%[3]s]->(b:%[1]s {%[2]s: $projection})
MATCH (ca:%[4]s {%[5]s: $id, %[6]s: a._key})
MATCH (cb:%[4]s {%[5]s: $id, %[6]s: b._key})
CREATE (ca)-[cr:%[3]s]->(cb)
SET cr = properties(r), cr.%[5]s = $id
REMOVE cr.%[7]s
RETURN count(cr) AS c`,
		resourceLabel, projectionProperty, relType,
		snapshotResourceLabel, snapshotProperty, origKeyProperty,
		syncTokenProperty)
}

// updateSnapshotCountsCypher records the copied subgraph's size on the
// metadata node.
const updateSnapshotCountsCypher = `
MATCH (s:` + snapshotMetaLabel + ` {id: $id, ` + projectionProperty + `: $projection})
SET s.nodes = $nodes, s.relationships = $relationships`

// listSnapshotsCypher returns the projection's snapshot metadata, newest first.
const listSnapshotsCypher = `
MATCH (s:` + snapshotMetaLabel + ` {` + projectionProperty + `: $projection})
RETURN s.id AS id, s.name AS name, s.createdAt AS createdAt,
       s.nodes AS nodes, s.relationships AS relationships
ORDER BY s.createdAt DESC`

// deleteSnapshotCypher removes a snapshot's metadata node and all its copies.
const deleteSnapshotCypher = `
OPTIONAL MATCH (s:` + snapshotMetaLabel + ` {id: $id, ` + projectionProperty + `: $projection})
DELETE s
WITH 1 AS _
OPTIONAL MATCH (n:` + snapshotResourceLabel + ` {` + snapshotProperty + `: $id, ` + projectionProperty + `: $projection})
DETACH DELETE n`

// deleteProjectionSnapshotsCypher removes every snapshot belonging to a
// projection; used when the projection itself is deleted.
const deleteProjectionSnapshotsCypher = `
OPTIONAL MATCH (s:` + snapshotMetaLabel + ` {` + projectionProperty + `: $projection})
DELETE s
WITH 1 AS _
OPTIONAL MATCH (n:` + snapshotResourceLabel + ` {` + projectionProperty + `: $projection})
DETACH DELETE n`

// CreateSnapshot copies the projection's current nodes and relationships into
// a new snapshot in a single write transaction, so the copy is consistent with
// respect to a concurrent Sync. A non-empty nodeIDs scopes the copy to those
// nodes (and the relationships between them); relationships are wired between
// the copies, so edges to excluded nodes are dropped naturally.
func (s *Neo4jStore) CreateSnapshot(ctx context.Context, projection ProjectionID, name string, nodeIDs []string) (SnapshotInfo, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return SnapshotInfo{}, fmt.Errorf("a snapshot name is required")
	}
	id, err := newSnapshotID()
	if err != nil {
		return SnapshotInfo{}, err
	}
	createdAt := time.Now().UTC()

	sess := s.session(ctx)
	defer func() { _ = sess.Close(ctx) }()

	counts, err := sess.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		ids := nodeIDs
		if ids == nil {
			ids = []string{}
		}
		base := map[string]any{
			"projection": string(projection), "id": id,
			"all": len(ids) == 0, "ids": ids,
		}

		meta := map[string]any{
			"projection": string(projection), "id": id,
			"name": name, "createdAt": createdAt.Format(time.RFC3339Nano),
		}
		if _, err := tx.Run(ctx, createSnapshotMetaCypher, meta); err != nil {
			return nil, err
		}

		res, err := tx.Run(ctx, copySnapshotNodesCypher, base)
		if err != nil {
			return nil, err
		}
		rec, err := res.Single(ctx)
		if err != nil {
			return nil, err
		}
		nodeCount, _ := rec.Get("c")

		res, err = tx.Run(ctx, snapshotRelTypesCypher, base)
		if err != nil {
			return nil, err
		}
		var relTypes []string
		for res.Next(ctx) {
			if t, ok := res.Record().Get("t"); ok {
				relTypes = append(relTypes, t.(string))
			}
		}
		if err := res.Err(); err != nil {
			return nil, err
		}

		var relCount int64
		for _, t := range relTypes {
			rt, err := sanitizeRelType(t)
			if err != nil {
				return nil, fmt.Errorf("unexpected relationship type %q: %w", t, err)
			}
			res, err := tx.Run(ctx, copySnapshotRelsCypher(rt), base)
			if err != nil {
				return nil, err
			}
			rec, err := res.Single(ctx)
			if err != nil {
				return nil, err
			}
			c, _ := rec.Get("c")
			relCount += asInt64(c)
		}

		params := map[string]any{
			"projection": string(projection), "id": id,
			"nodes": asInt64(nodeCount), "relationships": relCount,
		}
		if _, err := tx.Run(ctx, updateSnapshotCountsCypher, params); err != nil {
			return nil, err
		}
		return Counts{Nodes: asInt64(nodeCount), Relationships: relCount}, nil
	})
	if err != nil {
		return SnapshotInfo{}, fmt.Errorf("creating snapshot for projection %q: %w", projection, err)
	}

	c := counts.(Counts)
	return SnapshotInfo{
		ID: id, Name: name, CreatedAt: createdAt,
		Nodes: c.Nodes, Relationships: c.Relationships,
	}, nil
}

// ListSnapshots returns the projection's snapshots, newest first.
func (s *Neo4jStore) ListSnapshots(ctx context.Context, projection ProjectionID) ([]SnapshotInfo, error) {
	sess := s.session(ctx)
	defer func() { _ = sess.Close(ctx) }()

	out, err := sess.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		res, err := tx.Run(ctx, listSnapshotsCypher, map[string]any{"projection": string(projection)})
		if err != nil {
			return nil, err
		}
		var infos []SnapshotInfo
		for res.Next(ctx) {
			rec := res.Record()
			info := SnapshotInfo{}
			if v, ok := rec.Get("id"); ok {
				info.ID, _ = v.(string)
			}
			if v, ok := rec.Get("name"); ok {
				info.Name, _ = v.(string)
			}
			if v, ok := rec.Get("createdAt"); ok {
				if raw, ok := v.(string); ok {
					info.CreatedAt, _ = time.Parse(time.RFC3339Nano, raw)
				}
			}
			if v, ok := rec.Get("nodes"); ok {
				info.Nodes = asInt64(v)
			}
			if v, ok := rec.Get("relationships"); ok {
				info.Relationships = asInt64(v)
			}
			infos = append(infos, info)
		}
		return infos, res.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("listing snapshots for projection %q: %w", projection, err)
	}
	return out.([]SnapshotInfo), nil
}

// readSnapshotCypher returns a snapshot's copied nodes and the relationships
// between them, in one row (mirroring readGraphCypher). Endpoint identities
// come from the copies' uid property so callers see the same node IDs as the
// live graph.
const readSnapshotCypher = `
MATCH (n:` + snapshotResourceLabel + ` {` + snapshotProperty + `: $id, ` + projectionProperty + `: $projection})
WITH collect(n) AS nodes
OPTIONAL MATCH (a:` + snapshotResourceLabel + ` {` + snapshotProperty + `: $id})-[r {` + snapshotProperty + `: $id}]->(b:` + snapshotResourceLabel + `)
RETURN nodes, collect({type: type(r), fromUID: a.uid, toUID: b.uid, props: properties(r)}) AS rels`

// snapshotExistsCypher checks for the snapshot's metadata node.
const snapshotExistsCypher = `
MATCH (s:` + snapshotMetaLabel + ` {id: $id, ` + projectionProperty + `: $projection})
RETURN count(s) AS c`

// ReadSnapshot returns the nodes and relationships captured by a snapshot, in
// the same shape as ReadGraph. It returns ErrSnapshotNotFound when the
// snapshot does not exist.
func (s *Neo4jStore) ReadSnapshot(ctx context.Context, projection ProjectionID, snapshotID string) (GraphData, error) {
	sess := s.session(ctx)
	defer func() { _ = sess.Close(ctx) }()

	params := map[string]any{"projection": string(projection), "id": snapshotID}
	result, err := sess.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		res, err := tx.Run(ctx, snapshotExistsCypher, params)
		if err != nil {
			return nil, err
		}
		rec, err := res.Single(ctx)
		if err != nil {
			return nil, err
		}
		if c, _ := rec.Get("c"); asInt64(c) == 0 {
			return nil, ErrSnapshotNotFound
		}

		res, err = tx.Run(ctx, readSnapshotCypher, params)
		if err != nil {
			return nil, err
		}
		return res.Single(ctx)
	})
	if err != nil {
		if errors.Is(err, ErrSnapshotNotFound) {
			return GraphData{}, ErrSnapshotNotFound
		}
		return GraphData{}, fmt.Errorf("reading snapshot %q for projection %q: %w", snapshotID, projection, err)
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
			// Strip the snapshot bookkeeping so copies read like live nodes.
			props := make(map[string]any, len(node.Props))
			for k, v := range node.Props {
				if k == snapshotProperty || k == origKeyProperty {
					continue
				}
				props[k] = v
			}
			data.Nodes = append(data.Nodes, nodeFromProps(props))
		}
	}

	rawRels, _ := rec.Get("rels")
	if rels, ok := rawRels.([]any); ok {
		for _, r := range rels {
			row, ok := r.(map[string]any)
			if !ok || row["type"] == nil {
				continue
			}
			props := map[string]any{}
			manual := false
			if p, ok := row["props"].(map[string]any); ok {
				for k, v := range p {
					switch k {
					case manualProperty:
						manual, _ = v.(bool)
					case projectionProperty, snapshotProperty:
					default:
						props[k] = v
					}
				}
			}
			data.Relationships = append(data.Relationships, Relationship{
				Type:       asString(row["type"]),
				From:       Ref{UID: asString(row["fromUID"])},
				To:         Ref{UID: asString(row["toUID"])},
				Properties: props,
				Manual:     manual,
			})
		}
	}
	return data, nil
}

// addSnapshotLinkCypher matches both endpoints by their UID within the
// snapshot, merges the relationship, and flags it manual. The relationship
// type is interpolated (it cannot be parameterized) after validation by the
// caller. Mirrors addManualLinkCypher for live nodes.
func addSnapshotLinkCypher(relType string) string {
	return fmt.Sprintf(`
MATCH (from:%[1]s {%[2]s: $snapshot, %[3]s: $projection, uid: $fromID})
MATCH (to:%[1]s {%[2]s: $snapshot, %[3]s: $projection, uid: $toID})
MERGE (from)-[r:%[4]s]->(to)
SET r.%[3]s = $projection, r.%[2]s = $snapshot, r.%[5]s = true
RETURN count(r) AS c`,
		snapshotResourceLabel, snapshotProperty, projectionProperty,
		relType, manualProperty)
}

// snapshotLinkMatch is the shared MATCH clause for updating or deleting a
// manual relationship between two snapshot node copies (by UID).
const snapshotLinkMatch = `
MATCH (from:` + snapshotResourceLabel + ` {` + snapshotProperty + `: $snapshot, uid: $fromID})-[r {` + snapshotProperty + `: $snapshot}]->(to:` + snapshotResourceLabel + ` {` + snapshotProperty + `: $snapshot, uid: $toID})
WHERE type(r) = $relType AND coalesce(r.` + manualProperty + `, false) = true`

const setSnapshotLinkNoteCypher = snapshotLinkMatch + `
SET r.` + manualNoteProperty + ` = $note`

const clearSnapshotLinkNoteCypher = snapshotLinkMatch + `
REMOVE r.` + manualNoteProperty

const deleteSnapshotLinkCypher = snapshotLinkMatch + `
DELETE r`

// AddSnapshotLink creates a user-defined link between two node copies of a
// snapshot, so annotations work on frozen graphs the same way they do on the
// live projection. Snapshot links live only on the snapshot: they are never
// synced, embedded, or reflected back to the live graph.
func (s *Neo4jStore) AddSnapshotLink(ctx context.Context, projection ProjectionID, snapshotID, fromID, toID, relType string) error {
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
		res, err := tx.Run(ctx, addSnapshotLinkCypher(rt), map[string]any{
			"projection": string(projection),
			"snapshot":   snapshotID,
			"fromID":     fromID,
			"toID":       toID,
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
		return fmt.Errorf("adding link to snapshot %q: %w", snapshotID, err)
	}
	if created.(int64) == 0 {
		return fmt.Errorf("one or both nodes were not found in snapshot %q", snapshotID)
	}
	return nil
}

// DeleteSnapshotLink removes a user-defined link between two node copies of a
// snapshot. Only links flagged manual are removed, so captured projector
// edges are never affected. It is idempotent.
func (s *Neo4jStore) DeleteSnapshotLink(ctx context.Context, projection ProjectionID, snapshotID, fromID, toID, relType string) error {
	rt, err := sanitizeRelType(relType)
	if err != nil {
		return err
	}

	sess := s.session(ctx)
	defer func() { _ = sess.Close(ctx) }()

	_, err = sess.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		return tx.Run(ctx, deleteSnapshotLinkCypher, map[string]any{
			"snapshot": snapshotID,
			"fromID":   fromID,
			"toID":     toID,
			"relType":  rt,
		})
	})
	if err != nil {
		return fmt.Errorf("deleting link from snapshot %q: %w", snapshotID, err)
	}
	return nil
}

// SetSnapshotLinkNote sets (or, when note is empty, clears) the free-text
// note on a user-defined link between two node copies of a snapshot.
func (s *Neo4jStore) SetSnapshotLinkNote(ctx context.Context, projection ProjectionID, snapshotID, fromID, toID, relType, note string) error {
	rt, err := sanitizeRelType(relType)
	if err != nil {
		return err
	}

	sess := s.session(ctx)
	defer func() { _ = sess.Close(ctx) }()

	cypher := setSnapshotLinkNoteCypher
	if note == "" {
		cypher = clearSnapshotLinkNoteCypher
	}
	_, err = sess.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		return tx.Run(ctx, cypher, map[string]any{
			"snapshot": snapshotID,
			"fromID":   fromID,
			"toID":     toID,
			"relType":  rt,
			"note":     note,
		})
	})
	if err != nil {
		return fmt.Errorf("setting link note on snapshot %q: %w", snapshotID, err)
	}
	return nil
}

// DeleteSnapshot removes a snapshot's metadata and copied data. It is
// idempotent: deleting an absent snapshot is not an error.
func (s *Neo4jStore) DeleteSnapshot(ctx context.Context, projection ProjectionID, snapshotID string) error {
	sess := s.session(ctx)
	defer func() { _ = sess.Close(ctx) }()

	_, err := sess.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		return tx.Run(ctx, deleteSnapshotCypher, map[string]any{
			"projection": string(projection), "id": snapshotID,
		})
	})
	if err != nil {
		return fmt.Errorf("deleting snapshot %q for projection %q: %w", snapshotID, projection, err)
	}
	return nil
}
