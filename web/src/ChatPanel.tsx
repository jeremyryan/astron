import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  ActionIcon,
  Box,
  Group,
  Loader,
  ScrollArea,
  Select,
  Stack,
  Text,
  Textarea,
  Tooltip,
  UnstyledButton,
} from "@mantine/core";
import {
  askAgent,
  askQuestion,
  getChatModels,
  type AgentStep,
  type AnswerCard,
  type ChatHistoryMessage,
  type Projection,
} from "./api";
import { iconForKindOrGeneric } from "./kinds";
import { IconSend2, IconTool } from "./icons";
import { useSettings } from "./settings";

// A single entry in the conversation. Assistant messages carry the resource
// cards that grounded the answer so they can be listed as sources, or (for
// agentic answers) the tool calls the agent made while working it out.
interface ChatMessage {
  id: string;
  role: "user" | "assistant" | "error";
  text: string;
  sources?: AnswerCard[];
  steps?: AgentStep[];
  stepBudgetExhausted?: boolean;
}

// SourceList renders the resources that grounded an answer as a compact list.
// Clicking a source selects the corresponding node in the graph.
function SourceList({
  cards,
  onSelectSource,
}: {
  cards: AnswerCard[];
  onSelectSource?: (card: AnswerCard) => void;
}) {
  if (cards.length === 0) return null;
  return (
    <Stack gap={2} mt={6}>
      <Text size="xs" c="dimmed" tt="uppercase" style={{ letterSpacing: "0.05em" }}>
        Sources
      </Text>
      {cards.map((c) => {
        const label = `${c.kind} ${c.namespace ? `${c.namespace}/` : ""}${c.name}`;
        return (
          <UnstyledButton
            key={c.id}
            className="chat-source"
            onClick={() => onSelectSource?.(c)}
            title={label}
          >
            <Group gap={6} wrap="nowrap" align="center">
              <img src={iconForKindOrGeneric(c.kind)} width={12} height={12} alt="" />
              <Text size="xs" c="dimmed" truncate>
                {label}
              </Text>
            </Group>
          </UnstyledButton>
        );
      })}
    </Stack>
  );
}

// StepList renders the tool calls a chat agent made while working out an
// answer, for transparency into what it did (and with what arguments).
function StepList({ steps, stepBudgetExhausted }: { steps: AgentStep[]; stepBudgetExhausted?: boolean }) {
  if (steps.length === 0) return null;
  return (
    <Stack gap={2} mt={6}>
      <Text size="xs" c="dimmed" tt="uppercase" style={{ letterSpacing: "0.05em" }}>
        Tool activity
      </Text>
      {steps.map((s, i) => (
        <Group key={i} gap={6} wrap="nowrap" align="flex-start">
          <IconTool size={12} color="var(--muted)" style={{ marginTop: 2, flexShrink: 0 }} />
          {/* s.summary is already prefixed with the tool name (see the
              backend's agent.summarize), so it's shown as-is rather than
              repeating s.tool. */}
          <Text size="xs" c="dimmed" style={{ wordBreak: "break-word" }}>
            {s.summary || s.tool}
          </Text>
        </Group>
      ))}
      {stepBudgetExhausted && (
        <Text size="xs" c="orange">
          Reached the tool-call limit — this answer may be incomplete.
        </Text>
      )}
    </Stack>
  );
}

function MessageBubble({
  message,
  onSelectSource,
}: {
  message: ChatMessage;
  onSelectSource?: (card: AnswerCard) => void;
}) {
  const isUser = message.role === "user";
  return (
    <Box
      className={
        isUser
          ? "chat-bubble chat-bubble-user"
          : message.role === "error"
            ? "chat-bubble chat-bubble-error"
            : "chat-bubble chat-bubble-assistant"
      }
    >
      <Text size="sm" style={{ whiteSpace: "pre-wrap", wordBreak: "break-word" }}>
        {message.text}
      </Text>
      {message.sources && (
        <SourceList cards={message.sources} onSelectSource={onSelectSource} />
      )}
      {message.steps && (
        <StepList steps={message.steps} stepBudgetExhausted={message.stepBudgetExhausted} />
      )}
    </Box>
  );
}

// ChatPanel is a conversation view over the projection's GraphRAG answer
// endpoint: the user asks natural-language questions about the cluster graph
// and the configured chat provider replies with grounded answers.
export function ChatPanel({
  projection,
  onSelectSource,
}: {
  projection: Projection;
  // Called when the user clicks a source resource beneath an answer, so the
  // host view can select the corresponding graph node.
  onSelectSource?: (card: AnswerCard) => void;
}) {
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [input, setInput] = useState("");
  const [pending, setPending] = useState(false);
  const viewportRef = useRef<HTMLDivElement>(null);

  // Models the user may pick from (per the projection's allowedModels policy).
  // When only the default is allowed the selector is hidden entirely.
  const { data: chatModels } = useQuery({
    queryKey: ["chat-models", projection.uid],
    queryFn: () => getChatModels(projection.namespace, projection.name),
    staleTime: 5 * 60_000,
    retry: false,
  });
  const modelChoices = chatModels?.models ?? [];
  // The projection's own default chat model (empty when it configures none and
  // chat is available only via controller-wide providers).
  const projectionDefault = chatModels?.default ?? "";
  // The user's settings-wide default chat model, honoured when the projection
  // exposes it as a choice (i.e. it names a controller-wide chat provider).
  const { settings } = useSettings();
  const settingsDefault =
    settings.defaultChatModel && modelChoices.includes(settings.defaultChatModel)
      ? settings.defaultChatModel
      : null;
  // The effective default: the user's global preference, else the projection's
  // own default, else the first available choice.
  const effectiveDefault = settingsDefault ?? (projectionDefault || modelChoices[0] || null);
  // The user's explicit per-conversation choice; null falls back to the
  // effective default above.
  const [model, setModel] = useState<string | null>(null);
  const selectedModel = model && modelChoices.includes(model) ? model : effectiveDefault;

  // Keep the newest message in view as the conversation grows.
  useEffect(() => {
    viewportRef.current?.scrollTo({
      top: viewportRef.current.scrollHeight,
      behavior: "smooth",
    });
  }, [messages, pending]);

  const send = () => {
    const question = input.trim();
    if (!question || pending) return;
    setInput("");
    // The prior conversation's user/assistant turns, for the agent's history
    // parameter (built before the new question is appended below).
    const history: ChatHistoryMessage[] = messages
      .filter((m) => m.role === "user" || m.role === "assistant")
      .map((m) => ({ role: m.role as "user" | "assistant", content: m.text }));
    setMessages((prev) => [
      ...prev,
      { id: crypto.randomUUID(), role: "user", text: question },
    ]);
    setPending(true);
    // Send the chosen model whenever it isn't the projection's own default —
    // this is what routes a controller-wide provider (including the settings
    // default) to the backend.
    const override =
      selectedModel && selectedModel !== projectionDefault ? selectedModel : undefined;
    const request = settings.agenticChat
      ? askAgent(projection.namespace, projection.name, question, history, override).then(
          (answer) => ({
            text: answer.answer,
            sources: undefined,
            steps: answer.steps,
            stepBudgetExhausted: answer.stepBudgetExhausted,
          }),
        )
      : askQuestion(projection.namespace, projection.name, question, override).then(
          (answer) => ({
            text: answer.answer,
            sources: answer.retrieval.cards,
            steps: undefined,
            stepBudgetExhausted: undefined,
          }),
        );
    request
      .then((result) => {
        setMessages((prev) => [
          ...prev,
          {
            id: crypto.randomUUID(),
            role: "assistant",
            text: result.text,
            sources: result.sources,
            steps: result.steps,
            stepBudgetExhausted: result.stepBudgetExhausted,
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

  return (
    <div className="chat-panel">
      <ScrollArea className="chat-messages" type="scroll" viewportRef={viewportRef}>
        <Stack gap="sm" p={14}>
          {messages.length === 0 && (
            <Text size="sm" c="dimmed">
              Ask a question about the resources in this projection, e.g. “Which
              pods mount a secret?” Answers are generated by the projection's
              configured chat provider using the cluster graph.
            </Text>
          )}
          {messages.map((m) => (
            <MessageBubble key={m.id} message={m} onSelectSource={onSelectSource} />
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
            placeholder="Ask about this projection…"
            autosize
            minRows={1}
            maxRows={5}
            style={{ flex: 1 }}
            disabled={pending}
          />
          <Tooltip label="Send" position="top" withArrow>
            <ActionIcon
              variant="filled"
              size="lg"
              aria-label="Send message"
              onClick={send}
              disabled={pending || input.trim() === ""}
            >
              <IconSend2 size={18} stroke={1.5} />
            </ActionIcon>
          </Tooltip>
        </div>
      </div>
    </div>
  );
}
