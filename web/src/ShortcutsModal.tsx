import { Fragment } from "react";
import { Group, Kbd, Modal, Stack, Table, Text } from "@mantine/core";

// One shortcut row: the key combination(s) and what they do.
interface Shortcut {
  keys: string[];
  action: string;
}

const KEYBOARD_SHORTCUTS: Shortcut[] = [
  { keys: ["Esc"], action: "Deselect everything / cancel link creation" },
  { keys: ["↑", "↓", "←", "→"], action: "Nudge the selected node(s); hold Shift for a larger step" },
  { keys: ["Y"], action: "Show the YAML manifest of the selected node" },
  { keys: ["L"], action: "Start creating a link from the selected node" },
  { keys: ["H"], action: "Hide the selected node(s) from the graph" },
  {
    keys: ["Shift", "H"],
    action: "Hide all nodes except the selection; with nothing selected, unhide all nodes",
  },
  { keys: ["C"], action: "Center the selected node(s) in the view" },
  { keys: ["E"], action: "Expand the selection to the directly-connected nodes" },
  {
    keys: ["J"],
    action: "Join the selected nodes: select the nodes along the shortest path between each pair",
  },
  { keys: ["A"], action: "Select all nodes connected to the selection, directly or indirectly" },
  { keys: ["Shift", "D"], action: "Deselect all nodes" },
  { keys: ["*"], action: "Arrange the selected node's neighbors in a circle around it" },
  { keys: ["Shift", "+"], action: "Zoom in" },
  { keys: ["Shift", "−"], action: "Zoom out" },
  { keys: ["Shift", "0"], action: "Reset the zoom to fit the whole graph" },
  {
    keys: ["Ctrl/Cmd", "↑", "↓", "←", "→"],
    action: "Pan the view, like dragging the background; hold Shift for a larger step",
  },
  { keys: ["F"], action: "Fit every visible node to the view" },
  {
    keys: ["Alt", "H"],
    action: "Reveal any hidden immediate neighbors of the selected node(s)",
  },
];

const MOUSE_GESTURES: Shortcut[] = [
  { keys: ["Click"], action: "Select a node or link" },
  { keys: ["Ctrl/Cmd", "Click"], action: "On a selected node: remove it from the selection" },
  { keys: ["Drag"], action: "Pan the canvas" },
  { keys: ["Scroll"], action: "Zoom in / out" },
  { keys: ["Shift", "Drag"], action: "On the background: draw a selection box" },
  {
    keys: ["Shift", "Drag"],
    action: "On a node: move the node(s), pulling directly-connected nodes along",
  },
  {
    keys: ["Ctrl/Cmd", "Drag"],
    action: "On a selected node (multiple selected): rotate the selection around its center",
  },
  { keys: ["Drag"], action: "On a link: rotate its target node around its source node" },
  { keys: ["Right-click"], action: "Open the context menu for a node or link" },
];

function ShortcutTable({ shortcuts }: { shortcuts: Shortcut[] }) {
  return (
    <Table verticalSpacing={6} withRowBorders={false}>
      <Table.Tbody>
        {shortcuts.map((s, i) => (
          <Table.Tr key={i}>
            <Table.Td w={160} style={{ whiteSpace: "nowrap" }}>
              <Group gap={4} wrap="nowrap">
                {s.keys.map((k, j) => (
                  <Fragment key={j}>
                    {j > 0 && (
                      <Text span size="xs" c="dimmed">
                        +
                      </Text>
                    )}
                    <Kbd size="xs">{k}</Kbd>
                  </Fragment>
                ))}
              </Group>
            </Table.Td>
            <Table.Td>
              <Text size="sm">{s.action}</Text>
            </Table.Td>
          </Table.Tr>
        ))}
      </Table.Tbody>
    </Table>
  );
}

// ShortcutsModal lists every keyboard shortcut and mouse gesture available on
// the graph view, opened from the help button in the header.
export function ShortcutsModal({ opened, onClose }: { opened: boolean; onClose: () => void }) {
  return (
    <Modal opened={opened} onClose={onClose} title="Keyboard shortcuts" size="lg">
      <Stack gap="md">
        <Text size="xs" c="dimmed">
          Single-key shortcuts act on the node(s) currently selected in the graph.
        </Text>
        <ShortcutTable shortcuts={KEYBOARD_SHORTCUTS} />
        <Text size="sm" fw={700} c="var(--accent-warm)">
          Mouse
        </Text>
        <ShortcutTable shortcuts={MOUSE_GESTURES} />
      </Stack>
    </Modal>
  );
}
