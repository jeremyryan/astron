# CRD Schema Knowledge — Design

Status: proposed

Today the chat agent only knows what it can observe: the flat properties
`nodeFor` (`internal/projector/node.go`) extracts from live resources, and —
when a projection's `spec.scope.crds` is configured — the bare fact that a CRD
exists (a node with `DEFINES` edges to its instances, visible in that
projection's graph). It has no access to the CRD's actual **schema**: field
names, types, descriptions, required/enum constraints, whether a field is
even populated on any current instance. Two real classes of question fall
through that gap:

- **Discovery**: "does this cluster have anything for certificate rotation?" —
  answerable only by knowing what CRDs exist and roughly what they're for, not
  by looking at instances (there may be none yet, or none the user has found).
- **Explanation**: "what does `spec.backoffLimit` do on a Job" or "what are the
  valid values for `spec.strategy.type`" — answerable only from the schema
  itself, since `get_graph_schema` reports only properties *observed on live
  instances*, and generated Cypher/instance cards never carry field
  descriptions.

This design adds a second, **invisible** layer to the graph: one schema node
per CRD, holding a searchable summary plus the full rendered schema, giving
the chat agent (and MCP clients) a way to answer both kinds of question
without polluting the live resource graph or its embeddings.

## Two separate concerns, two separate flags

`spec.scope.crds` (`CRDSelection`, in `api/v1alpha1/graphprojection_types.go`)
today conflates two things that turn out to want different lifecycles:
whether a CRD shows up as a node *in a particular projection's graph*, and
whether Astron has ever looked at its schema at all. This design splits them:

- **`spec.scope.crds.include`/`.names` keeps its current meaning, unchanged**:
  a per-projection choice about what's *visible* — CRD objects captured as
  ordinary `K8sResource` nodes with `DEFINES` edges, shown in that
  projection's UI graph and subject to its own `Sync`. Nothing here changes.
- **A new, separate, controller-wide flag** controls CRD **schema** capture
  for RAG: `enabled`/`names`, set once for the whole controller (not on any
  `GraphProjection`), completely independent of whether any projection
  captures CRDs as visible nodes at all. Turning it on makes every matching
  CRD's schema available to *every* projection's agent — there is nothing to
  coordinate per projection.

Decoupling these also resolves what would otherwise be a real coordination
problem: if schema capture were still driven by N independent projections'
own `crds.include` flags (as an earlier draft of this doc had it), avoiding
redundant embedding calls and knowing when it's safe to delete a schema
neither projection references anymore would need active coordination across
every running projector. A single controller-wide flag means there is exactly
**one** thing capturing and owning this data, so those problems don't arise
in the first place — not "solved by clever coordination," just no longer
present.

## Configuration

This extends the existing controller-wide agentic config
(`internal/rag/providers.go`'s `ProvidersConfig`, loaded once at startup via
`LoadProvidersConfig`/`--providers-config-file`/`ASTRON_PROVIDERS_CONFIG_FILE`)
rather than introducing a new file/flag, since it's the same kind of setting —
a controller-wide, opt-in agentic capability, alongside the provider
declarations it already holds:

```yaml
embeddingProviders:
  - name: openai
    provider: openai
    model: text-embedding-3-small
    apiKeySecret: {name: astron-embeddings}
chatProviders:
  - name: openai
    provider: openai
    model: gpt-4o-mini
    apiKeySecret: {name: astron-embeddings}

# New: controller-wide CRD schema capture for RAG.
crdSchemas:
  enabled: true
  names: []                  # optional allow-list (CRD names, e.g.
                              # widgets.example.com); empty captures all CRDs
  embeddingProvider: openai  # must name an entry in embeddingProviders above
```

`embeddingProvider` is required when `enabled` is true and must resolve to a
declared embedding provider — this is the first real consumer of the
provider registry's *embedding* side for actual embedding calls (today
`ProviderRegistry`'s embedding providers are surfaced to the UI's settings
dropdown but nothing yet resolves one into a live `rag.Embedder`; this needs
that resolution, the same class of work `BuildProviderChats` already did for
chat providers).

The chart's existing `providers-config.yaml` ConfigMap template and
`values.yaml providers:` section extend with a `crdSchemas:` block; no new
Helm primitives needed.

## Where the data comes from

`spec.versions[].schema.openAPIV3Schema` lives directly on each
`CustomResourceDefinition` object — no discovery API, no extra requests
beyond watching CRDs. A CRD can serve multiple versions (e.g. a deprecated
`v1beta1` alongside the current `v1`), each with its own schema, but exactly
one is marked `storage: true`. To avoid redundant, near-duplicate hits in
semantic search, **only the storage version is embedded** for discovery;
every served version's full schema is still rendered and available on
demand (see below).

## Two-tier retrieval

Real CRD schemas can be large and deeply nested (cert-manager, Istio, ...).
Rather than chunking them for embedding — a real RAG design problem in its own
right — this splits the capability in two, matching how the question is
usually actually asked:

1. **A single lightweight "overview" card per CRD** (its storage version):
   group, kind, scope (`Namespaced`/`Cluster`), the schema's top-level
   description, and the top-level `spec`/`status` field *names* only (not
   nested detail). This is what gets embedded — cheap, precise, good for
   discovery.
2. **A deterministic, on-demand full document**, stored alongside the
   overview but not embedded: the complete rendered schema (every field,
   type, description, required/enum/default, at every depth), for a
   specific known kind — and version, when more than one is served. No
   embeddings involved; this is a keyed lookup, the same shape as
   `get_resource_yaml`'s live fetch, except served from the graph store
   directly (the content is already captured, unlike a live manifest, so
   there's no need for a live k8s read on every call).

Rendering (`openAPIV3Schema` → text) produces both documents together, once
per CRD per resync.

## A new, standalone syncer — not part of `Projector`

Because this is controller-wide and independent of any `GraphProjection`, it
doesn't belong inside `Projector`/`Manager`'s per-projection machinery (no
relationship engine, no namespace/label scoping, no per-projection cards).
It's a new, much simpler component — call it `crdschema.Syncer` (new package
`internal/crdschema`) — with its own dynamic-client watch on
`CustomResourceDefinition` objects (cluster-scoped, so no namespace filter
needed), reusing the **same shared `dynClient`** `cmd/main.go` already
constructs for `projector.NewManager` and `api.NewServer`. Its shape mirrors
a minimal version of a `Projector`'s own watch/debounce/resync loop
(`internal/projector/run.go`), scaled down to one GVK:

1. On each debounced change (and on a periodic full resync, as a safety net):
   list current CRDs, filter by the configured `names` allow-list (or all, if
   empty).
2. Render each selected CRD's candidate overview + hash (pure function,
   `internal/rag/crdschema.go`, alongside the existing card-building code).
3. Diff against currently-stored hashes (one batched store read) and embed +
   upsert only what's new or changed — the same content-hash-skip-unchanged
   shape `refreshEmbeddings` already uses for ordinary cards, just reading the
   stored hash from the store rather than an in-memory cache, so a controller
   restart doesn't force a full re-embed of every CRD.
4. Delete schema nodes for any previously-captured CRD that no longer exists
   or no longer matches the allow-list. Because there's exactly one writer,
   this is a plain diff against "what's selected right now" — no cross-writer
   reasoning, no risk of one process's view being stale relative to another's.

Started once in `cmd/main.go` (only when `crdSchemas.enabled` is true), it
runs for the controller's lifetime — no per-projection start/stop, since it
isn't tied to any `GraphProjection`'s existence.

## Neo4j data model

A new label is the entire UI/live-graph isolation mechanism, exactly as it
was for snapshots: every existing query that matches `K8sResource`
(`ReadGraph`, the UI graph endpoint, `Sync`'s prune-and-sweep, the existing
vector index) ignores this label with **zero changes** to any of that Cypher.
Unlike every other label Astron writes, there is deliberately **no
`_projection` property** — identity is the CRD alone, and there is exactly
one writer (the syncer above), not one per projection:

```
(:ResourceSchema {
  _key: "<group>/<storageVersion>/<kind>",
  group, version, kind, scope,      // scope: Namespaced | Cluster
  crdName,                          // the owning CustomResourceDefinition's name

  overview,                         // the embedded card text
  overviewHash,                     // content hash, for incremental re-embedding
  embedding, embeddingModel,        // set by the (new) schema vector index

  // Every served version's full rendered document, keyed by version, so
  // get_resource_schema can serve an older/alternate version on request even
  // though only the storage version is embedded.
  fullDocsByVersion: "<JSON: {version: renderedDoc}>"
})
```

A **second vector index** is required (Neo4j vector indexes are per-label):
`resource_schema_embedding`, `FOR (n:ResourceSchema) ON (n.embedding)`,
built the same way `vectorIndexCypher` builds the existing one.

## Store interface

An additive optional capability, following the `SnapshotStore`/`LinkStore`
pattern so stores that don't implement it simply don't offer the feature. No
method takes a `ProjectionID` — schema data isn't projection-scoped:

```go
type SchemaStore interface {
    // ExistingResourceSchemaHashes returns the currently stored overviewHash
    // for each of the given keys (entries with no stored node are simply
    // absent from the result), so the syncer can compute candidate overviews
    // first and only embed+write the ones that actually changed.
    ExistingResourceSchemaHashes(ctx context.Context, keys []string) (map[string]string, error)

    // UpsertResourceSchemas creates/updates schema nodes. Called only for
    // entries the syncer has already determined are new or changed.
    UpsertResourceSchemas(ctx context.Context, schemas []ResourceSchema) error

    // DeleteResourceSchemas removes schema nodes by key (CRDs no longer
    // present or no longer matching the configured allow-list).
    DeleteResourceSchemas(ctx context.Context, keys []string) error

    // ReadResourceSchema returns one CRD's full rendered document for the
    // given kind. version selects a specific served version; empty selects
    // the storage version. ok is false when no matching schema is captured.
    ReadResourceSchema(ctx context.Context, kind, version string) (doc string, ok bool, err error)

    // SearchResourceSchemas performs vector search over CRD overview cards.
    SearchResourceSchemas(ctx context.Context, query []float32, topK int) ([]ResourceSchemaHit, error)
}
```

## Agent tools

Two new entries in the shared catalog (`internal/agent/catalog.go`), wired to
real handlers in `internal/projector/tools.go` exactly like the existing five
— every projection's `toolSet(model)` wires both to the same shared store,
regardless of that projection's own `crds.include` setting (which, per the
split above, no longer has anything to do with schema knowledge). Because the
data isn't projection-scoped, they follow `get_resource_yaml`'s precedent
rather than `search_cluster_graph`'s: registered over MCP **as-is**
(`registerCatalogToolAsIs`, no `agent.ForMultiProjection` wrapping, no
`projectionNamespace`/`projectionName` parameters) — a schema lookup isn't
"this projection's schema", it's the cluster's:

| Tool | Backed by | Purpose |
| --- | --- | --- |
| `search_resource_docs` | `SchemaStore.SearchResourceSchemas` | Semantic search over captured CRDs' overviews — "what handles X in this cluster?" |
| `get_resource_schema` | `SchemaStore.ReadResourceSchema` | Full field-level schema for a known kind (optionally a specific version) — "what does `spec.foo` mean on a Widget?" |

Both are read-only; `get_resource_schema` needs no embeddings at all (only
`search_resource_docs` needs the vector index). Following the existing
tool-error convention, requesting a kind with no captured schema (schema
capture disabled, CRD not in the allow-list, or not a CRD at all) is a normal
observation ("no schema captured for kind X"), not an aborting error — the
agent can recover by trying something else.

## API surface

Controller-wide, not nested under a projection — consistent with
`/api/providers`, the other controller-wide (not per-projection) route:

```
GET  /api/schema-docs?q=...&topK=...        overview search hits
GET  /api/schema/{kind}?version=...          full rendered document
```

These exist primarily so the two new tools have something to call (mirroring
how `get_resource_yaml`/`get_graph_schema` are thin wrappers over API/graph
reads), and incidentally give the CLI/UI a seam if a future "browse captured
CRDs" view is ever wanted — no such UI is planned as part of this design.

## Explicit non-interactions

- **The live resource graph is untouched.** No new labels appear in
  `ReadGraph`, the UI, or `Sync`'s node set.
- **`spec.scope.crds` keeps its exact current meaning.** CRD-as-a-visible-node
  capture (`crdEdges`, `DEFINES` edges, the UI) is entirely unaffected by this
  design; the two concerns now just have separate flags instead of one
  overloaded one.
- **The existing resource vector index and `search_cluster_graph` are
  untouched.** Schema overviews live in a separate index and are only ever
  reached through the two new tools, so instance retrieval can't be crowded
  out by documentation.
- **No UI work.** This is purely a backend/agent capability; there is no
  "captured CRDs" browser planned in this pass.
- **Built-in types (Pod, Service, Deployment, ...) are out of scope.** They
  don't carry their schema on a watchable object — supporting them would mean
  parsing the cluster's `/openapi/v3` discovery document, a separate, larger
  effort — and models already have substantial built-in knowledge of core
  Kubernetes types from training, unlike cluster-specific CRDs.

## Build order

1. `internal/rag`: schema rendering (`openAPIV3Schema` → overview text + full
   document, pure function, fixture-tested against real-world CRDs); resolving
   a named controller-wide provider into a live `rag.Embedder` (parallel to
   `BuildProviderChats`).
2. Store: `ResourceSchema`/`ResourceSchemaHit` types, `SchemaStore` on
   `Neo4jStore` (second vector index + hash/upsert/delete/read/search Cypher);
   unit tests against the fake-store pattern used elsewhere.
3. Config: `crdSchemas` section on `ProvidersConfig`/`LoadProvidersConfig`.
4. `internal/crdschema`: the standalone `Syncer` (watch, debounce/resync,
   render, diff, embed, upsert/delete), started from `cmd/main.go`.
5. Agent: two catalog entries + `internal/projector/tools.go` handlers
   (reading the shared store, independent of any specific projector); MCP
   registration via the shared catalog, unwrapped.
6. API: the two new controller-wide routes + DTOs + OpenAPI regen.
7. Docs (`graphrag-guide.md`/`agent-design.md` tool tables, `providers.md`).

The riskiest piece is step 1's rendering: real-world CRD schemas vary a lot in
shape (deeply nested `oneOf`/`anyOf`, `x-kubernetes-preserve-unknown-fields`,
recursive schemas via `x-kubernetes-embedded-resource`), so it needs fixture
tests against a handful of real-world CRDs (cert-manager, Istio, or similar)
up front, not just synthetic ones — that shape is the main implementation
risk, not the syncer/store/tool plumbing around it, which is now
straightforward single-writer reconciliation.
