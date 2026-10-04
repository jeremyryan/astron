---
name: astron-relationship-pack
description: Generate an Astron CLI "relationship pack" YAML file (for "astron projections generate/add --relationship-pack") describing how the resource kinds in a Kubernetes CRD-defined API group reference each other, by using Astron's agentic tools (get_resource_schema, search_resource_docs, get_graph_schema, query_graph) to discover the reference fields. Use when asked to create, write, or generate a relationship pack for a CRD group (e.g. cert-manager.io, external-dns, or any other installed CRD group) that Astron doesn't already have a built-in pack for.
---

# Astron Relationship Pack Generator

Produces a `--relationship-pack` YAML file (see `docs/relationships.md`) for
one or more CRD-defined API groups, by reading each Kind's schema to find its
reference fields and turning each one into a `FieldReference` rule. The
output is the same shape as the built-in cert-manager pack in
`internal/cli/relationships.go` — this skill is how you'd derive that pack
(or one for any other CRD group) in the first place.

## Prerequisites

- `kubectl` access to a cluster that has the target CRDs installed.
- The `astron` CLI on `PATH`, or build a throwaway one:
  `go build -o /tmp/astron-cli ./cmd/cli` from the repo root.
- Ideally, a running Astron deployment with its MCP server reachable
  (`astron mcp-server --api-base-url <...>`) so `get_resource_schema` /
  `search_resource_docs` / `get_graph_schema` / `query_graph` are available as
  tools. If CRD Schema Knowledge (`crdSchemas`) isn't enabled on this cluster,
  or no Astron deployment is reachable at all, fall back to `kubectl explain`
  / `kubectl get crd -o yaml` for schema discovery — the rest of the
  procedure is unchanged either way.

## Procedure

### 1. Identify the target group(s) and Kinds

Confirm the API group(s) exist and see what Kinds they define:

```bash
kubectl get crd | grep <group-substring>
```

For each CRD found, note its `spec.group` and `spec.names.kind`. If a group
spans multiple API groups (cert-manager.io's ACME resources live under the
separate `acme.cert-manager.io` group, for example), treat each as its own
group in the steps below, but they can share one pack file.

### 2. Read each Kind's schema

For each Kind, find the fields that reference another resource by name (and
maybe namespace/kind). Prefer the agentic tools when available:

- `search_resource_docs({"query": "<kind or concept>"})` to find which
  captured CRD kinds are relevant.
- `get_resource_schema({"kind": "<Kind>"})` for the full field-level schema:
  types, required fields, descriptions, enums. **Read the field
  descriptions, not just names** — a schema's prose usually says outright
  "Name of the Secret resource..." or similar. A field whose description
  mentions another Kind by name, or whose own name matches `*Ref`,
  `*RefName`, `*Name` (for a field that is clearly identifying another
  object), or a nested object with `name`/`namespace`/`kind` siblings
  (the classic `ObjectReference`/`LocalObjectReference` shape), is a strong
  candidate.

If those tools aren't available (no CRD Schema Knowledge, or no reachable
Astron deployment), use the same inputs directly:

```bash
kubectl explain <kind>.spec --recursive
# or, for the raw schema with descriptions:
kubectl get crd <crd-name> -o yaml
```

List every relationship found before moving on, e.g.:

```
Certificate.spec.secretName        -> Secret (same namespace)
Certificate.spec.issuerRef         -> Issuer | ClusterIssuer (kind given by issuerRef.kind)
CertificateRequest.spec.issuerRef  -> Issuer | ClusterIssuer (same shape)
```

### 3. Map each relationship to a `fieldRef`

For each relationship, work out (see `docs/relationships.md`'s `fieldRef`
table for the full semantics):

- `namePath` (required) — the dotted path to the target's name.
- `kindPath` — only if the field can point at more than one Kind (a sibling
  `kind` field, often with an enum of valid values in the schema — use those
  exact values as the `to` candidates below).
- `namespacePath` — only if the field has a sibling `namespace`; otherwise
  omit it (defaults to same-namespace, automatically skipped for
  cluster-scoped targets).
- `listPath` — only if the reference lives inside a list field (e.g.
  `subjects[]`).
- `on` — which side carries the field. Default to `From` unless the natural
  "X references Y" reading would have the field on the `to` Kind instead.
- `to` — one entry per possible target Kind (plural when `kindPath` is set,
  single-element otherwise). Order matters: the first candidate present in a
  given projection becomes the rule's static fallback target.
- `name` (unique, kebab-case, e.g. `certificate-issued-by`) and `type` (an
  edge label, SCREAMING_SNAKE, e.g. `ISSUED_BY`).

### 4. Write the pack file

Follow the exact schema documented in `docs/relationships.md` under
"Shipping your own relationship pack as a YAML file" (it includes the
built-in cert-manager pack reproduced as a worked example — use it as a
template). One file can hold rules spanning more than one API group.

### 5. Validate (no cluster mutation)

Run the generator in dry-run mode against a namespace that has the CRDs
installed, with `--with-relationships=false` so only the pack's own rules are
shown:

```bash
astron projections generate <namespace> \
  --include-group <group1>,<group2> \
  --with-relationships=false \
  --relationship-pack <pack-file> \
  --output-file -
```

`scripts/validate-pack.sh <namespace> <group1>[,<group2>...] <pack-file>...`
wraps this. Confirm every expected rule appears in `spec.relationships` and
there are no errors (a validation error here means a required field is
missing or malformed — fix the file and re-run, it fails fast with the exact
rule/field at fault).

### 6. Live-verify when possible

If live instances of the CRD already exist, `astron projections add` a
throwaway projection with the pack and inspect the resulting graph (via the
`astron` CLI, the API, or the UI) for the expected edges, then delete the
throwaway projection.

If no live instances exist, you may create minimal synthetic ones to exercise
the pack (mirroring how the built-in cert-manager pack was verified) — but
**ask before creating or deleting anything in the cluster**, and always clean
up afterward:

```bash
kubectl apply -f - <<'EOF'
# minimal instances of the Kinds under test
EOF
astron projections add <ns> --name <throwaway-name> --include-group <group> --relationship-pack <pack-file>
# inspect the graph (curl the API, or the astron CLI/UI), then:
astron projections delete <ns> <throwaway-name>   # or kubectl delete graphprojection
kubectl delete -f - <<'EOF'
# the same synthetic instances
EOF
```

`get_graph_schema`/`query_graph` (if the agentic tools are available) are
useful here too: after applying the throwaway projection, ask something like
"what relationships exist for Certificate in the <throwaway-name>
projection?" to confirm the edges resolved as intended.

### 7. Deliver

Report the finished pack file's path, and point at
`docs/relationships.md`'s `--relationship-pack` section for how to use it
going forward (`astron projections generate/add --relationship-pack
<file>`). If the group is popular enough to be worth shipping for everyone,
mention that it could also be promoted into the built-in registry
(`crdRelationshipPacks` in `internal/cli/relationships.go`) as a follow-up —
but don't do that automatically; it's a separate, deliberate step (new Go
code, tests, and a CLI rebuild) that the user should explicitly ask for.
