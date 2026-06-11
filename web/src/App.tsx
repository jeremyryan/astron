import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  ActionIcon,
  AppShell,
  Box,
  Group,
  Loader,
  NavLink,
  ScrollArea,
  Stack,
  Text,
  Title,
  Tooltip,
} from "@mantine/core";
import {
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
import { colorForRelationship, iconForKind } from "./kinds";
import {
  IconBookmark,
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

function NodeDetails({ node }: { node: GraphNode | null }) {
  if (!node)
    return (
      <Text size="sm" c="dimmed">
        Select a node to inspect it.
      </Text>
    );
  const props = Object.entries(node.properties ?? {});
  const scalarProps = props.filter(([k]) => !MAP_PROPS.has(k));
  const mapProps = props.filter(([k]) => MAP_PROPS.has(k));
  const icon = iconForKind(node.kind);
  return (
    <Stack gap="md">
      <Group gap={8} wrap="nowrap" align="center">
        {icon && <img src={icon} width={22} height={22} alt="" />}
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
          <Field key={k} label={k} value={typeof v === "string" ? v : JSON.stringify(v)} />
        ))}
      </Stack>
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
  const { settings } = useSettings();
  // The currently inspected element (node or edge), or null.
  const [selection, setSelection] = useState<GraphSelection | null>(null);
  const selectedNode = selection?.type === "node" ? selection.node : null;
  // Node whose YAML manifest is shown in the modal (null = closed).
  const [yamlNode, setYamlNode] = useState<GraphNode | null>(null);
  // Kinds the user has hidden. Empty = show everything (the default).
  const [hiddenKinds, setHiddenKinds] = useState<Set<string>>(new Set());
  // Namespaces the user has hidden ("" = cluster-scoped). Empty = show all.
  const [hiddenNamespaces, setHiddenNamespaces] = useState<Set<string>>(new Set());
  // Max hops from the selected node to keep visible; null = all (no fading).
  const [maxDistance, setMaxDistance] = useState<number | null>(null);
  // Whether to group resources into compound nodes by namespace.
  const [groupByNamespace, setGroupByNamespace] = useState(true);
  // Whether edge (relationship-type) labels are drawn on the graph.
  const [showEdgeLabels, setShowEdgeLabels] = useState(true);
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

  // Distinct relationship types currently visible, for the color legend.
  const edgeTypes = useMemo(() => {
    const set = new Set<string>();
    filteredGraph?.edges.forEach((e) => set.add(e.type));
    return [...set].sort();
  }, [filteredGraph]);

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
  };

  // When a saved view is selected in the navbar, apply its filters once.
  const appliedRef = useRef<View | null>(null);
  useEffect(() => {
    if (activeView && appliedRef.current !== activeView) {
      appliedRef.current = activeView;
      applyFilters(activeView.filters);
    }
    // applyFilters is stable for our purposes; only re-run when the view changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeView]);

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
            onSelect={setSelection}
            selectedId={selectedNode?.id ?? null}
            maxDistance={maxDistance}
            onShowYaml={setYamlNode}
            groupByNamespace={groupByNamespace}
            showEdgeLabels={showEdgeLabels}
            exportName={`${projection.namespace}-${projection.name}`}
          />
        )}
        {edgeTypes.length > 0 && (
          <EdgeLegend
            types={edgeTypes}
            showLabels={showEdgeLabels}
            onToggleLabels={() => setShowEdgeLabels((v) => !v)}
          />
        )}
      </div>
      <YamlModal node={yamlNode} onClose={() => setYamlNode(null)} />
      <ScrollArea component="aside" className="inspector" type="scroll">
        {selection?.type === "edge" ? (
          <EdgeDetails selection={selection} />
        ) : (
          <NodeDetails node={selectedNode} />
        )}
      </ScrollArea>
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
