import { Button, FileButton, Group, Image, Modal, Stack, Text } from "@mantine/core";
import { useSettings } from "./settings";

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
      </Stack>
    </Modal>
  );
}
