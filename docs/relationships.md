# Relationship Rules

A `GraphProjection`'s `spec.relationships` list tells Astron how to derive
edges between the resource kinds it captures (`spec.scope.resources`). Each
rule names a `from`/`to` resource kind, an edge `type` (the Neo4J relationship
type, e.g. `OWNS`), and a `strategy` that determines how the edge is found.

```yaml
relationships:
  - name: deployment-owns-replicaset
    type: OWNS
    strategy: OwnerReference
    from: { group: apps, version: v1, kind: Deployment }
    to: { group: apps, version: v1, kind: ReplicaSet }
```

## Built-in strategies

Most of Astron's default relationships (Deployment → Pod ownership, Service →
Pod selection, ConfigMap/Secret → Pod mounts, RBAC bindings, Ingress/HTTPRoute
backends, ...) are covered by nine built-in Go strategies:
`OwnerReference`, `LabelSelector`, `VolumeMount`, `ClaimRef`,
`ServiceBackend`, `GatewayParent`, `ServiceAccount`, `RoleRef`, and
`BindingSubject`. These ship with the operator and need no configuration
beyond naming them in a rule.

## `FieldReference`: relationships for your own CRDs

New CRDs commonly have relationships that are just a named reference
field — "this resource names that resource by name (and maybe namespace and
kind)". The `FieldReference` strategy captures that shape declaratively,
entirely through the `GraphProjection` spec: no Go code, no new operator
image.

### A worked example: cert-manager

cert-manager's `Certificate` resource references a `Secret` (where it writes
the issued certificate) and an `Issuer` or `ClusterIssuer` (who issues it):

```yaml
apiVersion: astron.astron.io/v1alpha1
kind: GraphProjection
metadata:
  name: cert-manager-resources
  namespace: cert-manager
spec:
  scope:
    namespaces: [cert-manager]
    resources:
      - { group: cert-manager.io, version: v1, kind: Certificate }
      - { group: cert-manager.io, version: v1, kind: Issuer }
      - { group: cert-manager.io, version: v1, kind: ClusterIssuer }
      - { version: v1, kind: Secret }
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
```

This produces, for a `Certificate` whose `spec.issuerRef.kind` is
`ClusterIssuer`, an `ISSUED_BY` edge to the matching `ClusterIssuer` node —
correctly namespace-less, even though `Certificate` itself is namespaced.
`CertificateRequest`/`Order`'s own `issuerRef` can be captured the same way;
their ownership back to the `Certificate`/`CertificateRequest` that created
them is already covered by the built-in `OwnerReference` strategy, since
cert-manager sets normal `ownerReferences` for those.

To get `scope.resources` right without listing every cert-manager Kind by
hand, `astron projections generate`/`add` can pull in a whole API group at
once with `--include-group` (every Kind in it, regardless of whether any
instance currently exists — unlike `--all-resources`, which only ever
discovers kinds that already have live instances):

```sh
astron projections generate cert-manager \
  --include-group cert-manager.io,acme.cert-manager.io
```

By default a projection watches only the namespace it is created in. Use
`--namespace a,b` to watch other namespaces (the projection is still created
in the positional namespace, so you can keep all your projections together),
or `--all-namespaces` to watch every namespace; the two are mutually
exclusive:

```sh
astron projections add astron --name cert-manager-all --all-namespaces \
  --include-group cert-manager.io,acme.cert-manager.io
```

See `astron projections generate --help` for the full set of `--include`/
`--include-group`/`--exclude` semantics (a Kind named in `--exclude` always
wins, even over `--include-group`).

### `fieldRef` fields

| Field | Required | Meaning |
| --- | --- | --- |
| `namePath` | yes | Dotted path (rooted at the scanned object, or at each element when `listPath` is set) to the target's name, e.g. `spec.secretName` or `spec.issuerRef.name`. |
| `kindPath` | no | Dotted path to the target's Kind, for fields that can point at more than one Kind (e.g. `Issuer` vs `ClusterIssuer`). The resolved Kind is matched against this projection's own `scope.resources`; a Kind not in scope silently yields no edge for that candidate. When omitted, the rule's static `to.kind` (or `from.kind`, when `on: To`) is used. |
| `namespacePath` | no | Dotted path to the target's namespace. When omitted, defaults to the scanned object's own namespace — unless the resolved target Kind is cluster-scoped, in which case no namespace is used automatically. |
| `listPath` | no | Dotted path to a list field to iterate (e.g. a RoleBinding's `subjects`); `namePath`/`namespacePath`/`kindPath` are then evaluated relative to each list element instead of the object root. |
| `on` | no (`From` by default) | Which side of the rule (`From` or `To`) actually carries the reference field. `From` is the intuitive default ("Certificate references Issuer"); use `To` for the reverse convention some relationships use (e.g. a ConfigMap is the `From` side, but the referencing field lives on the consuming Pod, the `To` side). |

Paths are a plain dotted sequence of map keys — no wildcards, no filters, no
expression evaluation. A path that resolves to an empty or missing value is
simply skipped (not every object will have every optional reference field
populated); a `FieldReference` rule with no `fieldRef` at all (or an empty
`namePath`) is a per-rule error, logged like any other bad rule, without
aborting the rest of the projection's sync.

## Shipping your own relationship pack as a YAML file

cert-manager ships as a *built-in* relationship pack (the CLI already knows
its rules), but you don't have to wait for a CRD group to get one: pass one
or more YAML files to `--relationship-pack` on `astron projections
generate`/`add`, and their rules are merged into the generated manifest the
same way a built-in pack's are — gated on whether the relevant resource kinds
are actually present, so a pack that's only partially relevant to this
projection doesn't produce a rule with a dangling target.

```sh
astron projections generate cert-manager \
  --include-group cert-manager.io \
  --relationship-pack ./cert-manager-relationships.yaml
```

A pack file is a list of rules, each shaped like a `FieldReference` rule
except `to` is a *list* of candidate target Kinds instead of one — the first
candidate present in the generated projection is used, exactly like the
built-in cert-manager pack's `Issuer`/`ClusterIssuer` fallback. In fact, the
following file reproduces that built-in pack exactly, as a worked example you
can adapt for another CRD group:

```yaml
# cert-manager-relationships.yaml
rules:
  - name: certificate-uses-secret
    type: USES_SECRET
    from: { group: cert-manager.io, version: v1, kind: Certificate }
    to:
      - { version: v1, kind: Secret }
    fieldRef:
      namePath: spec.secretName

  - name: certificate-issued-by
    type: ISSUED_BY
    from: { group: cert-manager.io, version: v1, kind: Certificate }
    to:
      - { group: cert-manager.io, version: v1, kind: Issuer }
      - { group: cert-manager.io, version: v1, kind: ClusterIssuer }
    fieldRef:
      namePath: spec.issuerRef.name
      kindPath: spec.issuerRef.kind

  - name: certificaterequest-issued-by
    type: ISSUED_BY
    from: { group: cert-manager.io, version: v1, kind: CertificateRequest }
    to:
      - { group: cert-manager.io, version: v1, kind: Issuer }
      - { group: cert-manager.io, version: v1, kind: ClusterIssuer }
    fieldRef:
      namePath: spec.issuerRef.name
      kindPath: spec.issuerRef.kind
```

`strategy` may be omitted when `fieldRef` is set (it defaults to
`FieldReference`, as above); any other strategy — `OwnerReference`,
`LabelSelector`, etc. — must be named explicitly and doesn't take a `fieldRef`.
Every rule still needs a `name` (unique within the file), a `type`, and a
`from.kind`; the file is parsed strictly (an unrecognized field is an error,
not silently ignored) and validated before any cluster calls are made, so a
typo is reported immediately with the specific rule and field at fault.

`--relationship-pack` is independent of `--with-relationships`: a pack file is
an explicit, individually-named request, so it's always included, even when
`--with-relationships=false` turns off the broad auto-detected rule set. You
can pass multiple files (repeat the flag, or comma-separate paths); if two
rules end up with the same `name` (from different packs, or a pack and a
built-in rule), the first one wins.

See
[`docs/relationship-extensibility-design.md`](./relationship-extensibility-design.md)
for the full design rationale.
