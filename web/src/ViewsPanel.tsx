import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ActionIcon,
  Button,
  Group,
  Modal,
  Select,
  Text,
  TextInput,
  Tooltip,
} from "@mantine/core";
import {
  createView,
  deleteView,
  listViews,
  updateView,
  type Projection,
  type View,
  type ViewFilters,
} from "./api";
import { IconDeviceFloppy, IconTrash } from "./icons";

interface Props {
  projection: Projection;
  // The current (possibly unsaved) filter state, in View DTO shape.
  currentFilters: ViewFilters;
  // Apply a saved view's filters to the graph panel.
  onApply: (filters: ViewFilters) => void;
}

// ViewsPanel is a floating toolbar over the graph that lets the user apply,
// save, update and delete named Views (saved filter sets) for the projection.
export function ViewsPanel({ projection, currentFilters, onApply }: Props) {
  const qc = useQueryClient();
  const viewsKey = ["views", projection.namespace, projection.name];
  const { data: views } = useQuery({
    queryKey: viewsKey,
    queryFn: () => listViews(projection.namespace, projection.name),
  });

  // Name of the currently applied view (null = unsaved/custom filters).
  const [activeName, setActiveName] = useState<string | null>(null);
  const [saveOpen, setSaveOpen] = useState(false);
  const [newName, setNewName] = useState("");
  const [error, setError] = useState<string | null>(null);

  const activeView = useMemo(
    () => views?.find((v) => v.name === activeName) ?? null,
    [views, activeName],
  );

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
      setActiveName(v.name);
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
    onSuccess: () => invalidate(),
    onError: (e) => setError((e as Error).message),
  });

  const deleteMut = useMutation({
    mutationFn: () => {
      if (!activeView) throw new Error("no view selected");
      return deleteView(activeView.namespace, activeView.name);
    },
    onSuccess: () => {
      setActiveName(null);
      invalidate();
    },
    onError: (e) => setError((e as Error).message),
  });

  const options = (views ?? []).map((v) => ({
    value: v.name,
    label: v.displayName || v.name,
  }));

  return (
    <div className="views-bar">
      <Text size="xs" fw={600} c="dimmed">
        View
      </Text>
      <Select
        size="xs"
        w={200}
        placeholder="Custom (unsaved)"
        data={options}
        value={activeName}
        onChange={(name) => {
          setActiveName(name);
          const v = views?.find((x) => x.name === name);
          if (v) onApply(v.filters);
        }}
        clearable
        comboboxProps={{ withinPortal: true }}
      />

      <Tooltip label="Update selected view" position="bottom" withArrow disabled={!activeView}>
        <ActionIcon
          variant="subtle"
          color="gray"
          size="lg"
          aria-label="Update view"
          disabled={!activeView}
          loading={updateMut.isPending}
          onClick={() => updateMut.mutate()}
        >
          <IconDeviceFloppy size={18} stroke={1.5} />
        </ActionIcon>
      </Tooltip>

      <Tooltip label="Delete selected view" position="bottom" withArrow disabled={!activeView}>
        <ActionIcon
          variant="subtle"
          color="red"
          size="lg"
          aria-label="Delete view"
          disabled={!activeView}
          loading={deleteMut.isPending}
          onClick={() => deleteMut.mutate()}
        >
          <IconTrash size={18} stroke={1.5} />
        </ActionIcon>
      </Tooltip>

      <Button
        size="compact-xs"
        variant="light"
        onClick={() => {
          setError(null);
          setSaveOpen(true);
        }}
      >
        Save as…
      </Button>

      {error && (
        <Text size="xs" c="red" maw={220} lineClamp={1} title={error}>
          {error}
        </Text>
      )}

      <Modal
        opened={saveOpen}
        onClose={() => setSaveOpen(false)}
        title="Save view"
        size="sm"
      >
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
    </div>
  );
}
