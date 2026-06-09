# GraphRAG User Guide

This guide explains how to **enable, configure, and use** GraphRAG in Project
Gamera. GraphRAG turns the Kubernetes resource graph that Gamera projects into
Neo4J into a retrieval backend for LLM/agent applications: semantic search,
graph traversal, natural-language question answering, and text-to-Cypher — over
HTTP and natively via the [Model Context Protocol](https://modelcontextprotocol.io/)
(MCP).

> Looking for *how it works* internally? See the design document at
> [`graphrag.md`](./graphrag.md). This guide is the *how to use it* companion.

---

## Contents

1. [What you get](#what-you-get)
2. [How it works (in one minute)](#how-it-works-in-one-minute)
3. [Prerequisites](#prerequisites)
4. [Enable GraphRAG](#enable-graphrag)
5. [Choosing an embedding/chat provider](#choosing-an-embeddingchat-provider)
6. [Verify it is working](#verify-it-is-working)
7. [Using the HTTP API](#using-the-http-api)
8. [Using the MCP server](#using-the-mcp-server)
9. [Configuration reference](#configuration-reference)
10. [Tuning](#tuning)
11. [Security](#security)
12. [Troubleshooting](#troubleshooting)
13. [Cost notes](#cost-notes)

---

## What you get

When GraphRAG is enabled on a `GraphProjection`, Gamera adds four retrieval
modes on top of the existing graph:

| Mode | Endpoint | MCP tool | Needs embeddings? | Needs a chat model? |
|------|----------|----------|:---:|:---:|
| **Semantic search** — find resources relevant to a question, plus their connecting subgraph | `POST …/rag/search` | `search_cluster_graph` | ✅ | — |
| **Neighborhood** — the "blast radius" around a specific resource | `POST …/rag/neighborhood` | `get_resource_neighborhood` | — | — |
| **Answer** — a grounded natural-language answer with citations | `POST …/rag/answer` | `answer_question` | ✅ | ✅ |
| **Text-to-Cypher** — translate a question into a guarded read-only query and run it | `POST …/rag/query` | `query_cluster` | — | ✅ |

Neighborhood retrieval needs neither embeddings nor a chat model — it works on
the graph alone.

---

## How it works (in one minute)

1. Every captured resource is rendered into a short **natural-language "card"**,
   e.g. *"Pod `web-7d9` in namespace `shop` is Running (2/2 ready, 0 restarts).
   Owned by Deployment `web`. Mounts ConfigMap `web-config`. Selected by Service
   `web-svc`."*
2. Each card is **embedded** into a vector and stored on its node in Neo4J,
   indexed for similarity search. Only cards whose content changed are
   re-embedded on each sync, so cost tracks churn, not graph size.
3. **Search** embeds your query, finds the nearest resources, then **expands the
   graph** around them to return a connected subgraph with provenance.
4. **Answer** does the above and asks a chat model to answer from the retrieved
   context. **Text-to-Cypher** asks a chat model to translate your question into
   a read-only Cypher query (validated and executed under guard rails).

---

## Prerequisites

- A running Gamera operator and at least one `GraphProjection` (see the project
  [README](../README.md)).
- **Neo4J 5.x** (the bundled chart's Neo4J qualifies) — required for native
  **vector indexes**.
- An **embedding provider** for search/answer. Options:
  - **OpenAI** or **Azure OpenAI** — an API key.
  - **Ollama** — a local server, no key.
  - **fake** — a built-in, deterministic, no-network embedder for wiring/tests
    only (not useful for real retrieval quality).
- A **chat model** (only for `answer`/`query`): same provider options.

---

## Enable GraphRAG

GraphRAG is **opt-in** and off by default. Enabling it requires two things:
a credentials Secret (for OpenAI/Azure) and the `graphRAG` block on your
`GraphProjection`.

### 1. Create the provider credentials Secret

For OpenAI (the embedding and chat keys can be the same Secret or different
ones; the default data key is `apiKey`):

```sh
kubectl -n gamera create secret generic gamera-embeddings \
  --from-literal=apiKey="$OPENAI_API_KEY"

# Optional: a separate secret for the chat model (answer/query).
kubectl -n gamera create secret generic gamera-chat \
  --from-literal=apiKey="$OPENAI_API_KEY"
```

> Not needed for the `fake` provider, and usually not for `ollama`.

### 2a. Enable it on an existing `GraphProjection`

Add a `graphRAG` block to `spec`:

```yaml
apiVersion: gamera.gamera.io/v1alpha1
kind: GraphProjection
metadata:
  name: default
  namespace: gamera
spec:
  neo4j:
    uri: neo4j://gamera-neo4j.gamera.svc:7687
    authSecretRef:
      name: neo4j-credentials
  # ... scope, relationships ...
  graphRAG:
    enabled: true
    embedding:
      provider: openai
      model: text-embedding-3-small
      dimensions: 1536
      authSecretRef:
        name: gamera-embeddings        # data key: apiKey
    include:
      labels: true
      annotations: false
    vectorIndex:
      similarity: cosine
    # Optional: enables /rag/answer and /rag/query (text-to-Cypher).
    chat:
      enabled: true
      provider: openai
      model: gpt-4o-mini
      authSecretRef:
        name: gamera-chat
```

Apply it: `kubectl apply -f your-projection.yaml`.

### 2b. Enable it via the Helm chart (default projection)

If you let the chart create the default projection, set the values instead:

```sh
helm upgrade gamera charts/gamera -n gamera \
  --set defaultProjection.graphRAG.enabled=true \
  --set defaultProjection.graphRAG.embedding.authSecretRef.name=gamera-embeddings \
  --set defaultProjection.graphRAG.chat.enabled=true \
  --set defaultProjection.graphRAG.chat.authSecretRef.name=gamera-chat
```

See [`charts/gamera/values.yaml`](../charts/gamera/values.yaml) for every
`defaultProjection.graphRAG.*` value.

---

## Choosing an embedding/chat provider

`provider` accepts `openai`, `azure`, `ollama`, or `fake`. All of them speak the
OpenAI-compatible API shape.

| Provider | `baseURL` | API key | Notes |
|----------|-----------|---------|-------|
| `openai` | optional (defaults to the public API) | required | The simplest setup. |
| `azure` | **required** — your resource endpoint | required | Point `baseURL` at your Azure OpenAI deployment. |
| `ollama` | **required** — e.g. `http://ollama.gamera.svc:11434/v1` | not needed | Fully local; good for air-gapped clusters. |
| `fake` | — | — | Deterministic, no network. Wiring/dev only; do **not** rely on result quality. |

The same `provider` set applies to both `embedding` and `chat`; they are
configured independently and may use different providers, models, and Secrets.

---

## Verify it is working

After enabling, watch the projection status:

```sh
kubectl -n gamera get graphprojection default
# NAME      PHASE   NODES   EDGES   AGE
# default   Ready   42      57      3m
```

Check the GraphRAG-specific status and conditions:

```sh
kubectl -n gamera get graphprojection default -o jsonpath='{.status.embeddedNodeCount}{"\n"}'
kubectl -n gamera get graphprojection default \
  -o jsonpath='{range .status.conditions[*]}{.type}={.status} {.reason}{"\n"}{end}'
# Available=True Synced
# Progressing=False Running
# RAGReady=True EmbeddingsReady
```

- `status.embeddedNodeCount` — how many nodes currently have an embedding.
- `status.lastEmbeddingTime` — when embeddings were last refreshed.
- The **`RAGReady`** condition is `True` once the vector index exists and nodes
  are embedded; `False` (`AwaitingEmbeddings`) while it is still warming up.

If enabling GraphRAG fails (e.g. a missing Secret key), the projection goes to
`Phase: Error` with an `Available=False` condition explaining why.

---

## Using the HTTP API

The read API is served on the operator's API port (default `:8082`). During
`make dev` it is port-forwarded to <http://localhost:8082>; otherwise forward it
yourself:

```sh
kubectl -n gamera port-forward svc/gamera-api 8082:8082
```

All retrieval endpoints are scoped to a projection by its **namespace and
name**: `/api/projections/{namespace}/{name}/…`.

### Semantic search

```sh
curl -s http://localhost:8082/api/projections/gamera/default/rag/search \
  -H 'Content-Type: application/json' \
  -d '{"query": "why is the web deployment unhealthy?", "topK": 5, "hops": 1}' | jq
```

Request fields: `query` (required), `topK` (default 5), `hops` (default 1),
`edgeTypes` (e.g. `["OWNS","SELECTS"]`), `kinds`, `namespaces` (filter the seed
selection).

Response (`retrieval`):

```jsonc
{
  "query": "why is the web deployment unhealthy?",
  "seeds": [
    { "id": "<uid>", "kind": "Pod", "name": "web-7d9", "score": 0.83 }
  ],
  "cards": [
    { "id": "<uid>", "kind": "Pod", "namespace": "shop", "name": "web-7d9",
      "text": "Pod web-7d9 ... CrashLoopBackOff. Owned by Deployment web. ..." }
  ],
  "subgraph": {
    "nodes": [ /* same shape as the /graph endpoint */ ],
    "edges": [ /* source/target/type */ ]
  }
}
```

### Neighborhood (no embeddings required)

```sh
curl -s http://localhost:8082/api/projections/gamera/default/rag/neighborhood \
  -H 'Content-Type: application/json' \
  -d '{"kind": "Pod", "namespace": "shop", "name": "web-7d9", "hops": 2}' | jq
```

Request fields: `kind` + `name` (required), `namespace`, `apiVersion`, `hops`
(default 1), `edgeTypes`. Returns the same `retrieval` shape.

### Answer a question (requires `chat`)

```sh
curl -s http://localhost:8082/api/projections/gamera/default/rag/answer \
  -H 'Content-Type: application/json' \
  -d '{"question": "what is preventing the web pods from starting?"}' | jq
```

Returns `{ "question", "answer", "retrieval" }` — the answer plus the context
that grounded it, so you can verify the citations.

### Text-to-Cypher (requires `chat`)

Best for precise/aggregate questions (counts, filters, joins):

```sh
curl -s http://localhost:8082/api/projections/gamera/default/rag/query \
  -H 'Content-Type: application/json' \
  -d '{"question": "how many pods are CrashLoopBackOff in namespace shop?"}' | jq
```

Returns `{ "question", "cypher", "rows" }` — the generated, validated Cypher and
its result rows.

### Status codes

| Code | Meaning |
|------|---------|
| `200` | Success. Search/neighborhood also return an **empty** result if the projector is not running yet. |
| `400` | Bad request (e.g. empty `query`/`question`, missing `kind`/`name`). |
| `404` | No such projection. |
| `422`/`500` | A generated query was rejected or execution failed. |
| `503` | The capability is not configured (e.g. `answer`/`query` without a `chat` model, or `search` without `embedding`). |

---

## Using the MCP server

The MCP server exposes the retrieval API as agent tools over stdio. It is a
**subcommand of the same `gamera` binary** and a thin client of the read API, so
it inherits projection scoping and read-only guarantees.

### Run it

```sh
# Make sure the read API is reachable (e.g. port-forwarded to :8082).
gamera mcp-server --api-base-url http://localhost:8082
```

The base URL also comes from `$GAMERA_API_URL` (default
`http://localhost:8082`). Logs go to **stderr**; **stdout** is the JSON-RPC
stream — don't mix anything else into stdout.

### Tools

| Tool | Purpose |
|------|---------|
| `list_projections` | Discover available projections (namespace, name, counts). |
| `search_cluster_graph` | Semantic search → relevant resources + subgraph. |
| `get_resource_neighborhood` | Structural context around a specific resource. |
| `answer_question` | Grounded natural-language answer with citations *(needs chat)*. |
| `query_cluster` | Text-to-Cypher: generate + run a read-only query *(needs chat)*. |
| `get_resource_yaml` | Fetch a single resource's live YAML. |

### Wire it into an MCP client

Example for an MCP client that launches servers by command (e.g. Claude
Desktop / Cursor `mcp.json`):

```jsonc
{
  "mcpServers": {
    "gamera": {
      "command": "/usr/local/bin/gamera",
      "args": ["mcp-server", "--api-base-url", "http://localhost:8082"]
    }
  }
}
```

Then ask the agent things like *"Using the gamera tools, what's the blast radius
of the `web-config` ConfigMap in the default projection?"*

---

## Configuration reference

All fields live under `spec.graphRAG` of a `GraphProjection`.

| Field | Default | Description |
|-------|---------|-------------|
| `enabled` | `false` | Master switch for embeddings + semantic retrieval. |
| `embedding.provider` | `openai` | `openai` \| `azure` \| `ollama` \| `fake`. |
| `embedding.model` | — | Embedding model name (e.g. `text-embedding-3-small`). |
| `embedding.dimensions` | `0` (model default) | Pin the vector length (model permitting). |
| `embedding.baseURL` | provider default | API base; required for `azure`/`ollama`. |
| `embedding.authSecretRef.name` | — | Secret with the API key. |
| `embedding.authSecretRef.namespace` | projection ns | Secret namespace. |
| `embedding.authSecretRef.apiKeyKey` | `apiKey` | Secret data key holding the key. |
| `include.labels` | `true` | Include resource labels in the embedded card. |
| `include.annotations` | `false` | Include annotations in the card. |
| `vectorIndex.similarity` | `cosine` | `cosine` \| `euclidean`. |
| `chat.enabled` | `false` | Enable `/rag/answer` and `/rag/query`. |
| `chat.provider` | `openai` | `openai` \| `azure` \| `ollama` \| `fake`. |
| `chat.model` | — | Chat model name (e.g. `gpt-4o-mini`). |
| `chat.baseURL` | provider default | API base; required for `azure`/`ollama`. |
| `chat.authSecretRef` | — | Secret with the chat API key (same shape as embedding). |

Status fields written back by the controller: `embeddedNodeCount`,
`lastEmbeddingTime`, and the `RAGReady` condition (alongside the existing
`nodeCount`, `relationshipCount`, `Available`/`Progressing`).

> **Changing config** (model, provider, dimensions, similarity, card includes)
> restarts the projector and re-embeds the graph. Changing `embedding.model` or
> `dimensions` effectively invalidates existing vectors — expect a full
> re-embed.

---

## Tuning

- **Retrieval breadth** — `topK` (number of seeds) and `hops` (expansion radius)
  are per-request. Start with `topK: 5`, `hops: 1`; increase `hops` to pull in
  more context (at the cost of larger subgraphs).
- **Focus the expansion** — pass `edgeTypes` (e.g. `["OWNS"]`) to follow only
  certain relationships, or `kinds`/`namespaces` to constrain seed selection.
- **Card content** — turn `include.annotations` on if your annotations carry
  meaningful signal; leave it off (default) to avoid noise and token cost.
- **Embedding cost vs. freshness** — embeddings refresh after each (debounced)
  sync; only changed cards are re-embedded. A longer `spec.resyncInterval`
  reduces background churn.
- **Similarity** — `cosine` is the usual choice; use `euclidean` only if your
  model's vectors are tuned for it.

---

## Security

- **Read-only by construction.** The read API and all retrieval endpoints never
  mutate the cluster or the graph.
- **Guarded text-to-Cypher.** Generated queries run in a **read-only
  transaction** with a **statement timeout** and a **deny-list** (no
  `CREATE/MERGE/DELETE/SET/REMOVE`, no `CALL` — so `apoc.*`/`db.*`/`dbms.*`
  procedures are blocked — no `LOAD`/`USE`/multiple statements; a `RETURN` is
  required). Embedding/internal bookkeeping properties are stripped from results.
- **Scoping caveat.** Queries are scoped to the projection on a best-effort
  basis: the projection id is passed as `$projection` and the model is instructed
  to filter on it, but this is not yet *enforced* by query rewriting. Don't rely
  on text-to-Cypher alone for hard multi-tenant isolation.
- **Credentials** are referenced from Kubernetes Secrets, never inlined in the
  CRD — the same pattern as the Neo4J credentials.
- **Secret names appear in cards.** Gamera never captures Secret *values*, but a
  Secret's name/keys can appear in a resource card (e.g. "Mounts Secret
  `web-tls`"). Keep that in mind for the embedding provider you choose.
- **Network egress.** With `openai`/`azure`, card text and queries are sent to
  the provider. Use `ollama` (or `fake`) to keep everything in-cluster.

---

## Troubleshooting

**`RAGReady` is `False` / `AwaitingEmbeddings`.**
The vector index/embeddings are still warming up, or no embeddings have been
written yet. Give it a sync cycle; check the operator logs for `refreshing
embeddings failed`. Confirm the provider is reachable and the API key is valid.

**`/rag/search` returns `503`.**
`graphRAG.enabled` is false or no embedding provider is configured for this
projection.

**`/rag/answer` or `/rag/query` returns `503`.**
`graphRAG.chat.enabled` is false (or absent). Add the `chat` block and a Secret.

**Search returns an empty result with `200`.**
The projector isn't running yet for that projection (just created, or
reconciling). Re-try after it reaches `Phase: Ready`.

**`Phase: Error`, `Available=False`, reason `EmbeddingConfigUnavailable`.**
The embedding/chat Secret is missing or lacks the expected data key. Verify:

```sh
kubectl -n gamera get secret gamera-embeddings -o jsonpath='{.data.apiKey}' | base64 -d | head -c4
```

**Provider errors in logs (`api error`, `unexpected status`).**
Wrong model name, bad/expired key, or (for `azure`/`ollama`) a missing/incorrect
`baseURL`. The error message from the provider is surfaced verbatim.

**Generated Cypher is rejected (`generated query rejected`).**
The model produced a non-read-only or malformed statement; it is **not**
executed. Re-phrase the question, or prefer `/rag/search`/`/rag/answer` for
open-ended questions.

**MCP client shows garbled output / no tools.**
Ensure nothing writes to the server's **stdout** except the protocol, and that
`--api-base-url` points at a reachable API. Run the binary by hand and send
`{"jsonrpc":"2.0","id":1,"method":"tools/list"}` on stdin to sanity-check.

---

## Cost notes

- Embedding cost is proportional to **change**, not graph size: unchanged
  resource cards are not re-embedded (content-hash diff).
- `answer`/`query` calls a chat model **per request** — those are on-demand and
  user-driven, not background.
- For zero external spend and full data locality, use `provider: ollama` for
  both embedding and chat, or `provider: fake` for non-production wiring.
