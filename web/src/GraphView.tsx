import { useEffect, useRef, useState } from "react";
import { Menu, Text } from "@mantine/core";
import { IconCode, IconPencil } from "./icons";
import cytoscape, { type Core, type ElementDefinition } from "cytoscape";
import dagre from "cytoscape-dagre";
import fcose from "cytoscape-fcose";
import type { Graph, GraphNode } from "./api";
import { colorForKind } from "./kinds";

cytoscape.use(dagre);
cytoscape.use(fcose);

// Class applied to synthetic compound "namespace" parent nodes so they can be
// excluded from selection, fading and menus.
const GROUP_CLASS = "namespace-group";
const GROUP_PREFIX = "ns::";
const CLUSTER_GROUP_ID = `${GROUP_PREFIX}__cluster__`;

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
    if (groupByNamespace) {
      data.parent = n.namespace ? GROUP_PREFIX + n.namespace : CLUSTER_GROUP_ID;
    }
    elements.push({ data });
  }

  // Drop edges whose endpoints are not present to avoid render errors.
  for (const e of graph.edges) {
    if (!ids.has(e.source) || !ids.has(e.target)) continue;
    elements.push({ data: { id: e.id, source: e.source, target: e.target, label: e.type } });
  }
  return elements;
}

interface Props {
  graph: Graph;
  onSelect: (node: GraphNode | null) => void;
  // Id of the currently selected node, used as the root for distance fading.
  selectedId: string | null;
  // Max number of hops from the selected node to keep fully visible. null means
  // unlimited (no fading).
  maxDistance: number | null;
  // Called when the user picks "YAML" from a node's right-click menu.
  onShowYaml: (node: GraphNode) => void;
  // When true, resources are grouped into compound nodes by namespace.
  groupByNamespace: boolean;
}

// Context menu anchored at a viewport position for a right-clicked node.
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
  groupByNamespace,
}: Props) {
  const containerRef = useRef<HTMLDivElement>(null);
  const cyRef = useRef<Core | null>(null);
  // Right-click context menu state (null = closed).
  const [menu, setMenu] = useState<NodeMenu | null>(null);
  // Tracks whether the view is currently zoomed into a distance subgraph, so we
  // can zoom back out when the filter is cleared.
  const fittedSubgraphRef = useRef(false);

  useEffect(() => {
    if (!containerRef.current) return;
    const cy = cytoscape({
      container: containerRef.current,
      // Cap zoom so fitting a tiny subgraph (or a single node) doesn't zoom in absurdly.
      maxZoom: 2.5,
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
            width: 26,
            height: 26,
          },
        },
        {
          selector: "edge",
          style: {
            width: 1.5,
            "line-color": "#555",
            "target-arrow-color": "#555",
            "target-arrow-shape": "triangle",
            "curve-style": "bezier",
            label: "data(label)",
            "font-size": 7,
            color: "#888",
            "text-rotation": "autorotate",
          },
        },
        {
          selector: "node:selected",
          style: { "border-width": 3, "border-color": "#fff" },
        },
        // Compound parent nodes used to group resources by namespace.
        {
          selector: `.${GROUP_CLASS}`,
          style: {
            shape: "round-rectangle",
            "background-color": "#2c313a",
            "background-opacity": 0.35,
            "border-color": "#444b57",
            "border-width": 1,
            label: "data(label)",
            "text-valign": "top",
            "text-halign": "center",
            "text-margin-y": -4,
            color: "#aab0bb",
            "font-size": 12,
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
        : ({ name: "dagre", rankDir: "TB", nodeSep: 30, rankSep: 50 } as cytoscape.LayoutOptions),
    });

    cy.on("tap", "node", (evt) => {
      if (evt.target.hasClass(GROUP_CLASS)) return;
      setMenu(null);
      const found = graph.nodes.find((n) => n.id === evt.target.id());
      onSelect(found ?? null);
    });
    cy.on("tap", (evt) => {
      if (evt.target === cy) {
        setMenu(null);
        onSelect(null);
      }
    });

    // Right-click a node to open a context menu at the cursor. Clicking the
    // background (or panning/zooming) dismisses it.
    cy.on("cxttap", "node", (evt) => {
      const found = graph.nodes.find((n) => n.id === evt.target.id());
      if (!found) return;
      const oe = evt.originalEvent as MouseEvent;
      setMenu({ x: oe.clientX, y: oe.clientY, node: found });
    });
    cy.on("cxttap", (evt) => {
      if (evt.target === cy) setMenu(null);
    });
    cy.on("viewport", () => setMenu(null));

    // Suppress the browser's native context menu over the canvas.
    const handleContextMenu = (e: MouseEvent) => e.preventDefault();
    containerRef.current.addEventListener("contextmenu", handleContextMenu);

    // Keyboard shortcuts acting on the currently selected node:
    //   Ctrl/Cmd-C  centers it in the view
    //   Ctrl/Cmd-Y  opens its YAML manifest modal
    const handleKeyDown = (e: KeyboardEvent) => {
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
    return () => {
      window.removeEventListener("keydown", handleKeyDown);
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

  return (
    <>
      <div ref={containerRef} className="graph-canvas" />
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
            {/* Edit is not implemented yet. */}
            <Menu.Item leftSection={<IconPencil size={16} stroke={1.5} />}>Edit</Menu.Item>
          </Menu.Dropdown>
        </Menu>
      )}
    </>
  );
}
