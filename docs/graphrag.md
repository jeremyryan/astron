# GraphRAG for Astron — Design

> **This is the design/architecture document.** For task-oriented usage —
> enabling GraphRAG, configuring providers, calling the API, and wiring the MCP
> server — see the **[GraphRAG User Guide](./graphrag-guide.md)**.
>
> Status: the design below is **implemented** (all phases in the roadmap table
> are marked *Done*). This document records the rationale, architecture, and
> trade-offs; per-phase status notes are inline.

## Motivation

Astron already captures a Kubernetes cluster — its resources and the typed
relationships between them — as a property graph in Neo4J. That graph is exactly
the kind of structured, connected knowledge that makes **GraphRAG** more
powerful than plain vector RAG over flat documents.

The value-add over document RAG is the **relationships**. The retrieval unit is
not an isolated node; it is a resource *plus its typed neighborhood* (owners,
mounts, selectors, definers). An LLM answering "why is the `web` Deployment
unhealthy?" benefits from being handed the Deployment, its Pods and their
status, the ConfigMaps/Secrets they mount, and the Services that select them —
as a connected subgraph, with provenance back to real cluster objects.

### Goal

Make the projected graph consumable by LLM/agent applications using three
complementary retrieval modes:

1. **Vector retrieval** — "find resources semantically related to this question."
2. **Graph expansion** — "pull the relevant neighborhood (blast radius,
   dependencies, owners) around those seed nodes."
3. **Structured query** — text-to-Cypher for precise/aggregate questions
   ("how many Pods are `CrashLoopBackOff` in namespace `shop`?").

### Non-goals

- Replacing the existing UI/read API or changing the projection model.
- Capturing Secret *values* (only names/keys ever appear, as today).
- Mandating a specific LLM or embedding vendor — all providers are pluggable.

## How this fits the existing architecture

The design reuses, rather than replaces, the current building blocks:

| Existing component | Role in GraphRAG |
|---|---|
| **Neo4J store** (`internal/graph/neo4j.go`) | Hosts vectors via Neo4J 5.x **native vector indexes** — no new datastore. |
| **`_projection` ownership property** | Scopes every retrieval and query to one projection (multi-tenancy). |
| **Flat node properties** (`internal/projector/node.go`) | Raw material for textualized "resource cards". |
| **`Store` interface** (`internal/graph/store.go`) | The seam where vector/retrieval methods are added. |
| **Read-only HTTP API** (`internal/api/server.go`) | Where `/api/.../rag/*` endpoints live. |
| **Debounced full re-sync** (`internal/projector`) | The hook to incrementally refresh embeddings. |
| **`GraphProjectionSpec`** (`api/v1alpha1`) | Where opt-in `graphRAG` configuration is declared. |

When GraphRAG is disabled or unconfigured, the operator behaves exactly as it
does today.

## Architecture overview

```
                       ┌─────────────────────────────────────────────┐
                       │                Operator binary               │
                       │                                              │
  K8s API ── informers ┤  Projector ──Sync──▶ Neo4J Store             │
                       │      │                  ▲   ▲                │
                       │      │ post-sync hook    │   │ vector index   │
                       │      ▼                   │   │                │
                       │  rag.Embedder ──vectors──┘   │                │
                       │      ▲                       │                │
                       │      │ resource cards        │ VectorSearch / │
                       │  rag.Document                │ traversal      │
                       │                              │                │
                       │  Read API  /api/.../rag/* ───┘                │
                       └───────────────┬──────────────────────────────┘
                                       │
                  ┌────────────────────┼─────────────────────┐
                  ▼                    ▼                     ▼
            Web UI (today)        HTTP clients          MCP server
                                                    (agents: Claude,
                                                     Cursor, custom)
```

New packages/components:

- `internal/rag/document.go` — renders a node (+ immediate edges) into a
  natural-language **resource card**; computes a content hash.
- `internal/rag/embedder.go` — pluggable `Embedder` interface + providers
  (OpenAI/Azure/Ollama) + a no-network fake for tests.
- Extensions to `internal/graph` — vector index management, embedding upserts,
  vector search, bounded traversal, read-only query.
- Extensions to `internal/api` — `rag/search`, `rag/neighborhood`,
  (optional) `rag/answer` endpoints + DTOs.
- `astron mcp-server` — exposes retrieval as Model Context Protocol tools.

## The retrieval contract

The core object returned to callers is a **`RetrievedContext`**: a ranked set of
resource cards plus the subgraph that connects them, with provenance so answers
can cite real objects.

```jsonc
{
  "query": "why is the web deployment unhealthy?",
  "seeds": [
    { "id": "<uid>", "kind": "Pod", "namespace": "shop", "name": "web-7d9",
      "score": 0.83 }
  ],
  "cards": [
    { "id": "<uid>", "kind": "Pod", "namespace": "shop", "name": "web-7d9",
      "text": "Pod web-7d9 in namespace shop is Running (1/2 ready, 7 restarts, CrashLoopBackOff). Owned by Deployment web. Mounts ConfigMap web-config and Secret web-tls. Selected by Service web-svc. Labels: app=web, tier=frontend." }
  ],
  "subgraph": {
    "nodes": [ /* nodeDTO[] — reuses existing API node shape */ ],
    "edges": [ /* edgeDTO[] — reuses existing API edge shape */ ]
  }
}
```

`nodeDTO`/`edgeDTO` reuse the shapes already defined in `internal/api/dto.go`,
so the subgraph is renderable by the existing Cytoscape UI as well.

## Resource cards (textualization)

For semantic retrieval each node needs a text representation. A pure,
fully-unit-testable function renders a node plus its immediate typed edges into
a compact description:

> *"Pod `web-7d9` in namespace `shop` is Running (2/2 ready, 0 restarts).
> Owned by Deployment `web`. Mounts ConfigMap `web-config` and Secret
> `web-tls`. Selected by Service `web-svc`. Labels: app=web, tier=frontend."*

Properties included are governed by `graphRAG.include` (e.g. labels yes,
annotations off by default to avoid noise). A **content hash** of the rendered
card is stored alongside it; this drives incremental embedding (below) and is
reusable for UI tooltips/exports.

## Embeddings and the vector index

- An `Embedder` interface abstracts the provider; the default for tests is a
  deterministic fake (the suite already uses fakes, e.g.
  `internal/controller/fake_store_test.go`).
- Vector capability is exposed as a **separate, optional `VectorStore`
  interface** (not folded into the core `Store`), so GraphRAG stays additive and
  callers can type-assert and degrade gracefully when a backend lacks it.
  `Neo4jStore` implements both. `VectorStore` provides:
  - `EnsureVectorIndex(ctx, dims, similarity)` — creates a Neo4J vector index
    over `K8sResource.embedding` (idempotent; `cosine`/`euclidean`).
  - `UpsertEmbeddings(ctx, projection, []NodeEmbedding)` — writes the vector
    (via `db.create.setNodeVectorProperty`) plus `card`, `cardHash`, and
    `embeddingModel` onto existing nodes, keyed by the projection-scoped node
    key; incremental (only supplied nodes are touched).
  - `VectorSearch(ctx, projection, queryVec, topK, filter)` — ranked seeds via
    `db.index.vector.queryNodes`, scoped by `_projection`, with optional
    kind/namespace filters (over-fetched then trimmed). Vector bookkeeping
    properties are hidden from ordinary `ReadGraph` results.
- Embedding refresh runs as a **post-sync hook** in the projector
  (`internal/projector/run.go`): after `Store.Sync`, diff each node's `cardHash`
  and embed **only changed nodes**, in batches. This is essential because the
  debounced sync rebuilds full desired state on every change — re-embedding
  everything each time would be slow and costly. The hook is opt-in and
  non-blocking so it never stalls projection.

## API surface

New read-only endpoints, consistent with the current server style and scoped by
projection like `GET /api/projections/{namespace}/{name}/graph`:

- `POST /api/projections/{ns}/{name}/rag/search`
  `{query, topK, filters, hops, edgeTypes}` → vector search → expand each seed
  `hops` along `edgeTypes` → assemble and return `RetrievedContext`.
- `POST /api/projections/{ns}/{name}/rag/neighborhood`
  `{kind, namespace, name, hops, edgeTypes}` → pure graph retrieval (no vector
  step) — the "blast radius" of a resource.
- `POST /api/projections/{ns}/{name}/rag/answer` *(optional, Phase 5)* → full
  RAG: retrieve → assemble context → call an LLM → return an answer plus the
  resources it cited.

Graph expansion is a parameterized, bounded Cypher traversal (hop count + edge
type allow-list) with the `_projection` filter injected — reusing the ownership
model already present in `internal/graph/neo4j.go`.

If `graphRAG.enabled=false` or no provider is configured, these endpoints return
`503`, mirroring how the existing live-resource (YAML) endpoint behaves when its
dependencies are nil.

## MCP server (agent-native access)

To make the graph directly usable by LLM agents, expose the retrieval API as
[Model Context Protocol](https://modelcontextprotocol.io/) tools via a
`astron mcp-server` subcommand (stdio for local agents; SSE/HTTP in-cluster):

- `search_cluster_graph(query, topK)` — hybrid vector+graph retrieval.
- `get_resource_neighborhood(kind, namespace, name, hops)` — structural context.
- `query_cluster(cypher)` — read-only, allow-listed (see below).
- `list_projections()` — discovery.

The MCP server is a thin client of the read API, so it inherits projection
scoping and read-only guarantees.

**Status: Done (stdio).** Implemented in `internal/mcp` (JSON-RPC 2.0 over a
newline-delimited stdio transport, standard library only) and wired as the
`astron mcp-server` subcommand of the CLI (`internal/cli/mcp.go`, built into the
`astron` binary). It reads the API base from `--api-base-url`, falling back to
the global `--server` flag and then `$ASTRON_API_URL` (default
`http://localhost:8082`), and logs to stderr to keep stdout clean for the
protocol. Tools shipped:
`list_projections`, `search_cluster_graph`, `get_resource_neighborhood`,
`get_resource_yaml`. (`query_cluster` / read-only Cypher is deferred to Phase 8;
an SSE/HTTP transport can be added later.)

## Text-to-Cypher (precise / aggregate questions)

For questions vector search handles poorly (counts, filters, joins), a guarded
text-to-Cypher path:

- The LLM is given an auto-generated **schema summary** (labels, edge types,
  property keys) derived from the live projection, keeping prompts grounded.
- Generated Cypher executes **read-only** through the `QueryStore.ReadOnlyQuery`
  capability with: a read-mode transaction (the server rejects writes), a
  statement timeout, a deny-list (no `CREATE/MERGE/DELETE/SET/REMOVE`, no `CALL`
  so `db.*`/`apoc.*`/`dbms.*` procedures are blocked, no `LOAD`/`USE`/multiple
  statements), and a required `RETURN`. Internal/embedding properties are
  stripped from results.

**Status: Done.** Implemented as:
- A separate optional `QueryStore` interface + Neo4J `ReadOnlyQuery`
  (`internal/graph/query.go`) with the `ValidateReadOnlyCypher` deny-list.
- A provider-agnostic `Chat` interface with a fake, an OpenAI-compatible client,
  and a factory (`internal/rag/{chat,fake_chat,openai_chat}.go`).
- Schema rendering and prompt/extraction helpers (`internal/rag/schema.go`,
  `internal/rag/qa.go`).
- Projector `Query` (text-to-Cypher) and `Answer` (RAG) methods
  (`internal/projector/qa.go`), exposed via the `Manager`, the
  `POST /rag/query` and `POST /rag/answer` endpoints, and the `query_cluster`
  and `answer_question` MCP tools.
- A `graphRAG.chat` CRD block resolved by the controller (its own API-key
  Secret) and threaded through `Manager.Ensure`.

> **Scoping caveat.** `$projection` is passed to the query and the prompt
> instructs the model to filter on `{_projection: $projection}`, but scoping is
> not yet *enforced* by query rewriting (that needs Cypher AST parsing). Treat
> text-to-Cypher as best-effort-scoped; read-only enforcement and the deny-list
> are hard. AST-level scope injection is tracked under open questions.

## CRD configuration

`GraphProjectionSpec` gains an optional `graphRAG` block (mirroring the existing
`Neo4jConnection` secret-ref pattern for credentials):

```yaml
spec:
  graphRAG:
    enabled: true
    embedding:
      provider: openai            # openai | azure | ollama
      model: text-embedding-3-small
      dimensions: 1536
      authSecretRef:
        name: astron-embeddings    # key: apiKey (mirrors Neo4j auth secret style)
    include:                       # which properties feed the resource card
      labels: true
      annotations: false
    vectorIndex:
      similarity: cosine
    chat:                          # optional: answering + text-to-Cypher
      enabled: true
      provider: openai             # openai | azure | ollama
      model: gpt-4o-mini
      authSecretRef:
        name: astron-chat
```

New `GraphProjectionStatus` fields mirror the existing count/condition pattern:

- `embeddedNodeCount` — nodes with a current embedding.
- `lastEmbeddingTime` — timestamp of the last embedding refresh.
- a `RAGReady` condition — provider reachable + vector index present.

Corresponding Helm values are added under `charts/astron`.

## Cross-cutting concerns

- **Cost & incrementality** — never re-embed unchanged nodes; the `cardHash`
  diff is mandatory given full-state re-sync. Batch and rate-limit provider
  calls.
- **Multi-tenancy / scoping** — every retrieval, traversal, and Cypher path
  stays scoped by `_projection`; projections never leak into each other.
- **Security** — read-only enforcement on any LLM-generated Cypher; provider
  credentials via Secret refs (consistent with Neo4J creds); note that Secret
  *names/keys* may appear in cards even though values are never captured.
- **Provider-agnostic** — `Embedder` and any LLM call sit behind interfaces;
  defaults are no-network fakes for tests.
- **Graceful degradation** — disabling GraphRAG returns the operator to its
  current behavior exactly.

## Implementation phases

Each phase is independently useful; 1–5 deliver a working hybrid-retrieval API
with no CRD changes, and 6–8 productionize and make it agent-native.

| Phase | Deliverable | Notes |
|---|---|---|
| 0 | Retrieval contract (`RetrievedContext` DTO) | Design lock; no code. |
| 1 | `internal/rag/document.go` + tests | **Done.** Resource cards; no external deps. |
| 2 | `Embedder` interface + fake + one real provider | **Done.** Pluggable embeddings (`internal/rag/{embedder,fake_embedder,openai_embedder,factory}.go`); OpenAI-compatible HTTP client (also Azure/Ollama), no new deps. |
| 3 | `VectorStore` methods + Neo4J vector index (`internal/graph/vector.go`) | **Done.** Pure Cypher/param helpers unit-tested; a live path gated behind `ASTRON_NEO4J_TEST_URI`. |
| 4 | Projector post-sync embedding hook (`internal/projector/embed.go`) | **Done.** Best-effort, incremental via `cardHash`; lazy vector-index creation; never fails the sync. Disabled unless an `Embedder` + `VectorStore` are configured. |
| 5 | `/rag/search` + `/rag/neighborhood` endpoints + DTOs | **Done.** Retrieval orchestration in `internal/projector/retrieval.go` (vector seed → bounded BFS expansion → assembled `Retrieval`); exposed via `Manager`; `POST` endpoints in `internal/api`. Not-running → empty 200, GraphRAG-disabled → 503. |
| 6 | CRD `graphRAG` config + status + controller wiring + Helm values | **Done.** `GraphRAGSpec` on the CRD; controller resolves the embedding config + API-key Secret and passes it to `Manager.Ensure`; `RAGReady` condition + `embeddedNodeCount`/`lastEmbeddingTime` status; sample + Helm values/CRD synced. Opt-in (`enabled: false` by default). |
| 7 | `astron mcp-server` subcommand | **Done.** Stdio MCP server (`internal/mcp`, stdlib-only) exposing `list_projections`, `search_cluster_graph`, `get_resource_neighborhood`, `get_resource_yaml` as a thin client of the read API. |
| 8 | text-to-Cypher + `/rag/answer` | **Done.** Guarded read-only `QueryStore`, `Chat` interface (+ fake/OpenAI), schema + prompts, projector `Query`/`Answer`, `/rag/query` + `/rag/answer` endpoints, `query_cluster` + `answer_question` MCP tools, `graphRAG.chat` CRD config. |

## Open questions

- **Embedding granularity** — per-node cards only, or also per-subgraph /
  per-namespace "community" summaries (à la Microsoft GraphRAG) for
  higher-level questions?
- **Edge-type weighting** during expansion — should `OWNS` count as "closer"
  than `SELECTS` when ranking neighborhood relevance?
- **Embedding storage location** — on the resource node (simple) vs. a sibling
  `:Embedding` node (keeps hot vectors off the resource node; more complex).
- **Provider failure handling** — degrade to pure graph retrieval when the
  embedding provider is unavailable?
