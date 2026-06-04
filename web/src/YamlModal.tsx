import { Code, Group, Loader, Modal, ScrollArea, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { getResourceYaml, type GraphNode } from "./api";

// YamlModal displays the live YAML manifest for the given node. It is open while
// `node` is non-null and fetches the manifest lazily on open.
export function YamlModal({ node, onClose }: { node: GraphNode | null; onClose: () => void }) {
  const { data, isLoading, error } = useQuery({
    queryKey: ["yaml", node?.apiVersion, node?.kind, node?.namespace, node?.name],
    queryFn: () => getResourceYaml(node!),
    enabled: !!node,
  });

  const title = node
    ? `${node.kind} · ${node.namespace ? `${node.namespace}/` : ""}${node.name}`
    : "";

  return (
    <Modal
      opened={!!node}
      onClose={onClose}
      size="xl"
      title={title}
      scrollAreaComponent={ScrollArea.Autosize}
    >
      {isLoading && (
        <Group gap="xs">
          <Loader size="sm" />
          <Text c="dimmed">Loading manifest…</Text>
        </Group>
      )}
      {error && <Text c="red">{(error as Error).message}</Text>}
      {data && (
        <Code block style={{ maxHeight: "70vh", overflow: "auto", fontSize: 12 }}>
          {data}
        </Code>
      )}
    </Modal>
  );
}
