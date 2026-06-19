import { createContext, useContext, useEffect, useState, type ReactNode } from "react";

// Settings holds user preferences that persist across sessions. For now these
// are stored in localStorage; a server-backed store can replace loadSettings/
// the persistence effect later without changing consumers.
export interface Settings {
  // Data URL of an image to use as the graph-area background, or null for the
  // default solid color.
  wallpaper: string | null;
  // Whether edge (relationship-type) labels are drawn on the graph.
  showEdgeLabels: boolean;
  // Whether the reference grid is overlaid on the graph display.
  showGrid: boolean;
}

const DEFAULT_SETTINGS: Settings = {
  wallpaper: null,
  showEdgeLabels: true,
  showGrid: false,
};

const STORAGE_KEY = "gamera.settings";

function loadSettings(): Settings {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return DEFAULT_SETTINGS;
    return { ...DEFAULT_SETTINGS, ...(JSON.parse(raw) as Partial<Settings>) };
  } catch {
    return DEFAULT_SETTINGS;
  }
}

interface SettingsContextValue {
  settings: Settings;
  update: (patch: Partial<Settings>) => void;
}

const SettingsContext = createContext<SettingsContextValue | null>(null);

export function SettingsProvider({ children }: { children: ReactNode }) {
  const [settings, setSettings] = useState<Settings>(loadSettings);

  // Persist on every change. Wrapped in try/catch so a full/disabled storage
  // (or an oversized image exceeding the quota) doesn't break the app — the
  // value still applies for the current session.
  useEffect(() => {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(settings));
    } catch {
      // ignore persistence failures
    }
  }, [settings]);

  const update = (patch: Partial<Settings>) =>
    setSettings((prev) => ({ ...prev, ...patch }));

  return (
    <SettingsContext.Provider value={{ settings, update }}>
      {children}
    </SettingsContext.Provider>
  );
}

export function useSettings(): SettingsContextValue {
  const ctx = useContext(SettingsContext);
  if (!ctx) throw new Error("useSettings must be used within a SettingsProvider");
  return ctx;
}
