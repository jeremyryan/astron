// Typed client for the Gamera read API.

export interface Projection {
  uid: string;
  namespace: string;
  name: string;
  phase?: string;
  nodeCount: number;
  relationshipCount: number;
}

export interface GraphNode {
  id: string;
  apiVersion: string;
  kind: string;
  namespace?: string;
  name: string;
  properties?: Record<string, unknown>;
}

export interface GraphEdge {
  id: string;
  source: string;
  target: string;
  type: string;
  properties?: Record<string, unknown>;
}

export interface Graph {
  nodes: GraphNode[];
  edges: GraphEdge[];
}

async function getJSON<T>(url: string): Promise<T> {
  const res = await fetch(url);
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error((body as { error?: string }).error ?? `request failed: ${res.status}`);
  }
  return res.json() as Promise<T>;
}

export function listProjections(): Promise<Projection[]> {
  return getJSON<Projection[]>("/api/projections");
}

export function getGraph(namespace: string, name: string): Promise<Graph> {
  return getJSON<Graph>(`/api/projections/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/graph`);
}
