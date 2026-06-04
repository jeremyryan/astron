import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { getGraph, listProjections, type Graph, type GraphNode, type Projection } from "./api";
import { GraphView } from "./GraphView";
import { FilterPanel, kindCounts } from "./Filters";

function ProjectionList({
  selected,
  onSelect,
}: {
  selected?: Projection;
  onSelect: (p: Projection) => void;
}) {
  const { data, isLoading, error } = useQuery({
    queryKey: ["projections"],
    queryFn: listProjections,
    refetchInterval: 10_000,
  });

  if (isLoading) return <p className="muted">Loading projections…</p>;
  if (error) return <p className="error">{(error as Error).message}</p>;
  if (!data?.length) return <p className="muted">No GraphProjections found.</p>;

  return (
    <ul className="projection-list">
      {data.map((p) => (
        <li
          key={p.uid}
          className={selected?.uid === p.uid ? "active" : ""}
          onClick={() => onSelect(p)}
        >
          <div className="name">{p.name}</div>
          <div className="meta">
            {p.namespace} · {p.phase ?? "—"} · {p.nodeCount}n / {p.relationshipCount}e
          </div>
        </li>
      ))}
    </ul>
  );
}

// Properties that hold a map of key/value pairs (stored as a JSON string by the
// backend) and should be rendered as individual entries rather than raw JSON.
const MAP_PROPS = new Set(["labels", "annotations"]);

// asKeyValues parses a property value into sorted key/value entries when it
// represents an object (either an object or a JSON-encoded string). Returns null
// when the value is not a map.
function asKeyValues(value: unknown): Array<[string, string]> | null {
  let obj: unknown = value;
  if (typeof value === "string") {
    try {
      obj = JSON.parse(value);
    } catch {
      return null;
    }
  }
  if (!obj || typeof obj !== "object" || Array.isArray(obj)) return null;
  return Object.entries(obj as Record<string, unknown>)
    .map(([k, v]): [string, string] => [k, typeof v === "string" ? v : JSON.stringify(v)])
    .sort((a, b) => a[0].localeCompare(b[0]));
}

function KeyValueSection({ title, value }: { title: string; value: unknown }) {
  const entries = asKeyValues(value);
  return (
    <div className="kv-section">
      <h4>{title}</h4>
      {entries && entries.length > 0 ? (
        <dl className="kv-list">
          {entries.map(([k, v]) => (
            <div key={k} className="kv">
              <dt>{k}</dt>
              <dd>{v}</dd>
            </div>
          ))}
        </dl>
      ) : (
        <p className="muted">none</p>
      )}
    </div>
  );
}

function NodeDetails({ node }: { node: GraphNode | null }) {
  if (!node) return <p className="muted">Select a node to inspect it.</p>;
  const props = Object.entries(node.properties ?? {});
  const scalarProps = props.filter(([k]) => !MAP_PROPS.has(k));
  const mapProps = props.filter(([k]) => MAP_PROPS.has(k));
  return (
    <div className="node-details">
      <h3>
        {node.kind} <span className="muted">{node.apiVersion}</span>
      </h3>
      <dl>
        <dt>Name</dt>
        <dd>{node.name}</dd>
        {node.namespace && (
          <>
            <dt>Namespace</dt>
            <dd>{node.namespace}</dd>
          </>
        )}
        {scalarProps.map(([k, v]) => (
          <div key={k} className="prop">
            <dt>{k}</dt>
            <dd>{typeof v === "string" ? v : JSON.stringify(v)}</dd>
          </div>
        ))}
      </dl>
      {mapProps.map(([k, v]) => (
        <KeyValueSection key={k} title={k} value={v} />
      ))}
    </div>
  );
}

function GraphPanel({ projection }: { projection: Projection }) {
  const [selected, setSelected] = useState<GraphNode | null>(null);
  // Kinds the user has hidden. Empty = show everything (the default).
  const [hiddenKinds, setHiddenKinds] = useState<Set<string>>(new Set());
  // Max hops from the selected node to keep visible; null = all (no fading).
  const [maxDistance, setMaxDistance] = useState<number | null>(null);
  const { data, isLoading, error } = useQuery({
    queryKey: ["graph", projection.uid],
    queryFn: () => getGraph(projection.namespace, projection.name),
    refetchInterval: 10_000,
  });

  const kinds = useMemo(() => (data ? kindCounts(data) : []), [data]);

  const filteredGraph = useMemo<Graph | undefined>(() => {
    if (!data) return undefined;
    if (hiddenKinds.size === 0) return data;
    const nodes = data.nodes.filter((n) => !hiddenKinds.has(n.kind));
    const visibleIds = new Set(nodes.map((n) => n.id));
    const edges = data.edges.filter(
      (e) => visibleIds.has(e.source) && visibleIds.has(e.target),
    );
    return { nodes, edges };
  }, [data, hiddenKinds]);

  const toggleKind = (kind: string) =>
    setHiddenKinds((prev) => {
      const next = new Set(prev);
      if (next.has(kind)) next.delete(kind);
      else next.add(kind);
      return next;
    });
  const showAll = () => setHiddenKinds(new Set());
  const hideAll = () => setHiddenKinds(new Set(kinds.map((k) => k.kind)));

  return (
    <div className="graph-panel">
      <FilterPanel
        kinds={kinds}
        hiddenKinds={hiddenKinds}
        onToggleKind={toggleKind}
        onShowAll={showAll}
        onHideAll={hideAll}
        hasSelection={selected !== null}
        maxDistance={maxDistance}
        onChangeDistance={setMaxDistance}
      />
      <div className="graph-area">
        {isLoading && <p className="muted">Loading graph…</p>}
        {error && <p className="error">{(error as Error).message}</p>}
        {filteredGraph && (
          <GraphView
            graph={filteredGraph}
            onSelect={setSelected}
            selectedId={selected?.id ?? null}
            maxDistance={maxDistance}
          />
        )}
      </div>
      <aside className="inspector">
        <NodeDetails node={selected} />
      </aside>
    </div>
  );
}

export default function App() {
  const [selected, setSelected] = useState<Projection>();

  return (
    <div className="app">
      <header>
        <h1>Project Gamera</h1>
        <span className="subtitle">Kubernetes Cluster Graph</span>
      </header>
      <div className="body">
        <nav className="sidebar">
          <h2>Projections</h2>
          <ProjectionList selected={selected} onSelect={setSelected} />
        </nav>
        <main>
          {selected ? (
            <GraphPanel projection={selected} />
          ) : (
            <p className="muted center">Choose a projection to view its graph.</p>
          )}
        </main>
      </div>
    </div>
  );
}
