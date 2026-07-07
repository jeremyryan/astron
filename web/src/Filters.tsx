import { useState, type ReactNode } from "react";
import {
  ActionIcon,
  Badge,
  Box,
  Button,
  Checkbox,
  Collapse,
  Group,
  NumberInput,
  SegmentedControl,
  Stack,
  Text,
  TextInput,
  Tooltip,
  UnstyledButton,
} from "@mantine/core";
import type { Graph } from "./api";
import { iconForKindOrGeneric } from "./kinds";
import {
  IconChevronLeft,
  IconChevronRight,
  IconEye,
  IconEyeOff,
  IconFilter,
  IconPlus,
  IconX,
} from "./icons";

// A single label filter row. An empty value matches any value for the key.
export interface LabelFilter {
  id: string;
  key: string;
  value: string;
}

// Combine label filters with OR ("any") or AND ("all") logic.
export type LabelMatchMode = "any" | "all";

export interface NamespaceCount {
  // Empty string represents cluster-scoped resources.
  namespace: string;
  count: number;
}

// namespaceCounts returns the distinct namespaces present in a graph with their
// node counts, sorted alphabetically (cluster-scoped sorts first).
export function namespaceCounts(graph: Graph): NamespaceCount[] {
  const counts = new Map<string, number>();
  for (const n of graph.nodes) {
    const ns = n.namespace ?? "";
    counts.set(ns, (counts.get(ns) ?? 0) + 1);
  }
  return [...counts.entries()]
    .map(([namespace, count]) => ({ namespace, count }))
    .sort((a, b) => a.namespace.localeCompare(b.namespace));
}

export interface KindCount {
  kind: string;
  count: number;
}

// kindCounts returns the distinct kinds present in a graph with their node
// counts, sorted alphabetically.
export function kindCounts(graph: Graph): KindCount[] {
  const counts = new Map<string, number>();
  for (const n of graph.nodes) {
    counts.set(n.kind, (counts.get(n.kind) ?? 0) + 1);
  }
  return [...counts.entries()]
    .map(([kind, count]) => ({ kind, count }))
    .sort((a, b) => a.kind.localeCompare(b.kind));
}

interface Props {
  kinds: KindCount[];
  // Kind-filter mode: "hide" (hide-list) or "show" (allow-list).
  kindMode: "hide" | "show";
  onChangeKindMode: (mode: "hide" | "show") => void;
  // Kinds the user has hidden (hide mode). Empty means everything is shown.
  hiddenKinds: Set<string>;
  // Kinds the user has chosen to show (show / allow-list mode).
  visibleKinds: Set<string>;
  onToggleKind: (kind: string) => void;
  onShowAll: () => void;
  onHideAll: () => void;
  // Namespace filtering (only meaningful when more than one namespace exists).
  namespaces: NamespaceCount[];
  hiddenNamespaces: Set<string>;
  onToggleNamespace: (namespace: string) => void;
  onShowAllNamespaces: () => void;
  onHideAllNamespaces: () => void;
  // Connection-distance filter (relative to the selected node).
  hasSelection: boolean;
  // Max hops from the selected node to keep visible; null = all (no fading).
  maxDistance: number | null;
  onChangeDistance: (value: number | null) => void;
  // Whether resources are grouped into namespace boxes.
  groupByNamespace: boolean;
  onToggleGroupByNamespace: (value: boolean) => void;
  // Label filtering.
  labelFilters: LabelFilter[];
  labelMode: LabelMatchMode;
  onAddLabel: () => void;
  onUpdateLabel: (id: string, patch: Partial<Pick<LabelFilter, "key" | "value">>) => void;
  onRemoveLabel: (id: string) => void;
  onChangeLabelMode: (mode: LabelMatchMode) => void;
  // Saved-view controls (Save / Delete / Save as…) rendered at the top.
  viewControls?: ReactNode;
  // Whether the panel is collapsed to a thin strip, and a toggle for it.
  collapsed: boolean;
  onToggleCollapse: () => void;
}

const MAX_DISTANCE = 9;

// Compact icon-only "show all" / "hide all" controls used by the section headers.
function ShowHideButtons({
  onShowAll,
  onHideAll,
  showDisabled,
  hideDisabled,
}: {
  onShowAll: () => void;
  onHideAll: () => void;
  showDisabled: boolean;
  hideDisabled: boolean;
}) {
  return (
    <ActionIcon.Group>
      <Tooltip label="Show all" withArrow>
        <ActionIcon variant="default" size="sm" aria-label="Show all" onClick={onShowAll} disabled={showDisabled}>
          <IconEye size={14} stroke={1.5} />
        </ActionIcon>
      </Tooltip>
      <Tooltip label="Hide all" withArrow>
        <ActionIcon variant="default" size="sm" aria-label="Hide all" onClick={onHideAll} disabled={hideDisabled}>
          <IconEyeOff size={14} stroke={1.5} />
        </ActionIcon>
      </Tooltip>
    </ActionIcon.Group>
  );
}

// FilterSection is a collapsible filter group: a clickable title row (with a
// rotating chevron) toggles the body. An optional `actions` slot on the right
// (e.g. show-all/hide-all or an Any/All control) does not toggle the section.
function FilterSection({
  title,
  badge,
  actions,
  defaultOpen = true,
  children,
}: {
  title: string;
  badge?: ReactNode;
  actions?: ReactNode;
  defaultOpen?: boolean;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <Stack gap="xs">
      <Group justify="space-between" align="center" wrap="nowrap" gap={6}>
        <UnstyledButton
          onClick={() => setOpen((o) => !o)}
          aria-expanded={open}
          style={{ flex: 1, minWidth: 0 }}
        >
          <Group gap={6} wrap="nowrap">
            <IconChevronRight
              size={14}
              stroke={2}
              style={{
                flex: "0 0 auto",
                transition: "transform 150ms ease",
                transform: open ? "rotate(90deg)" : "none",
              }}
            />
            <Text size="sm" fw={600}>
              {title}
            </Text>
            {badge}
          </Group>
        </UnstyledButton>
        {actions}
      </Group>
      <Collapse expanded={open}>{children}</Collapse>
    </Stack>
  );
}

export function FilterPanel({
  kinds,
  kindMode,
  onChangeKindMode,
  hiddenKinds,
  visibleKinds,
  onToggleKind,
  onShowAll,
  onHideAll,
  namespaces,
  hiddenNamespaces,
  onToggleNamespace,
  onShowAllNamespaces,
  onHideAllNamespaces,
  hasSelection,
  maxDistance,
  onChangeDistance,
  groupByNamespace,
  onToggleGroupByNamespace,
  labelFilters,
  labelMode,
  onAddLabel,
  onUpdateLabel,
  onRemoveLabel,
  onChangeLabelMode,
  viewControls,
  collapsed,
  onToggleCollapse,
}: Props) {
  const kindVisible = (kind: string) =>
    kindMode === "show" ? visibleKinds.has(kind) : !hiddenKinds.has(kind);
  const visibleCount = kinds.filter((k) => kindVisible(k.kind)).length;
  const filtering = visibleCount < kinds.length;
  const visibleNamespaces = namespaces.filter((n) => !hiddenNamespaces.has(n.namespace)).length;
  const nsFiltering = hiddenNamespaces.size > 0;

  return (
    <Box component="aside" className={collapsed ? "filters filters-collapsed" : "filters"}>
      {collapsed ? (
        <div className="filters-collapsed-inner">
          <Tooltip label="Expand filters" position="right">
            <ActionIcon
              variant="subtle"
              color="gray"
              onClick={onToggleCollapse}
              aria-label="Expand filters"
            >
              <IconChevronRight size={18} />
            </ActionIcon>
          </Tooltip>
        </div>
      ) : (
        <div className="filters-scroll">
      <Stack gap="lg">
        <Group gap={6} align="center" justify="space-between" wrap="nowrap">
          <Group gap={6} align="center">
            <IconFilter size={14} stroke={1.5} />
            <Text size="xs" fw={700} tt="uppercase" c="dimmed">
              Filters
            </Text>
          </Group>
          <Tooltip label="Collapse filters" position="right">
            <ActionIcon
              variant="subtle"
              color="gray"
              onClick={onToggleCollapse}
              aria-label="Collapse filters"
            >
              <IconChevronLeft size={18} />
            </ActionIcon>
          </Tooltip>
        </Group>

        {/* Views (saved filter sets) */}
        {viewControls && <FilterSection title="View">{viewControls}</FilterSection>}

        {/* Resource types */}
        <FilterSection
          title="Resource types"
          badge={
            filtering ? (
              <Badge size="sm" variant="light" color="gray">
                {visibleCount}/{kinds.length}
              </Badge>
            ) : null
          }
          actions={
            <ShowHideButtons
              onShowAll={onShowAll}
              onHideAll={onHideAll}
              showDisabled={visibleCount === kinds.length}
              hideDisabled={visibleCount === 0}
            />
          }
        >
          {kinds.length === 0 ? (
            <Text size="sm" c="dimmed">
              No resources.
            </Text>
          ) : (
            <Stack gap={6}>
              <SegmentedControl
                size="xs"
                fullWidth
                value={kindMode}
                onChange={(v) => onChangeKindMode(v as "hide" | "show")}
                data={[
                  { label: "Hide-list", value: "hide" },
                  { label: "Allow-list", value: "show" },
                ]}
              />
              <Text size="xs" c="dimmed">
                {kindMode === "show"
                  ? "Only checked kinds are shown; new kinds stay hidden."
                  : "All kinds are shown except those unchecked."}
              </Text>
              {kinds.map(({ kind, count }) => (
                <Checkbox
                  key={kind}
                  size="xs"
                  checked={kindVisible(kind)}
                  onChange={() => onToggleKind(kind)}
                  styles={{ labelWrapper: { flex: 1 } }}
                  label={
                    <Group justify="space-between" wrap="nowrap" gap={8}>
                      <Group gap={8} wrap="nowrap">
                        <img
                          src={iconForKindOrGeneric(kind)}
                          width={16}
                          height={16}
                          alt=""
                          style={{ flex: "0 0 auto" }}
                        />
                        <Text size="sm">{kind}</Text>
                      </Group>
                      <Text size="xs" c="dimmed">
                        {count}
                      </Text>
                    </Group>
                  }
                />
              ))}
            </Stack>
          )}
        </FilterSection>

        {/* Namespaces (shown only when more than one is present) */}
        {namespaces.length > 1 && (
          <FilterSection
            title="Namespaces"
            badge={
              nsFiltering ? (
                <Badge size="sm" variant="light" color="gray">
                  {visibleNamespaces}/{namespaces.length}
                </Badge>
              ) : null
            }
            actions={
              <ShowHideButtons
                onShowAll={onShowAllNamespaces}
                onHideAll={onHideAllNamespaces}
                showDisabled={hiddenNamespaces.size === 0}
                hideDisabled={visibleNamespaces === 0}
              />
            }
          >
            <Stack gap={6}>
              {namespaces.map(({ namespace, count }) => (
                <Checkbox
                  key={namespace || "__cluster__"}
                  size="xs"
                  checked={!hiddenNamespaces.has(namespace)}
                  onChange={() => onToggleNamespace(namespace)}
                  styles={{ labelWrapper: { flex: 1 } }}
                  label={
                    <Group justify="space-between" wrap="nowrap" gap={8}>
                      <Text
                        size="sm"
                        c={namespace ? undefined : "dimmed"}
                        style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
                      >
                        {namespace || "(cluster-scoped)"}
                      </Text>
                      <Text size="xs" c="dimmed">
                        {count}
                      </Text>
                    </Group>
                  }
                />
              ))}
            </Stack>
          </FilterSection>
        )}

        {/* Connection distance */}
        <FilterSection title="Connection distance">
          <Stack gap="xs">
          <Checkbox
            size="xs"
            label="All connections"
            checked={maxDistance === null}
            onChange={(e) => onChangeDistance(e.currentTarget.checked ? null : 2)}
          />
          <NumberInput
            size="xs"
            min={1}
            max={MAX_DISTANCE}
            clampBehavior="strict"
            disabled={maxDistance === null}
            value={maxDistance ?? 2}
            onChange={(v) => onChangeDistance(typeof v === "number" ? v : 2)}
            suffix=" hops"
            allowDecimal={false}
            allowNegative={false}
          />
          {maxDistance !== null && !hasSelection && (
            <Text size="xs" c="dimmed">
              Select a node to apply.
            </Text>
          )}
          </Stack>
        </FilterSection>

        {/* Labels */}
        <FilterSection
          title="Labels"
          actions={
            <SegmentedControl
              size="xs"
              value={labelMode}
              onChange={(v) => onChangeLabelMode(v as LabelMatchMode)}
              data={[
                { label: "Any", value: "any" },
                { label: "All", value: "all" },
              ]}
            />
          }
        >
          <Stack gap="xs">
          {labelFilters.length === 0 ? (
            <Text size="xs" c="dimmed">
              No label filters.
            </Text>
          ) : (
            <Stack gap={6}>
              {labelFilters.map((f) => (
                <Group key={f.id} gap={4} wrap="nowrap" align="center">
                  <TextInput
                    size="xs"
                    placeholder="key"
                    value={f.key}
                    onChange={(e) => onUpdateLabel(f.id, { key: e.currentTarget.value })}
                    style={{ flex: 1, minWidth: 0 }}
                  />
                  <Text size="xs" c="dimmed">
                    =
                  </Text>
                  <TextInput
                    size="xs"
                    placeholder="value"
                    value={f.value}
                    onChange={(e) => onUpdateLabel(f.id, { value: e.currentTarget.value })}
                    style={{ flex: 1, minWidth: 0 }}
                  />
                  <ActionIcon
                    size="sm"
                    variant="subtle"
                    color="gray"
                    aria-label="Remove label filter"
                    onClick={() => onRemoveLabel(f.id)}
                  >
                    <IconX size={14} stroke={1.5} />
                  </ActionIcon>
                </Group>
              ))}
              <Text size="xs" c="dimmed">
                Leave a value empty to match any value for that key.
              </Text>
            </Stack>
          )}

          <Button
            size="compact-xs"
            variant="default"
            leftSection={<IconPlus size={13} stroke={1.5} />}
            onClick={onAddLabel}
            style={{ alignSelf: "flex-start" }}
          >
            Add label
          </Button>
          </Stack>
        </FilterSection>

        {/* Grouping */}
        <FilterSection title="Grouping">
          <Checkbox
            size="xs"
            label="Group by namespace"
            checked={groupByNamespace}
            onChange={(e) => onToggleGroupByNamespace(e.currentTarget.checked)}
          />
        </FilterSection>
      </Stack>
        </div>
      )}
    </Box>
  );
}
