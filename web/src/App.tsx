import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { getGraph, listProjections, type GraphNode, type Projection } from "./api";
import { GraphView } from "./GraphView";

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

function NodeDetails({ node }: { node: GraphNode | null }) {
  if (!node) return <p className="muted">Select a node to inspect it.</p>;
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
        {Object.entries(node.properties ?? {}).map(([k, v]) => (
          <div key={k} className="prop">
            <dt>{k}</dt>
            <dd>{typeof v === "string" ? v : JSON.stringify(v)}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}

function GraphPanel({ projection }: { projection: Projection }) {
  const [selected, setSelected] = useState<GraphNode | null>(null);
  const { data, isLoading, error } = useQuery({
    queryKey: ["graph", projection.uid],
    queryFn: () => getGraph(projection.namespace, projection.name),
    refetchInterval: 10_000,
  });

  return (
    <div className="graph-panel">
      <div className="graph-area">
        {isLoading && <p className="muted">Loading graph…</p>}
        {error && <p className="error">{(error as Error).message}</p>}
        {data && <GraphView graph={data} onSelect={setSelected} />}
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
