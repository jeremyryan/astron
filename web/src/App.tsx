import { useCallback, useEffect, useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ActionIcon,
  Anchor,
  AppShell,
  Box,
  Button,
  Group,
  Loader,
  NavLink,
  ScrollArea,
  Stack,
  Text,
  Title,
  Tooltip,
  UnstyledButton,
} from "@mantine/core";
import {
  createLink,
  deleteLink,
  getGraph,
  listProjections,
  listViews,
  type Graph,
  type GraphNode,
  type GraphSelection,
  type Projection,
  type View,
  type ViewFilters,
} from "./api";
import { GraphView } from "./GraphView";
import { ViewControls } from "./ViewControls";
import {
  FilterPanel,
  kindCounts,
  namespaceCounts,
  type LabelFilter,
  type LabelMatchMode,
} from "./Filters";
import { YamlModal } from "./YamlModal";
import { SettingsModal } from "./SettingsModal";
import { useSettings } from "./settings";
import { colorForRelationship, iconForKindOrGeneric } from "./kinds";
import {
  IconArrowLeft,
  IconBookmark,
  IconChevronLeft,
  IconChevronRight,
  IconEye,
  IconEyeOff,
  IconFileCode,
  IconHierarchy2,
  IconSettings,
  IconTag,
  IconTagOff,
  IconTopologyStar3,
} from "./icons";

// ProjectionNavItem renders one projection in the navbar with its saved Views
// nested beneath it. Clicking the projection selects it (custom filters);
// clicking a view selects the projection and applies that view's filters.
function ProjectionNavItem({
  projection,
  selected,
  activeView,
  onSelectProjection,
  onSelectView,
}: {
  projection: Projection;
  selected?: Projection;
  activeView: View | null;
  onSelectProjection: (p: Projection) => void;
  onSelectView: (p: Projection, v: View) => void;
}) {
  const { data: views } = useQuery({
    queryKey: ["views", projection.namespace, projection.name],
    queryFn: () => listViews(projection.namespace, projection.name),
  });
  const isSelected = selected?.uid === projection.uid;
  const items = views ?? [];
  return (
    <Box>
      <NavLink
        active={isSelected && !activeView}
        onClick={() => onSelectProjection(projection)}
        leftSection={<IconHierarchy2 size={16} stroke={1.5} />}
        label={<Text fw={600}>{projection.name}</Text>}
        description={`${projection.namespace} · ${projection.phase ?? "—"} · ${projection.nodeCount}n / ${projection.relationshipCount}e`}
      />
      {/* Views associated with this projection, always shown indented below it. */}
      {items.map((v) => (
        <NavLink
          key={v.uid ?? `${v.namespace}/${v.name}`}
          pl={28}
          active={
            isSelected &&
            activeView?.namespace === v.namespace &&
            activeView?.name === v.name
          }
          onClick={() => onSelectView(projection, v)}
          leftSection={<IconBookmark size={14} stroke={1.5} />}
          label={v.displayName || v.name}
        />
      ))}
    </Box>
  );
}

function ProjectionList({
  selected,
  activeView,
  onSelectProjection,
  onSelectView,
}: {
  selected?: Projection;
  activeView: View | null;
  onSelectProjection: (p: Projection) => void;
  onSelectView: (p: Projection, v: View) => void;
}) {
  const { data, isLoading, error } = useQuery({
    queryKey: ["projections"],
    queryFn: listProjections,
    refetchInterval: 10_000,
  });

  if (isLoading)
    return (
      <Group gap="xs">
        <Loader size="xs" />
        <Text size="sm" c="dimmed">
          Loading projections…
        </Text>
      </Group>
    );
  if (error)
    return (
      <Text size="sm" c="red">
        {(error as Error).message}
      </Text>
    );
  if (!data?.length)
    return (
      <Text size="sm" c="dimmed">
        No GraphProjections found.
      </Text>
    );

  return (
    <Stack gap={4}>
      {data.map((p) => (
        <ProjectionNavItem
          key={p.uid}
          projection={p}
          selected={selected}
          activeView={activeView}
          onSelectProjection={onSelectProjection}
          onSelectView={onSelectView}
        />
      ))}
    </Stack>
  );
}

// Properties that hold a map of key/value pairs (stored as a JSON string by the
// backend) and should be rendered as individual entries rather than raw JSON.
const MAP_PROPS = new Set(["labels", "annotations"]);

// Properties hidden from the resource details panel: internal/noisy fields, or
// values rendered by a dedicated section (e.g. hostnames).
const HIDDEN_PROPS = new Set(["resourceVersion", "hostnames"]);

// formatTimestamp renders an RFC3339 timestamp as 'MM/DD/YYYY HH:MM' in local
// time, falling back to the raw value when it can't be parsed.
function formatTimestamp(value: string): string {
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${pad(d.getMonth() + 1)}/${pad(d.getDate())}/${d.getFullYear()} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

// nodeHostnames extracts an HTTPRoute's hostnames, which the backend stores as a
// string list (or, in some transports, a JSON-encoded array).
function nodeHostnames(node: GraphNode): string[] {
  const raw = node.properties?.hostnames;
  if (Array.isArray(raw)) return raw.map(String);
  if (typeof raw === "string") {
    try {
      const parsed = JSON.parse(raw);
      if (Array.isArray(parsed)) return parsed.map(String);
    } catch {
      // not JSON; fall through
    }
    return raw ? [raw] : [];
  }
  return [];
}

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

// Field renders a single uppercase label above its value.
function Field({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <Text size="xs" c="dimmed" tt="uppercase" style={{ letterSpacing: "0.05em" }}>
        {label}
      </Text>
      <Text size="sm" style={{ wordBreak: "break-word" }}>
        {value}
      </Text>
    </div>
  );
}

function KeyValueSection({ title, value }: { title: string; value: unknown }) {
  const entries = asKeyValues(value);
  return (
    <div>
      <Text size="xs" c="dimmed" tt="uppercase" mb={6} style={{ letterSpacing: "0.05em" }}>
        {title}
      </Text>
      {entries && entries.length > 0 ? (
        <Stack gap={0}>
          {entries.map(([k, v], i) => (
            <Box
              key={k}
              py={4}
              style={{
                borderTop: i > 0 ? "1px solid var(--mantine-color-dark-4)" : undefined,
              }}
            >
              <Text size="xs" c="dimmed" style={{ wordBreak: "break-all" }}>
                {k}
              </Text>
              <Text size="sm" style={{ wordBreak: "break-word" }}>
                {v}
              </Text>
            </Box>
          ))}
        </Stack>
      ) : (
        <Text size="sm" c="dimmed">
          none
        </Text>
      )}
    </div>
  );
}

// groupResources buckets nodes by namespace and then by kind, sorting each
// level (and the resources within a kind) alphabetically. Cluster-scoped
// resources are collected under a synthetic "(cluster-scoped)" group.
function groupResources(nodes: GraphNode[]) {
  const byNs = new Map<string, GraphNode[]>();
  for (const n of nodes) {
    const ns = n.namespace || "(cluster-scoped)";
    let bucket = byNs.get(ns);
    if (!bucket) byNs.set(ns, (bucket = []));
    bucket.push(n);
  }
  return [...byNs.keys()]
    .sort((a, b) => a.localeCompare(b))
    .map((namespace) => {
      const byKind = new Map<string, GraphNode[]>();
      for (const n of byNs.get(namespace)!) {
        let bucket = byKind.get(n.kind);
        if (!bucket) byKind.set(n.kind, (bucket = []));
        bucket.push(n);
      }
      const kinds = [...byKind.keys()]
        .sort((a, b) => a.localeCompare(b))
        .map((kind) => ({
          kind,
          nodes: byKind.get(kind)!.sort((a, b) => a.name.localeCompare(b.name)),
        }));
      return { namespace, kinds };
    });
}

// ResourceList is shown in the inspector when nothing is selected: a browsable
// index of the visible resources grouped by namespace then kind. Clicking a
// resource name selects that node.
function ResourceList({
  nodes,
  onSelect,
  selectedIds,
  hiddenIds,
  onToggleVisibility,
}: {
  nodes: GraphNode[];
  onSelect: (node: GraphNode) => void;
  selectedIds: Set<string>;
  hiddenIds: Set<string>;
  onToggleVisibility: (id: string) => void;
}) {
  const groups = useMemo(() => groupResources(nodes), [nodes]);
  // Kind sections the user has collapsed, keyed by "<namespace>/<kind>".
  const [collapsedKinds, setCollapsedKinds] = useState<Set<string>>(new Set());
  const toggleKindCollapsed = (key: string) =>
    setCollapsedKinds((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  if (nodes.length === 0)
    return (
      <Text size="sm" c="dimmed">
        No resources to display.
      </Text>
    );
  return (
    <Stack gap="lg">
      <Text size="xs" c="dimmed">
        Select a node to inspect it, or pick a resource below.
      </Text>
      {groups.map((g) => (
        <Stack gap="xs" key={g.namespace}>
          <Text
            size="xs"
            fw={700}
            tt="uppercase"
            c="dimmed"
            style={{ letterSpacing: "0.06em" }}
          >
            {g.namespace}
          </Text>
          {g.kinds.map((k) => {
            const icon = iconForKindOrGeneric(k.kind);
            const kindKey = `${g.namespace}/${k.kind}`;
            const collapsed = collapsedKinds.has(kindKey);
            return (
              <Stack gap={2} key={k.kind}>
                <UnstyledButton
                  className="resource-kind-header"
                  onClick={() => toggleKindCollapsed(kindKey)}
                  aria-expanded={!collapsed}
                >
                  <Group gap={6} wrap="nowrap" align="center">
                    <IconChevronRight
                      size={12}
                      style={{
                        transition: "transform 150ms ease",
                        transform: collapsed ? "none" : "rotate(90deg)",
                        flex: "0 0 auto",
                      }}
                    />
                    <img src={icon} width={14} height={14} alt="" />
                    <Text size="xs" fw={600}>
                      {k.kind}
                    </Text>
                    <Text size="xs" c="dimmed">
                      {k.nodes.length}
                    </Text>
                  </Group>
                </UnstyledButton>
                <Stack gap={0} pl={20} display={collapsed ? "none" : undefined}>
                  {k.nodes.map((n) => {
                    const hidden = hiddenIds.has(n.id);
                    return (
                      <Group key={n.id} gap={2} wrap="nowrap" align="center">
                        <UnstyledButton
                          className={
                            (selectedIds.has(n.id)
                              ? "resource-link resource-link-selected"
                              : "resource-link") + (hidden ? " resource-link-hidden" : "")
                          }
                          style={{ flex: 1, minWidth: 0 }}
                          onClick={() => onSelect(n)}
                          title={n.name}
                        >
                          {n.name}
                        </UnstyledButton>
                        <Tooltip label={hidden ? "Show in graph" : "Hide from graph"} position="left">
                          <ActionIcon
                            variant="subtle"
                            color="gray"
                            size="sm"
                            onClick={() => onToggleVisibility(n.id)}
                            aria-label={hidden ? "Show in graph" : "Hide from graph"}
                          >
                            {hidden ? <IconEyeOff size={14} /> : <IconEye size={14} />}
                          </ActionIcon>
                        </Tooltip>
                      </Group>
                    );
                  })}
                </Stack>
              </Stack>
            );
          })}
        </Stack>
      ))}
    </Stack>
  );
}

function NodeDetails({ node }: { node: GraphNode | null }) {
  if (!node)
    return (
      <Text size="sm" c="dimmed">
        Select a node to inspect it.
      </Text>
    );
  const props = Object.entries(node.properties ?? {});
  const scalarProps = props.filter(([k]) => !MAP_PROPS.has(k) && !HIDDEN_PROPS.has(k));
  const mapProps = props.filter(([k]) => MAP_PROPS.has(k));
  const hostnames = nodeHostnames(node);
  const icon = iconForKindOrGeneric(node.kind);
  return (
    <Stack gap="md">
      <Group gap={8} wrap="nowrap" align="center">
        <img src={icon} width={22} height={22} alt="" />
        <Title order={3} size="h4">
          {node.kind}{" "}
          <Text span c="dimmed" size="sm" fw={400}>
            {node.apiVersion}
          </Text>
        </Title>
      </Group>
      <Stack gap="xs">
        <Field label="Name" value={node.name} />
        {node.namespace && <Field label="Namespace" value={node.namespace} />}
        {scalarProps.map(([k, v]) => (
          <Field
            key={k}
            label={k}
            value={
              k === "creationTimestamp"
                ? formatTimestamp(String(v))
                : typeof v === "string"
                  ? v
                  : JSON.stringify(v)
            }
          />
        ))}
      </Stack>
      {hostnames.length > 0 && (
        <Stack gap={4}>
          <Text size="xs" c="dimmed" tt="uppercase" style={{ letterSpacing: "0.05em" }}>
            Hostnames
          </Text>
          {hostnames.map((h) => (
            <Anchor
              key={h}
              href={`https://${h}`}
              target="_blank"
              rel="noreferrer"
              size="sm"
              style={{ wordBreak: "break-word" }}
            >
              {h}
            </Anchor>
          ))}
        </Stack>
      )}
      {mapProps.map(([k, v]) => (
        <KeyValueSection key={k} title={k} value={v} />
      ))}
    </Stack>
  );
}

// renderEdgeProp renders a single edge property: a JSON-encoded map (e.g.
// selectorLabels) as a key/value section, an array (e.g. via/hosts/paths) as a
// comma-separated value, and anything else as a scalar field.
function renderEdgeProp(k: string, v: unknown) {
  if (asKeyValues(v)) return <KeyValueSection key={k} title={k} value={v} />;
  if (Array.isArray(v))
    return <Field key={k} label={k} value={v.map((x) => String(x)).join(", ")} />;
  return <Field key={k} label={k} value={typeof v === "string" ? v : JSON.stringify(v)} />;
}

function EdgeDetails({ selection }: { selection: Extract<GraphSelection, { type: "edge" }> }) {
  const { edge, source, target } = selection;
  const props = Object.entries(edge.properties ?? {});
  const refLabel = (node: GraphNode | undefined, fallback: string) =>
    node ? `${node.kind} ${node.namespace ? `${node.namespace}/` : ""}${node.name}` : fallback;
  return (
    <Stack gap="md">
      <Title order={3} size="h4">
        {edge.type}{" "}
        <Text span c="dimmed" size="sm" fw={400}>
          relationship
        </Text>
      </Title>
      <Stack gap="xs">
        <Field label="From" value={refLabel(source, edge.source)} />
        <Field label="To" value={refLabel(target, edge.target)} />
      </Stack>
      {props.length === 0 ? (
        <Text size="sm" c="dimmed">
          No relationship data.
        </Text>
      ) : (
        <Stack gap="md">{props.map(([k, v]) => renderEdgeProp(k, v))}</Stack>
      )}
    </Stack>
  );
}

// nodeLabels parses a node's labels property (stored as a JSON string by the
// backend) into a flat string map.
function nodeLabels(node: GraphNode): Record<string, string> {
  const raw = node.properties?.labels;
  let obj: unknown = raw;
  if (typeof raw === "string") {
    try {
      obj = JSON.parse(raw);
    } catch {
      return {};
    }
  }
  if (!obj || typeof obj !== "object" || Array.isArray(obj)) return {};
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(obj as Record<string, unknown>)) {
    out[k] = typeof v === "string" ? v : JSON.stringify(v);
  }
  return out;
}

// matchesLabelFilters returns true when a node satisfies the active label
// filters. A filter with an empty value matches any value for the key. Rows
// with an empty key are ignored. Mode "all" requires every filter to match
// (AND); "any" requires at least one (OR).
function matchesLabelFilters(
  node: GraphNode,
  filters: LabelFilter[],
  mode: LabelMatchMode,
): boolean {
  const active = filters.filter((f) => f.key.trim() !== "");
  if (active.length === 0) return true;
  const labels = nodeLabels(node);
  const test = (f: LabelFilter) => {
    const key = f.key.trim();
    if (!(key in labels)) return false;
    const value = f.value.trim();
    return value === "" ? true : labels[key] === value;
  };
  return mode === "all" ? active.every(test) : active.some(test);
}

// EdgeLegend is a small floating key mapping relationship types to their edge
// colors, shown over the graph. It also carries a toggle for edge labels.
function EdgeLegend({
  types,
  showLabels,
  onToggleLabels,
}: {
  types: string[];
  showLabels: boolean;
  onToggleLabels: () => void;
}) {
  return (
    <div className="edge-legend">
      <div className="edge-legend-header">
        <span className="edge-legend-title">Edges</span>
        <Tooltip
          label={showLabels ? "Hide edge labels" : "Show edge labels"}
          position="top"
          withArrow
        >
          <ActionIcon
            variant={showLabels ? "filled" : "default"}
            size="sm"
            aria-label="Toggle edge labels"
            aria-pressed={showLabels}
            onClick={onToggleLabels}
          >
            {showLabels ? (
              <IconTag size={14} stroke={1.5} />
            ) : (
              <IconTagOff size={14} stroke={1.5} />
            )}
          </ActionIcon>
        </Tooltip>
      </div>
      {types.map((t) => (
        <div key={t} className="edge-legend-item">
          <span className="edge-legend-swatch" style={{ background: colorForRelationship(t) }} />
          <span>{t}</span>
        </div>
      ))}
    </div>
  );
}

function GraphPanel({
  projection,
  activeView,
  onActiveViewChange,
}: {
  projection: Projection;
  activeView: View | null;
  onActiveViewChange: (v: View | null) => void;
}) {
  const { settings, update } = useSettings();
  const queryClient = useQueryClient();
  // The currently inspected element (node or edge), or null.
  const [selection, setSelection] = useState<GraphSelection | null>(null);
  const selectedNode = selection?.type === "node" ? selection.node : null;
  // Ids of all nodes selected on the canvas (supports box-select), used to
  // highlight them in the resource list.
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  // When true, the inspector shows the resource list even if a node/edge is
  // selected (the user navigated "Back" from the detail view).
  const [showResourceList, setShowResourceList] = useState(false);
  // Whether the inspector panel is collapsed to a thin strip.
  const [inspectorCollapsed, setInspectorCollapsed] = useState(false);
  // Whether the left filters panel is collapsed to a thin strip.
  const [filtersCollapsed, setFiltersCollapsed] = useState(false);
  // Selecting/inspecting an element returns from the list to the detail view.
  // Memoized so its identity stays stable: GraphView rebuilds its canvas when
  // onSelect changes, so an inline function here would relayout on every render.
  const handleSelect = useCallback((sel: GraphSelection | null) => {
    setSelection(sel);
    if (sel) setShowResourceList(false);
  }, []);
  // Node whose YAML manifest is shown in the modal (null = closed).
  const [yamlNode, setYamlNode] = useState<GraphNode | null>(null);
  // Kinds the user has hidden. Empty = show everything (the default).
  const [hiddenKinds, setHiddenKinds] = useState<Set<string>>(new Set());
  // Namespaces the user has hidden ("" = cluster-scoped). Empty = show all.
  const [hiddenNamespaces, setHiddenNamespaces] = useState<Set<string>>(new Set());
  // Individual node ids the user has hidden from the graph via the resource
  // list. They remain listed (so they can be shown again), just not drawn.
  const [hiddenNodeIds, setHiddenNodeIds] = useState<Set<string>>(new Set());
  // Max hops from the selected node to keep visible; null = all (no fading).
  const [maxDistance, setMaxDistance] = useState<number | null>(null);
  // Whether to group resources into compound nodes by namespace.
  const [groupByNamespace, setGroupByNamespace] = useState(true);
  // Whether edge (relationship-type) labels are drawn on the graph. Persisted
  // across sessions via settings.
  const showEdgeLabels = settings.showEdgeLabels;
  // Label filters and the AND/OR mode used to combine them.
  const [labelFilters, setLabelFilters] = useState<LabelFilter[]>([]);
  const [labelMode, setLabelMode] = useState<LabelMatchMode>("any");
  const { data, isLoading, error } = useQuery({
    queryKey: ["graph", projection.uid],
    queryFn: () => getGraph(projection.namespace, projection.name),
    refetchInterval: 10_000,
  });

  const kinds = useMemo(() => (data ? kindCounts(data) : []), [data]);
  const namespaces = useMemo(() => (data ? namespaceCounts(data) : []), [data]);

  const filteredGraph = useMemo<Graph | undefined>(() => {
    if (!data) return undefined;
    const hasLabelFilter = labelFilters.some((f) => f.key.trim() !== "");
    if (hiddenKinds.size === 0 && hiddenNamespaces.size === 0 && !hasLabelFilter) return data;
    const nodes = data.nodes.filter(
      (n) =>
        !hiddenKinds.has(n.kind) &&
        !hiddenNamespaces.has(n.namespace ?? "") &&
        matchesLabelFilters(n, labelFilters, labelMode),
    );
    const visibleIds = new Set(nodes.map((n) => n.id));
    const edges = data.edges.filter(
      (e) => visibleIds.has(e.source) && visibleIds.has(e.target),
    );
    return { nodes, edges };
  }, [data, hiddenKinds, hiddenNamespaces, labelFilters, labelMode]);

  // Edges whose endpoints are both visible, used only for the relationship
  // legend. The full filtered graph (including individually-hidden nodes) is
  // handed to the GraphView, which hides those nodes in place — removing them
  // from the data would change the node set and force a relayout.
  const visibleEdgeTypesSource = useMemo(() => {
    if (!filteredGraph) return undefined;
    if (hiddenNodeIds.size === 0) return filteredGraph.edges;
    return filteredGraph.edges.filter(
      (e) => !hiddenNodeIds.has(e.source) && !hiddenNodeIds.has(e.target),
    );
  }, [filteredGraph, hiddenNodeIds]);

  const toggleNodeVisibility = (id: string) =>
    setHiddenNodeIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  // Distinct relationship types currently visible, for the color legend.
  const edgeTypes = useMemo(() => {
    const set = new Set<string>();
    visibleEdgeTypesSource?.forEach((e) => set.add(e.type));
    return [...set].sort();
  }, [visibleEdgeTypesSource]);

  const toggleKind = (kind: string) =>
    setHiddenKinds((prev) => {
      const next = new Set(prev);
      if (next.has(kind)) next.delete(kind);
      else next.add(kind);
      return next;
    });
  const showAll = () => setHiddenKinds(new Set());
  const hideAll = () => setHiddenKinds(new Set(kinds.map((k) => k.kind)));

  const toggleNamespace = (ns: string) =>
    setHiddenNamespaces((prev) => {
      const next = new Set(prev);
      if (next.has(ns)) next.delete(ns);
      else next.add(ns);
      return next;
    });
  const showAllNamespaces = () => setHiddenNamespaces(new Set());
  const hideAllNamespaces = () =>
    setHiddenNamespaces(new Set(namespaces.map((n) => n.namespace)));

  const addLabel = () =>
    setLabelFilters((prev) => [...prev, { id: crypto.randomUUID(), key: "", value: "" }]);
  const updateLabel = (id: string, patch: Partial<Pick<LabelFilter, "key" | "value">>) =>
    setLabelFilters((prev) => prev.map((f) => (f.id === id ? { ...f, ...patch } : f)));
  const removeLabel = (id: string) =>
    setLabelFilters((prev) => prev.filter((f) => f.id !== id));

  // Current filter state serialized into the View DTO shape, for saving.
  const currentFilters = useMemo<ViewFilters>(
    () => ({
      hiddenKinds: [...hiddenKinds],
      hiddenNamespaces: [...hiddenNamespaces],
      labelFilters: labelFilters
        .filter((f) => f.key.trim() !== "")
        .map((f) => ({ key: f.key, value: f.value || undefined })),
      labelMode,
      maxDistance: maxDistance ?? undefined,
      groupByNamespace,
    }),
    [hiddenKinds, hiddenNamespaces, labelFilters, labelMode, maxDistance, groupByNamespace],
  );

  // Apply a saved view's filters to the panel state.
  const applyFilters = (f: ViewFilters) => {
    setHiddenKinds(new Set(f.hiddenKinds ?? []));
    setHiddenNamespaces(new Set(f.hiddenNamespaces ?? []));
    setLabelFilters(
      (f.labelFilters ?? []).map((lf) => ({
        id: crypto.randomUUID(),
        key: lf.key,
        value: lf.value ?? "",
      })),
    );
    setLabelMode((f.labelMode as LabelMatchMode) ?? "any");
    setMaxDistance(f.maxDistance ?? null);
    setGroupByNamespace(f.groupByNamespace ?? true);
    // Per-node visibility is transient, not part of a saved view; reset it when
    // the projection/view changes so hidden nodes don't carry over.
    setHiddenNodeIds(new Set());
  };

  // Keep the panel filters in sync with the navbar selection: apply a view's
  // filters when one is active, and otherwise reset to the unfiltered default.
  // Keying on the projection means moving from one projection to another
  // without picking a view starts from a clean slate rather than carrying the
  // previous view's filters over. Manual filter edits don't touch activeView,
  // so they persist (this only runs when the projection or active view
  // changes).
  useEffect(() => {
    applyFilters(activeView ? activeView.filters : {});
    // applyFilters is stable for our purposes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projection.uid, activeView]);

  return (
    <div className="graph-panel">
      <FilterPanel
        kinds={kinds}
        hiddenKinds={hiddenKinds}
        onToggleKind={toggleKind}
        onShowAll={showAll}
        onHideAll={hideAll}
        namespaces={namespaces}
        hiddenNamespaces={hiddenNamespaces}
        onToggleNamespace={toggleNamespace}
        onShowAllNamespaces={showAllNamespaces}
        onHideAllNamespaces={hideAllNamespaces}
        hasSelection={selectedNode !== null}
        maxDistance={maxDistance}
        onChangeDistance={setMaxDistance}
        groupByNamespace={groupByNamespace}
        onToggleGroupByNamespace={setGroupByNamespace}
        labelFilters={labelFilters}
        labelMode={labelMode}
        onAddLabel={addLabel}
        onUpdateLabel={updateLabel}
        onRemoveLabel={removeLabel}
        onChangeLabelMode={setLabelMode}
        collapsed={filtersCollapsed}
        onToggleCollapse={() => setFiltersCollapsed((v) => !v)}
        viewControls={
          <ViewControls
            projection={projection}
            currentFilters={currentFilters}
            activeView={activeView}
            onActiveViewChange={onActiveViewChange}
          />
        }
      />
      <div
        className="graph-area"
        style={
          settings.wallpaper
            ? {
                backgroundImage: `url(${settings.wallpaper})`,
                backgroundSize: "cover",
                backgroundPosition: "center",
                backgroundRepeat: "no-repeat",
              }
            : undefined
        }
      >
        {isLoading && (
          <Group gap="xs" p="md">
            <Loader size="sm" />
            <Text c="dimmed">Loading graph…</Text>
          </Group>
        )}
        {error && (
          <Text c="red" p="md">
            {(error as Error).message}
          </Text>
        )}
        {filteredGraph && (
          <GraphView
            graph={filteredGraph}
            onToggleVisibility={toggleNodeVisibility}
            hiddenIds={hiddenNodeIds}
            onSelect={handleSelect}
            onSelectedIdsChange={(ids) => setSelectedIds(new Set(ids))}
            selectedId={selectedNode?.id ?? null}
            maxDistance={maxDistance}
            onShowYaml={setYamlNode}
            groupByNamespace={groupByNamespace}
            showEdgeLabels={showEdgeLabels}
            onAddLink={(from, to) => {
              createLink(projection.namespace, projection.name, from, to)
                .then(() =>
                  queryClient.invalidateQueries({ queryKey: ["graph", projection.uid] }),
                )
                .catch(() => {
                  // Surfacing failures in the UI can come later; for now the
                  // graph simply won't gain the edge.
                });
            }}
            onDeleteLink={(edge) => {
              deleteLink(projection.namespace, projection.name, edge.source, edge.target, edge.type)
                .then(() =>
                  queryClient.invalidateQueries({ queryKey: ["graph", projection.uid] }),
                )
                .catch(() => {
                  // Best-effort; the edge stays if the delete fails.
                });
            }}
            exportName={`${projection.namespace}-${projection.name}`}
          />
        )}
        {edgeTypes.length > 0 && (
          <EdgeLegend
            types={edgeTypes}
            showLabels={showEdgeLabels}
            onToggleLabels={() => update({ showEdgeLabels: !showEdgeLabels })}
          />
        )}
      </div>
      <YamlModal node={yamlNode} onClose={() => setYamlNode(null)} />
      {inspectorCollapsed ? (
        <aside className="inspector inspector-collapsed">
          <Tooltip label="Expand panel" position="left">
            <ActionIcon
              variant="subtle"
              color="gray"
              onClick={() => setInspectorCollapsed(false)}
              aria-label="Expand panel"
            >
              <IconChevronLeft size={18} />
            </ActionIcon>
          </Tooltip>
        </aside>
      ) : (
        (() => {
          const showDetails =
            !showResourceList && (selection?.type === "edge" || !!selectedNode);
          return (
            <aside className="inspector">
              <div className="inspector-header">
                {showDetails ? (
                  <Button
                    variant="subtle"
                    color="gray"
                    size="compact-sm"
                    leftSection={<IconArrowLeft size={16} />}
                    onClick={() => setShowResourceList(true)}
                  >
                    Back
                  </Button>
                ) : (
                  <span />
                )}
                <Tooltip label="Collapse panel" position="left">
                  <ActionIcon
                    variant="subtle"
                    color="gray"
                    onClick={() => setInspectorCollapsed(true)}
                    aria-label="Collapse panel"
                  >
                    <IconChevronRight size={18} />
                  </ActionIcon>
                </Tooltip>
              </div>
              <ScrollArea className="inspector-body" type="scroll">
                <Box p={14}>
                  {showDetails && selection?.type === "edge" ? (
                    <EdgeDetails selection={selection} />
                  ) : showDetails ? (
                    <NodeDetails node={selectedNode} />
                  ) : (
                    <ResourceList
                      nodes={filteredGraph?.nodes ?? []}
                      selectedIds={selectedIds}
                      hiddenIds={hiddenNodeIds}
                      onSelect={(node) => handleSelect({ type: "node", node })}
                      onToggleVisibility={toggleNodeVisibility}
                    />
                  )}
                </Box>
              </ScrollArea>
            </aside>
          );
        })()
      )}
    </div>
  );
}

export default function App() {
  const [selected, setSelected] = useState<Projection>();
  // The saved view currently applied (null = custom/unsaved filters).
  const [activeView, setActiveView] = useState<View | null>(null);
  // Whether the settings modal is open.
  const [settingsOpen, setSettingsOpen] = useState(false);

  const selectProjection = (p: Projection) => {
    setSelected(p);
    setActiveView(null);
  };
  const selectView = (p: Projection, v: View) => {
    setSelected(p);
    setActiveView(v);
  };

  return (
    <AppShell header={{ height: 52 }} navbar={{ width: 260, breakpoint: "sm" }} padding={0}>
      <AppShell.Header>
        <Group h="100%" px="md" gap="sm" align="center" justify="space-between" wrap="nowrap">
          <Group gap="sm" align="center" wrap="nowrap">
            <IconTopologyStar3 size={22} stroke={1.5} color="var(--mantine-color-brand-6)" />
            <Title order={1} size="h4" c="white">
              Project Gamera
            </Title>
            <Text size="xs" c="dimmed">
              Kubernetes Cluster Graph
            </Text>
          </Group>
          <Group gap={4} align="center" wrap="nowrap">
            <Tooltip label="API reference" position="bottom" withArrow>
              <ActionIcon
                component="a"
                href="/api/docs"
                target="_blank"
                rel="noopener noreferrer"
                variant="subtle"
                color="gray"
                size="lg"
                aria-label="API reference"
              >
                <IconFileCode size={20} stroke={1.5} />
              </ActionIcon>
            </Tooltip>
            <Tooltip label="Settings" position="bottom" withArrow>
              <ActionIcon
                variant="subtle"
                color="gray"
                size="lg"
                aria-label="Settings"
                onClick={() => setSettingsOpen(true)}
              >
                <IconSettings size={20} stroke={1.5} />
              </ActionIcon>
            </Tooltip>
          </Group>
        </Group>
      </AppShell.Header>

      <AppShell.Navbar p="md">
        <Text size="xs" fw={700} tt="uppercase" c="dimmed" mb="sm" style={{ letterSpacing: "0.08em" }}>
          Projections
        </Text>
        <AppShell.Section grow component={ScrollArea}>
          <ProjectionList
            selected={selected}
            activeView={activeView}
            onSelectProjection={selectProjection}
            onSelectView={selectView}
          />
        </AppShell.Section>
      </AppShell.Navbar>

      <AppShell.Main className="app-main">
        {selected ? (
          <GraphPanel
            projection={selected}
            activeView={activeView}
            onActiveViewChange={setActiveView}
          />
        ) : (
          <Text c="dimmed" p="md">
            Choose a projection to view its graph.
          </Text>
        )}
      </AppShell.Main>

      <SettingsModal opened={settingsOpen} onClose={() => setSettingsOpen(false)} />
    </AppShell>
  );
}
