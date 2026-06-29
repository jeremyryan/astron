import { useEffect, useRef, useState } from "react";
import { ActionIcon, Divider, Group, Menu, Text, Tooltip } from "@mantine/core";
import {
  IconCode,
  IconDownload,
  IconGrid3x3,
  IconLink,
  IconTrash,
  IconLayoutAlignCenter,
  IconLayoutAlignMiddle,
  IconLayoutDistributeHorizontal,
  IconLayoutDistributeVertical,
  IconPencil,
  IconZoomIn,
  IconZoomOut,
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
    case "Succeeded":
    case "Completed":
      return "#4caf50"; // healthy / done
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
  // Ref mirror of onSelectedIdsChange so the once-registered cytoscape handler
  // always calls the latest callback without rebuilding the graph.
  const selectedIdsCbRef = useRef(onSelectedIdsChange);
  selectedIdsCbRef.current = onSelectedIdsChange;

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
      elements: toElements(graph, groupByNamespace),
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
        {
          selector: "edge.no-label",
          style: { "text-opacity": 0, "text-background-opacity": 0 },
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
      layout: groupByNamespace
        ? ({
            name: "fcose",
            quality: "proof",
            animate: false,
            nodeSeparation: 75,
            padding: 20,
            nodeDimensionsIncludeLabels: true,
          } as unknown as cytoscape.LayoutOptions)
        : // Force-directed rather than a strict dagre hierarchy: a cluster graph
          // is mostly many small, disconnected source -> target trees, which
          // dagre packs into a couple of cramped rows. fcose spreads them
          // organically across the canvas and packs the disconnected components
          // so the available 2D space is used.
          ({
            name: "fcose",
            quality: "proof",
            animate: false,
            packComponents: true,
            nodeSeparation: 75,
            idealEdgeLength: 80,
            padding: 30,
            nodeDimensionsIncludeLabels: true,
          } as unknown as cytoscape.LayoutOptions),
    });

    cy.on("tap", "node", (evt) => {
      if (linkingRef.current) return; // handled by the linking effect
      if (evt.target.hasClass(GROUP_CLASS)) return;
      setMenu(null);
      setEdgeMenu(null);
      const found = graph.nodes.find((n) => n.id === evt.target.id());
      onSelect(found ? { type: "node", node: found } : null);
    });
    cy.on("tap", "edge", (evt) => {
      if (linkingRef.current) return;
      setMenu(null);
      setEdgeMenu(null);
      const edge = graph.edges.find((e) => e.id === evt.target.id());
      if (!edge) return;
      const source = graph.nodes.find((n) => n.id === edge.source);
      const target = graph.nodes.find((n) => n.id === edge.target);
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
      const found = graph.nodes.find((n) => n.id === evt.target.id());
      if (!found) return;
      const oe = evt.originalEvent as MouseEvent;
      setEdgeMenu(null);
      setMenu({ x: oe.clientX, y: oe.clientY, node: found });
    });
    // Right-click a user-created (manual) edge to delete it. Derived edges have
    // no menu.
    cy.on("cxttap", "edge", (evt) => {
      if (linkingRef.current) return;
      const edge = graph.edges.find((e) => e.id === evt.target.id());
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
      // Disable box-select while hovering a node so Shift+drag moves (and
      // 45°-constrains) the node rather than starting a selection box. Shift+drag
      // on the background still box-selects.
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

    // Shift-constrained dragging: while Shift is held, the drag is locked to the
    // nearest 45° axis (horizontal, vertical or diagonal) measured from each
    // node's position when the drag began. The grabbed node's constrained delta
    // is applied to every node moving with it so the group stays rigid.
    let shiftDown = false;
    const trackShift = (e: KeyboardEvent) => {
      shiftDown = e.shiftKey;
    };
    window.addEventListener("keydown", trackShift);
    window.addEventListener("keyup", trackShift);

    let dragState:
      | { grabbedId: string; start: { x: number; y: number }; ids: string[] }
      | null = null;

    cy.on("grab", "node", (evt) => {
      const grabbed = evt.target as NodeSingular;
      if (grabbed.hasClass(GROUP_CLASS)) {
        dragState = null;
        return;
      }
      // Nodes that move together: the selection if the grabbed node belongs to
      // it, otherwise just the grabbed node.
      const selected = cy.nodes(":selected").filter((n) => !n.hasClass(GROUP_CLASS));
      const moving = grabbed.selected() && selected.nonempty() ? selected : grabbed;
      const ids = (moving.toArray() as NodeSingular[]).map((n) => n.id());
      dragState = { grabbedId: grabbed.id(), start: { ...grabbed.position() }, ids };
    });

    cy.on("drag", "node", (evt) => {
      if (!shiftDown || !dragState) return;
      // Cytoscape fires "drag" per moving node; act once, off the grabbed node.
      if ((evt.target as NodeSingular).id() !== dragState.grabbedId) return;
      const grabbed = cy.getElementById(dragState.grabbedId);
      const free = grabbed.position();
      const dx = free.x - dragState.start.x;
      const dy = free.y - dragState.start.y;
      if (dx === 0 && dy === 0) return;
      // Lock to the nearest 45° axis and project the drag onto it.
      const STEP = Math.PI / 4;
      const axis = Math.round(Math.atan2(dy, dx) / STEP) * STEP;
      const proj = dx * Math.cos(axis) + dy * Math.sin(axis);
      const cx = dragState.start.x + proj * Math.cos(axis);
      const cy2 = dragState.start.y + proj * Math.sin(axis);
      const corrX = cx - free.x;
      const corrY = cy2 - free.y;
      if (corrX === 0 && corrY === 0) return;
      // Same correction for every moving node keeps the group rigid.
      for (const id of dragState.ids) {
        const n = cy.getElementById(id);
        const p = n.position();
        n.position({ x: p.x + corrX, y: p.y + corrY });
      }
    });

    cy.on("free", "node", () => {
      dragState = null;
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

    // Suppress the browser's native context menu over the canvas.
    const handleContextMenu = (e: MouseEvent) => e.preventDefault();
    containerRef.current.addEventListener("contextmenu", handleContextMenu);

    // Keyboard shortcuts acting on the currently selected node(s):
    //   Arrow keys  nudge the selection (hold Shift for a larger step)
    //   L           starts an "Add Link" gesture from the single selected node
    //   Ctrl/Cmd-C  centers it in the view
    //   Ctrl/Cmd-Y  opens its YAML manifest modal
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

      // L (no modifiers): start linking from the single selected node, mirroring
      // the "Add Link" context-menu action. A plain key avoids clashing with
      // browser shortcuts (Ctrl-N/Ctrl-L are reserved). No effect with zero or
      // multiple nodes selected, or while a link is already being drawn.
      if (e.key.toLowerCase() === "l" && !(e.ctrlKey || e.metaKey || e.altKey)) {
        if (linkingRef.current) return;
        const selectedNodes = cy.nodes(":selected").filter((n) => !n.hasClass(GROUP_CLASS));
        if (selectedNodes.length !== 1) return;
        e.preventDefault();
        setLinkingSourceId(selectedNodes.first().id());
        return;
      }

      if (!(e.ctrlKey || e.metaKey)) return;
      const key = e.key.toLowerCase();
      if (key !== "c" && key !== "y") return;
      const selected = cy.nodes(":selected");
      if (selected.empty()) return;
      e.preventDefault();
      if (key === "c") {
        cy.animate({ center: { eles: selected }, duration: 200 });
      } else {
        const found = graph.nodes.find((n) => n.id === selected.first().id());
        if (found) onShowYaml(found);
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
      container.removeEventListener("contextmenu", handleContextMenu);
      cy.destroy();
      cyRef.current = null;
    };
  }, [graph, onSelect, onShowYaml, groupByNamespace]);

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
  // inspector list) onto the canvas: select and center the node. A node picked
  // by tapping the canvas is already :selected by the time this runs, so taps
  // are ignored here and never trigger an unwanted recenter.
  useEffect(() => {
    const cy = cyRef.current;
    if (!cy || !selectedId) return;
    const node = cy.getElementById(selectedId);
    if (node.empty() || node.selected()) return;
    cy.elements().unselect();
    node.select();
    // When a distance filter is active the fading effect already refits the
    // view, so only center here otherwise.
    if (maxDistance === null) {
      cy.animate({ center: { eles: node }, duration: 250 });
    }
  }, [selectedId, maxDistance]);

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

  // Zoom in or out by a fixed factor, anchored on the center of the viewport so
  // the graph grows / shrinks in place rather than drifting.
  const zoomBy = (factor: number) => {
    const cy = cyRef.current;
    if (!cy) return;
    const center = { x: cy.width() / 2, y: cy.height() / 2 };
    cy.zoom({ level: cy.zoom() * factor, renderedPosition: center });
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
    a.download = `${exportName || "gamera-graph"}.png`;
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
            {/* Edit is not implemented yet. */}
            <Menu.Item leftSection={<IconPencil size={16} stroke={1.5} />}>Edit</Menu.Item>
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
