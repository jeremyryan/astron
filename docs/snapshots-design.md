# Snapshots — Design

Status: proposed

A **snapshot** is an immutable, point-in-time copy of (a subset of) a
projection's graph, stored in the same Neo4j database but completely outside
the sync lifecycle. A Kubernetes cluster consists of ephemeral resources —
Pods are replaced on every rollout — so snapshots let a user capture the set
of nodes and edges at a moment in time and return to it later, while the live
projection keeps tracking the cluster.

Two properties define a snapshot:

- **Copy, not flag.** Live nodes are updated in place and pruned by `Sync`'s
  mark-and-sweep, so a snapshot cannot be a marker on live nodes — it must be
  *separate copies* of nodes and edges. (The `_manual` links precedent shows
  how sync-exempt data works, but manual links attach to live nodes; snapshot
  data must survive those nodes' deletion.)
- **Inert.** The projector never touches snapshot data: no upserts, no
  pruning, no embedding refresh.

## Neo4j data model

Live nodes today: label `Resource` (+ per-kind label), with
`_key = <projection>|<apiVersion>|<kind>|<ns>|<name>` and the `_projection`
and `_syncToken` bookkeeping properties.

Snapshots get a **distinct label**, which is the key isolation mechanism:

```
(:SnapshotResource {_key: "<snapshotID>|<apiVersion>|<kind>|<ns>|<name>",
                    _projection: <projectionID>, _snapshot: <snapshotID>,
                    apiVersion, kind, namespace, name, uid, ...properties})
```

- A distinct label means `Sync`'s prune queries (which match `:Resource`),
  `ReadGraph`, the vector index (label-based), and GraphRAG retrieval all
  ignore snapshots with **zero changes to their Cypher** — no risk of a
  snapshot regression breaking sync.
- Copies strip the sync/RAG bookkeeping: `_syncToken`, `embedding`, `card`,
  `cardHash`.
- Relationships are copied between the snapshot copies with the same types
  (`OWNS`, `SELECTS`, `CUSTOM`, ...) plus `_snapshot`. Manual links and their
  notes are included — they are part of the graph the user sees.
- One **metadata node** per snapshot:
  `(:Snapshot {id, _projection, name, description, createdAt, nodeCount,
  edgeCount, filter...})`. Listing snapshots reads only these.

Creation is a **server-side Cypher copy**
(`MATCH (n:Resource {_projection: $p}) ... CREATE (:SnapshotResource ...)`)
in one transaction — atomic and consistent with respect to a concurrent
`Sync` (a Neo4j write transaction sees a consistent state), with no data
round-tripping through the operator.

## Store interface

Follow the `LinkStore` pattern — an optional capability so the feature stays
additive:

```go
type SnapshotStore interface {
    CreateSnapshot(ctx, projection ProjectionID, opts SnapshotOptions) (SnapshotInfo, error)
    ListSnapshots(ctx, projection ProjectionID) ([]SnapshotInfo, error)
    ReadSnapshot(ctx, projection ProjectionID, snapshotID string) (GraphData, error)
    DeleteSnapshot(ctx, projection ProjectionID, snapshotID string) error
}
```

- `SnapshotOptions`: `Name`, `Description`, and an optional filter
  (namespaces, kinds, label selector — reusing the `GraphViewFilters`
  vocabulary so "snapshot what this view shows" falls out naturally).
- `SnapshotInfo`: id, name, description, createdAt, node/edge counts.

`DeleteProjection` (the finalizer path) extends to also delete
`:Snapshot`/`:SnapshotResource` data for the projection — snapshots are
scoped to their projection and should not outlive it. (A "detach or export
before delete" story can come later.)

## API surface

Consistent with the existing routes:

```
POST   /api/projections/{ns}/{name}/snapshots            create {name?, description?, filter?}
GET    /api/projections/{ns}/{name}/snapshots            list SnapshotInfo
GET    /api/projections/{ns}/{name}/snapshots/{id}/graph GraphData (same DTO as /graph)
DELETE /api/projections/{ns}/{name}/snapshots/{id}
```

Reusing the `GraphData` DTO for the read endpoint means the UI canvas and the
CLI's existing graph rendering (table/dot/mermaid) work on snapshots almost
for free. Snapshot mutation endpoints (links, hide, etc.) are simply not
offered — immutability by omission.

## No new CRD (initially)

Snapshot *data* is bulky, imperative, and DB-resident — the wrong shape for a
CRD (contrast with GraphViews, which are small declarative configs).

- **Phase 1:** metadata lives only in Neo4j; creation is an API/CLI/UI
  action.
- **Phase 2 (future):** declarative *policy* on the GraphProjection spec —
  `spec.snapshots: {schedule: "0 2 * * *", retention: 10}` — where the
  controller takes scheduled snapshots and enforces retention
  (delete-oldest). Retention also guards unbounded database growth, which is
  the main operational risk of this feature.

## UI

- A **camera button** in the view controls ("Take snapshot", with an optional
  name prompt) hitting the create endpoint, honoring the current view's
  filters.
- A **snapshot picker** in the left sidebar under the projection (like
  views): "Live" plus a timestamped list. Selecting one loads the snapshot
  graph into the canvas in **read-only mode** — linking, hiding, and RAG chat
  disabled; a visible banner/border ("Snapshot · 2026-07-24 03:12") so users
  always know they are looking at the past.
- Future win this design enables: **diff view** — live and snapshot nodes
  share the `apiVersion|kind|ns|name` identity suffix, so added, removed, and
  changed nodes can be computed and color-coded.

## CLI

`astron snapshots create <ns> <projection> [--name ...] [--filter ...]`,
`list`, `rm`, and `graph <ns> <projection> <id> [--format dot|mermaid|table]`
— thin wrappers over the API, mirroring the `views`/`links` command groups.

## Explicit non-interactions

- **GraphRAG:** snapshots are excluded from cards, embeddings, and retrieval
  (different label ⇒ outside the vector index). "RAG over a snapshot" is
  deliberately out of scope.
- **Projector:** no involvement beyond the store; snapshots do not affect
  sync counts or status conditions (a `snapshotCount` in projection status is
  optional polish).

## Build order

1. Store: model + `SnapshotStore` on `Neo4jStore`, extend `DeleteProjection`;
   unit tests against the fake-store pattern, Cypher tests like the existing
   graph tests.
2. API endpoints + OpenAPI regen; server tests with the fake store.
3. CLI command group + tests.
4. UI picker / create / read-only mode.
5. Docs (user guide + ui-guide section).
6. Later: scheduled snapshots + retention in the CRD, diff view, export.

The riskiest piece is step 1's copy query on large graphs (memory in one
transaction) — mitigable with batched copies
(`CALL { ... } IN TRANSACTIONS`) if projections grow beyond tens of
thousands of nodes.
