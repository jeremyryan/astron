// Typed client for the Astron read API.

export interface Projection {
  uid: string;
  namespace: string;
  name: string;
  phase?: string;
  nodeCount: number;
  relationshipCount: number;
  // True when the projection has GraphRAG configured with a chat provider,
  // enabling the natural-language answer endpoint (and the chat UI).
  chatEnabled?: boolean;
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

// ----- Controller-wide model providers -----

// ProviderInfo describes one controller-wide model provider (an embedding or
// chat provider configured on the controller and shared by every projection).
export interface ProviderInfo {
  name: string;
  provider: string;
  model?: string;
}

// Providers is the set of controller-wide embedding and chat providers.
export interface Providers {
  embeddingProviders: ProviderInfo[];
  chatProviders: ProviderInfo[];
}

export function listProviders(): Promise<Providers> {
  return getJSON<Providers>("/api/providers");
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

// ----- Snapshots (point-in-time graph copies) -----

export interface Snapshot {
  id: string;
  name: string;
  createdAt: string;
  nodeCount: number;
  relationshipCount: number;
}

export function listSnapshots(
  projectionNamespace: string,
  projectionName: string,
): Promise<Snapshot[]> {
  return getJSON<Snapshot[]>(
    `/api/projections/${encodeURIComponent(projectionNamespace)}/${encodeURIComponent(projectionName)}/snapshots`,
  );
}

export function createSnapshot(
  projectionNamespace: string,
  projectionName: string,
  name: string,
  // Node IDs to capture; omitted captures the entire projection.
  nodeIds?: string[],
): Promise<Snapshot> {
  return sendJSON<Snapshot>(
    "POST",
    `/api/projections/${encodeURIComponent(projectionNamespace)}/${encodeURIComponent(projectionName)}/snapshots`,
    { name, nodeIds },
  ) as Promise<Snapshot>;
}

export function getSnapshotGraph(
  projectionNamespace: string,
  projectionName: string,
  id: string,
): Promise<Graph> {
  return getJSON<Graph>(
    `/api/projections/${encodeURIComponent(projectionNamespace)}/${encodeURIComponent(projectionName)}/snapshots/${encodeURIComponent(id)}/graph`,
  );
}

export async function renameSnapshot(
  projectionNamespace: string,
  projectionName: string,
  id: string,
  name: string,
): Promise<void> {
  await sendJSON<void>(
    "PATCH",
    `/api/projections/${encodeURIComponent(projectionNamespace)}/${encodeURIComponent(projectionName)}/snapshots/${encodeURIComponent(id)}`,
    { name },
  );
}

export async function deleteSnapshot(
  projectionNamespace: string,
  projectionName: string,
  id: string,
): Promise<void> {
  await sendJSON<void>(
    "DELETE",
    `/api/projections/${encodeURIComponent(projectionNamespace)}/${encodeURIComponent(projectionName)}/snapshots/${encodeURIComponent(id)}`,
  );
}

// ----- GraphRAG chat -----

// AnswerCard is a natural-language description of a resource that grounded an
// answer.
export interface AnswerCard {
  id: string;
  kind: string;
  namespace?: string;
  name: string;
  text: string;
}

// Answer is the response of the RAG answer endpoint: the generated answer plus
// the retrieval context that grounded it.
export interface Answer {
  question: string;
  answer: string;
  retrieval: {
    query: string;
    seeds: Array<{ id: string; kind: string; name: string; score: number }>;
    cards: AnswerCard[];
    subgraph: Graph;
  };
}

// askQuestion sends a natural-language question about a projection's graph to
// the configured chat provider and returns the grounded answer. model, when
// set, overrides the projection's default chat model (it must be permitted by
// the projection's allowedModels policy).
export function askQuestion(
  namespace: string,
  name: string,
  question: string,
  model?: string,
): Promise<Answer> {
  return sendJSON<Answer>(
    "POST",
    `/api/projections/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/rag/answer`,
    model ? { question, model } : { question },
  ) as Promise<Answer>;
}

// ChatModels is the set of chat models a user may choose from for a
// projection, plus the configured default.
export interface ChatModels {
  default: string;
  models: string[];
}

// getChatModels lists the chat models selectable for a projection under its
// allowedModels policy.
export function getChatModels(namespace: string, name: string): Promise<ChatModels> {
  return getJSON<ChatModels>(
    `/api/projections/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/rag/models`,
  );
}

// ChatHistoryMessage is one prior turn of a conversation, sent to the agent
// endpoint so follow-up questions have context. Only "user"/"assistant" turns
// round-trip; a run's internal tool-call transcript is not carried across
// requests.
export interface ChatHistoryMessage {
  role: "user" | "assistant";
  content: string;
}

// AgentStep is one tool call the chat agent made while answering a question:
// which tool, with what arguments, and a short rendering of its result.
export interface AgentStep {
  tool: string;
  args?: unknown;
  summary: string;
}

// AgentAnswer is the response of the tool-using chat agent endpoint.
export interface AgentAnswer {
  question: string;
  answer: string;
  steps: AgentStep[];
  // Whether the tool-using loop actually ran; false means the resolved chat
  // backend didn't support tool calling and the fixed answer pipeline (the
  // same one askQuestion uses) was used instead.
  agentic: boolean;
  // Whether the run hit its tool-call budget before volunteering a final
  // answer (the answer may be less complete than an unbounded run's).
  stepBudgetExhausted?: boolean;
}

// askAgent sends a natural-language question to a bounded, tool-using chat
// agent that can call the projection's own retrieval capabilities (search,
// neighborhood, guarded Cypher, schema, live resource reads) as it works out
// the answer, rather than grounding on a single pre-baked retrieval like
// askQuestion. history carries the prior conversation's user/assistant turns.
// model, when set, overrides the projection's default chat model (it must be
// permitted by the projection's allowedModels policy).
export function askAgent(
  namespace: string,
  name: string,
  question: string,
  history?: ChatHistoryMessage[],
  model?: string,
): Promise<AgentAnswer> {
  return sendJSON<AgentAnswer>(
    "POST",
    `/api/projections/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/rag/agent`,
    {
      question,
      ...(history && history.length > 0 ? { history } : {}),
      ...(model ? { model } : {}),
    },
  ) as Promise<AgentAnswer>;
}

// ----- Links (user-created edges) -----

// createLink adds a user-defined edge between two nodes (by their graph node
// ids) within a projection. The backend defaults the relationship type to a
// Custom link.
// linksPath returns the manual-links endpoint for the live graph or, when a
// snapshot id is given, for that snapshot's copied graph.
function linksPath(namespace: string, name: string, snapshotId?: string): string {
  const base = `/api/projections/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`;
  return snapshotId ? `${base}/snapshots/${encodeURIComponent(snapshotId)}/links` : `${base}/links`;
}

export async function createLink(
  namespace: string,
  name: string,
  from: string,
  to: string,
  snapshotId?: string,
): Promise<void> {
  await sendJSON<void>("POST", linksPath(namespace, name, snapshotId), { from, to });
}

// updateLink sets (or clears, when note is empty) the free-text note associated
// with a user-created edge within a projection.
export async function updateLink(
  namespace: string,
  name: string,
  from: string,
  to: string,
  type: string,
  note: string,
  snapshotId?: string,
): Promise<void> {
  await sendJSON<void>("PATCH", linksPath(namespace, name, snapshotId), { from, to, type, note });
}

// deleteLink removes a user-created edge between two nodes within a projection.
export async function deleteLink(
  namespace: string,
  name: string,
  from: string,
  to: string,
  type: string,
  snapshotId?: string,
): Promise<void> {
  const params = new URLSearchParams({ from, to, type });
  await sendJSON<void>("DELETE", `${linksPath(namespace, name, snapshotId)}?${params.toString()}`);
}
