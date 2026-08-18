import {
  Button,
  Divider,
  FileButton,
  Group,
  Image,
  Loader,
  Modal,
  Select,
  Slider,
  Stack,
  Switch,
  Text,
} from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { listProviders, type ProviderInfo } from "./api";
import { LAYOUT_LIMITS, useSettings } from "./settings";

// providerOptions turns providers into Select data, labelling each by its name
// with the underlying model/provider as a hint.
function providerOptions(providers: ProviderInfo[]) {
  return providers.map((p) => ({
    value: p.name,
    label: p.model ? `${p.name} (${p.model})` : p.name,
  }));
}

// ModelSettings lets the user pick an embedding model and a default chat model
// from the providers configured on the controller (GET /api/providers).
function ModelSettings() {
  const { settings, update } = useSettings();
  const { data, isLoading, error } = useQuery({
    queryKey: ["providers"],
    queryFn: listProviders,
  });

  const embeddingProviders = data?.embeddingProviders ?? [];
  const chatProviders = data?.chatProviders ?? [];

  return (
    <Stack gap="md">
      <div>
        <Text fw={600} size="sm">
          Models
        </Text>
        <Text size="xs" c="dimmed">
          Choose from the embedding and chat providers configured on the
          controller. Manage the available options via the controller&apos;s
          providers ConfigMap.
        </Text>
      </div>

      {isLoading ? (
        <Group gap="xs">
          <Loader size="xs" />
          <Text size="sm" c="dimmed">
            Loading providers…
          </Text>
        </Group>
      ) : error ? (
        <Text size="sm" c="red">
          {(error as Error).message}
        </Text>
      ) : (
        <>
          <Select
            label="Embedding model"
            description={
              embeddingProviders.length === 0
                ? "No embedding providers are configured on the controller."
                : undefined
            }
            placeholder="Select an embedding model…"
            data={providerOptions(embeddingProviders)}
            value={settings.embeddingModel}
            onChange={(v) => update({ embeddingModel: v })}
            disabled={embeddingProviders.length === 0}
            clearable
            searchable
          />
          <Select
            label="Default chat model"
            description={
              chatProviders.length === 0
                ? "No chat providers are configured on the controller."
                : undefined
            }
            placeholder="Select a default chat model…"
            data={providerOptions(chatProviders)}
            value={settings.defaultChatModel}
            onChange={(v) => update({ defaultChatModel: v })}
            disabled={chatProviders.length === 0}
            clearable
            searchable
          />
        </>
      )}

      <Switch
        label="Agentic chat"
        description="Let the chat model call tools (search, neighborhood, guarded
          Cypher, schema, live resource reads) to work out an answer, instead
          of grounding on a single retrieval. Falls back automatically when
          the selected model doesn't support tool calling."
        checked={settings.agenticChat}
        onChange={(e) => update({ agenticChat: e.currentTarget.checked })}
      />
    </Stack>
  );
}

// LayoutSlider is one labelled slider bound to a numeric layout setting.
function LayoutSlider({
  label,
  hint,
  value,
  limits,
  onChange,
}: {
  label: string;
  hint: string;
  value: number;
  limits: { min: number; max: number; step: number };
  onChange: (v: number) => void;
}) {
  return (
    <Stack gap={4}>
      <Group justify="space-between" gap={8} wrap="nowrap">
        <Text size="sm">{label}</Text>
        <Text size="xs" c="dimmed">
          {value}
        </Text>
      </Group>
      <Slider
        min={limits.min}
        max={limits.max}
        step={limits.step}
        value={value}
        onChange={onChange}
        label={null}
      />
      <Text size="xs" c="dimmed">
        {hint}
      </Text>
    </Stack>
  );
}

// SettingsModal manages user settings. The first setting is a "wallpaper":
// an image used as the background of the graph area instead of the solid color.
export function SettingsModal({ opened, onClose }: { opened: boolean; onClose: () => void }) {
  const { settings, update } = useSettings();

  const onPick = (file: File | null) => {
    if (!file) return;
    const reader = new FileReader();
    reader.onload = () => {
      if (typeof reader.result === "string") update({ wallpaper: reader.result });
    };
    reader.readAsDataURL(file);
  };

  return (
    <Modal opened={opened} onClose={onClose} title="Settings" size="lg">
      <Stack gap="lg">
        <Stack gap="xs">
          <div>
            <Text fw={600} size="sm">
              Wallpaper
            </Text>
            <Text size="xs" c="dimmed">
              Use an image as the background of the graph area instead of the solid color.
            </Text>
          </div>

          {settings.wallpaper ? (
            <Image
              src={settings.wallpaper}
              radius="sm"
              h={140}
              fit="cover"
              alt="Selected wallpaper preview"
            />
          ) : (
            <Text size="sm" c="dimmed">
              No wallpaper selected.
            </Text>
          )}

          <Group>
            <FileButton onChange={onPick} accept="image/png,image/jpeg,image/webp,image/gif,image/svg+xml">
              {(props) => (
                <Button {...props} variant="default" size="xs">
                  {settings.wallpaper ? "Change image…" : "Choose image…"}
                </Button>
              )}
            </FileButton>
            {settings.wallpaper && (
              <Button
                variant="subtle"
                color="red"
                size="xs"
                onClick={() => update({ wallpaper: null })}
              >
                Remove
              </Button>
            )}
          </Group>
        </Stack>

        <Divider />

        <ModelSettings />

        <Divider />

        <Stack gap="md">
          <div>
            <Text fw={600} size="sm">
              Graph layout
            </Text>
            <Text size="xs" c="dimmed">
              Tune the force-directed layout. Changes re-run the layout, so node
              positions will be recomputed.
            </Text>
          </div>
          <LayoutSlider
            label="Repulsion force"
            hint="How strongly nodes push each other apart."
            value={settings.layoutRepulsion}
            limits={LAYOUT_LIMITS.layoutRepulsion}
            onChange={(v) => update({ layoutRepulsion: v })}
          />
          <LayoutSlider
            label="Link length"
            hint="The ideal length of a link between two connected nodes."
            value={settings.layoutEdgeLength}
            limits={LAYOUT_LIMITS.layoutEdgeLength}
            onChange={(v) => update({ layoutEdgeLength: v })}
          />
          <LayoutSlider
            label="Gravity"
            hint="How strongly nodes are pulled toward the center."
            value={settings.layoutGravity}
            limits={LAYOUT_LIMITS.layoutGravity}
            onChange={(v) => update({ layoutGravity: v })}
          />
        </Stack>
      </Stack>
    </Modal>
  );
}
