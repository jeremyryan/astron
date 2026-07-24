import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Button, Group, Modal, Stack, Text, TextInput } from "@mantine/core";
import {
  createView,
  deleteView,
  updateView,
  type Projection,
  type View,
  type ViewFilters,
} from "./api";
import { IconDeviceFloppy, IconPlus, IconTrash } from "./icons";

interface Props {
  projection: Projection;
  // The current (possibly unsaved) filter state, in View DTO shape.
  currentFilters: ViewFilters;
  // The saved view currently applied (null = unsaved/custom filters).
  activeView: View | null;
  // Notify the parent when the active view changes (created / updated / deleted).
  onActiveViewChange: (view: View | null) => void;
}

// ViewControls renders the Save / Delete / Save as… actions for the active
// projection's Views (saved filter sets). It lives in the filters panel; view
// selection happens in the left navbar.
export function ViewControls({ projection, currentFilters, activeView, onActiveViewChange }: Props) {
  const qc = useQueryClient();
  const viewsKey = ["views", projection.namespace, projection.name];
  const [saveOpen, setSaveOpen] = useState(false);
  const [newName, setNewName] = useState("");
  const [error, setError] = useState<string | null>(null);
  // Whether the "delete this view?" confirmation modal is open.
  const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false);

  const invalidate = () => qc.invalidateQueries({ queryKey: viewsKey });

  const baseView = (name: string, displayName?: string): Omit<View, "uid"> => ({
    namespace: projection.namespace,
    name,
    displayName,
    projectionRef: { name: projection.name, namespace: projection.namespace },
    filters: currentFilters,
  });

  // Build a DNS-1123-ish resource name from the user-supplied display name.
  const slugify = (s: string) =>
    s
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "")
      .slice(0, 200) || "view";

  const createMut = useMutation({
    mutationFn: (displayName: string) => createView(baseView(slugify(displayName), displayName)),
    onSuccess: (v) => {
      onActiveViewChange(v);
      setSaveOpen(false);
      setNewName("");
      setError(null);
      invalidate();
    },
    onError: (e) => setError((e as Error).message),
  });

  const updateMut = useMutation({
    mutationFn: () => {
      if (!activeView) throw new Error("no view selected");
      return updateView(activeView.namespace, activeView.name, {
        ...baseView(activeView.name, activeView.displayName),
        description: activeView.description,
      });
    },
    onSuccess: (v) => {
      onActiveViewChange(v);
      setError(null);
      invalidate();
    },
    onError: (e) => setError((e as Error).message),
  });

  const deleteMut = useMutation({
    mutationFn: () => {
      if (!activeView) throw new Error("no view selected");
      return deleteView(activeView.namespace, activeView.name);
    },
    onSuccess: () => {
      onActiveViewChange(null);
      invalidate();
    },
    onError: (e) => setError((e as Error).message),
  });

  return (
    <Stack gap="xs">
      <Group gap={6} grow wrap="nowrap">
        <Button
          size="compact-xs"
          variant="light"
          leftSection={<IconDeviceFloppy size={14} stroke={1.5} />}
          disabled={!activeView}
          loading={updateMut.isPending}
          onClick={() => updateMut.mutate()}
        >
          Save
        </Button>
        <Button
          size="compact-xs"
          variant="light"
          color="red"
          leftSection={<IconTrash size={14} stroke={1.5} />}
          disabled={!activeView}
          loading={deleteMut.isPending}
          onClick={() => {
            setError(null);
            setDeleteConfirmOpen(true);
          }}
        >
          Delete
        </Button>
      </Group>
      <Button
        size="compact-xs"
        variant="default"
        leftSection={<IconPlus size={13} stroke={1.5} />}
        onClick={() => {
          setError(null);
          setSaveOpen(true);
        }}
        style={{ alignSelf: "flex-start" }}
      >
        Save as…
      </Button>
      {!activeView && (
        <Text size="xs" c="dimmed">
          Custom (unsaved) filters.
        </Text>
      )}
      {error && (
        <Text size="xs" c="red" title={error} lineClamp={2}>
          {error}
        </Text>
      )}

      <Modal opened={saveOpen} onClose={() => setSaveOpen(false)} title="Save view" size="sm">
        <TextInput
          label="Name"
          placeholder="e.g. Team A workloads"
          value={newName}
          onChange={(e) => setNewName(e.currentTarget.value)}
          data-autofocus
          onKeyDown={(e) => {
            if (e.key === "Enter" && newName.trim()) createMut.mutate(newName.trim());
          }}
        />
        {error && (
          <Text size="xs" c="red" mt="xs">
            {error}
          </Text>
        )}
        <Group justify="flex-end" mt="md">
          <Button variant="default" size="xs" onClick={() => setSaveOpen(false)}>
            Cancel
          </Button>
          <Button
            size="xs"
            disabled={!newName.trim()}
            loading={createMut.isPending}
            onClick={() => createMut.mutate(newName.trim())}
          >
            Save
          </Button>
        </Group>
      </Modal>

      <Modal
        opened={deleteConfirmOpen}
        onClose={() => setDeleteConfirmOpen(false)}
        title="Delete view"
        size="sm"
      >
        <Text size="sm">
          Delete the view{" "}
          <Text span fw={600}>
            {activeView?.displayName || activeView?.name}
          </Text>
          ? This can't be undone.
        </Text>
        {error && (
          <Text size="xs" c="red" mt="xs">
            {error}
          </Text>
        )}
        <Group justify="flex-end" mt="md">
          <Button variant="default" size="xs" onClick={() => setDeleteConfirmOpen(false)}>
            Cancel
          </Button>
          <Button
            size="xs"
            color="red"
            leftSection={<IconTrash size={14} stroke={1.5} />}
            loading={deleteMut.isPending}
            onClick={() =>
              deleteMut.mutate(undefined, {
                onSuccess: () => setDeleteConfirmOpen(false),
              })
            }
          >
            Delete
          </Button>
        </Group>
      </Modal>
    </Stack>
  );
}
