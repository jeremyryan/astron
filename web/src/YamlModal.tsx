import { ActionIcon, Box, Code, CopyButton, Group, Loader, Modal, Text, Tooltip } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { getResourceYaml, type GraphNode } from "./api";
import { IconCheck, IconCopy, IconFileCode } from "./icons";

// YamlModal displays the live YAML manifest for the given node. It is open while
// `node` is non-null and fetches the manifest lazily on open.
export function YamlModal({ node, onClose }: { node: GraphNode | null; onClose: () => void }) {
  const { data, isLoading, error } = useQuery({
    queryKey: ["yaml", node?.apiVersion, node?.kind, node?.namespace, node?.name],
    queryFn: () => getResourceYaml(node!),
    enabled: !!node,
  });

  const title = node ? (
    <Group gap={8} wrap="nowrap">
      <IconFileCode size={18} stroke={1.5} />
      <Text fw={600}>
        {node.kind} · {node.namespace ? `${node.namespace}/` : ""}
        {node.name}
      </Text>
    </Group>
  ) : (
    ""
  );

  return (
    <Modal opened={!!node} onClose={onClose} size="xl" title={title}>
      {isLoading && (
        <Group gap="xs">
          <Loader size="sm" />
          <Text c="dimmed">Loading manifest…</Text>
        </Group>
      )}
      {error && <Text c="red">{(error as Error).message}</Text>}
      {data && (
        <Box style={{ position: "relative" }}>
          <CopyButton value={data} timeout={1500}>
            {({ copied, copy }) => (
              <Tooltip label={copied ? "Copied" : "Copy YAML"} position="left" withArrow>
                <ActionIcon
                  variant="default"
                  size="md"
                  aria-label="Copy YAML to clipboard"
                  onClick={copy}
                  // Float over the manifest's top-right corner, clear of the
                  // vertical scrollbar.
                  style={{ position: "absolute", top: 6, right: 12, zIndex: 1 }}
                >
                  {copied ? (
                    <IconCheck size={16} stroke={1.5} color="var(--mantine-color-teal-5)" />
                  ) : (
                    <IconCopy size={16} stroke={1.5} />
                  )}
                </ActionIcon>
              </Tooltip>
            )}
          </CopyButton>
          <Code
            block
            // Keep the manifest's own scrolling self-contained so a long line
            // never widens the dialog and pushes the close button out of view.
            style={{ maxWidth: "100%", maxHeight: "70vh", overflow: "auto", fontSize: 12 }}
          >
            {data}
          </Code>
        </Box>
      )}
    </Modal>
  );
}
