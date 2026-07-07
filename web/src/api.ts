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
  // True for user-created links, which can be deleted from the UI.
  manual?: boolean;
}

export interface Graph {
  nodes: GraphNode[];
  edges: GraphEdge[];
}

// GraphSelection is the currently inspected element: either a node or an edge
// (with its resolved endpoint nodes, when available).
export type GraphSelection =
  | { type: "node"; node: GraphNode }
  | { type: "edge"; edge: GraphEdge; source?: GraphNode; target?: GraphNode };

async function getJSON<T>(url: string): Promise<T> {
  const res = await fetch(url);
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error((body as { error?: string }).error ?? `request failed: ${res.status}`);
  }
  return res.json() as Promise<T>;
}

async function sendJSON<T>(method: string, url: string, body?: unknown): Promise<T | undefined> {
  const res = await fetch(url, {
    method,
    headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) {
    const errBody = await res.json().catch(() => ({}));
    throw new Error((errBody as { error?: string }).error ?? `request failed: ${res.status}`);
  }
  if (res.status === 204) return undefined;
  return res.json() as Promise<T>;
}

export function listProjections(): Promise<Projection[]> {
  return getJSON<Projection[]>("/api/projections");
}

export function getGraph(namespace: string, name: string): Promise<Graph> {
  return getJSON<Graph>(`/api/projections/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/graph`);
}

// getResourceYaml fetches the live YAML manifest for a single resource.
export async function getResourceYaml(node: {
  apiVersion: string;
  kind: string;
  namespace?: string;
  name: string;
}): Promise<string> {
  const params = new URLSearchParams({
    apiVersion: node.apiVersion,
    kind: node.kind,
    name: node.name,
  });
  if (node.namespace) params.set("namespace", node.namespace);
  const res = await fetch(`/api/resource?${params.toString()}`);
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error((body as { error?: string }).error ?? `request failed: ${res.status}`);
  }
  return res.text();
}

// ----- Views (saved filter sets) -----

export interface ViewLabelFilter {
  key: string;
  value?: string;
}

export interface ViewFilters {
  // "hide" (hide-list, the default) or "show" (allow-list: only visibleKinds).
  kindMode?: "hide" | "show";
  hiddenKinds?: string[];
  visibleKinds?: string[];
  hiddenNamespaces?: string[];
  labelFilters?: ViewLabelFilter[];
  labelMode?: string;
  // Omitted/undefined means "all connections".
  maxDistance?: number;
  groupByNamespace?: boolean;
}

export interface View {
  namespace: string;
  name: string;
  uid?: string;
  displayName?: string;
  description?: string;
  projectionRef: { name: string; namespace?: string };
  filters: ViewFilters;
}

export function listViews(projectionNamespace: string, projectionName: string): Promise<View[]> {
  const params = new URLSearchParams({
    projectionNamespace,
    projectionName,
  });
  return getJSON<View[]>(`/api/views?${params.toString()}`);
}

export function createView(view: Omit<View, "uid">): Promise<View> {
  return sendJSON<View>("POST", "/api/views", view) as Promise<View>;
}

export function updateView(namespace: string, name: string, view: Omit<View, "uid">): Promise<View> {
  return sendJSON<View>(
    "PUT",
    `/api/views/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`,
    view,
  ) as Promise<View>;
}

export async function deleteView(namespace: string, name: string): Promise<void> {
  await sendJSON<void>(
    "DELETE",
    `/api/views/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`,
  );
}

// ----- Links (user-created edges) -----

// createLink adds a user-defined edge between two nodes (by their graph node
// ids) within a projection. The backend defaults the relationship type to a
// Custom link.
export async function createLink(
  namespace: string,
  name: string,
  from: string,
  to: string,
): Promise<void> {
  await sendJSON<void>(
    "POST",
    `/api/projections/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/links`,
    { from, to },
  );
}

// deleteLink removes a user-created edge between two nodes within a projection.
export async function deleteLink(
  namespace: string,
  name: string,
  from: string,
  to: string,
  type: string,
): Promise<void> {
  const params = new URLSearchParams({ from, to, type });
  await sendJSON<void>(
    "DELETE",
    `/api/projections/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/links?${params.toString()}`,
  );
}
