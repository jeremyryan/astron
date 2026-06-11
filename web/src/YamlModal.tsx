import { Code, Group, Loader, Modal, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { getResourceYaml, type GraphNode } from "./api";
import { IconFileCode } from "./icons";

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
        <Code
          block
          // Keep the manifest's own scrolling self-contained so a long line
          // never widens the dialog and pushes the close button out of view.
          style={{ maxWidth: "100%", maxHeight: "70vh", overflow: "auto", fontSize: 12 }}
        >
          {data}
        </Code>
      )}
    </Modal>
  );
}
