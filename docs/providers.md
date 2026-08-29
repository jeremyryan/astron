# Controller-wide model providers

Astron is growing agentic capabilities (embeddings, chat, and more to come).
Rather than configuring a model provider on every `GraphProjection`, you can
declare a set of **named providers once on the controller**. They are loaded at
startup and made available across every projection.

> Status: configuration is parsed, validated, and held in a registry shared
> by all projections. Chat requests route to a named controller-wide provider
> automatically when the requested model matches one (see "Using
> controller-wide providers" below); a per-projection `graphRAG` block, when
> configured, continues to work unchanged alongside these. `crdSchemas`
> (below) is the first controller-wide *embedding* provider consumer.

## The configuration

The controller reads a YAML file, typically mounted from a ConfigMap, via the
`--providers-config-file` flag (or the `ASTRON_PROVIDERS_CONFIG_FILE`
environment variable; the flag wins):

```yaml
embeddingProviders:
  - name: openai-small            # required, unique among embedding providers
    provider: openai              # openai | azure | ollama | litellm | fake (default openai)
    model: text-embedding-3-small
    dimensions: 1536              # optional
    apiKeySecret:                 # optional; resolved from a Secret (never inlined)
      name: astron-embeddings
      namespace: astron           # optional; defaults to the controller namespace
      key: apiKey                 # optional; defaults to "apiKey"
  - name: local
    provider: ollama
    model: nomic-embed-text
    baseURL: http://ollama.astron.svc:11434/v1   # required for azure/ollama/litellm

chatProviders:
  - name: gpt4o
    provider: openai
    model: gpt-4o-mini
    allowedModels: ["*"]          # optional; per-request model selection policy
    apiKeySecret:
      name: astron-chat

# Controller-wide CRD schema capture for RAG (see docs/crd-schema-design.md):
# renders each selected CustomResourceDefinition's schema into searchable
# documentation for the chat agent, kept invisible to the live resource graph.
# This is a separate concern from a GraphProjection's own scope.crds, which
# only controls whether CRDs are captured as ordinary, visible nodes in that
# projection's graph; crdSchemas applies cluster-wide regardless.
crdSchemas:
  enabled: true
  embeddingProvider: openai-small  # required when enabled; must name an entry
                                    # in embeddingProviders above
  names: []                        # optional allow-list of full CRD names
                                    # (e.g. widgets.example.com); empty
                                    # captures every CRD in the cluster
```

Validation performed at load time:

- every provider needs a **unique, non-empty `name`** (within its list);
- `provider` must be one of `openai`, `azure`, `ollama`, `litellm`, `fake`
  (empty defaults to `openai`);
- `model` is required for every non-`fake` provider;
- `baseURL` is required for `azure`, `ollama`, and `litellm`;
- when `crdSchemas.enabled` is true, `embeddingProvider` must be set and must
  name a provider actually declared in `embeddingProviders`, and any
  `crdSchemas.names` entry must be non-blank. `crdSchemas` is otherwise
  unvalidated while `enabled` is left `false` (the default), so it can be
  declared ahead of actually turning it on.

Credentials are **never** placed in this file (it is mounted from a ConfigMap
and therefore not secret). Each provider references a Secret key via
`apiKeySecret`, resolved from the Secret at controller startup (chat
providers and the `crdSchemas.embeddingProvider`) or lazily as needed.

An empty/absent file simply means no controller-wide providers are configured.

## Using controller-wide providers

- **Chat.** A chat request (`/rag/answer`, `/rag/query`, `/rag/agent`) whose
  `model` names a controller-wide chat provider routes to it directly — this
  takes precedence over the projection's own `graphRAG.chat`, so a user's
  chosen provider always wins regardless of what that projection configured.
  A projection with no `graphRAG.chat` of its own still gets chat features
  when at least one controller-wide chat provider is configured.
- **CRD schema embeddings.** When `crdSchemas.enabled` is true, the named
  `embeddingProvider` is resolved once at startup and used by a standalone,
  controller-wide syncer (`internal/crdschema`, unrelated to any single
  projection) to embed captured CRDs' schema overviews for search. This
  powers two agent/MCP tools, `search_resource_docs` and
  `get_resource_schema`, and the `GET /api/schema-docs`/`GET /api/schema/{kind}`
  API routes — see [`crd-schema-design.md`](./crd-schema-design.md) and
  [`graphrag-guide.md`](./graphrag-guide.md).
- **Embedding providers** other than `crdSchemas.embeddingProvider` are
  currently only surfaced for UI model selection; a projection's own
  `graphRAG.embedding` still configures its own embedder directly (it does
  not yet route through this registry by name).

## Configuring it with the Helm chart

Set inline lists under `providers` and the chart creates the ConfigMap, mounts
it into the controller, and passes `--providers-config-file`:

```yaml
providers:
  embeddingProviders:
    - name: openai-small
      provider: openai
      model: text-embedding-3-small
      apiKeySecret:
        name: astron-embeddings
  chatProviders:
    - name: gpt4o
      provider: openai
      model: gpt-4o-mini
      apiKeySecret:
        name: astron-chat
  crdSchemas:
    enabled: true
    embeddingProvider: openai-small
```

To manage the ConfigMap yourself, point the chart at it instead (the inline
lists are then ignored); it must expose the config under a `providers.yaml`
key:

```yaml
providers:
  existingConfigMap: my-astron-providers
```
