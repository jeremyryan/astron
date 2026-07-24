# Astron Web UI — User Guide

The Astron web UI is an interactive tool for exploring and understanding your Kubernetes cluster's resources and their relationships. This guide walks you through the main features and how to use them effectively.

## Getting Started

### Accessing the UI

Once Astron is deployed in your cluster, the web UI is available at the address where the Astron API service is exposed. By default:

- **Local development**: `http://localhost:8082` (when running `make dev`)
- **In a cluster**: Use `kubectl port-forward` to access it:

  ```bash
  kubectl -n astron port-forward svc/astron-api 8082:8082
  # Then open http://localhost:8082
  ```

### First Look: Main Layout

The UI is divided into three main areas:

1. **Left Sidebar** — Projection & View navigation
2. **Main Canvas** — Interactive graph visualization
3. **Right Panel** — Filters and node inspection

```
┌─────────────────────────────────────────────────────┐
│  Projections & Views   │   Graph Canvas   │ Filters │
│  (Left Sidebar)        │                  │ & Details
├─────────────────────────────────────────────────────┤
│                                                     │
│  • default              │  [Graph Nodes &    │ Kind │
│    • My First View      │   Edges]           │ Filters
│    • Production Apps    │                    │       │
│  • staging              │                    │ Label │
│  • monitoring           │                    │ Filters
│                         │                    │       │
└─────────────────────────────────────────────────────┘
```

---

## Navigating Projections and Views

### What Are Projections?

A **GraphProjection** is a declaration of what resources and relationships from your cluster should be captured into Neo4j. Each projection focuses on a scope (e.g., a namespace, label selector, or resource types).

The left sidebar lists all available projections:

- Click a projection name to view it with no filters applied.
- The projection displays its **phase** (Pending, Active, etc.), **node count**, and **relationship count**.

### What Are Views?

A **View** is a saved set of filters applied to a projection. Instead of manually filtering every time, save your current filter state as a named view and recall it instantly.

**Nested beneath each projection in the sidebar:**

- Click a view name to load the projection with those filters pre-applied.
- Views appear indented under their associated projection.

---

## The Graph Canvas

### Exploring the Graph

The interactive graph canvas shows your cluster's resources as **nodes** (circles) and their relationships as **edges** (lines).

- **Nodes** represent Kubernetes resources: Deployments, Pods, Services, ConfigMaps, Secrets, etc.
- **Edges** represent relationships: `OWNS` (workload → Pod), `SELECTS` (Service → Pod), `MOUNTS` (ConfigMap/Secret → Pod), etc.
- **Node styling** includes:
  - **Color** indicates the relationship to the selected node (or neutral gray if none selected)
  - **Icon** represents the resource kind (Pod, Deployment, etc.)
  - **Label** shows the resource name

### Selecting a Node

Click any node to select it. When a node is selected:

- It highlights in the graph.
- Its details appear in the right panel.
- Related nodes are colored by their relationship type.
- The **connection-distance filter** becomes active (see Filtering section below).

### Viewing Node Details

When a node is selected, the right panel displays:

- **Name** and **Namespace**
- **Kind** (resource type)
- **Status** and other key metadata
- **Relationships** — links to related nodes (click to navigate)
- **YAML** — full resource definition (click "View YAML" button)

### Graph Controls

**Toolbar icons at the top of the canvas:**

- **Zoom In / Zoom Out** — adjust magnification
- **Fit to View** — auto-scale to show all nodes
- **Layout** — choose graph layout algorithm (dagre, fcose, or grid)
- **Pan** — drag the canvas to reposition

---

## Filtering

The right panel contains powerful filters to narrow down the graph and focus on what matters.

### Hide/Show Resource Kinds

Collapse resources by type to declutter the graph:

1. In the **Kinds** section, view a list of all resource kinds in the current graph with their counts.
2. **Toggle** any kind to hide or show all nodes of that type.
3. Use **Show All** or **Hide All** to manage multiple kinds at once.

**Example:** Hide ConfigMaps and Secrets to focus on workloads and Pods.

### Hide/Show Namespaces

Filter by namespace to focus on a specific part of your cluster:

1. In the **Namespaces** section, view all namespaces in the current graph.
2. **Toggle** any namespace to hide or show all resources in that namespace.
3. Use **Show All** or **Hide All** to manage multiple namespaces at once.

**Tip:** Cluster-scoped resources (with no namespace) appear first.

### Connection-Distance Filter (Hops)

When a node is selected, use the **Max Distance** slider to fade connections based on how many hops away they are:

- **Hops** measure the number of edges between the selected node and other nodes.
- Set **Max Distance** to `1` to show only direct connections.
- Set it to `2` to show nodes up to 2 edges away (e.g., a Deployment → Pod → ConfigMount).
- Leave it empty (∞) to show all connections regardless of distance.

**Use case:** Understand the immediate blast radius of a change to a specific Deployment.

### Label Filtering

Filter resources by their Kubernetes labels:

1. Click the **+ Label** button to add a label filter.
2. Enter the **label key** (e.g., `app`, `version`).
3. Optionally enter a **label value** (leave blank to match any value for that key).
4. Choose **any** (OR) or **all** (AND) logic to combine multiple label filters:
   - **Any** — show nodes matching at least one label filter.
   - **All** — show nodes matching every label filter.

5. Click the **×** button on a filter row to remove it.

**Examples:**
- Key: `app`, Value: `my-app` — show only nodes with that label
- Key: `environment`, Value: empty — show nodes with the `environment` label (any value)
- Multiple filters with **any** — show nodes matching any of the selected labels

### Grouping by Namespace

Toggle the **Group by Namespace** checkbox to:

- **On** — visually group nodes into namespace "boxes" in the graph (default)
- **Off** — display all nodes flattly without namespace grouping

This helps when managing many namespaces; grouping makes namespace boundaries clear.

### Grouping Nodes Together

**ReplicaSets are grouped by namespace automatically** as soon as a namespace has two or more of them, since a Deployment's rollout history tends to leave several old ReplicaSets behind that add noise without adding insight. Right-click the group and choose **Ungroup** to break it apart if you'd rather see them individually — they won't be automatically re-grouped again for the rest of the session.

Beyond that default, you can merge any other set of nodes into a single collapsed node yourself:

1. Select two or more nodes (Shift/Ctrl-click, or box-select).
2. Right-click one of them and choose **Group**.

The selected nodes collapse into one dashed, warm-accented node labeled with the plural of each kind it contains (e.g. a group of ReplicaSets is labeled "ReplicaSets"; a mix of Pods and Services is labeled "Pods/Services"). It shows the generic blank icon, since it may stand in for several different kinds, and carries every edge that any of its members had — so its connections to the rest of the graph are preserved.

Grouping is a display-only convenience (nothing changes in the cluster or the underlying graph data), so it resets if you reload the page. To undo it, right-click the group node and choose **Ungroup** to restore its individual members. You can also select a group alongside other nodes/groups and choose **Group** again to merge them further.

---

## Saving and Managing Views

### Save the Current Filters as a View

When you've configured filters that you want to reuse:

1. Click the **Save View** button (bookmark icon) at the top.
2. Enter a **display name** for your view (e.g., "Production Apps").
3. Optionally add a **description** (e.g., "All production workloads in the app namespace").
4. Click **Save**.

The view now appears in the left sidebar, nested under the projection.

### Load a Saved View

Click any view name in the left sidebar to:

1. Load the associated projection.
2. Automatically apply all saved filters.

This is faster than manually re-configuring filters every time.

### Edit a Saved View

To modify a saved view:

1. **Load** the view (click its name).
2. **Adjust** any filters in the right panel.
3. Click the **Update View** button (or similar) to save changes.

### Delete a Saved View

If you no longer need a view:

1. In the left sidebar, locate the view name.
2. Right-click (or use the context menu) and select **Delete**, or
3. Load the view and use the **Delete View** button.

---

## Node Inspection & YAML

### View YAML Details

When a node is selected:

1. Click the **YAML** button (code icon) at the top or in the details panel.
2. A modal opens showing the complete Kubernetes resource definition.
3. Use **Copy** to copy the YAML to your clipboard.
4. Close the modal when done.

This is useful for:
- Verifying resource configuration
- Debugging issues by examining actual state
- Exporting resources for reuse in other clusters

### Inspect Relationships

In the node details panel, relationships are listed with **links**:

- **OWNS** — workload (Deployment, StatefulSet, DaemonSet) to Pod
- **SELECTS** — Service to Pod (via label selector)
- **MOUNTS** — ConfigMap/Secret to Pod (via volume or env mounts)

Click any relationship link to navigate to the related node.

---

## Settings & Preferences

### Accessing Settings

Click the **Settings** (gear) icon at the top of the UI to open the Settings modal.

### Available Settings

**Display Preferences:**
- **Theme** — choose between light, dark, or system default
- **Graph Layout** — set a default layout algorithm (dagre, fcose, grid)
- **Auto-arrange on load** — automatically layout new nodes when the graph refreshes

**Connection & API:**
- **API Base URL** — configure the backend API endpoint (useful if accessing from a different host)

**Caching & Performance:**
- **Auto-refresh interval** — how often to refresh the graph from the backend (in seconds, or disable)
- **Cache invalidation** — clear cached data when needed

These settings are persisted in your browser's local storage, so they persist across sessions.

---

## Tips & Tricks

### Finding Specific Resources

1. **Use label filters** to narrow by `app`, `environment`, `team`, or custom labels.
2. **Hide irrelevant kinds** (e.g., ConfigMaps) to reduce clutter.
3. **Click a view** if you've already saved a similar filter set.

### Understanding Relationships

- **OWNS** edges show which workload created a Pod.
- **SELECTS** edges reveal which Services expose a Pod.
- **MOUNTS** edges show what configuration (ConfigMaps/Secrets) a Pod is using.
- Follow these to understand **dependencies** and **impact radius**.

### Debugging a Service Outage

1. Click the **Service** node you're debugging.
2. Look at **SELECTS** edges to see which Pods it routes to.
3. Click each Pod to inspect its logs, status, and mounts.
4. Use **connection distance** to see upstream (other services, ingresses) and downstream (databases, external APIs).

### Exploring Cluster Architecture

1. **Hide** ConfigMaps and Secrets to focus on workloads and their relationships.
2. **Group by Namespace** to see how resources are isolated.
3. Click a Deployment to see the Pods it manages and the Services that expose them.
4. Save this as a "High-Level View" for quick reference.

### Tracking Configuration Changes

1. Select a **ConfigMap** or **Secret** node.
2. Use **MOUNTS** edges to find all Pods using it.
3. Set **Max Distance = 1** to isolate direct mounts.
4. Save as "ConfigMap Dependents" view to track impact of changes.

---

## Keyboard Shortcuts

| Shortcut | Action |
|----------|--------|
| `Click` | Select a node |
| `Ctrl/Cmd + Click` | On a selected node: remove it from the selection |
| `Drag` | Pan the canvas |
| `Shift + Drag` (on a node) | Move the node(s), pulling directly-connected nodes along |
| `Ctrl/Cmd + Drag` (on a selected node, multiple selected) | Rotate the selection around its center point |
| `Scroll` | Zoom in/out |
| `Esc` | Deselect the current node |
| `Arrow keys` | Nudge the selected node(s) (hold `Shift` for a larger step) |
| `Y` | Show the YAML manifest of the selected node |
| `L` | Start creating a link from the selected node |
| `H` | Hide the selected node(s) from the graph |
| `Shift + H` | Hide all nodes except the selection; with nothing selected, unhide all nodes |
| `C` | Center the selected node(s) in the view |
| `E` | Expand the selection to include directly-connected nodes (press repeatedly to keep growing it) |
| `J` | Join the selected nodes: also select the nodes along the shortest path between each pair, if one exists |
| `A` | Select all nodes connected to the selection, directly or indirectly |
| `Shift + D` | Deselect all nodes |
| `*` | Arrange the selected node's neighbors in a circle around it |
| `Shift + +` | Zoom in |
| `Shift + -` | Zoom out |
| `Shift + 0` | Reset the zoom to fit the whole graph |
| `Ctrl/Cmd + Arrow keys` | Pan the view, like dragging the background (hold `Shift` for a larger step) |
| `F` | Fit every visible node to the view |
| `Alt + H` | Reveal any hidden immediate neighbors of the selected node(s) |
| `Alt` (hold) | Preview hidden immediate neighbors of the selection, translucent, for as long as it's held |
| `Ctrl/Cmd + F` | (Browser search) Find in page |

---

## Troubleshooting

### Graph Not Loading

- **Check projection status**: In the sidebar, verify the projection shows an active phase and node/edge counts.
- **Verify API connectivity**: Open your browser's developer console (F12) and check for network errors.
- **Refresh the page**: Sometimes a stale cache prevents loading (or clear local storage in Settings).

### Filters Not Working

- **Ensure a node is selected** for connection-distance filtering (it requires a starting point).
- **Double-check label filters** — Kubernetes labels are case-sensitive.
- **Verify namespace names** — cluster-scoped resources use an empty namespace.

### Performance Issues with Large Graphs

- **Hide resource kinds** you don't need (e.g., ConfigMaps, Secrets).
- **Group by namespace** to help the layout engine.
- **Use connection distance** to limit visible connections.
- **Save filtered views** to avoid re-filtering on each visit.

### Saved Views Not Appearing

- Views are stored as **GraphView** Kubernetes resources in the same namespace as the projection.
- Verify you have **read access** to the namespace:
  ```bash
  kubectl get graphviews -n <namespace>
  ```
- If the view was created in a different namespace, switch projections or re-save it under the correct namespace.

---

## Related Documentation

- **[GraphProjection CRD](./views-design.md)** — how to define what gets projected
- **[GraphRAG Integration](./graphrag-guide.md)** — semantic search and AI features
- **[OpenAPI Reference](./openapi.md)** — full API specification for programmatic access

---

## Feedback & Support

Found a bug or have a feature request? Open an issue on the [Astron repository](https://github.com/your-org/astron). Include:

- Steps to reproduce
- Screenshots or a short video
- Your cluster version and Astron version
- Browser and OS information
