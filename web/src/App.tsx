import { useMemo, useState } from "react";
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
import { getGraph, listProjections, type Graph, type GraphNode, type Projection } from "./api";
import { GraphView } from "./GraphView";
import {
  FilterPanel,
  kindCounts,
  type LabelFilter,
  type LabelMatchMode,
} from "./Filters";
import { YamlModal } from "./YamlModal";
import { SettingsModal } from "./SettingsModal";
import { useSettings } from "./settings";
import { IconHierarchy2, IconSettings, IconTopologyStar3 } from "./icons";

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
        <NavLink
          key={p.uid}
          active={selected?.uid === p.uid}
          onClick={() => onSelect(p)}
          leftSection={<IconHierarchy2 size={16} stroke={1.5} />}
          label={<Text fw={600}>{p.name}</Text>}
          description={`${p.namespace} · ${p.phase ?? "—"} · ${p.nodeCount}n / ${p.relationshipCount}e`}
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
  return (
    <Stack gap="md">
      <Title order={3} size="h4">
        {node.kind}{" "}
        <Text span c="dimmed" size="sm" fw={400}>
          {node.apiVersion}
        </Text>
      </Title>
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

function GraphPanel({ projection }: { projection: Projection }) {
  const { settings } = useSettings();
  const [selected, setSelected] = useState<GraphNode | null>(null);
  // Node whose YAML manifest is shown in the modal (null = closed).
  const [yamlNode, setYamlNode] = useState<GraphNode | null>(null);
  // Kinds the user has hidden. Empty = show everything (the default).
  const [hiddenKinds, setHiddenKinds] = useState<Set<string>>(new Set());
  // Max hops from the selected node to keep visible; null = all (no fading).
  const [maxDistance, setMaxDistance] = useState<number | null>(null);
  // Whether to group resources into compound nodes by namespace.
  const [groupByNamespace, setGroupByNamespace] = useState(true);
  // Label filters and the AND/OR mode used to combine them.
  const [labelFilters, setLabelFilters] = useState<LabelFilter[]>([]);
  const [labelMode, setLabelMode] = useState<LabelMatchMode>("any");
  const { data, isLoading, error } = useQuery({
    queryKey: ["graph", projection.uid],
    queryFn: () => getGraph(projection.namespace, projection.name),
    refetchInterval: 10_000,
  });

  const kinds = useMemo(() => (data ? kindCounts(data) : []), [data]);

  const filteredGraph = useMemo<Graph | undefined>(() => {
    if (!data) return undefined;
    const hasLabelFilter = labelFilters.some((f) => f.key.trim() !== "");
    if (hiddenKinds.size === 0 && !hasLabelFilter) return data;
    const nodes = data.nodes.filter(
      (n) => !hiddenKinds.has(n.kind) && matchesLabelFilters(n, labelFilters, labelMode),
    );
    const visibleIds = new Set(nodes.map((n) => n.id));
    const edges = data.edges.filter(
      (e) => visibleIds.has(e.source) && visibleIds.has(e.target),
    );
    return { nodes, edges };
  }, [data, hiddenKinds, labelFilters, labelMode]);

  const toggleKind = (kind: string) =>
    setHiddenKinds((prev) => {
      const next = new Set(prev);
      if (next.has(kind)) next.delete(kind);
      else next.add(kind);
      return next;
    });
  const showAll = () => setHiddenKinds(new Set());
  const hideAll = () => setHiddenKinds(new Set(kinds.map((k) => k.kind)));

  const addLabel = () =>
    setLabelFilters((prev) => [...prev, { id: crypto.randomUUID(), key: "", value: "" }]);
  const updateLabel = (id: string, patch: Partial<Pick<LabelFilter, "key" | "value">>) =>
    setLabelFilters((prev) => prev.map((f) => (f.id === id ? { ...f, ...patch } : f)));
  const removeLabel = (id: string) =>
    setLabelFilters((prev) => prev.filter((f) => f.id !== id));

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
        groupByNamespace={groupByNamespace}
        onToggleGroupByNamespace={setGroupByNamespace}
        labelFilters={labelFilters}
        labelMode={labelMode}
        onAddLabel={addLabel}
        onUpdateLabel={updateLabel}
        onRemoveLabel={removeLabel}
        onChangeLabelMode={setLabelMode}
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
            onSelect={setSelected}
            selectedId={selected?.id ?? null}
            maxDistance={maxDistance}
            onShowYaml={setYamlNode}
            groupByNamespace={groupByNamespace}
          />
        )}
      </div>
      <YamlModal node={yamlNode} onClose={() => setYamlNode(null)} />
      <ScrollArea component="aside" className="inspector" type="scroll">
        <NodeDetails node={selected} />
      </ScrollArea>
    </div>
  );
}

export default function App() {
  const [selected, setSelected] = useState<Projection>();
  // Whether the settings modal is open.
  const [settingsOpen, setSettingsOpen] = useState(false);

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
          <ProjectionList selected={selected} onSelect={setSelected} />
        </AppShell.Section>
      </AppShell.Navbar>

      <AppShell.Main className="app-main">
        {selected ? (
          <GraphPanel projection={selected} />
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
