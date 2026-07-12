import { useEffect, useMemo, useRef, useState } from "react";
import { ActionIcon, Divider, Group, Menu, Text, Tooltip } from "@mantine/core";
import {
  IconCode,
  IconDownload,
  IconEye,
  IconEyeOff,
  IconGrid3x3,
  IconLink,
  IconTrash,
  IconLayoutAlignCenter,
  IconLayoutAlignMiddle,
  IconCircleDashed,
  IconLayoutDistributeHorizontal,
  IconLayoutDistributeVertical,
  IconPencil,
  IconZoomIn,
  IconZoomOut,
  IconMaximize,
} from "./icons";
import cytoscape, { type Core, type ElementDefinition, type NodeSingular } from "cytoscape";
import dagre from "cytoscape-dagre";
import fcose from "cytoscape-fcose";
import type { Graph, GraphEdge, GraphNode, GraphSelection } from "./api";
import { colorForKind, colorForRelationship, genericIcon, iconForKind } from "./kinds";
import { useSettings } from "./settings";

cytoscape.use(dagre);
cytoscape.use(fcose);

// Vertices of a regular heptagon with a single vertex pointing straight up,
// Class applied to synthetic compound "namespace" parent nodes so they can be
// excluded from selection, fading and menus.
const GROUP_CLASS = "namespace-group";
const GROUP_PREFIX = "ns::";
const CLUSTER_GROUP_ID = `${GROUP_PREFIX}__cluster__`;

// phaseColor maps a Pod status (phase, or a refining reason like
// CrashLoopBackOff) to a node border color so pod health is visible at a glance.
// Returns undefined for resources without a status, leaving them unbordered.
function phaseColor(status: unknown): string | undefined {
  if (typeof status !== "string" || status === "") return undefined;
  switch (status) {
    case "Running":
      return "#4caf50"; // healthy
    case "Succeeded":
    case "Completed":
      // Terminal success (e.g. a finished Job pod): not actively "healthy", so
      // show the same neutral gray outline as non-status resources.
      return undefined;
    case "Pending":
    case "ContainerCreating":
    case "PodInitializing":
      return "#f0a020"; // in progress
    case "Failed":
    case "Unknown":
    case "CrashLoopBackOff":
    case "ImagePullBackOff":
    case "ErrImagePull":
    case "Error":
    case "OOMKilled":
      return "#e03131"; // unhealthy
    default:
      return "#f0a020"; // unrecognized, transient state
  }
}

function toElements(graph: Graph, groupByNamespace: boolean): ElementDefinition[] {
  const ids = new Set(graph.nodes.map((n) => n.id));
  const elements: ElementDefinition[] = [];

  // When grouping, synthesize one compound parent node per namespace (plus one
  // for cluster-scoped resources) and parent each resource into it.
  const groups = new Map<string, string>();
  if (groupByNamespace) {
    for (const n of graph.nodes) {
      const gid = n.namespace ? GROUP_PREFIX + n.namespace : CLUSTER_GROUP_ID;
      if (!groups.has(gid)) groups.set(gid, n.namespace ?? "(cluster-scoped)");
    }
    for (const [gid, label] of groups) {
      elements.push({
        data: { id: gid, label },
        classes: GROUP_CLASS,
        selectable: false,
        grabbable: false,
      });
    }
  }

  for (const n of graph.nodes as GraphNode[]) {
    const data: Record<string, unknown> = {
      id: n.id,
      label: `${n.kind}\n${n.name}`,
      kind: n.kind,
      color: colorForKind(n.kind),
    };
    // Border pods (and any status-bearing node) by health. Prefer the refined
    // `status` (e.g. CrashLoopBackOff) over the coarse `phase`.
    const statusColor = phaseColor(n.properties?.status ?? n.properties?.phase);
    if (statusColor) data.statusColor = statusColor;
    // Use the kind's official icon, falling back to the generic Kubernetes
    // badge so resources without a dedicated icon still render as a badge.
    data.icon = iconForKind(n.kind) ?? genericIcon;

    if (groupByNamespace) {
      data.parent = n.namespace ? GROUP_PREFIX + n.namespace : CLUSTER_GROUP_ID;
    }
    elements.push({ data });
  }

  // Drop edges whose endpoints are not present to avoid render errors.
  for (const e of graph.edges) {
    if (!ids.has(e.source) || !ids.has(e.target)) continue;
    elements.push({
      data: {
        id: e.id,
        source: e.source,
        target: e.target,
        label: e.type,
        edgeColor: colorForRelationship(e.type),
        manual: e.manual ? 1 : 0,
      },
    });
  }
  return elements;
}

// LayoutParams are the user-tunable fcose knobs surfaced in the settings modal.
interface LayoutParams {
  repulsion: number;
  edgeLength: number;
  gravity: number;
}

// buildLayout constructs the fcose layout options, injecting the user-tunable
// repulsion / ideal edge length / gravity. The rest of the tuning (separation,
// iterations, compound gravity) is kept fixed per layout mode.
function buildLayout(grouped: boolean, p: LayoutParams): cytoscape.LayoutOptions {
  const common = {
    name: "fcose",
    quality: "proof",
    animate: false,
    randomize: true,
    idealEdgeLength: p.edgeLength,
    nodeRepulsion: p.repulsion,
    gravity: p.gravity,
    nodeDimensionsIncludeLabels: true,
  };
  const opts = grouped
    ? {
        // Grouped by namespace: spread nodes and boxes apart with more
        // iterations to reduce crossing / overlapping links.
        ...common,
        nodeSeparation: 130,
        edgeElasticity: 0.5,
        gravityRange: 3.8,
        gravityCompound: 1.2,
        nestingFactor: 0.1,
        numIter: 4000,
        padding: 20,
      }
    : {
        // Force-directed, packing the many small disconnected trees across the
        // canvas so the 2D space is used.
        ...common,
        packComponents: true,
        nodeSeparation: 75,
        padding: 30,
      };
  return opts as unknown as cytoscape.LayoutOptions;
}

interface Props {
  graph: Graph;
  // Called when the selection changes: a node, an edge, or null (cleared).
  onSelect: (selection: GraphSelection | null) => void;
  // Id of the currently selected node, used as the root for distance fading.
  selectedId: string | null;
  // Max number of hops from the selected node to keep fully visible. null means
  // unlimited (no fading).
  maxDistance: number | null;
  // Called when the user picks "YAML" from a node's right-click menu.
  onShowYaml: (node: GraphNode) => void;
  // Called when the user completes an "Add Link" gesture from one node to
  // another, with the source and target node ids.
  onAddLink: (sourceId: string, targetId: string) => void;
  // Called when the user deletes a user-created link via its context menu.
  onDeleteLink: (edge: GraphEdge) => void;
  // Called when the user picks "Edit" on a user-created link, to edit its note.
  onEditLink: (edge: GraphEdge) => void;
  // Sets the visibility of several nodes at once (used by the context menu's
  // Hide/View item so it applies to the whole current selection). hiddenIds is
  // also used to label the menu item.
  onSetVisibility: (ids: string[], hidden: boolean) => void;
  hiddenIds: Set<string>;
  // Request to toggle a node's selection on the canvas (clicks in the
  // resource list) without centering or opening its details. The nonce makes
  // repeated requests for the same node re-trigger.
  toggleSelect: { id: string; nonce: number } | null;
  // Called whenever the set of selected (real) nodes changes, with their ids.
  // Lets the inspector highlight every selected resource in its list view.
  onSelectedIdsChange?: (ids: string[]) => void;
  // When true, resources are grouped into compound nodes by namespace.
  groupByNamespace: boolean;
  // When false, edge (relationship-type) labels are hidden.
  showEdgeLabels: boolean;
  // Base file name (without extension) used when exporting the graph image.
  exportName?: string;
}

// Context menu anchored at a viewport position for a right-clicked node.
interface EdgeMenu {
  x: number;
  y: number;
  edge: GraphEdge;
}

interface NodeMenu {
  x: number;
  y: number;
  node: GraphNode;
}

export function GraphView({
  graph,
  onSelect,
  selectedId,
  maxDistance,
  onShowYaml,
  onAddLink,
  onDeleteLink,
  onEditLink,
  onSetVisibility,
  hiddenIds,
  toggleSelect,
  onSelectedIdsChange,
  groupByNamespace,
  showEdgeLabels,
  exportName,
}: Props) {
  const containerRef = useRef<HTMLDivElement>(null);
  const cyRef = useRef<Core | null>(null);
  // Right-click context menu state (null = closed).
  const [menu, setMenu] = useState<NodeMenu | null>(null);
  // Right-click context menu for a user-created (manual) edge (null = closed).
  const [edgeMenu, setEdgeMenu] = useState<EdgeMenu | null>(null);
  // Whether a reference grid is overlaid on the display. Persisted across
  // sessions via settings.
  const { settings, update } = useSettings();
  const showGrid = settings.showGrid;
  // Number of (non-group) nodes currently selected; alignment tools appear when
  // two or more are selected.
  const [selectedCount, setSelectedCount] = useState(0);
  // Tracks whether the view is currently zoomed into a distance subgraph, so we
  // can zoom back out when the filter is cleared.
  const fittedSubgraphRef = useRef(false);
  // Id of the node a link is being drawn from (null = not linking). A ref mirror
  // lets the canvas event handlers, registered once, read the live value.
  const [linkingSourceId, setLinkingSourceId] = useState<string | null>(null);
  const linkingRef = useRef<string | null>(null);
  // Set when a press-drag on an edge rotated its target node, so the edge
  // "tap" fired on release is swallowed instead of selecting the edge.
  const edgeRotatedRef = useRef(false);
  // Ref mirror of onSelectedIdsChange so the once-registered cytoscape handler
  // always calls the latest callback without rebuilding the graph.
  const selectedIdsCbRef = useRef(onSelectedIdsChange);
  selectedIdsCbRef.current = onSelectedIdsChange;
  // Ref mirror of the latest graph so the once-registered canvas event handlers
  // (tap, context menu, YAML) read current data without being rebuilt on every
  // poll.
  const graphRef = useRef(graph);
  graphRef.current = graph;
  // User-tunable layout parameters from settings. A ref mirror lets the once-
  // built layout read the latest without adding them as rebuild dependencies.
  const layoutParams = useMemo(
    () => ({
      repulsion: settings.layoutRepulsion,
      edgeLength: settings.layoutEdgeLength,
      gravity: settings.layoutGravity,
    }),
    [settings.layoutRepulsion, settings.layoutEdgeLength, settings.layoutGravity],
  );
  const layoutParamsRef = useRef(layoutParams);
  layoutParamsRef.current = layoutParams;
  const groupByNamespaceRef = useRef(groupByNamespace);
  groupByNamespaceRef.current = groupByNamespace;

  // A signature of the graph's *structure* that warrants a full relayout: which
  // nodes exist and whether they're grouped. Edges are deliberately excluded so
  // that adding/removing a link (or a derived edge appearing on a poll) is
  // reconciled in place — added/removed without moving any nodes — by the
  // data-sync effect below. Only node changes trigger the expensive rebuild.
  const structuralKey = useMemo(() => {
    const nodeIds = graph.nodes.map((n) => n.id).sort().join(",");
    return `${groupByNamespace ? "g" : "f"}|${nodeIds}`;
  }, [graph, groupByNamespace]);

  useEffect(() => {
    if (!containerRef.current) return;
    const cy = cytoscape({
      container: containerRef.current,
      // Cap zoom so fitting a tiny subgraph (or a single node) doesn't zoom in absurdly.
      maxZoom: 2.5,
      // Shift+drag on the background draws a selection box (panning is the
      // unmodified drag). Selecting multiple nodes lets them be moved together.
      boxSelectionEnabled: true,
      selectionType: "single",
      elements: toElements(graphRef.current, groupByNamespace),
      style: [
        {
          selector: "node",
          style: {
            "background-color": "data(color)",
            label: "data(label)",
            "text-wrap": "wrap",
            "text-valign": "bottom",
            "text-margin-y": 4,
            "font-size": 9,
            color: "#d0d0d0",
            width: 28,
            height: 28,
          },
        },
        // Nodes with an official Kubernetes icon render as a circular disc with
        // the icon inset inside it. The disc carries a ring border that conveys
        // state (a neutral default, a health color for status-bearing nodes, or
        // white when selected); insetting the glyph leaves room for that ring to
        // sit cleanly around it rather than cutting across the icon.
        {
          selector: "node[icon]",
          style: {
            shape: "ellipse",
            "background-color": "#252a33",
            "background-opacity": 1,
            "background-image": "data(icon)",
            "background-fit": "none",
            "background-width": "66%",
            "background-height": "66%",
            "background-position-x": "50%",
            "background-position-y": "50%",
            "background-clip": "none",
            "border-width": 1.5,
            "border-color": "#3c4350",
            width: 34,
            height: 34,
          },
        },
        {
          selector: "edge",
          style: {
            width: 1.5,
            // Color each edge (line, arrow and label) by its relationship type.
            "line-color": "data(edgeColor)",
            "target-arrow-color": "data(edgeColor)",
            "target-arrow-shape": "triangle",
            "curve-style": "bezier",
            label: "data(label)",
            "font-size": 8,
            color: "data(edgeColor)",
            // Sit the label on a dark rounded pill so colored text stays legible
            // over the similarly-colored edge line/arrow.
            "text-background-color": "#23272f",
            "text-background-opacity": 0.9,
            "text-background-padding": "2px",
            "text-background-shape": "roundrectangle",
            "text-border-color": "data(edgeColor)",
            "text-border-width": 0.5,
            "text-border-opacity": 0.6,
            "text-rotation": "autorotate",
          },
        },
        // Status-bearing nodes (e.g. Pods) get a health-colored ring border.
        {
          selector: "node[statusColor]",
          style: { "border-width": 3, "border-color": "data(statusColor)", "border-opacity": 1 },
        },
        // Selection takes precedence over the health ring: a bright white ring.
        {
          selector: "node:selected",
          style: { "border-width": 3, "border-color": "#fff", "border-opacity": 1 },
        },
        // Edges whose labels are toggled off hide just the relationship text
        // while keeping the line, arrow and color.
        // Individually-hidden nodes (and, implicitly, their incident edges) are
        // removed from rendering and layout via display:none, which keeps their
        // position so toggling them back on doesn't disturb the layout.
        {
          selector: "node.hidden",
          style: { display: "none" },
        },
        {
          selector: "edge.no-label",
          style: { "text-opacity": 0, "text-background-opacity": 0 },
        },
        // The transient marker showing the pivot point while a rotation drag is
        // in progress: a small warm dot with a white ring. It ignores pointer
        // events so it never interferes with the drag itself.
        {
          selector: ".rotate-pivot",
          style: {
            shape: "ellipse",
            width: 9,
            height: 9,
            "background-color": "#e8785a",
            "background-opacity": 0.95,
            "border-width": 2,
            "border-color": "#fff",
            "border-opacity": 0.9,
            label: "",
            events: "no",
            "z-index": 9999,
          },
        },
        // Highlights the node acting as the pivot while a link's target is
        // being rotated around it.
        {
          selector: ".rotate-pivot-node",
          style: {
            "border-width": 3,
            "border-color": "#e8785a",
            "border-opacity": 1,
          },
        },
        // The transient node tracking the cursor while drawing a new link. It is
        // invisible and ignores pointer events so taps fall through to the real
        // node beneath it.
        {
          selector: ".link-ghost",
          style: { width: 1, height: 1, "background-opacity": 0, "border-width": 0, label: "", events: "no" },
        },
        // The dashed arrow drawn from the source node to the cursor.
        {
          selector: ".link-ghost-edge",
          style: {
            width: 2,
            "line-color": "#16a3b8",
            "line-style": "dashed",
            "target-arrow-color": "#16a3b8",
            "target-arrow-shape": "triangle",
            "curve-style": "straight",
            label: "",
            events: "no",
          },
        },
        // The Shift+drag box-selection overlay: mostly transparent fill with a
        // visible border, so the nodes and edges beneath it stay readable while
        // the selection area is being drawn.
        {
          selector: "core",
          style: {
            "selection-box-color": "#16a3b8",
            "selection-box-opacity": 0.12,
            "selection-box-border-color": "#16a3b8",
            "selection-box-border-width": 1,
            // The typings require every Core style property; only the
            // selection-box ones are being overridden here.
          } as unknown as cytoscape.Css.Core,
        },
        // A selected edge keeps its relationship color; a thin white "outline"
        // edge is drawn just behind it (added by the select/unselect handler)
        // so both the line and the arrowhead get a white stroke. Both are forced
        // straight so the outline overlaps the edge exactly.
        {
          selector: "edge:selected",
          style: { "curve-style": "straight", "z-index": 10 },
        },
        {
          selector: ".edge-outline",
          style: {
            "curve-style": "straight",
            "line-color": "#fff",
            "target-arrow-color": "#fff",
            "target-arrow-shape": "triangle",
            // Slightly wider than the 1.5px edge -> a thin white stroke; the
            // arrowhead scales with width, so it frames the real arrow too.
            width: 3.5,
            label: "",
            events: "no",
            "z-index": 9,
          },
        },
        // Compound parent nodes used to group resources by namespace.
        {
          selector: `.${GROUP_CLASS}`,
          style: {
            shape: "round-rectangle",
            // Let presses fall through to the background so the canvas can be
            // panned / box-selected from over a namespace box (its empty
            // interior would otherwise swallow the drag). Child nodes, drawn
            // on top, still receive their own events.
            events: "no",
            "background-color": "#2c313a",
            "background-opacity": 0.35,
            "border-color": "#444b57",
            "border-width": 1,
            label: "data(label)",
            "text-valign": "top",
            "text-halign": "center",
            "text-margin-y": -6,
            color: "#aab0bb",
            "font-size": 18,
            "font-weight": "bold",
            padding: "14px",
          },
        },
        // Nodes/edges outside the selected node's connection distance are
        // greyed out and faded so they recede into the background.
        {
          selector: "node.faded",
          style: {
            "background-color": "#5a606b",
            opacity: 0.2,
            "text-opacity": 0.1,
          },
        },
        {
          selector: "edge.faded",
          style: { opacity: 0.08 },
        },
      ],
      layout: buildLayout(groupByNamespace, layoutParamsRef.current),
    });

    // Tap counting for a node: cytoscape has no native double/triple click, so
    // we count taps on the same node within a short window. 1 = normal select
    // (details), 2 = also select its direct neighbours, 3 = select its entire
    // connected component (everything transitively linked to it).
    let tapCount = 0;
    let tapNodeId: string | null = null;
    let tapTimer: ReturnType<typeof setTimeout> | null = null;
    const resetTapCount = () => {
      tapCount = 0;
      tapNodeId = null;
    };
    cy.on("tap", "node", (evt) => {
      if (linkingRef.current) return; // handled by the linking effect
      const target = evt.target as NodeSingular;
      if (target.hasClass(GROUP_CLASS)) return;
      setMenu(null);
      setEdgeMenu(null);

      const id = target.id();
      if (id !== tapNodeId) {
        tapNodeId = id;
        tapCount = 0;
      }
      tapCount += 1;
      if (tapTimer) clearTimeout(tapTimer);
      tapTimer = setTimeout(resetTapCount, 350);

      if (tapCount === 1) {
        const found = graphRef.current.nodes.find((n) => n.id === id);
        onSelect(found ? { type: "node", node: found } : null);
        return;
      }

      let ids: string[];
      if (tapCount === 2) {
        // Double click: the node and its directly-connected neighbours.
        ids = (target.closedNeighborhood().nodes().toArray() as NodeSingular[])
          .filter((n) => !n.hasClass(GROUP_CLASS))
          .map((n) => n.id());
      } else {
        // Triple click: the whole connected component (BFS over the undirected
        // neighbourhood, i.e. everything reachable through any links).
        const within = new Set<string>([id]);
        let frontier: string[] = [id];
        while (frontier.length > 0) {
          const next: string[] = [];
          for (const nid of frontier) {
            cy.getElementById(nid)
              .neighborhood()
              .nodes()
              .forEach((nb) => {
                if (nb.hasClass(GROUP_CLASS) || within.has(nb.id())) return;
                within.add(nb.id());
                next.push(nb.id());
              });
          }
          frontier = next;
        }
        ids = [...within];
        resetTapCount();
      }

      // Defer so cytoscape's own tap-selection (applied after this event fires)
      // doesn't clobber the multi-selection we're about to set.
      setTimeout(() => {
        cy.batch(() => {
          cy.elements().unselect();
          ids.forEach((i) => cy.getElementById(i).select());
        });
      }, 0);
    });
    cy.on("tap", "edge", (evt) => {
      if (linkingRef.current) return;
      // A drag that rotated the edge's target node shouldn't also select the
      // edge on release.
      if (edgeRotatedRef.current) {
        edgeRotatedRef.current = false;
        return;
      }
      setMenu(null);
      setEdgeMenu(null);
      const edge = graphRef.current.edges.find((e) => e.id === evt.target.id());
      if (!edge) return;
      const source = graphRef.current.nodes.find((n) => n.id === edge.source);
      const target = graphRef.current.nodes.find((n) => n.id === edge.target);
      onSelect({ type: "edge", edge, source, target });
    });
    cy.on("tap", (evt) => {
      if (linkingRef.current) return; // handled by the linking effect
      if (evt.target === cy) {
        setMenu(null);
        setEdgeMenu(null);
        onSelect(null);
      }
    });

    // Track how many real nodes are selected so the alignment tools can appear
    // for multi-selections (e.g. via Shift box-select).
    const updateSelectedCount = () => {
      const sel = cy.nodes(":selected").filter((n) => !n.hasClass(GROUP_CLASS));
      setSelectedCount(sel.length);
      selectedIdsCbRef.current?.(sel.map((n) => n.id()));
    };
    cy.on("select unselect", "node", updateSelectedCount);

    // Maintain a white "outline" edge behind each selected edge so the
    // selection reads as a thin stroke around the line and arrow (cytoscape's
    // underlay/line-outline don't cover the arrowhead).
    const OUTLINE_CLASS = "edge-outline";
    const syncEdgeOutlines = () => {
      cy.edges(`.${OUTLINE_CLASS}`).remove();
      cy.edges(":selected").forEach((e) => {
        cy.add({
          group: "edges",
          data: {
            id: `__outline__${e.id()}`,
            source: e.source().id(),
            target: e.target().id(),
          },
          classes: OUTLINE_CLASS,
          selectable: false,
        });
      });
    };
    cy.on("select unselect", "edge", syncEdgeOutlines);

    // Right-click a node to open a context menu at the cursor. Clicking the
    // background (or panning/zooming) dismisses it.
    cy.on("cxttap", "node", (evt) => {
      if (linkingRef.current) return;
      const found = graphRef.current.nodes.find((n) => n.id === evt.target.id());
      if (!found) return;
      const oe = evt.originalEvent as MouseEvent;
      setEdgeMenu(null);
      setMenu({ x: oe.clientX, y: oe.clientY, node: found });
    });
    // Right-click a user-created (manual) edge to delete it. Derived edges have
    // no menu.
    cy.on("cxttap", "edge", (evt) => {
      if (linkingRef.current) return;
      const edge = graphRef.current.edges.find((e) => e.id === evt.target.id());
      if (!edge || !edge.manual) return;
      const oe = evt.originalEvent as MouseEvent;
      setMenu(null);
      setEdgeMenu({ x: oe.clientX, y: oe.clientY, edge });
    });
    cy.on("cxttap", (evt) => {
      if (evt.target === cy) {
        setMenu(null);
        setEdgeMenu(null);
      }
    });
    cy.on("viewport", () => {
      setMenu(null);
      setEdgeMenu(null);
    });

    // Cursor feedback: a "grabbing" cursor while dragging the canvas (pan) or a
    // node, and a "pointer" cursor when hovering a selectable node.
    const el = containerRef.current;
    let dragging = false;
    cy.on("mouseover", "node", (evt) => {
      if (evt.target.hasClass(GROUP_CLASS)) return;
      // Disable box-select while hovering a node so Shift+drag moves the node
      // (pulling its connected neighbours along) rather than starting a selection
      // box. Shift+drag on the background still box-selects.
      cy.boxSelectionEnabled(false);
      if (!dragging) el.style.cursor = "pointer";
    });
    cy.on("mouseout", "node", (evt) => {
      if (evt.target.hasClass(GROUP_CLASS)) return;
      cy.boxSelectionEnabled(true);
      if (!dragging) el.style.cursor = "";
    });
    // Edges are clickable (select / inspect / right-click menu), so show a
    // pointer on hover. Outline/ghost edges have events disabled and never fire
    // these. Skip while panning/dragging or aiming a new link (crosshair).
    cy.on("mouseover", "edge", () => {
      if (dragging || linkingRef.current) return;
      el.style.cursor = "pointer";
    });
    cy.on("mouseout", "edge", () => {
      if (dragging || linkingRef.current) return;
      el.style.cursor = "";
    });
    cy.on("grab", "node", () => {
      dragging = true;
      el.style.cursor = "grabbing";
    });
    cy.on("free", "node", (evt) => {
      dragging = false;
      // The cursor ends up over the just-dropped node, which is selectable.
      el.style.cursor = evt.target.hasClass(GROUP_CLASS) ? "" : "pointer";
    });
    cy.on("mousedown", (evt) => {
      const oe = evt.originalEvent as MouseEvent | undefined;
      if (evt.target === cy && !(oe && oe.shiftKey)) {
        // Background press without shift begins a pan (shift+drag box-selects).
        dragging = true;
        el.style.cursor = "grabbing";
      }
    });
    const endDrag = () => {
      if (!dragging) return;
      dragging = false;
      el.style.cursor = "";
    };
    cy.on("mouseup", endDrag);
    // Catch releases that happen outside the canvas during a pan/drag.
    window.addEventListener("mouseup", endDrag);

    // Shift-drag "pull": while Shift is held, dragging a node keeps its link
    // lengths fixed by translating each directly-connected neighbour by the same
    // delta, so the neighbours follow the node instead of the links stretching.
    let shiftDown = false;
    const trackShift = (e: KeyboardEvent) => {
      shiftDown = e.shiftKey;
    };
    window.addEventListener("keydown", trackShift);
    window.addEventListener("keyup", trackShift);

    let dragState:
      | { grabbedId: string; last: { x: number; y: number }; followerIds: string[] }
      | null = null;

    // Ctrl-drag "rotate": with several nodes selected, Ctrl+dragging one of
    // them spins the whole selection around its centroid instead of moving it.
    // Each node keeps its distance from the (fixed) centroid; the rotation
    // angle follows the grabbed node's angle around that point. (Shift+drag is
    // reserved for the "pull" above, which works for any number of nodes.)
    let rotateState:
      | {
          grabbedId: string;
          center: { x: number; y: number };
          startAngle: number;
          nodes: { id: string; dx: number; dy: number }[];
        }
      | null = null;

    // Transient marker showing the point the nodes are rotating around. It is
    // unselectable/ungrabbable and skipped by the data-reconciliation effect
    // (ids starting with "__").
    const PIVOT_ID = "__rotate_pivot__";
    const showPivotMarker = (pos: { x: number; y: number }) => {
      removePivotMarker();
      cy.add({
        group: "nodes",
        data: { id: PIVOT_ID },
        position: { x: pos.x, y: pos.y },
        classes: "rotate-pivot",
        selectable: false,
        grabbable: false,
      });
    };
    const removePivotMarker = () => {
      cy.getElementById(PIVOT_ID).remove();
    };

    // The node the pointer actually pressed to start the gesture. Cytoscape
    // fires "grab" for every node dragged along with a selection, so the grab
    // handler alone can't tell which one is under the cursor; "tapstart" fires
    // only for the pressed node. Note that grab fires *before* tapstart within
    // the same mousedown, so a rotation initialized during grab starts from a
    // stale handle and is re-anchored here to the node actually pressed.
    let pressedNodeId: string | null = null;
    cy.on("tapstart", "node", (evt) => {
      const pressed = evt.target as NodeSingular;
      pressedNodeId = pressed.id();
      if (rotateState && pressed.selected() && !pressed.hasClass(GROUP_CLASS)) {
        const p = pressed.position();
        rotateState.grabbedId = pressed.id();
        rotateState.startAngle = Math.atan2(
          p.y - rotateState.center.y,
          p.x - rotateState.center.x,
        );
      }
    });

    cy.on("grab", "node", (evt) => {
      const grabbed = evt.target as NodeSingular;
      rotateState = null;
      if (grabbed.hasClass(GROUP_CLASS)) {
        dragState = null;
        return;
      }
      // Nodes that move together under cytoscape's own drag: the selection if the
      // grabbed node belongs to it, otherwise just the grabbed node.
      const selected = cy.nodes(":selected").filter((n) => !n.hasClass(GROUP_CLASS));
      const oe = evt.originalEvent as MouseEvent | undefined;
      // Cmd serves as the rotate modifier on macOS, where Ctrl+click is the
      // OS's right-click gesture.
      if ((oe?.ctrlKey || oe?.metaKey) && grabbed.selected() && selected.length > 1) {
        // Rotation mode: capture the selection's centroid, every node's offset
        // from it, and the handle's starting angle around it. The handle is
        // the node the pointer pressed (not whichever companion fired this
        // grab event), so the rotation circle is the one that node sits on.
        const handle =
          pressedNodeId !== null ? cy.getElementById(pressedNodeId) : grabbed;
        const handleNode = handle.nonempty() && handle.selected() ? handle : grabbed;
        const pts = (selected.toArray() as NodeSingular[]).map((n) => ({
          id: n.id(),
          ...n.position(),
        }));
        const cx = pts.reduce((s, p) => s + p.x, 0) / pts.length;
        const cyy = pts.reduce((s, p) => s + p.y, 0) / pts.length;
        const gp = handleNode.position();
        rotateState = {
          grabbedId: handleNode.id(),
          center: { x: cx, y: cyy },
          startAngle: Math.atan2(gp.y - cyy, gp.x - cx),
          nodes: pts.map((p) => ({ id: p.id, dx: p.x - cx, dy: p.y - cyy })),
        };
        showPivotMarker({ x: cx, y: cyy });
        dragState = null;
        return;
      }
      const moving = grabbed.selected() && selected.nonempty() ? selected : grabbed;
      const movingIds = new Set((moving.toArray() as NodeSingular[]).map((n) => n.id()));
      // Followers pulled along on Shift+drag: the directly-connected neighbours
      // of the moving nodes that aren't themselves being dragged.
      const followerIds = [
        ...new Set(
          (moving.neighborhood().nodes().toArray() as NodeSingular[])
            .filter((n) => !n.hasClass(GROUP_CLASS) && !movingIds.has(n.id()))
            .map((n) => n.id()),
        ),
      ];
      dragState = { grabbedId: grabbed.id(), last: { ...grabbed.position() }, followerIds };
    });

    cy.on("drag", "node", (evt) => {
      if (rotateState) {
        // Cytoscape fires "drag" per moving node; act once, off the grabbed node.
        if ((evt.target as NodeSingular).id() !== rotateState.grabbedId) return;
        const gp = cy.getElementById(rotateState.grabbedId).position();
        const { center, startAngle, nodes } = rotateState;
        // Ignore positions too close to the centroid, where the angle is noise.
        if (Math.hypot(gp.x - center.x, gp.y - center.y) < 1) return;
        const delta = Math.atan2(gp.y - center.y, gp.x - center.x) - startAngle;
        const cos = Math.cos(delta);
        const sin = Math.sin(delta);
        // Place every selected node (the grabbed one included, snapping it back
        // onto its own circle) at its original offset rotated by the delta.
        for (const { id, dx, dy } of nodes) {
          cy.getElementById(id).position({
            x: center.x + dx * cos - dy * sin,
            y: center.y + dx * sin + dy * cos,
          });
        }
        return;
      }
      if (!dragState) return;
      // Cytoscape fires "drag" per moving node; act once, off the grabbed node.
      if ((evt.target as NodeSingular).id() !== dragState.grabbedId) return;
      const grabbed = cy.getElementById(dragState.grabbedId);
      const pos = grabbed.position();
      const dx = pos.x - dragState.last.x;
      const dy = pos.y - dragState.last.y;
      // Always track the latest position so toggling Shift mid-drag never causes
      // the followers to jump by an accumulated delta.
      dragState.last = { x: pos.x, y: pos.y };
      if (!shiftDown || (dx === 0 && dy === 0)) return;
      // Translate every follower by the grabbed node's delta, keeping the link
      // between them the same length.
      for (const id of dragState.followerIds) {
        const n = cy.getElementById(id);
        const p = n.position();
        n.position({ x: p.x + dx, y: p.y + dy });
      }
    });

    cy.on("free", "node", () => {
      dragState = null;
      if (rotateState) {
        rotateState = null;
        removePivotMarker();
      }
    });

    // --- Drag a whole namespace by grabbing its name -----------------------
    // The compound "namespace" boxes have events disabled so the canvas can be
    // panned / box-selected from their empty interior. To still let users move
    // an entire namespace, we treat a press on the box's *label* (its name,
    // rendered at the top) as a grab and move every child node, just as if the
    // group had been box-selected and dragged. The compound parent's bounding
    // box — and thus the label — follows its children automatically.

    // Height, in model units, of the grabbable band around a namespace label
    // at the top edge of its box. Generous enough to cover the rendered name.
    const LABEL_BAND = 22;

    // The namespace group whose label band contains a model-space position, if
    // any. The label sits at the top edge (nudged slightly above it), so the
    // band straddles that edge.
    const groupAtLabel = (pos: { x: number; y: number }): NodeSingular | null => {
      let hit: NodeSingular | null = null;
      cy.nodes(`.${GROUP_CLASS}`).forEach((g) => {
        // Box body only (exclude the label) so bb.y1 is the top edge, where the
        // name is drawn.
        const bb = g.boundingBox({ includeLabels: false, includeOverlays: false });
        if (
          pos.x >= bb.x1 &&
          pos.x <= bb.x2 &&
          pos.y >= bb.y1 - LABEL_BAND &&
          pos.y <= bb.y1 + LABEL_BAND
        ) {
          hit = g;
        }
      });
      return hit;
    };

    let groupDrag: { childIds: string[]; last: { x: number; y: number } } | null = null;

    cy.on("tapstart", (evt) => {
      // Group bodies have events:"no", so a press over one reports the core as
      // the target. Only react to a primary, unmodified press over a label.
      if (evt.target !== cy) return;
      const oe = evt.originalEvent as MouseEvent | undefined;
      if (oe && (oe.button !== 0 || oe.shiftKey)) return;
      const g = groupAtLabel(evt.position);
      if (!g) return;
      const children = g.children().filter((n) => !n.hasClass(GROUP_CLASS));
      if (children.empty()) return;
      groupDrag = {
        childIds: (children.toArray() as NodeSingular[]).map((n) => n.id()),
        last: { x: evt.position.x, y: evt.position.y },
      };
      // Hold off panning / box-selecting while we move the namespace.
      cy.userPanningEnabled(false);
      cy.boxSelectionEnabled(false);
      el.style.cursor = "grabbing";
    });

    cy.on("tapdrag", (evt) => {
      if (!groupDrag) return;
      const dx = evt.position.x - groupDrag.last.x;
      const dy = evt.position.y - groupDrag.last.y;
      if (dx === 0 && dy === 0) return;
      groupDrag.last = { x: evt.position.x, y: evt.position.y };
      for (const id of groupDrag.childIds) {
        const n = cy.getElementById(id);
        const p = n.position();
        n.position({ x: p.x + dx, y: p.y + dy });
      }
    });

    const endGroupDrag = () => {
      if (!groupDrag) return;
      groupDrag = null;
      cy.userPanningEnabled(true);
      cy.boxSelectionEnabled(true);
      el.style.cursor = "";
    };
    cy.on("tapend", endGroupDrag);
    window.addEventListener("mouseup", endGroupDrag);

    // Cursor affordance: show a "grab" cursor when hovering a namespace label
    // so it's discoverable as draggable (the box interior stays a pan target).
    cy.on("mousemove", (evt) => {
      if (groupDrag || dragging || evt.target !== cy) return;
      el.style.cursor = groupAtLabel(evt.position) ? "grab" : "";
    });

    // --- Rotate a link's target node around its source ---------------------
    // Press on an edge and drag: the edge's target node orbits the edge's
    // source node at a fixed radius (the current link length), following the
    // pointer's angle. This lets a node be swung a full 360° around the node
    // its link comes from. A plain click (no movement) still selects the edge.
    let edgeRotate:
      | { targetId: string; sourceId: string; radius: number; start: { x: number; y: number }; active: boolean }
      | null = null;

    cy.on("tapstart", "edge", (evt) => {
      if (linkingRef.current) return;
      const oe = evt.originalEvent as MouseEvent | undefined;
      if (oe && (oe.button !== 0 || oe.shiftKey)) return;
      const edge = evt.target;
      const src = edge.source() as NodeSingular;
      const tgt = edge.target() as NodeSingular;
      if (src.hasClass(GROUP_CLASS) || tgt.hasClass(GROUP_CLASS)) return;
      const sp = src.position();
      const tp = tgt.position();
      const radius = Math.hypot(tp.x - sp.x, tp.y - sp.y);
      if (radius === 0) return;
      edgeRotate = {
        targetId: tgt.id(),
        sourceId: src.id(),
        radius,
        start: { x: evt.position.x, y: evt.position.y },
        active: false,
      };
    });

    cy.on("tapdrag", (evt) => {
      if (!edgeRotate) return;
      // Ignore tiny jitters so a plain click still selects the edge rather
      // than nudging the target node.
      if (!edgeRotate.active) {
        const moved = Math.hypot(
          evt.position.x - edgeRotate.start.x,
          evt.position.y - edgeRotate.start.y,
        );
        if (moved < 4) return;
        edgeRotate.active = true;
        cy.userPanningEnabled(false);
        cy.boxSelectionEnabled(false);
        el.style.cursor = "grabbing";
        // Mark the source node as the pivot the target is rotating around.
        cy.getElementById(edgeRotate.sourceId).addClass("rotate-pivot-node");
      }
      const center = cy.getElementById(edgeRotate.sourceId).position();
      const dx = evt.position.x - center.x;
      const dy = evt.position.y - center.y;
      const dist = Math.hypot(dx, dy);
      if (dist === 0) return;
      // Place the target on the circle around the source, at the pointer's angle.
      cy.getElementById(edgeRotate.targetId).position({
        x: center.x + (dx / dist) * edgeRotate.radius,
        y: center.y + (dy / dist) * edgeRotate.radius,
      });
    });

    const endEdgeRotate = () => {
      if (!edgeRotate) return;
      if (edgeRotate.active) {
        cy.getElementById(edgeRotate.sourceId).removeClass("rotate-pivot-node");
        // Swallow the edge "tap" that fires on release after a real rotation.
        edgeRotatedRef.current = true;
        cy.userPanningEnabled(true);
        cy.boxSelectionEnabled(true);
        el.style.cursor = "";
      }
      edgeRotate = null;
    };
    cy.on("tapend", endEdgeRotate);
    window.addEventListener("mouseup", endEdgeRotate);

    // Suppress the browser's native context menu over the canvas.
    const handleContextMenu = (e: MouseEvent) => e.preventDefault();
    containerRef.current.addEventListener("contextmenu", handleContextMenu);

    // Keyboard shortcuts acting on the currently selected node(s). Single keys
    // (no modifiers) are only active while at least one node is selected:
    //   Arrow keys  nudge the selection (hold Shift for a larger step)
    //   Y           opens the YAML manifest modal (single node)
    //   L           starts an "Add Link" gesture (single node)
    //   H           hides the selected node(s) from the graph
    //   C           centers the selection in the view
    //   E           expands the selection to the directly-connected nodes
    //   J           joins the selected nodes: selects the nodes along the
    //               shortest path between each pair, when one exists
    //   A           selects everything connected to the selection, directly
    //               or indirectly (the whole connected component)
    //   Shift+H     hides every node except the selection; with nothing
    //               selected, unhides all nodes
    //   Shift+D     deselects all nodes
    const ARROW_DELTAS: Record<string, [number, number]> = {
      ArrowUp: [0, -1],
      ArrowDown: [0, 1],
      ArrowLeft: [-1, 0],
      ArrowRight: [1, 0],
    };
    const handleKeyDown = (e: KeyboardEvent) => {
      // Don't hijack keys while the user is typing in a form field.
      const target = e.target as HTMLElement | null;
      if (
        target &&
        (target.tagName === "INPUT" ||
          target.tagName === "TEXTAREA" ||
          target.isContentEditable)
      ) {
        return;
      }

      // Arrow keys move the selected node(s) in model space. Plain arrows only
      // (modifiers are reserved for other shortcuts / browser behavior).
      const delta = ARROW_DELTAS[e.key];
      if (delta && !(e.ctrlKey || e.metaKey || e.altKey)) {
        const selected = cy.nodes(":selected").filter((n) => !n.hasClass(GROUP_CLASS));
        if (selected.empty()) return;
        e.preventDefault();
        const step = e.shiftKey ? 50 : 10;
        const [dx, dy] = delta;
        selected.forEach((n) => {
          const p = n.position();
          n.position({ x: p.x + dx * step, y: p.y + dy * step });
        });
        return;
      }

      // Single-key shortcuts (no modifiers), available only while at least one
      // node is selected. Plain keys avoid clashing with reserved browser
      // shortcuts (Ctrl-L, Ctrl-N, Ctrl-Y, ...).
      if (e.ctrlKey || e.metaKey || e.altKey) return;
      const key = e.key.toLowerCase();
      if (
        key !== "y" &&
        key !== "l" &&
        key !== "h" &&
        key !== "c" &&
        key !== "e" &&
        key !== "j" &&
        key !== "a" &&
        key !== "d"
      )
        return;
      const selected = cy.nodes(":selected").filter((n) => !n.hasClass(GROUP_CLASS));

      // Shift+H: focus the view on the selection by hiding every other node,
      // or — with nothing selected — bring every hidden node back.
      if (key === "h" && e.shiftKey) {
        e.preventDefault();
        if (selected.empty()) {
          onSetVisibility(
            graphRef.current.nodes.map((n) => n.id),
            false,
          );
        } else {
          const selIds = new Set(selected.map((n) => n.id()));
          onSetVisibility(
            graphRef.current.nodes.filter((n) => !selIds.has(n.id)).map((n) => n.id),
            true,
          );
        }
        return;
      }
      // Shift+D: clear the selection (and the inspector's detail view).
      if (key === "d" && e.shiftKey) {
        if (selected.empty()) return;
        e.preventDefault();
        cy.elements().unselect();
        onSelect(null);
        return;
      }
      if (e.shiftKey) return;
      // "d" is only meaningful with Shift (handled above).
      if (key === "d") return;
      if (selected.empty()) return;

      if (key === "y") {
        // YAML manifest for the single selected node.
        if (selected.length !== 1) return;
        const found = graphRef.current.nodes.find((n) => n.id === selected.first().id());
        if (!found) return;
        e.preventDefault();
        onShowYaml(found);
      } else if (key === "l") {
        // Start linking from the single selected node, mirroring the "Add Link"
        // context-menu action. No effect with multiple nodes selected, or while
        // a link is already being drawn.
        if (linkingRef.current || selected.length !== 1) return;
        e.preventDefault();
        setLinkingSourceId(selected.first().id());
      } else if (key === "h") {
        // Hide the selected node(s); they stay listed in the resource list so
        // they can be shown again.
        e.preventDefault();
        onSetVisibility(selected.map((n) => n.id()), true);
      } else if (key === "j") {
        // Join: select the nodes along a shortest path (ignoring edge
        // direction) between each pair of selected nodes, so a connecting path
        // is formed where one exists. Pairs with no path are left as-is.
        if (selected.length < 2) return;
        e.preventDefault();
        // Search only the visible, non-group part of the graph.
        const pathNodes = cy
          .nodes()
          .filter((n) => !n.hasClass(GROUP_CLASS) && !n.hasClass("hidden"));
        const eles = pathNodes.union(pathNodes.edgesWith(pathNodes));
        const arr = selected.toArray() as NodeSingular[];
        for (let i = 0; i < arr.length; i++) {
          for (let j = i + 1; j < arr.length; j++) {
            const res = eles.aStar({ root: arr[i], goal: arr[j], directed: false });
            if (res.found) res.path.nodes().select();
          }
        }
      } else if (key === "a") {
        // Select the connected component(s) of the selection: every visible
        // node reachable from a selected node through any chain of links,
        // ignoring edge direction.
        e.preventDefault();
        const reachable = cy
          .nodes()
          .filter((n) => !n.hasClass(GROUP_CLASS) && !n.hasClass("hidden"));
        const eles = reachable.union(reachable.edgesWith(reachable));
        eles.bfs({
          roots: selected,
          directed: false,
          visit: (n) => {
            n.select();
          },
        });
      } else if (key === "e") {
        // Expand the selection by one hop: also select every visible node
        // directly connected to a currently-selected node. Pressing repeatedly
        // keeps growing the selection along connections.
        e.preventDefault();
        selected
          .neighborhood("node")
          .filter((n) => !n.hasClass(GROUP_CLASS) && !n.hasClass("hidden"))
          .select();
      } else {
        // "c": center the selection in the view.
        e.preventDefault();
        cy.animate({ center: { eles: selected }, duration: 200 });
      }
    };
    window.addEventListener("keydown", handleKeyDown);

    const container = containerRef.current;
    cyRef.current = cy;
    // Dev-only handle for debugging / e2e tests; stripped from production builds.
    if (import.meta.env.DEV) (window as unknown as { __cy?: Core }).__cy = cy;
    return () => {
      window.removeEventListener("keydown", handleKeyDown);
      window.removeEventListener("keydown", trackShift);
      window.removeEventListener("keyup", trackShift);
      window.removeEventListener("mouseup", endDrag);
      window.removeEventListener("mouseup", endGroupDrag);
      window.removeEventListener("mouseup", endEdgeRotate);
      container.removeEventListener("contextmenu", handleContextMenu);
      cy.destroy();
      cyRef.current = null;
    };
    // graph is intentionally read via graphRef (not a dep): only structural
    // changes (structuralKey) rebuild the canvas; data updates are reconciled
    // in place by the effect below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [structuralKey, onSelect, onShowYaml, groupByNamespace]);

  // Reconcile the latest data into the existing canvas without a rebuild or
  // relayout. Node data (labels, kind/status colors, icons) is patched in place;
  // node add/remove is handled by the rebuild effect above (structuralKey).
  // Edges, however, are reconciled here — added, removed and patched in place —
  // so creating or deleting a link (custom or derived) never moves any nodes.
  useEffect(() => {
    const cy = cyRef.current;
    if (!cy) return;
    cy.batch(() => {
      for (const n of graph.nodes) {
        const el = cy.getElementById(n.id);
        if (el.empty()) continue;
        el.data("label", `${n.kind}\n${n.name}`);
        el.data("color", colorForKind(n.kind));
        el.data("icon", iconForKind(n.kind) ?? genericIcon);
        const statusColor = phaseColor(n.properties?.status ?? n.properties?.phase);
        if (statusColor) el.data("statusColor", statusColor);
        else el.removeData("statusColor");
      }

      // Desired edges: those with an id whose endpoints are present as nodes.
      const nodeIds = new Set(graph.nodes.map((n) => n.id));
      const wantEdges = new Map<string, GraphEdge>();
      for (const e of graph.edges) {
        if (e.id && nodeIds.has(e.source) && nodeIds.has(e.target)) wantEdges.set(e.id, e);
      }
      // Remove real edges that are no longer present, along with any orphaned
      // selection-outline edge (id "__outline__<edgeId>") whose edge is gone —
      // otherwise deleting a selected link leaves its highlight behind.
      cy.edges().forEach((el) => {
        if (el.hasClass("edge-outline")) {
          const realId = el.id().slice("__outline__".length);
          if (!wantEdges.has(realId)) el.remove();
          return;
        }
        if (el.id().startsWith("__")) return; // transient link-ghost edge
        if (!wantEdges.has(el.id())) el.remove();
      });
      // Add new edges; patch data on existing ones.
      for (const [id, e] of wantEdges) {
        const el = cy.getElementById(id);
        const data = {
          id,
          source: e.source,
          target: e.target,
          label: e.type,
          edgeColor: colorForRelationship(e.type),
          manual: e.manual ? 1 : 0,
        };
        if (el.empty()) cy.add({ group: "edges", data });
        else {
          el.data("label", data.label);
          el.data("edgeColor", data.edgeColor);
          el.data("manual", data.manual);
        }
      }
    });
  }, [graph]);

  // Re-run the layout when the user changes a layout parameter in settings. The
  // initial layout runs at canvas creation, so skip the first invocation.
  const firstLayoutRef = useRef(true);
  useEffect(() => {
    if (firstLayoutRef.current) {
      firstLayoutRef.current = false;
      return;
    }
    const cy = cyRef.current;
    if (!cy) return;
    cy.layout(buildLayout(groupByNamespaceRef.current, layoutParams)).run();
  }, [layoutParams]);

  // Fade nodes/edges that are more than `maxDistance` hops from the selected
  // node. Runs without rebuilding the graph so selecting/adjusting stays cheap.
  useEffect(() => {
    const cy = cyRef.current;
    if (!cy) return;
    cy.elements().removeClass("faded");

    // No fading when nothing is selected or distance is unlimited. If we were
    // previously zoomed into a subgraph, zoom back out to the whole graph.
    if (selectedId === null || maxDistance === null) {
      if (fittedSubgraphRef.current) {
        fittedSubgraphRef.current = false;
        cy.animate({ fit: { eles: cy.elements(), padding: 30 }, duration: 250 });
      }
      return;
    }
    const root = cy.getElementById(selectedId);
    if (root.empty()) return;

    // Breadth-first traversal up to maxDistance hops (treating edges as
    // undirected, i.e. connected "directly or indirectly").
    const within = new Set<string>([selectedId]);
    let frontier: string[] = [selectedId];
    for (let d = 0; d < maxDistance && frontier.length > 0; d++) {
      const next: string[] = [];
      for (const id of frontier) {
        cy.getElementById(id)
          .neighborhood()
          .nodes()
          .forEach((nb) => {
            const nid = nb.id();
            if (!within.has(nid)) {
              within.add(nid);
              next.push(nid);
            }
          });
      }
      frontier = next;
    }

    cy.nodes().forEach((n) => {
      if (n.hasClass(GROUP_CLASS)) return;
      if (!within.has(n.id())) n.addClass("faded");
    });
    cy.edges().forEach((e) => {
      if (!within.has(e.source().id()) || !within.has(e.target().id())) {
        e.addClass("faded");
      }
    });

    // Zoom to fit the in-range subgraph so it fills the view.
    const withinNodes = cy.nodes().filter((n) => within.has(n.id()));
    cy.animate({ fit: { eles: withinNodes, padding: 50 }, duration: 250 });
    fittedSubgraphRef.current = true;
  }, [graph, selectedId, maxDistance]);

  // Toggle edge labels without rebuilding the graph. Re-runs after a rebuild
  // (graph dep) so the current preference is reapplied to fresh elements.
  useEffect(() => {
    const cy = cyRef.current;
    if (!cy) return;
    cy.edges().toggleClass("no-label", !showEdgeLabels);
  }, [graph, showEdgeLabels]);

  // Apply per-node visibility without rebuilding: hidden nodes get display:none
  // (which also hides their edges) rather than being removed, so unaffected
  // nodes keep their positions. Re-runs after a rebuild (graph dep).
  useEffect(() => {
    const cy = cyRef.current;
    if (!cy) return;
    cy.batch(() => {
      cy.nodes().forEach((n) => {
        if (n.hasClass(GROUP_CLASS)) return;
        n.toggleClass("hidden", hiddenIds.has(n.id()));
      });
    });
  }, [graph, hiddenIds]);

  // "Add Link" gesture: while linkingSourceId is set, draw a dashed arrow from
  // the source node to the cursor. Tapping another node creates the link;
  // tapping empty space (or pressing Escape) cancels without changes.
  useEffect(() => {
    const cy = cyRef.current;
    const el = containerRef.current;
    if (!cy || !el || !linkingSourceId) {
      linkingRef.current = null;
      return;
    }
    const source = cy.getElementById(linkingSourceId);
    if (source.empty()) {
      setLinkingSourceId(null);
      return;
    }
    linkingRef.current = linkingSourceId;
    el.style.cursor = "crosshair";
    // Don't box-select or pan while aiming the link.
    cy.boxSelectionEnabled(false);

    const GHOST = "__link_ghost__";
    const GHOST_EDGE = "__link_ghost_edge__";
    cy.add({
      group: "nodes",
      data: { id: GHOST },
      position: { ...source.position() },
      classes: "link-ghost",
      selectable: false,
      grabbable: false,
    });
    cy.add({
      group: "edges",
      data: { id: GHOST_EDGE, source: linkingSourceId, target: GHOST },
      classes: "link-ghost-edge",
    });

    const onMove = (evt: cytoscape.EventObject) => {
      const g = cy.getElementById(GHOST);
      if (g.nonempty()) g.position(evt.position);
    };
    cy.on("mousemove", onMove);

    const finish = (targetId: string | null) => {
      if (targetId && targetId !== linkingSourceId) onAddLink(linkingSourceId, targetId);
      setLinkingSourceId(null);
    };
    const onTapNode = (evt: cytoscape.EventObject) => {
      const t = evt.target as NodeSingular;
      if (t.hasClass(GROUP_CLASS) || t.id() === GHOST) return;
      finish(t.id());
    };
    const onTapBg = (evt: cytoscape.EventObject) => {
      if (evt.target === cy) finish(null);
    };
    cy.on("tap", "node", onTapNode);
    cy.on("tap", onTapBg);

    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setLinkingSourceId(null);
    };
    window.addEventListener("keydown", onKey);

    return () => {
      cy.off("mousemove", onMove);
      cy.off("tap", "node", onTapNode);
      cy.off("tap", onTapBg);
      window.removeEventListener("keydown", onKey);
      cy.getElementById(GHOST_EDGE).remove();
      cy.getElementById(GHOST).remove();
      cy.boxSelectionEnabled(true);
      el.style.cursor = "";
      linkingRef.current = null;
    };
  }, [linkingSourceId, onAddLink]);

  // Reflect an externally-driven selection (e.g. clicking a resource in the
  // inspector list) onto the canvas by selecting the node. A node picked by
  // tapping the canvas is already :selected by the time this runs, so taps are
  // ignored here.
  useEffect(() => {
    const cy = cyRef.current;
    if (!cy || !selectedId) return;
    const node = cy.getElementById(selectedId);
    if (node.empty() || node.selected()) return;
    cy.elements().unselect();
    node.select();
  }, [selectedId]);

  // Selection toggling from the resource list: Ctrl/Cmd-click adds or removes
  // the node from the canvas selection without clearing others, centering, or
  // opening its details — so several nodes can be gathered into (or dropped
  // from) a multi-selection. A plain click on an already-selected resource
  // also arrives here to unselect it.
  useEffect(() => {
    const cy = cyRef.current;
    if (!cy || !toggleSelect) return;
    const node = cy.getElementById(toggleSelect.id);
    if (node.empty()) return;
    if (node.selected()) node.unselect();
    else node.select();
  }, [toggleSelect]);

  // Align the currently selected nodes onto a common axis: "horizontal" puts
  // them in a row (shared Y = their average), "vertical" in a column (shared X).
  const alignSelected = (axis: "horizontal" | "vertical") => {
    const cy = cyRef.current;
    if (!cy) return;
    const nodes = cy
      .nodes(":selected")
      .filter((n) => !n.hasClass(GROUP_CLASS))
      .toArray() as NodeSingular[];
    if (nodes.length < 2) return;
    if (axis === "horizontal") {
      const y = nodes.reduce((s, n) => s + n.position().y, 0) / nodes.length;
      nodes.forEach((n) => {
        n.position({ x: n.position().x, y });
      });
    } else {
      const x = nodes.reduce((s, n) => s + n.position().x, 0) / nodes.length;
      nodes.forEach((n) => {
        n.position({ x, y: n.position().y });
      });
    }
  };

  // Distribute the selected nodes so their centers are evenly spaced along an
  // axis, keeping the two extremes fixed. Needs at least three nodes.
  const distributeSelected = (axis: "horizontal" | "vertical") => {
    const cy = cyRef.current;
    if (!cy) return;
    const nodes = cy
      .nodes(":selected")
      .filter((n) => !n.hasClass(GROUP_CLASS))
      .toArray() as NodeSingular[];
    if (nodes.length < 3) return;
    const key = axis === "horizontal" ? "x" : "y";
    const sorted = [...nodes].sort((a, b) => a.position()[key] - b.position()[key]);
    const first = sorted[0].position()[key];
    const last = sorted[sorted.length - 1].position()[key];
    const step = (last - first) / (sorted.length - 1);
    sorted.forEach((n, i) => {
      const p = n.position();
      const v = first + i * step;
      n.position(axis === "horizontal" ? { x: v, y: p.y } : { x: p.x, y: v });
    });
  };

  // Arrange the selected nodes in a circle: each node sits at the same distance
  // from the selection's center of mass, evenly spaced by angle. Nodes keep
  // their current angular order around the center so the arrangement reads as a
  // gentle "rounding out" rather than a shuffle. Needs at least three nodes.
  const arrangeSelectedInCircle = () => {
    const cy = cyRef.current;
    if (!cy) return;
    const nodes = cy
      .nodes(":selected")
      .filter((n) => !n.hasClass(GROUP_CLASS))
      .toArray() as NodeSingular[];
    if (nodes.length < 3) return;
    const cx = nodes.reduce((s, n) => s + n.position().x, 0) / nodes.length;
    const cy0 = nodes.reduce((s, n) => s + n.position().y, 0) / nodes.length;
    // Radius: the average distance from the center, but never so small that
    // neighbouring nodes on the circle would overlap (~60 units apart).
    const avg =
      nodes.reduce((s, n) => s + Math.hypot(n.position().x - cx, n.position().y - cy0), 0) /
      nodes.length;
    const radius = Math.max(avg, (60 * nodes.length) / (2 * Math.PI));
    const sorted = [...nodes].sort(
      (a, b) =>
        Math.atan2(a.position().y - cy0, a.position().x - cx) -
        Math.atan2(b.position().y - cy0, b.position().x - cx),
    );
    const step = (2 * Math.PI) / sorted.length;
    // Anchor the spacing at the first node's current angle so the circle forms
    // around where the nodes already are.
    const start = Math.atan2(sorted[0].position().y - cy0, sorted[0].position().x - cx);
    sorted.forEach((n, i) => {
      const a = start + i * step;
      n.position({ x: cx + radius * Math.cos(a), y: cy0 + radius * Math.sin(a) });
    });
  };

  // Zoom in or out by a fixed factor, anchored on the center of the viewport so
  // the graph grows / shrinks in place rather than drifting.
  const zoomBy = (factor: number) => {
    const cy = cyRef.current;
    if (!cy) return;
    const center = { x: cy.width() / 2, y: cy.height() / 2 };
    cy.zoom({ level: cy.zoom() * factor, renderedPosition: center });
  };

  // Reset the view to fit every (visible) node within the display area, undoing
  // any manual pan/zoom or distance-filter framing.
  const fitView = () => {
    const cy = cyRef.current;
    if (!cy) return;
    fittedSubgraphRef.current = false;
    cy.animate({ fit: { eles: cy.elements(), padding: 30 }, duration: 250 });
  };

  // Export the current graph as a PNG and trigger a download. Uses Cytoscape's
  // built-in raster export (full graph, 2x scale, on the app background).
  const exportPng = () => {
    const cy = cyRef.current;
    if (!cy) return;
    const blob = cy.png({ output: "blob", full: true, scale: 2, bg: "#1a1d23" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `${exportName || "astron-graph"}.png`;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  };

  return (
    <>
      <div ref={containerRef} className="graph-canvas" />
      {showGrid && <div className="graph-grid-overlay" aria-hidden />}
      {linkingSourceId && (
        <div className="link-hint">
          Click a target node to link it · Esc to cancel
        </div>
      )}
      <Group gap={6} className="graph-controls">
        {selectedCount >= 2 && (
          <>
            <Tooltip label="Align horizontally (same row)" position="bottom" withArrow>
              <ActionIcon
                variant="default"
                size="lg"
                aria-label="Align selected nodes horizontally"
                onClick={() => alignSelected("horizontal")}
              >
                <IconLayoutAlignMiddle size={18} stroke={1.5} />
              </ActionIcon>
            </Tooltip>
            <Tooltip label="Align vertically (same column)" position="bottom" withArrow>
              <ActionIcon
                variant="default"
                size="lg"
                aria-label="Align selected nodes vertically"
                onClick={() => alignSelected("vertical")}
              >
                <IconLayoutAlignCenter size={18} stroke={1.5} />
              </ActionIcon>
            </Tooltip>
            {selectedCount >= 3 && (
              <>
                <Tooltip label="Distribute horizontally (even spacing)" position="bottom" withArrow>
                  <ActionIcon
                    variant="default"
                    size="lg"
                    aria-label="Distribute selected nodes horizontally"
                    onClick={() => distributeSelected("horizontal")}
                  >
                    <IconLayoutDistributeVertical size={18} stroke={1.5} />
                  </ActionIcon>
                </Tooltip>
                <Tooltip label="Distribute vertically (even spacing)" position="bottom" withArrow>
                  <ActionIcon
                    variant="default"
                    size="lg"
                    aria-label="Distribute selected nodes vertically"
                    onClick={() => distributeSelected("vertical")}
                  >
                    <IconLayoutDistributeHorizontal size={18} stroke={1.5} />
                  </ActionIcon>
                </Tooltip>
                <Tooltip label="Arrange in a circle" position="bottom" withArrow>
                  <ActionIcon
                    variant="default"
                    size="lg"
                    aria-label="Arrange selected nodes in a circle"
                    onClick={arrangeSelectedInCircle}
                  >
                    <IconCircleDashed size={18} stroke={1.5} />
                  </ActionIcon>
                </Tooltip>
              </>
            )}
            <Divider orientation="vertical" />
          </>
        )}
        <Tooltip label="Zoom in" position="bottom" withArrow>
          <ActionIcon
            variant="default"
            size="lg"
            aria-label="Zoom in"
            onClick={() => zoomBy(1.2)}
          >
            <IconZoomIn size={18} stroke={1.5} />
          </ActionIcon>
        </Tooltip>
        <Tooltip label="Zoom out" position="bottom" withArrow>
          <ActionIcon
            variant="default"
            size="lg"
            aria-label="Zoom out"
            onClick={() => zoomBy(1 / 1.2)}
          >
            <IconZoomOut size={18} stroke={1.5} />
          </ActionIcon>
        </Tooltip>
        <Tooltip label="Fit all nodes to view" position="bottom" withArrow>
          <ActionIcon
            variant="default"
            size="lg"
            aria-label="Fit all nodes to view"
            onClick={fitView}
          >
            <IconMaximize size={18} stroke={1.5} />
          </ActionIcon>
        </Tooltip>
        <Divider orientation="vertical" />
        <Tooltip label="Export as PNG" position="bottom" withArrow>
          <ActionIcon
            variant="default"
            size="lg"
            aria-label="Export graph as PNG"
            onClick={exportPng}
          >
            <IconDownload size={18} stroke={1.5} />
          </ActionIcon>
        </Tooltip>
        <Tooltip label={showGrid ? "Hide grid" : "Show grid"} position="bottom" withArrow>
          <ActionIcon
            variant={showGrid ? "filled" : "default"}
            size="lg"
            aria-label="Toggle grid overlay"
            aria-pressed={showGrid}
            onClick={() => update({ showGrid: !showGrid })}
          >
            <IconGrid3x3 size={18} stroke={1.5} />
          </ActionIcon>
        </Tooltip>
      </Group>
      {menu && (
        <Menu
          opened
          onClose={() => setMenu(null)}
          position="bottom-start"
          shadow="md"
          width={160}
          withinPortal
        >
          <Menu.Target>
            <div
              style={{ position: "fixed", left: menu.x, top: menu.y, width: 1, height: 1 }}
            />
          </Menu.Target>
          <Menu.Dropdown>
            <Menu.Label>
              <Text size="xs" truncate>
                {menu.node.kind}/{menu.node.name}
              </Text>
            </Menu.Label>
            <Menu.Item
              leftSection={<IconCode size={16} stroke={1.5} />}
              onClick={() => {
                onShowYaml(menu.node);
                setMenu(null);
              }}
            >
              YAML
            </Menu.Item>
            <Menu.Item
              leftSection={<IconLink size={16} stroke={1.5} />}
              rightSection={
                <Text size="xs" c="dimmed">
                  L
                </Text>
              }
              onClick={() => {
                setLinkingSourceId(menu.node.id);
                setMenu(null);
              }}
            >
              Add Link
            </Menu.Item>
            <Menu.Item
              leftSection={
                hiddenIds.has(menu.node.id) ? (
                  <IconEye size={16} stroke={1.5} />
                ) : (
                  <IconEyeOff size={16} stroke={1.5} />
                )
              }
              onClick={() => {
                // Direction of the action follows the clicked node's current
                // state. When the clicked node is part of a multi-selection,
                // apply it to every selected node; otherwise just this node.
                const targetHidden = !hiddenIds.has(menu.node.id);
                let ids = [menu.node.id];
                const cy = cyRef.current;
                if (cy) {
                  const selIds = cy
                    .nodes(":selected")
                    .filter((n) => !n.isParent())
                    .map((n) => n.id());
                  if (selIds.length > 0 && selIds.includes(menu.node.id)) {
                    ids = selIds;
                  }
                }
                onSetVisibility(ids, targetHidden);
                setMenu(null);
              }}
            >
              {hiddenIds.has(menu.node.id) ? "View" : "Hide"}
            </Menu.Item>
          </Menu.Dropdown>
        </Menu>
      )}
      {edgeMenu && (
        <Menu
          opened
          onClose={() => setEdgeMenu(null)}
          position="bottom-start"
          shadow="md"
          width={160}
          withinPortal
        >
          <Menu.Target>
            <div
              style={{ position: "fixed", left: edgeMenu.x, top: edgeMenu.y, width: 1, height: 1 }}
            />
          </Menu.Target>
          <Menu.Dropdown>
            <Menu.Label>
              <Text size="xs" truncate>
                {edgeMenu.edge.type} link
              </Text>
            </Menu.Label>
            <Menu.Item
              leftSection={<IconPencil size={16} stroke={1.5} />}
              onClick={() => {
                onEditLink(edgeMenu.edge);
                setEdgeMenu(null);
              }}
            >
              Edit
            </Menu.Item>
            <Menu.Item
              color="red"
              leftSection={<IconTrash size={16} stroke={1.5} />}
              onClick={() => {
                onDeleteLink(edgeMenu.edge);
                setEdgeMenu(null);
              }}
            >
              Delete
            </Menu.Item>
          </Menu.Dropdown>
        </Menu>
      )}
    </>
  );
}
