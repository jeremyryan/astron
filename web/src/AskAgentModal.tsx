import { useEffect, useRef, useState } from "react";
import { useQueries, useQuery } from "@tanstack/react-query";
import {
  ActionIcon,
  Group,
  Loader,
  Modal,
  ScrollArea,
  Select,
  Stack,
  Text,
  Textarea,
  Tooltip,
} from "@mantine/core";
import {
  askAgent,
  getChatModels,
  getResourceYaml,
  type ChatHistoryMessage,
  type GraphNode,
  type Projection,
} from "./api";
import { type ChatMessage, MessageBubble } from "./ChatPanel";
import { iconForKindOrGeneric } from "./kinds";
import { IconMessageChatbot, IconSend2 } from "./icons";
import { useSettings } from "./settings";

// Caps how many resources' manifests a single Ask Agent conversation attaches
// as context, bounding both the number of parallel YAML fetches and the size
// of the context sent with every question (full manifests add up quickly).
const MAX_CONTEXT_RESOURCES = 8;

// describeNode renders a short "Kind namespace/name" label for a resource.
function describeNode(n: GraphNode): string {
  return `${n.kind} ${n.namespace ? `${n.namespace}/` : ""}${n.name}`;
}

// AskAgentModal is a focused conversation with the tool-using chat agent (see
// docs/agent-design.md) about one or more resources: opened from the graph's
// right-click menu (a single node, or a multi-selection), it fetches each
// targeted resource's live YAML manifest and attaches all of them as context,
// so the agent can answer about their configuration and current status
// without the user having to paste anything in. It always calls the agent
// endpoint (/rag/agent), independent of the global "Agentic chat" setting,
// since attaching context is the point of this modal; the endpoint's own
// fallback (see Projector.AnswerWithTools) still preserves that context even
// when the resolved model can't call tools.
export function AskAgentModal({
  nodes,
  projection,
  onClose,
}: {
  // The resources to ask about; the modal is open while this is non-empty.
  nodes: GraphNode[];
  projection: Projection;
  onClose: () => void;
}) {
  const opened = nodes.length > 0;
  const truncated = nodes.length > MAX_CONTEXT_RESOURCES;
  const targetNodes = nodes.slice(0, MAX_CONTEXT_RESOURCES);
  // A stable key for the targeted set, to reset the conversation when it
  // changes (order-sensitive is fine: a different selection order is a
  // different "conversation" as far as the user is concerned).
  const targetKey = targetNodes.map((n) => n.id).join(",");

  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [input, setInput] = useState("");
  const [pending, setPending] = useState(false);
  const viewportRef = useRef<HTMLDivElement>(null);

  // Start a fresh conversation whenever the targeted resource(s) change.
  useEffect(() => {
    setMessages([]);
    setInput("");
    setPending(false);
    // Deliberately keyed only on targetKey (not targetNodes, which is a new
    // array reference every render).
  }, [targetKey]);

  // Each targeted resource's live manifest, fetched once per open. They are
  // sent as context with every question below but never themselves shown as
  // chat bubbles (the user can already inspect any one via "YAML").
  const manifestQueries = useQueries({
    queries: targetNodes.map((n) => ({
      queryKey: ["yaml", n.apiVersion, n.kind, n.namespace, n.name],
      queryFn: () => getResourceYaml(n),
      enabled: opened,
    })),
  });
  const manifestsLoading = manifestQueries.some((q) => q.isLoading);
  const manifestErrorCount = manifestQueries.filter((q) => q.error).length;
  const loadedManifests = targetNodes
    .map((n, i) => ({ node: n, manifest: manifestQueries[i]?.data }))
    .filter((m): m is { node: GraphNode; manifest: string } => !!m.manifest);

  // Models the user may pick from (per the projection's allowedModels policy).
  const { data: chatModels } = useQuery({
    queryKey: ["chat-models", projection.uid],
    queryFn: () => getChatModels(projection.namespace, projection.name),
    staleTime: 5 * 60_000,
    retry: false,
    enabled: opened,
  });
  const modelChoices = chatModels?.models ?? [];
  const projectionDefault = chatModels?.default ?? "";
  const { settings } = useSettings();
  const settingsDefault =
    settings.defaultChatModel && modelChoices.includes(settings.defaultChatModel)
      ? settings.defaultChatModel
      : null;
  const effectiveDefault = settingsDefault ?? (projectionDefault || modelChoices[0] || null);
  const [model, setModel] = useState<string | null>(null);
  const selectedModel = model && modelChoices.includes(model) ? model : effectiveDefault;

  useEffect(() => {
    viewportRef.current?.scrollTo({
      top: viewportRef.current.scrollHeight,
      behavior: "smooth",
    });
  }, [messages, pending]);

  const send = () => {
    const question = input.trim();
    if (!question || pending || !opened || manifestsLoading) return;
    setInput("");
    const priorTurns: ChatHistoryMessage[] = messages
      .filter((m) => m.role === "user" || m.role === "assistant")
      .map((m) => ({ role: m.role as "user" | "assistant", content: m.text }));
    // The manifest(s) are re-seeded ahead of the conversation on every
    // request (the API is stateless across calls), not just the first one.
    const seed: ChatHistoryMessage[] =
      loadedManifests.length === 0
        ? []
        : loadedManifests.length === 1
          ? [
              {
                role: "user",
                content:
                  `Here is the current live manifest for ${describeNode(loadedManifests[0].node)}, ` +
                  `for context on the question(s) below:\n\n\`\`\`yaml\n${loadedManifests[0].manifest}\n\`\`\``,
              },
            ]
          : [
              {
                role: "user",
                content:
                  `Here are the current live manifests for ${loadedManifests.length} resources, ` +
                  "for context on the question(s) below:\n\n" +
                  loadedManifests
                    .map(
                      ({ node, manifest }) =>
                        `### ${describeNode(node)}\n\n\`\`\`yaml\n${manifest}\n\`\`\``,
                    )
                    .join("\n\n"),
              },
            ];
    const history = [...seed, ...priorTurns];
    setMessages((prev) => [...prev, { id: crypto.randomUUID(), role: "user", text: question }]);
    setPending(true);
    const override =
      selectedModel && selectedModel !== projectionDefault ? selectedModel : undefined;
    askAgent(projection.namespace, projection.name, question, history, override)
      .then((answer) => {
        setMessages((prev) => [
          ...prev,
          {
            id: crypto.randomUUID(),
            role: "assistant",
            text: answer.answer,
            steps: answer.steps,
            stepBudgetExhausted: answer.stepBudgetExhausted,
          },
        ]);
      })
      .catch((err: Error) => {
        setMessages((prev) => [
          ...prev,
          { id: crypto.randomUUID(), role: "error", text: err.message },
        ]);
      })
      .finally(() => setPending(false));
  };

  const title =
    targetNodes.length === 1 ? (
      <Group gap={8} wrap="nowrap">
        <IconMessageChatbot size={18} stroke={1.5} />
        <Text fw={600} truncate>
          Ask Agent · {describeNode(targetNodes[0])}
        </Text>
      </Group>
    ) : targetNodes.length > 1 ? (
      <Group gap={8} wrap="nowrap">
        <IconMessageChatbot size={18} stroke={1.5} />
        <Text fw={600} truncate>
          Ask Agent · {targetNodes.length} resources
        </Text>
      </Group>
    ) : (
      ""
    );

  const caption = manifestsLoading
    ? targetNodes.length === 1
      ? "Loading the resource's live manifest…"
      : `Loading ${targetNodes.length} resources' live manifests…`
    : manifestErrorCount > 0
      ? `Couldn't load ${manifestErrorCount} of ${targetNodes.length} manifests; asking with ` +
        "the rest."
      : targetNodes.length === 1
        ? "The resource's live manifest is attached as context to every question below."
        : `The live manifests for these ${targetNodes.length} resources are attached as ` +
          "context to every question below.";

  const inputDisabled = pending || manifestsLoading;

  return (
    <Modal opened={opened} onClose={onClose} size="lg" title={title}>
      <Text size="xs" c="dimmed" mb={truncated || targetNodes.length > 1 ? 4 : "sm"}>
        {caption}
      </Text>
      {truncated && (
        <Text size="xs" c="orange" mb="sm">
          {nodes.length} resources were selected; only asking about the first {MAX_CONTEXT_RESOURCES}
          .
        </Text>
      )}
      {targetNodes.length > 1 && (
        <Group gap={6} mb="sm">
          {targetNodes.map((n) => (
            <Group key={n.id} gap={4} wrap="nowrap" className="ask-agent-chip">
              <img src={iconForKindOrGeneric(n.kind)} width={12} height={12} alt="" />
              <Text size="xs" c="dimmed" truncate maw={160}>
                {describeNode(n)}
              </Text>
            </Group>
          ))}
        </Group>
      )}
      <div className="chat-panel" style={{ height: "60vh" }}>
        <ScrollArea className="chat-messages" type="scroll" viewportRef={viewportRef}>
          <Stack gap="sm" p={14}>
            {messages.length === 0 && (
              <Text size="sm" c="dimmed">
                Ask a question about {targetNodes.length === 1 ? "this resource's" : "these resources'"}{" "}
                configuration or current status, e.g. “Why might this be failing to become ready?”
              </Text>
            )}
            {messages.map((m) => (
              <MessageBubble key={m.id} message={m} />
            ))}
            {pending && (
              <Group gap="xs" className="chat-bubble chat-bubble-assistant">
                <Loader size="xs" />
                <Text size="sm" c="dimmed">
                  Thinking…
                </Text>
              </Group>
            )}
          </Stack>
        </ScrollArea>
        <div className="chat-input-area">
          {modelChoices.length > 1 && (
            <Select
              size="xs"
              variant="unstyled"
              className="chat-model-select"
              aria-label="Chat model"
              data={modelChoices}
              value={selectedModel}
              onChange={setModel}
              allowDeselect={false}
              searchable={modelChoices.length > 8}
              comboboxProps={{ withinPortal: true }}
            />
          )}
          <div className="chat-input">
            <Textarea
              value={input}
              onChange={(e) => setInput(e.currentTarget.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey) {
                  e.preventDefault();
                  send();
                }
              }}
              placeholder={
                targetNodes.length === 1 ? "Ask about this resource…" : "Ask about these resources…"
              }
              autosize
              minRows={1}
              maxRows={5}
              style={{ flex: 1 }}
              disabled={inputDisabled}
            />
            <Tooltip label="Send" position="top" withArrow>
              <ActionIcon
                variant="filled"
                size="lg"
                aria-label="Send message"
                onClick={send}
                disabled={inputDisabled || input.trim() === ""}
              >
                <IconSend2 size={18} stroke={1.5} />
              </ActionIcon>
            </Tooltip>
          </div>
        </div>
      </div>
    </Modal>
  );
}
