# Views — design & persistence

## What a View is

A **View** is a named, saved set of filters applied to a **GraphProjection**.
Projections capture a set of resources and relationships from the cluster into
Neo4j; Views let a user narrow that graph down to a meaningful subset and recall
it later by name.

A View captures the same filters the UI exposes interactively today:

- the projection it applies to (`projectionRef`)
- `hiddenKinds` — hidden resource kinds (e.g. `ConfigMap`, `Secret`)
- `hiddenNamespaces` — hidden namespaces
- `labelFilters` + `labelMode` — label key/value constraints combined with
  any (OR) / all (AND)
- `maxDistance` — connection-distance fading (hops from the selected node)
- `groupByNamespace` — whether to group nodes into namespace boxes

## Where the data lives

Views are persisted as a **namespaced Custom Resource Definition**,
`GraphView` (`gamera.gamera.io/v1alpha1`), stored in etcd via the Kubernetes
API server — the same mechanism as the existing `GraphProjection` CRD.

### Why a CRD

- **Consistent with the operator pattern.** Gamera is a Kubernetes operator and
  already owns the `GraphProjection` CRD; Views are configuration that naturally
  references a projection.
- **No new infrastructure.** Reuses the API server / etcd already in play. No
  extra database, PVC, migrations or backups to operate.
- **Free capabilities.** OpenAPI schema validation, RBAC, `kubectl`, GitOps, and
  watch/list via the controller-runtime client.

### Why not the alternatives

- **Neo4j** holds the *projected graph*, which projectors continuously rebuild
  and overwrite, and **each projection can target a different Neo4j database** —
  so there is no single canonical store for cross-projection config.
- **Browser `localStorage`** (used today for UI settings) is per-browser and not
  shareable; "saved under a name" implies a shared catalog. It remains the right
  place for *ephemeral/unsaved* UI state only.
- **A dedicated SQL/SQLite service** is over-engineered for a few small filter
  sets and breaks the "no extra infrastructure" property.
- **ConfigMaps** would work but offer no schema validation and clunky listing; a
  CRD is strictly better if we go Kubernetes-native.

## Shape

`GraphView` is a **data-only** custom resource: there is no reconcile loop in
Phase 1 (it behaves like a schema-validated ConfigMap). A minimal `status`
subresource is reserved for future validation (e.g. dangling `projectionRef`).

```
GraphView
  spec:
    projectionRef: { name, namespace? }
    displayName?: string
    description?: string
    filters:
      hiddenKinds?: [string]
      hiddenNamespaces?: [string]
      labelFilters?: [{ key, value? }]
      labelMode?: any | all            # default: any
      maxDistance?: int                # >= 1; omitted = all connections
      groupByNamespace?: bool          # default: true
  status:
    observedGeneration?: int
    conditions?: [Condition]
```

Design choices:

- **Separate kind, not a field on `GraphProjection`.** Views are
  many-per-projection and edited frequently by end users; decoupling avoids
  churning the projection object (and its reconcile loop).
- **Namespaced**, normally co-located with the projection it references.
- Optionally set an `ownerReference` to the `GraphProjection` so deleting a
  projection garbage-collects its views (deferred; off by default in Phase 1).

## Sharing model (Phase 1)

The web UI has **no user authentication**, so all writes happen as the
operator's ServiceAccount. Phase-1 Views are therefore **shared per namespace**
— everyone sees the same saved views. Per-user ownership/visibility would
require adding auth and an `owner` field, and is deferred.

## API & RBAC

The read API gains a small CRUD surface (the first write path in the server):

- `GET    /api/views?projectionNamespace=&projectionName=` — list
- `POST   /api/views` — create
- `PUT    /api/views/{namespace}/{name}` — update
- `DELETE /api/views/{namespace}/{name}` — delete

This requires adding `create/update/patch/delete` verbs on `graphviews`
(and `graphviews/status`) to the operator ClusterRole; read verbs already exist
via the wildcard read rule.

## Phasing

- **Phase 1 (this work):** `GraphView` CRD + generated code + CRD manifests +
  chart wiring + RBAC + read/write API + UI to save/list/apply/delete views.
  Shared per namespace.
- **Phase 2 (later):** `ownerReference` GC, a validating controller/webhook for
  `projectionRef`, per-user ownership + auth, a default view per projection,
  and import/export.
