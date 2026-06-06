import {
  Badge,
  Box,
  Button,
  Checkbox,
  Group,
  NumberInput,
  Stack,
  Text,
} from "@mantine/core";
import type { Graph } from "./api";
import { colorForKind } from "./kinds";
import { IconEye, IconEyeOff, IconFilter } from "./icons";

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
  // Kinds the user has hidden. Empty means everything is shown (the default).
  hiddenKinds: Set<string>;
  onToggleKind: (kind: string) => void;
  onShowAll: () => void;
  onHideAll: () => void;
  // Connection-distance filter (relative to the selected node).
  hasSelection: boolean;
  // Max hops from the selected node to keep visible; null = all (no fading).
  maxDistance: number | null;
  onChangeDistance: (value: number | null) => void;
  // Whether resources are grouped into namespace boxes.
  groupByNamespace: boolean;
  onToggleGroupByNamespace: (value: boolean) => void;
}

const MAX_DISTANCE = 9;

export function FilterPanel({
  kinds,
  hiddenKinds,
  onToggleKind,
  onShowAll,
  onHideAll,
  hasSelection,
  maxDistance,
  onChangeDistance,
  groupByNamespace,
  onToggleGroupByNamespace,
}: Props) {
  const visibleCount = kinds.filter((k) => !hiddenKinds.has(k.kind)).length;
  const filtering = hiddenKinds.size > 0;

  return (
    <Box component="aside" className="filters">
      <Stack gap="lg">
        <Group gap={6} align="center">
          <IconFilter size={14} stroke={1.5} />
          <Text size="xs" fw={700} tt="uppercase" c="dimmed">
            Filters
          </Text>
        </Group>

        {/* Resource types */}
        <Stack gap="xs">
          <Group justify="space-between" align="center" wrap="nowrap">
            <Group gap={6} wrap="nowrap">
              <Text size="sm" fw={600}>
                Resource types
              </Text>
              {filtering && (
                <Badge size="sm" variant="light" color="gray">
                  {visibleCount}/{kinds.length}
                </Badge>
              )}
            </Group>
            <Button.Group>
              <Button
                size="compact-xs"
                variant="default"
                leftSection={<IconEye size={13} stroke={1.5} />}
                onClick={onShowAll}
                disabled={hiddenKinds.size === 0}
              >
                All
              </Button>
              <Button
                size="compact-xs"
                variant="default"
                leftSection={<IconEyeOff size={13} stroke={1.5} />}
                onClick={onHideAll}
                disabled={visibleCount === 0}
              >
                None
              </Button>
            </Button.Group>
          </Group>

          {kinds.length === 0 ? (
            <Text size="sm" c="dimmed">
              No resources.
            </Text>
          ) : (
            <Stack gap={6}>
              {kinds.map(({ kind, count }) => (
                <Checkbox
                  key={kind}
                  size="xs"
                  checked={!hiddenKinds.has(kind)}
                  onChange={() => onToggleKind(kind)}
                  styles={{ labelWrapper: { flex: 1 } }}
                  label={
                    <Group justify="space-between" wrap="nowrap" gap={8}>
                      <Group gap={8} wrap="nowrap">
                        <Box
                          w={10}
                          h={10}
                          style={{
                            borderRadius: 2,
                            background: colorForKind(kind),
                            flex: "0 0 auto",
                          }}
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
        </Stack>

        {/* Connection distance */}
        <Stack gap="xs">
          <Text size="sm" fw={600}>
            Connection distance
          </Text>
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

        {/* Grouping */}
        <Stack gap="xs">
          <Text size="sm" fw={600}>
            Grouping
          </Text>
          <Checkbox
            size="xs"
            label="Group by namespace"
            checked={groupByNamespace}
            onChange={(e) => onToggleGroupByNamespace(e.currentTarget.checked)}
          />
        </Stack>
      </Stack>
    </Box>
  );
}
