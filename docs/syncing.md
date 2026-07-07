# Syncing cluster state into Neo4J

This document describes how often, and by what mechanisms, Kubernetes resources
are reconciled into the Neo4J graph.

## Summary

| Trigger | Cadence | Purpose |
|---|---|---|
| Watch event (debounced) | ~1s after a change | Keep the graph current in near real-time |
| Periodic ticker / informer relist | 5m default (`spec.resyncInterval`) | Reconcile drift / catch missed events |
| Initial sync | Once at projector startup | Populate the graph when a projection starts |

In short: the graph is updated **effectively continuously** — within about a
second of any change — with a **full reconciliation every 5 minutes** by
default (tunable per projection).

## How it works

Each `GraphProjection` runs its own **projector**
(`internal/projector`), which owns a set of dynamic informers and a debounced
sync loop. There are two triggers that write to Neo4J.

### 1. Event-driven — near real-time (~1 second)

Each projection creates **dynamic informers** that watch every resource kind in
its scope. Any add / update / delete calls `enqueue()`, which signals a
debounced sync loop (`internal/projector/run.go`):

- The loop waits a **`debounceWindow` of 1 second** (`internal/projector/projector.go`)
  to coalesce a burst of changes, drains the trigger, then runs a full `Sync`.
- `Sync` rebuilds the projection's entire desired graph from the informer
  caches (nodes + derived edges) and calls `Store.Sync`, which upserts current
  data and prunes anything stale.

So a change in the cluster typically lands in Neo4J **within about a second**.
The write is declarative / full-state each time — the whole projection is
recomputed from cache — rather than an incremental per-object write.

### 2. Periodic full re-sync — default every 5 minutes

A `time.NewTicker(ResyncInterval)` fires a full `Sync` on a fixed interval
regardless of events. This is a **safety net against missed watch events and
drift**. The same interval is used as the informer factory's relist period, so
the informers also re-list every cycle.

- **Default: 5 minutes.** Set in `internal/projector/projector.go`
  (`resync = 5 * time.Minute` when unset) and mirrored by the controller's
  `defaultResyncInterval`.
- **Configurable per projection** via `spec.resyncInterval` (a duration). The
  CLI `generate` command defaults `--resync-interval 5m`.

### Initial sync

When a projector starts, it performs a one-time sync as soon as the informer
caches are warm, so the graph is populated immediately rather than waiting for
the first event or ticker tick.

## Not the same thing: the controller's own requeue

The `GraphProjectionReconciler`
(`internal/controller/graphprojection_controller.go`) re-reconciles the
**`GraphProjection` object itself** (its configuration and status) with
`RequeueAfter: resyncInterval` (default 5m), and `30s` on an error path. That
reconciles the projection's *config and status* — the actual resource→graph
data sync is driven by the projector's watch + ticker loop described above, not
by this requeue.

## Configuration example

```yaml
apiVersion: astron.astron.io/v1alpha1
kind: GraphProjection
metadata:
  name: default
spec:
  # Full reconciliation interval (safety net). Watch events still update the
  # graph within ~1s regardless of this value. Defaults to 5m when omitted.
  resyncInterval: 5m
  # ...
```

## Key references

- `internal/projector/run.go` — event handlers (`AddFunc`/`UpdateFunc`/`DeleteFunc`
  → `enqueue`), the debounced `run` loop, `doSync`, and `Sync`.
- `internal/projector/projector.go` — `debounceWindow` (1s) and the 5m
  `ResyncInterval` default.
- `internal/projector/node.go` — `newFactory` (dynamic shared informer factory
  with the resync period).
- `internal/controller/graphprojection_controller.go` — `defaultResyncInterval`
  and the object-level requeue.
- `api/v1alpha1/graphprojection_types.go` — the `resyncInterval` spec field.
