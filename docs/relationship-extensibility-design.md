# Extensible Relationship Capture: the `FieldReference` Strategy — Design

Status: implemented

See [`docs/relationships.md`](./relationships.md) for the user-facing
how-to (the `fieldRef` field reference and the cert-manager worked
example now live there; this document stays as the design rationale).

Today, deriving a new kind of edge requires writing Go: every entry in
`spec.relationships[]` names one of nine hardcoded `RelationshipStrategy`
values (`internal/relationship/strategies.go`), each backed by a Go struct
registered once in `Engine.strategies` (`internal/relationship/engine.go`).
Adding support for a new CRD's relationships — say, cert-manager's
`Certificate` referencing an `Issuer` — means writing a new `Strategy`
implementation, adding an enum constant (with a `+kubebuilder:validation:Enum`
update), registering it, and rebuilding and redeploying the operator. That's a
real barrier for something as common as "this CRD has a named reference field
pointing at another resource," which is by far the most common relationship
shape CRDs actually have.

**The opportunity**: looking at the nine existing strategies, most of them
(`ClaimRef`, `ServiceAccount`, `RoleRef`, `BindingSubject`, `ServiceBackend`,
`GatewayParent`, and part of `VolumeMount`) do the *same shape of work* with
different hardcoded field paths — read a name (and maybe a namespace or kind)
from a field on one object, look up the matching node, emit an edge.
`claimRefStrategy` and `serviceAccountStrategy` in particular are nearly
line-for-line identical except for the field path. That's a strong signal
that one generic, **declarative** strategy — configured entirely through the
`GraphProjection` spec, no Go code or rebuild required — covers the large
majority of real-world CRD relationships.

This design adds exactly that: a `FieldReference` strategy, configured via a
new `fieldRef` block on `RelationshipRule`, motivated end-to-end by a worked
cert-manager example.

## Goals

- Let a cluster operator capture a new CRD's relationships by editing a
  `GraphProjection`'s spec — no Go, no new controller image.
- Handle the real variety of reference field shapes seen in practice: a plain
  string field (`Certificate.spec.secretName`), a `LocalObjectReference`-style
  field (name only, implicit same namespace), and a full reference object that
  also names its own target *Kind* (cert-manager's `issuerRef`, which can
  point at either `Issuer` or `ClusterIssuer`).
- Stay consistent with the existing model: a target Kind that isn't in the
  projection's own `scope.resources` simply yields no edge, same as every
  other strategy today.
- Prove the abstraction by being expressive enough to describe most of the
  *existing* bespoke strategies too (without requiring migrating them now).

## Non-goals (for this design)

- **Not** a full expression language (CEL or similar). A structured,
  path-based schema covers the motivating cases; if real rules later need
  conditionals or string templating a fixed schema can't express, that's a
  natural follow-on design (a `CELExpression` strategy alongside, not instead
  of, `FieldReference`), not something to speculatively build now.
- **Not** an out-of-process plugin/webhook mechanism. `CustomStrategy` is
  already reserved in the API for exactly that kind of future extension
  point (and `Engine.Register` already exists as the in-process Go hook) —
  this design doesn't touch either, it just makes the declarative path good
  enough that the escape hatch is rarely needed.
- **Not** migrating the nine existing strategies onto `FieldReference`. They
  keep working exactly as they do today; consolidation is a separate,
  optional cleanup once this lands and proves itself.
- **Not** a distribution mechanism for pre-built rule sets ("ship a
  cert-manager relationship pack in the chart"). That's a real and valuable
  follow-on once authoring a rule by hand is possible at all, but it's a
  packaging question, not a capability question — out of scope here.

## Worked example: cert-manager

The concrete motivating case, confirmed against this cluster's actual
installed CRDs:

| Field | On | Points at | Shape |
| --- | --- | --- | --- |
| `spec.secretName` | `Certificate` | `Secret` (same namespace) | plain string |
| `spec.issuerRef` | `Certificate`, `CertificateRequest`, `Order` | `Issuer` (namespaced) or `ClusterIssuer` (cluster-scoped) | object with `name` + `kind` |

`Order`/`Challenge` back to their owning `CertificateRequest`/`Order` are
ordinary `ownerReferences` — `OwnerReferenceStrategy` already covers those,
no new mechanism needed. The `issuerRef` case is deliberately the harder one
to design for: the field names *which Kind* it points at, and that Kind can
be cluster-scoped.

With this design, the whole cert-manager relationship set becomes pure
configuration:

```yaml
relationships:
  - name: certificate-uses-secret
    type: USES_SECRET
    strategy: FieldReference
    from: { group: cert-manager.io, version: v1, kind: Certificate }
    to: { version: v1, kind: Secret }
    fieldRef:
      namePath: spec.secretName

  - name: certificate-issued-by
    type: ISSUED_BY
    strategy: FieldReference
    from: { group: cert-manager.io, version: v1, kind: Certificate }
    to: { group: cert-manager.io, version: v1, kind: Issuer } # fallback; kindPath usually wins
    fieldRef:
      namePath: spec.issuerRef.name
      kindPath: spec.issuerRef.kind   # "Issuer" or "ClusterIssuer"

  - name: certificaterequest-issued-by
    type: ISSUED_BY
    strategy: FieldReference
    from: { group: cert-manager.io, version: v1, kind: CertificateRequest }
    to: { group: cert-manager.io, version: v1, kind: Issuer }
    fieldRef:
      namePath: spec.issuerRef.name
      kindPath: spec.issuerRef.kind
```

No Go, no rebuild — this drops straight into an existing `GraphProjection`'s
`spec.relationships`.

## The `fieldRef` schema

A new optional field on `RelationshipRule`, required (and only meaningful)
when `strategy: FieldReference`:

```go
// RelationshipRule (existing type, api/v1alpha1/graphprojection_types.go)
type RelationshipRule struct {
    Name     string
    Type     string
    From     ResourceSelector
    To       ResourceSelector
    Strategy RelationshipStrategy
    // fieldRef configures the FieldReference strategy. Required when
    // strategy is FieldReference; ignored otherwise.
    // +optional
    FieldRef *FieldReferenceSpec `json:"fieldRef,omitempty"`
}

// FieldReferenceSpec configures the FieldReference strategy: how to find a
// target object's identity from fields on the scanned object (see On below).
type FieldReferenceSpec struct {
    // on selects which end of the rule ("From" or "To") is the object whose
    // fields are actually read. Defaults to "From" (the intuitive reading:
    // "this rule's From resource carries the reference field"). Set to "To"
    // for the reverse convention some relationships use today (e.g.
    // ConfigMap/Secret/PVC mounts: From is the config resource, but the
    // field lives on the Pod, i.e. To).
    // +optional
    // +kubebuilder:validation:Enum=From;To
    // +kubebuilder:default=From
    On string `json:"on,omitempty"`

    // listPath optionally names a list field (dotted path, rooted at the
    // scanned object) to iterate. When set, namePath/namespacePath/kindPath
    // are evaluated relative to each element instead of the object root, so
    // one rule can express a field like RoleBinding's `subjects[]`.
    // +optional
    ListPath string `json:"listPath,omitempty"`

    // namePath is the dotted field path (rooted at the scanned object, or at
    // each element when listPath is set) to the target's name.
    // +required
    NamePath string `json:"namePath"`

    // namespacePath is the dotted field path to the target's namespace.
    // Omitted for the default: same namespace as the scanned object,
    // ignored automatically when the resolved target Kind turns out to be
    // cluster-scoped.
    // +optional
    NamespacePath string `json:"namespacePath,omitempty"`

    // kindPath is the dotted field path to the target's Kind, for reference
    // fields that can point at more than one Kind (e.g. cert-manager's
    // issuerRef.kind: "Issuer" | "ClusterIssuer"). The resolved Kind name is
    // looked up against the projection's own scope.resources to find its
    // group/version; a Kind that isn't in scope yields no edge for that
    // candidate. When omitted, the rule's own to.kind (or from.kind, when
    // on: To) is used as a fixed target Kind.
    // +optional
    KindPath string `json:"kindPath,omitempty"`
}
```

Path syntax is deliberately minimal: a dotted sequence of map keys (e.g.
`spec.issuerRef.name`), evaluated the same way `unstructured.NestedString`
already does everywhere else in this codebase — no wildcards, no filters, no
expression evaluation. `listPath` is a separate, explicit field rather than
an inline `[]` marker in the path string, so there's no bracket-parsing to
get wrong and no ambiguity about what's relative to what.

### Why `on: From | To` instead of a fixed convention

The existing strategies don't agree on this: `role-grants-rolebinding` has
the reference field (`roleRef`) on the `To` side (`RoleBinding`), while
`bindingSubjectStrategy`'s field (`subjects[]`) is on the `From` side
(`RoleBinding` again, but now as the source). There is no safe universal
default that matches both conventions, so this makes it an explicit,
per-rule choice rather than guessing. New rules read naturally with the
default (`From` carries the field, matching "Certificate references
Issuer"); the reverse (`To` carries the field) is available for rules that
want the same "config/secret/claim points at its consumer" edge direction
`VolumeMount`/`ClaimRef` already use.

### Resolving a dynamic Kind (`kindPath`)

When `kindPath` is set, the resolved Kind name is matched against the
projection's own `spec.scope.resources` (by Kind, case-sensitive) to find its
group/version — the same set of kinds the projection already knows how to
watch. This means:

- A `kindPath` value naming a Kind the projection hasn't opted into simply
  produces no edge for that candidate (logged at debug level, not an error) —
  exactly how an uncaptured target already behaves for every other strategy.
- No new "kind registry" is needed; the projection's existing scope is the
  registry.

### Resolving the namespace

`namespacePath`, when set, is read directly. When omitted, the target
namespace defaults to the scanned object's own namespace — but only when the
resolved target Kind is namespaced. Determining that requires a scope check
this package doesn't currently have: `relationship.Index` only offers
`ByKind`/`Lookup`. This design adds one small capability to close that gap:

```go
// Index (existing interface, internal/relationship/engine.go) gains:

// Namespaced reports whether gvk is a namespaced kind, when known (e.g. via
// the projector's REST mapper). ok is false when the kind's scope can't be
// determined, in which case FieldReference falls back to treating the
// target as namespaced (the common case) rather than silently dropping the
// namespace.
Namespaced(gvk schema.GroupVersionKind) (namespaced bool, ok bool)
```

Backed by the `Projector`'s existing `meta.RESTMapper` (already used in
`run.go`'s `mappingFor`), so this is a thin addition to the existing index
implementation, not new plumbing end to end.

## The strategy implementation

`fieldReferenceStrategy.Derive` follows the same shape every existing
strategy already uses (`internal/relationship/strategies.go`):

1. List the scanned side's objects: `index.ByKind(selectorGVK(scannedSelector))`
   where `scannedSelector` is `rule.From` or `rule.To` per `fieldRef.on`.
2. For each object, resolve zero or more candidate elements: the object
   itself, or each element of `fieldRef.listPath` when set.
3. For each candidate, read `namePath` (skip if empty), optionally
   `kindPath` (resolve against `scope.resources`; skip the candidate if it
   names a Kind not in scope) and `namespacePath` (or default per the
   namespace rule above).
4. Build a `graph.Ref`, resolve it against `index.Lookup` for the real UID
   (falling back to an unresolved ref, exactly like `claimRefStrategy` and
   `serviceAccountStrategy` already do) and emit the edge, oriented
   according to `rule.From`/`rule.To` (independent of which side was
   scanned).

No new dependencies, no code execution beyond structured map traversal — the
risk profile is the same as the `unstructured.NestedString` calls every
existing strategy already makes.

## Validation and failure behavior

Consistent with how `Engine.Derive` already works today ("errors from
individual rules are collected... a single bad rule does not abort the whole
projection" — there's no validating webhook for `GraphProjection` currently):

- A `FieldReference` rule with no `fieldRef` (or an empty `namePath`) is a
  per-rule derive error, logged and skipped, not a webhook rejection.
- A candidate with an empty resolved name, or a `kindPath`/`namespacePath`
  that doesn't resolve, is silently skipped (not an error) — many objects
  legitimately won't have every optional reference field populated.

## API and CRD changes

- `api/v1alpha1/graphprojection_types.go`: `FieldReferenceStrategy
  RelationshipStrategy = "FieldReference"` added to the enum (and its
  `+kubebuilder:validation:Enum` list on `RelationshipRule.Strategy`); new
  `FieldReferenceSpec` type; `FieldRef *FieldReferenceSpec` field on
  `RelationshipRule`.
- `make manifests` regenerates the CRD (`config/crd/bases`), and
  `hack/sync-helm-crds.sh` copies it into `charts/astron/templates/crds.yaml`
  as usual — no manual chart editing.
- No new Helm values, no new controller flags.

## Build order

1. [x] API types: `FieldReferenceSpec`, the new enum value, `make manifests`.
2. [x] `internal/relationship`: extend `Index` with `ResolveKind` and
   `Namespaced`; implement `fieldReferenceStrategy`; register it in
   `Engine.NewEngine`.
3. [x] Wire the projector's real `Index` implementation's `Namespaced` (and
   `ResolveKind`, via `effectiveResources`) to its `meta.RESTMapper` /
   `scope.resources`.
4. [x] Unit tests: plain string field, object field with a fixed Kind, object
   field with `kindPath` disambiguation (the cert-manager `issuerRef` case,
   both the namespaced-`Issuer` and cluster-scoped-`ClusterIssuer`
   outcomes), `listPath` iteration, an out-of-scope `kindPath` value (no
   edge, no error), both values of `on`, a missing/empty `fieldRef` (error),
   and an end-to-end `Projector.Sync` test proving the real REST-mapper
   wiring (not just a hardcoded default) decides the cluster-scoped case.
5. [x] Live verification: applied the cert-manager rule set above to the dev
   cluster's `cert-manager-resources` projection (scoped to the
   `cert-manager` namespace), with synthetic `Certificate`/`ClusterIssuer`
   objects (the cluster had zero real cert-manager instances, the exact
   zero-instance case this design calls out). Confirmed both edges:
   `USES_SECRET Certificate/astron-test-cert -> Secret/astron-test-tls` and
   `ISSUED_BY Certificate/astron-test-cert -> ClusterIssuer/astron-test-selfsigned`
   (correctly namespace-less despite `Certificate` being namespaced).
6. [x] Docs: [`docs/relationships.md`](./relationships.md) (the
   `FieldReference` how-to), linked from the README; this doc's `Status`
   flipped to implemented.

The riskiest piece is the namespace-scope resolution (`Index.Namespaced`) —
everything else is pure, easily-unit-tested field traversal against
`unstructured.Unstructured`, the same pattern nine existing strategies
already use safely.
