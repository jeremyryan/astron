# Controller-wide model providers

Astron is growing agentic capabilities (embeddings, chat, and more to come).
Rather than configuring a model provider on every `GraphProjection`, you can
declare a set of **named providers once on the controller**. They are loaded at
startup and made available across every projection.

> Status: this first step wires the configuration into the controller (it is
> parsed, validated, and held in a registry shared by all projections). How
> projections and agents *select* among these providers is layered on in
> follow-up work; today's per-projection `graphRAG` block is unchanged.

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
```

Validation performed at load time:

- every provider needs a **unique, non-empty `name`** (within its list);
- `provider` must be one of `openai`, `azure`, `ollama`, `litellm`, `fake`
  (empty defaults to `openai`);
- `model` is required for every non-`fake` provider;
- `baseURL` is required for `azure`, `ollama`, and `litellm`.

Credentials are **never** placed in this file (it is mounted from a ConfigMap
and therefore not secret). Each provider references a Secret key via
`apiKeySecret`; the reference is recorded now and resolved from the Secret by
later functionality.

An empty/absent file simply means no controller-wide providers are configured.

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
```

To manage the ConfigMap yourself, point the chart at it instead (the inline
lists are then ignored); it must expose the config under a `providers.yaml`
key:

```yaml
providers:
  existingConfigMap: my-astron-providers
```
