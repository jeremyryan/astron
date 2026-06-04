import { useEffect, useRef } from "react";
import cytoscape, { type Core, type ElementDefinition } from "cytoscape";
import dagre from "cytoscape-dagre";
import type { Graph, GraphNode } from "./api";
import { colorForKind } from "./kinds";

cytoscape.use(dagre);

function toElements(graph: Graph): ElementDefinition[] {
  const ids = new Set(graph.nodes.map((n) => n.id));
  const nodes: ElementDefinition[] = graph.nodes.map((n: GraphNode) => ({
    data: {
      id: n.id,
      label: `${n.kind}\n${n.name}`,
      kind: n.kind,
      color: colorForKind(n.kind),
    },
  }));
  // Drop edges whose endpoints are not present to avoid render errors.
  const edges: ElementDefinition[] = graph.edges
    .filter((e) => ids.has(e.source) && ids.has(e.target))
    .map((e) => ({
      data: { id: e.id, source: e.source, target: e.target, label: e.type },
    }));
  return [...nodes, ...edges];
}

interface Props {
  graph: Graph;
  onSelect: (node: GraphNode | null) => void;
}

export function GraphView({ graph, onSelect }: Props) {
  const containerRef = useRef<HTMLDivElement>(null);
  const cyRef = useRef<Core | null>(null);

  useEffect(() => {
    if (!containerRef.current) return;
    const cy = cytoscape({
      container: containerRef.current,
      elements: toElements(graph),
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
      ],
      layout: { name: "dagre", rankDir: "TB", nodeSep: 30, rankSep: 50 } as cytoscape.LayoutOptions,
    });

    cy.on("tap", "node", (evt) => {
      const found = graph.nodes.find((n) => n.id === evt.target.id());
      onSelect(found ?? null);
    });
    cy.on("tap", (evt) => {
      if (evt.target === cy) onSelect(null);
    });

    // Ctrl-C (or Cmd-C) centers the currently selected node in the view.
    const handleKeyDown = (e: KeyboardEvent) => {
      if (!(e.ctrlKey || e.metaKey) || e.key.toLowerCase() !== "c") return;
      const selected = cy.nodes(":selected");
      if (selected.nonempty()) {
        e.preventDefault();
        cy.animate({ center: { eles: selected }, duration: 200 });
      }
    };
    window.addEventListener("keydown", handleKeyDown);

    cyRef.current = cy;
    return () => {
      window.removeEventListener("keydown", handleKeyDown);
      cy.destroy();
      cyRef.current = null;
    };
  }, [graph, onSelect]);

  return <div ref={containerRef} className="graph-canvas" />;
}
