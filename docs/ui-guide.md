# Gamera UI User Guide

This guide explains how to **explore and understand a Kubernetes cluster** with
the Project Gamera web UI. The UI renders the resource graph that Gamera projects
into Neo4J as an interactive diagram: nodes are Kubernetes resources, links are
the relationships between them (ownership, selection, mounts, and more).

> Looking for how projections and relationships are produced? See
> [`syncing.md`](./syncing.md) and [`views-design.md`](./views-design.md).
> For the read API behind the UI, see [`openapi.md`](./openapi.md).

---

## Contents

- [Layout at a glance](#layout-at-a-glance)
- [Projections and Views](#projections-and-views)
- [The Filters panel](#the-filters-panel)
- [Reading the graph](#reading-the-graph)
  - [Nodes](#nodes)
  - [Links (relationships)](#links-relationships)
- [Navigating the graph](#navigating-the-graph)
- [Selecting nodes](#selecting-nodes)
- [Arranging and moving nodes](#arranging-and-moving-nodes)
- [The Inspector (right panel)](#the-inspector-right-panel)
- [Inspecting a resource](#inspecting-a-resource)
- [Custom links](#custom-links)
- [The graph toolbar](#the-graph-toolbar)
- [Settings](#settings)
- [Keyboard shortcuts](#keyboard-shortcuts)
- [Tips](#tips)

---

## Layout at a glance

The window is divided into four regions, left to right:

1. **Projections** — the list of graph projections available in the cluster,
   with any saved **Views** nested beneath each one.
2. **Filters** — controls for what the graph shows (resource kinds, namespaces,
   connection distance, labels, grouping) and for saving Views. *Collapsible.*
3. **Graph** — the interactive diagram, with a toolbar in the top‑right corner
   and a relationship‑color legend in the bottom‑left.
4. **Inspector** — details about the selected resource or link, or a browsable
   list of everything on screen. *Collapsible.*

The **settings gear** is in the top‑right of the header.

Either side panel can be collapsed to a thin strip by clicking the **chevron**
in its header; click the chevron again (or the expand arrow) to reopen it. The
graph area grows to fill the freed space, with a smooth animation.

---

## Projections and Views

- A **projection** captures a slice of the cluster (a set of resource kinds and
  namespaces). Select one in the left panel to load its graph. Each entry shows
  the projection's namespace, its phase (e.g. `Ready`), and a count of
  **N nodes / E edges**.
- A **View** is a saved set of filters for a projection. Views appear indented
  under their projection; click one to apply its filters. Switching projections
  without picking a View starts from a clean, unfiltered slate.

The graph refreshes automatically every few seconds, so status changes (for
example a Pod going from `Pending` to `Running`) appear on their own **without
moving the existing layout**.

---

## The Filters panel

The Filters panel (left, next to the graph) controls what is shown:

- **View** — save the current filters:
  - **Save as…** stores the current filters as a new named View.
  - **Save** updates the currently applied View.
  - **Delete** removes it. When your filters don't match a saved View, the panel
    shows *"Custom (unsaved) filters."*
- **Resource types** — a checkbox per kind (with its icon and a count). Use the
  **eye** / **eye‑off** buttons to show or hide **all** kinds at once. Unchecking
  a kind removes those resources (and links to them) from the graph.
- **Namespaces** — when more than one namespace is present, toggle each on or off.
- **Connection distance** — with **All connections** checked, everything is
  shown. Uncheck it and set a number of **hops** to fade out everything more than
  that many links away from the **selected** node, then zoom to that neighborhood.
- **Labels** — add one or more `key` / `value` label filters. Choose **Any** to
  match resources satisfying *any* filter, or **All** to require *all* of them.
  A filter with an empty value matches any value for that key.
- **Grouping** — **Group by namespace** draws each namespace as a labeled box
  containing its resources (on by default). Turn it off for a single
  force‑directed layout of the whole graph.

---

## Reading the graph

### Nodes

Each resource is drawn as a **circular badge** with its Kubernetes icon inside.
Resources without a dedicated icon use a generic blue Kubernetes badge. The label
below a node shows its **Kind** and **name**.

The **ring around a Pod** reflects its health at a glance:

| Ring color | Meaning |
|---|---|
| Green | Healthy / completed (`Running`, `Succeeded`, `Completed`) |
| Amber | In progress / transient (`Pending`, `ContainerCreating`, …) |
| Red | Unhealthy (`Failed`, `CrashLoopBackOff`, `ImagePullBackOff`, `OOMKilled`, …) |

A **bright white ring** means the node is currently selected.

### Links (relationships)

Links are colored by relationship type. The **legend** in the bottom‑left lists
the types currently on screen:

| Type | Color | Meaning |
|---|---|---|
| `OWNS` | Blue | Controller owns a managed resource (Deployment → ReplicaSet → Pod) |
| `SELECTS` | Orange | A Service (or workload) selects Pods by label |
| `MOUNTS` | Purple | A ConfigMap / Secret / PersistentVolumeClaim consumed by a Pod |
| `BINDS` | Teal | A PersistentVolume bound to a PersistentVolumeClaim |
| `ROUTES` | Yellow | An Ingress / HTTPRoute forwarding to a Service |
| `RUNS` | Green | A ServiceAccount a Pod runs as |
| `DEFINES` | Pink | A CustomResourceDefinition and its instances |
| `CUSTOM` | Cyan | A user‑created (manual) link |

The **Edges** toggle in the legend shows or hides the relationship‑type labels
drawn on the links (this preference is remembered between sessions).

A **selected link** is outlined with a thin white stroke around both the line
and its arrowhead.

---

## Navigating the graph

- **Pan** — drag an empty area of the canvas.
- **Zoom** — scroll the mouse wheel, or use the zoom buttons in the toolbar.
- **Fit everything** — click **Fit all nodes to view** in the toolbar to reset
  the view so every visible node fits in the display area.

The cursor changes to a **pointer** when hovering over a node or a link to
indicate they are clickable.

---

## Selecting nodes

Clicking selects and, where noted, centers the node and opens its details:

| Action | Result |
|---|---|
| **Click** a node | Select it, center the view on it, and show its details |
| **Double‑click** a node | Select the node **and its directly‑connected neighbors** |
| **Triple‑click** a node | Select the node's **entire connected component** (everything transitively linked to it) |
| **Shift + drag** on empty canvas | Box‑select multiple nodes |
| **Click** a link | Show the relationship's details |

You can also select from the Inspector's resource list — see below.

When two or more nodes are selected, **alignment and distribution** tools appear
in the toolbar (see [The graph toolbar](#the-graph-toolbar)).

---

## Arranging and moving nodes

- **Move a node** — drag it. Its links stretch to follow.
- **Move a node and pull its neighbors** — hold **Shift** while dragging. The
  directly‑connected neighbors move along by the same amount, so those links keep
  their length instead of stretching.
- **Move a whole namespace** — when grouped by namespace, drag the namespace's
  **name label** at the top of its box to move all of its resources together.
- **Nudge the selection** — use the **arrow keys** to move the selected node(s)
  in small steps; hold **Shift** for larger steps.

Layout is preserved across automatic refreshes and while adding or removing
links, so nodes you have arranged stay put.

---

## The Inspector (right panel)

The Inspector shows one of two things:

- **Resource list** — a browsable index of everything currently on screen,
  grouped by **namespace**, then by **kind**. It appears when nothing is
  selected, or when you press **Back** from a details view.
  - Click a **kind heading** to expand or collapse that group.
  - Click a resource **name** to select it (centers it and opens its details).
  - **Ctrl / Cmd + click** a resource name to **add it to the current selection**
    on the canvas *without* centering or opening details — handy for gathering
    several nodes into a multi‑selection.
  - Use the **eye** button next to a resource to **hide it from the graph** (it
    stays in the list, struck through, so you can show it again). Hiding a node
    doesn't disturb the rest of the layout.
  - Resources currently selected on the canvas are highlighted in the list.
- **Details** — information about the selected node or link (see next section).
  Press **Back** to return to the resource list while keeping your canvas
  selection intact.

Collapse the whole Inspector with the chevron in its header to give the graph
more room.

---

## Inspecting a resource

Selecting a node shows its details:

- Its **Kind** and **apiVersion**, **Name**, and **Namespace**.
- **Creation time**, formatted as `MM/DD/YYYY HH:MM`.
- Other scalar properties captured for the resource. (Noisy internal fields such
  as `resourceVersion` are omitted.)
- For **Pods**, status fields such as phase, readiness and restarts.
- For **HTTPRoutes**, the list of **hostnames**, rendered as clickable links that
  open `https://<hostname>` in a new tab.
- **Labels** and **annotations**, shown as key/value sections.

Selecting a **link** instead shows the relationship type, its **From** and **To**
endpoints, and any relationship data.

### Viewing the manifest (YAML)

Right‑click a node and choose **YAML** (or select it and press **Ctrl / Cmd + Y**)
to open its full manifest in a modal. The modal has a **Copy** button to copy the
YAML to the clipboard.

---

## Custom links

You can draw your own relationships between resources — for example to record a
dependency Gamera doesn't derive automatically.

**To add a link:**

1. Select the source node.
2. Press **L** (or right‑click the node and choose **Add Link**).
3. A dashed arrow follows the cursor; **click the target node** to create the
   link. Press **Esc** to cancel.

Custom links are drawn in the `CUSTOM` (cyan) color and are **persisted** — they
survive projection re‑syncs and reloads.

**To annotate a link:** right‑click a custom link and choose **Edit** to open a
note editor. The note you enter is stored on the relationship, shown in the
link's details in the Inspector, and — for projections with GraphRAG enabled —
folded into the endpoints' embeddings so it is searchable.

**To remove a link:** right‑click a custom link and choose **Delete**.

---

## The graph toolbar

The toolbar sits in the top‑right of the graph area:

- **Zoom in** / **Zoom out** — step the zoom level.
- **Fit all nodes to view** — frame every visible node in the display area.
- **Export as PNG** — download the current graph as an image.
- **Toggle grid overlay** — show or hide a reference grid (remembered between
  sessions).

When **two or more** nodes are selected, alignment tools appear:

- **Align horizontally / vertically** — line the selected nodes up on a shared
  row or column.
- **Distribute horizontally / vertically** — with **three or more** selected,
  space them evenly between the two outermost nodes.

---

## Settings

Open **Settings** from the gear icon in the header:

- **Wallpaper** — choose an image to use as the graph‑area background instead of
  the solid color, or remove it. (Stored locally in your browser.)
- **Graph layout** — tune the force‑directed layout. Changing any of these
  re‑runs the layout, so node positions are recomputed:
  - **Repulsion force** — how strongly nodes push each other apart.
  - **Link length** — the ideal length of a link between connected nodes.
  - **Gravity** — how strongly nodes are pulled toward the center.

Other preferences — the **Edges** label toggle (in the legend) and the **grid**
overlay (in the toolbar) — are also remembered between sessions.

---

## Keyboard shortcuts

| Shortcut | Action |
|---|---|
| **Arrow keys** | Move the selected node(s) a small step |
| **Shift + Arrow keys** | Move the selected node(s) a larger step |
| **L** | Start an "Add Link" from the selected node |
| **Ctrl / Cmd + C** | Center the view on the selection |
| **Ctrl / Cmd + Y** | Open the YAML manifest of the selected node |
| **Esc** | Cancel the in‑progress link |
| **Shift + drag** (canvas) | Box‑select nodes |
| **Shift + drag** (node) | Move a node and pull its neighbors along |
| **Ctrl / Cmd + click** (list) | Add a resource to the selection without centering |

Shortcuts are ignored while you are typing in a text field.

---

## Tips

- Use **Group by namespace** to see cluster structure, and turn it off for a more
  organic, space‑filling layout of a busy graph.
- Combine **Connection distance** with a selection to focus on a single
  resource's neighborhood and hide the rest.
- **Triple‑click** a node to grab its whole connected component, then use the
  **alignment** tools to tidy it up.
- **Hide** noisy resources (via the eye button or the node's **Hide/View**
  context‑menu item) to declutter without changing the layout.
- If a busy graph looks tangled, open **Settings → Graph layout** and increase
  **Repulsion force** or **Link length** to spread it out.
