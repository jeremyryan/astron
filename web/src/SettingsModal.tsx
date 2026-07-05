import {
  Button,
  Divider,
  FileButton,
  Group,
  Image,
  Modal,
  Slider,
  Stack,
  Text,
} from "@mantine/core";
import { LAYOUT_LIMITS, useSettings } from "./settings";

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
