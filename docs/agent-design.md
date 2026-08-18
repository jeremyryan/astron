# Chat Agent Tools — Design

Status: proposed

Today the chat panel answers questions through a **fixed pipeline**:
`Projector.Answer` runs one hybrid retrieval (vector + graph) and makes one
chat completion call over the retrieved context. The model never decides what
to look up — it gets a single, pre-baked slice of the graph and must answer
from it. Multi-step questions ("which Service exposes the Pod that mounts the
`elasticsearch-config` ConfigMap, and is it healthy?") fall outside what one
retrieval can ground.

A **chat agent** turns that fixed pipeline into a bounded loop: the model is
given a set of read-only, projection-scoped **tools** and decides, turn by
turn, which to call to gather the information it needs before answering. The
capabilities we want as tools already exist as `Projector` methods (`Search`,
`Neighborhood`, `Query`) and one live-cluster read (`ResourceYAML`); the work
is exposing them to the model and running the loop.

Two properties define this design:

- **Reuse, not reinvent.** The tool catalog is the same set the MCP server
  already exposes to external agents (`search_cluster_graph`,
  `get_resource_neighborhood`, `query_graph`, `get_resource_yaml`,
  `list_projections`). The in-process agent calls `Manager`/`Projector`
  **directly** — no HTTP hop — while MCP keeps its HTTP client. Names,
  descriptions and JSON schemas come from **one shared catalog** so the two
  surfaces never drift.
- **Additive and safe.** Tool-calling is an *optional* capability on the chat
  abstraction (like `ModelSelector` today). Backends that don't support it,
  and projections without it enabled, fall back to the existing `Answer`
  pipeline unchanged. Every tool is read-only and scoped to a single
  projection; the loop is bounded in steps, time, and output size.

## Scope (phase 1)

- New `POST /api/projections/{ns}/{name}/rag/agent` endpoint (the existing
  `/rag/answer` fixed pipeline stays as-is).
- **No streaming** — the endpoint returns the full result, including the list
  of tool steps the agent took. Streaming (SSE) is deferred to phase 2.
- **Single-projection scope** — the agent operates only within the projection
  named in the route. Cross-projection reasoning is out of scope for phase 1.
- **No new CRD field** — enablement is gated on chat availability plus a
  client-side toggle for now; a `graphRAG.agent` spec block can come later.

## Tool-calling on the chat abstraction

`rag.Chat` is single-shot today: `Complete(ctx, []Message) (string, error)`.
Tool use is added as a **separate optional interface**, so no existing backend
or caller is forced to change:

```go
// ToolSpec advertises a tool to the model (JSON Schema parameters).
type ToolSpec struct {
    Name        string
    Description string
    Parameters  map[string]any // JSON Schema object
}

// ToolCall is the model's request to invoke a tool.
type ToolCall struct {
    ID        string
    Name      string
    Arguments json.RawMessage
}

// Reply is one turn of a tool-using completion: either free-text Content
// (the final answer) or one/more ToolCalls to execute, never both meaningful.
type Reply struct {
    Content   string
    ToolCalls []ToolCall
}

// ToolCaller is implemented by chat backends that support native tool calling.
type ToolCaller interface {
    CompleteWithTools(ctx context.Context, msgs []Message, tools []ToolSpec) (Reply, error)
}
```

`Message` grows two optional fields used only on the tool path: `ToolCalls`
(on an assistant message that requested tools) and `ToolCallID` (on a
`RoleTool` message carrying a tool's result). A new `RoleTool` role is added.

- **`OpenAIChat` implements `ToolCaller`.** The chat-completions payload gains
  `tools` (function definitions from `ToolSpec`), the response is parsed for
  `tool_calls`, and tool results are sent back as `{role:"tool",
  tool_call_id, content}` messages. This is the OpenAI/Azure/LiteLLM/Ollama
  `/v1` function-calling shape — the same client already used for all four
  providers. The existing temperature-omit retry logic is reused.
- **`FakeChat` gains a scripted tool path** (`ToolScript [][]ToolCall` or a
  `ToolFunc`) so the agent loop can be tested deterministically — tool call →
  observation → final answer — with no network and no tokens.

Backends that don't implement `ToolCaller` (or a `fake` with no script) are
detected via a type assertion and the caller falls back to `Answer`.

## The agent loop — `internal/agent`

A new package, kept separate from `rag` (which stays transport-agnostic) and
`projector` (which owns the data), so the loop is small and unit-testable.

```go
// Tool is one invocable capability: its advertised spec plus a handler that
// returns a textual (JSON) observation for the model.
type Tool struct {
    Spec   rag.ToolSpec
    Invoke func(ctx context.Context, args json.RawMessage) (string, error)
}

type ToolSet []Tool

type Step struct {
    Tool    string `json:"tool"`
    Args    any    `json:"args"`
    Summary string `json:"summary"` // short, for UI/observability
}

type Result struct {
    Answer string
    Steps  []Step
    // Citations link back to graph nodes surfaced during the run (phase 2 can
    // use these to highlight the canvas).
    Citations []graph.Ref
}

type Runner struct {
    chat     rag.ToolCaller
    tools    ToolSet
    maxSteps int
    // per-tool timeout, max observation bytes, system prompt, ...
}

func (r *Runner) Answer(ctx context.Context, question string, history []rag.Message) (Result, error)
```

The loop:

1. Seed messages: a **system prompt** (the agent's role, that it is scoped to
   one projection, tool-use guidance, and an instruction to cite resources) +
   prior `history` + the user `question`.
2. Call `chat.CompleteWithTools(ctx, msgs, specs)`.
3. If the reply has `ToolCalls`: execute each against the `ToolSet`, record a
   `Step`, append the assistant tool-call message and a `RoleTool` result
   message (observation, truncated to a byte cap), and go to step 2.
4. If the reply has `Content`: that is the answer — return `Result`.
5. Stop at `maxSteps` (default ~6) and return the best answer so far with a
   note that the step budget was exhausted.

**`catalog.go`** holds the single source of truth for tool names,
descriptions and schemas, imported both here and by a refactored
`internal/mcp/tools.go` so the two surfaces stay identical.

## Tool set (phase 1)

Handlers call the `Projector` (and the API server's k8s client for live YAML)
directly. All are read-only and scoped to the route's projection:

| Tool | Backed by | Purpose |
| --- | --- | --- |
| `search_cluster_graph` | `Projector.Search` | Semantic retrieval — relevant resources + connecting subgraph. |
| `get_resource_neighborhood` | `Projector.Neighborhood` | Structural "blast radius" of a known resource. |
| `query_graph` | `Projector.Query` | Precise/aggregate answers via guarded, read-only Cypher. |
| `get_graph_schema` | `rag.SchemaSummary(ReadGraph)` | Cheap orientation (labels, relationship types) to help form queries. |
| `get_resource_yaml` | live k8s read (API server client) | Full manifest of one resource, server-managed noise stripped. |

The fixed `answer_question` pipeline is deliberately **not** a tool — it *is*
what the agent replaces. `list_projections` is omitted in phase 1 because the
agent is single-projection scoped.

## Wiring — `Projector.AnswerWithTools`

A new method mirrors `Answer`, reusing all the routing already in place:

```go
func (p *Projector) AnswerWithTools(
    ctx context.Context, question, model string,
    history []rag.Message, opts SearchOptions,
) (agent.Result, error)
```

- Resolves the chat via the existing `chatFor(model)` (controller-wide
  providers, `allowedModels` policy, model overrides all apply unchanged).
- If the resolved chat implements `rag.ToolCaller`, build a `ToolSet` over
  `p` and run `agent.Runner`.
- **Otherwise fall back to `p.Answer`** and flag the result as
  non-agentic — so a `fake`-without-script or a tool-less model still works.

`chatEnabled`, provider routing, and model policy are untouched.

## API surface

One new route, consistent with the existing RAG endpoints:

```
POST /api/projections/{ns}/{name}/rag/agent
     req:  { question, model?, history? }
     resp: { answer, steps: [{tool, args, summary}], cards, agentic }
```

- `history` carries the prior transcript so follow-up questions have context.
- `steps` makes the agent's actions visible to the UI (transparency and
  debuggability) — which tools ran, with what arguments.
- `cards` reuses the existing `AnswerCard` DTO for cited resources, so the
  chat panel renders citations exactly as it does for `/rag/answer`.
- `agentic:false` signals the fallback path was taken (tool-less backend).
- `docs/openapi.yaml` is regenerated (`make openapi`; guarded by
  `TestOpenAPIYAMLInSync`).

## UI

- **`ChatPanel`** sends the prior transcript as `history` and, when the
  "Agentic (use tools)" toggle is on, calls `/rag/agent` instead of
  `/rag/answer`. The model selector and provider routing already built are
  reused as-is.
- Each answer renders its `steps` as a collapsible **"tool activity"** list —
  `🔧 searched graph…`, `🔧 fetched Pod elasticsearch-master-0 yaml…` — so
  users see how the agent reached its answer. Citations (`cards`) render as
  today.

## Safety and limits

- **Read-only, scoped:** every tool is read-only and confined to the route's
  projection; Cypher stays behind `graph.ValidateReadOnlyCypher`;
  `get_resource_yaml` reuses the existing strip/live-read path.
- **Bounded:** hard caps on step count (`maxSteps`), per-tool timeout, and
  total observation bytes fed back to the model, to bound latency and token
  cost. Each step is recorded for observability.
- **Graceful degradation:** non-`ToolCaller` backends fall back to `Answer`;
  a tool error is returned to the model as an observation (so it can recover)
  rather than aborting the whole request.

## Testing

- `rag`: `OpenAIChat.CompleteWithTools` request/response marshalling
  (`httptest` — tools sent, `tool_calls` parsed, `role:"tool"` round-trips);
  `FakeChat` scripted tool calls.
- `agent`: `Runner` drives a full fake sequence (tool call → observation →
  final answer); `maxSteps` termination; tool-error-as-observation; a table
  test asserting each `ToolSet` handler maps to the right `Projector` method.
- `api`: `handleRAGAgent` happy path and fallback path; OpenAPI sync.
- `mcp`: existing tests still pass after the catalog refactor.
- Live: `skaffold dev` — a multi-hop question against the `elasticsearch`
  projection routed to a real model; and a token-free `fake` (scripted) run
  proving the loop executes tools with no network.

## Build order

1. `rag`: tool-calling types + `ToolCaller`, `OpenAIChat.CompleteWithTools`,
   `FakeChat` scripting; unit tests.
2. `internal/agent`: shared `catalog`, `ToolSet`, `Runner`; unit tests.
3. `Projector.AnswerWithTools` with fallback; refactor `internal/mcp/tools.go`
   onto the shared catalog.
4. API endpoint + DTOs + OpenAPI regen; server tests.
5. UI: agentic toggle, `history`, `steps` rendering.
6. Docs (graphrag guide + ui guide sections).

## Phase 2 and beyond

- **Streaming (SSE):** stream `step` events (tool started/finished) and final
  tokens for a live "thinking/acting" experience.
- **CRD config:** a `graphRAG.agent` block — `enabled`, `maxSteps`, tool
  allow-list — surfaced in the settings modal.
- **Richer citations:** link each step to graph nodes and highlight them in
  the canvas.
- **More tools:** cluster events/logs (live k8s reads), `list_snapshots` and a
  "compare to snapshot" tool, and — if the single-projection constraint is
  relaxed — cross-projection discovery via `list_projections`.

## Explicit non-interactions

- **Fixed pipeline preserved:** `/rag/answer` and `Projector.Answer` are
  unchanged; the agent is strictly additive.
- **MCP behavior preserved:** the external MCP server keeps its HTTP-backed
  tools; only the *catalog definitions* are shared, not the transport.
- **Sync/projector data path:** the agent only reads; it never affects sync,
  embeddings, or projection status.
