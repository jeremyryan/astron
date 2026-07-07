import {
  createContext,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";

// Settings holds user preferences that persist across sessions. Small scalar
// preferences live in localStorage; the wallpaper image — which can be several
// MB as a data URL and would blow localStorage's ~5MB quota — is stored in
// IndexedDB instead (see the persistence effects below).
export interface Settings {
  // Data URL of an image to use as the graph-area background, or null for the
  // default solid color.
  wallpaper: string | null;
  // Whether edge (relationship-type) labels are drawn on the graph.
  showEdgeLabels: boolean;
  // Whether the reference grid is overlaid on the graph display.
  showGrid: boolean;
  // Graph layout (fcose) tuning. Changing these re-runs the layout.
  //   layoutRepulsion:  how strongly nodes push each other apart (nodeRepulsion)
  //   layoutEdgeLength: the ideal length of a link (idealEdgeLength)
  //   layoutGravity:    how strongly nodes are pulled toward the center (gravity)
  layoutRepulsion: number;
  layoutEdgeLength: number;
  layoutGravity: number;
}

// Bounds for the layout tuning controls (also used to clamp stored values).
export const LAYOUT_LIMITS = {
  layoutRepulsion: { min: 1000, max: 20000, step: 500 },
  layoutEdgeLength: { min: 40, max: 250, step: 10 },
  layoutGravity: { min: 0, max: 1, step: 0.05 },
} as const;

// Preferences small enough to keep in localStorage (everything except the
// wallpaper image).
type Prefs = Omit<Settings, "wallpaper">;

const DEFAULT_PREFS: Prefs = {
  showEdgeLabels: true,
  showGrid: false,
  layoutRepulsion: 8000,
  layoutEdgeLength: 100,
  layoutGravity: 0.2,
};

const STORAGE_KEY = "astron.settings";

// IndexedDB coordinates for the wallpaper image.
const IDB_NAME = "astron";
const IDB_STORE = "settings";
const WALLPAPER_KEY = "wallpaper";

// loadPrefs reads the scalar preferences from localStorage. It also returns any
// legacy wallpaper found in the old combined blob so it can be migrated into
// IndexedDB (older builds stored the image here, which is exactly what broke
// persistence for large images).
function loadPrefs(): { prefs: Prefs; legacyWallpaper: string | null } {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return { prefs: DEFAULT_PREFS, legacyWallpaper: null };
    const parsed = JSON.parse(raw) as Partial<Settings>;
    const num = (v: unknown, d: number) => (typeof v === "number" && Number.isFinite(v) ? v : d);
    return {
      prefs: {
        showEdgeLabels: parsed.showEdgeLabels ?? DEFAULT_PREFS.showEdgeLabels,
        showGrid: parsed.showGrid ?? DEFAULT_PREFS.showGrid,
        layoutRepulsion: num(parsed.layoutRepulsion, DEFAULT_PREFS.layoutRepulsion),
        layoutEdgeLength: num(parsed.layoutEdgeLength, DEFAULT_PREFS.layoutEdgeLength),
        layoutGravity: num(parsed.layoutGravity, DEFAULT_PREFS.layoutGravity),
      },
      legacyWallpaper: parsed.wallpaper ?? null,
    };
  } catch {
    return { prefs: DEFAULT_PREFS, legacyWallpaper: null };
  }
}

// openWallpaperDB opens (and lazily creates) the IndexedDB database used for the
// wallpaper image.
function openWallpaperDB(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(IDB_NAME, 1);
    req.onupgradeneeded = () => {
      if (!req.result.objectStoreNames.contains(IDB_STORE)) {
        req.result.createObjectStore(IDB_STORE);
      }
    };
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}

async function loadWallpaper(): Promise<string | null> {
  try {
    const db = await openWallpaperDB();
    return await new Promise<string | null>((resolve, reject) => {
      const tx = db.transaction(IDB_STORE, "readonly");
      const req = tx.objectStore(IDB_STORE).get(WALLPAPER_KEY);
      req.onsuccess = () => resolve((req.result as string | undefined) ?? null);
      req.onerror = () => reject(req.error);
    });
  } catch {
    return null;
  }
}

async function saveWallpaper(value: string | null): Promise<void> {
  try {
    const db = await openWallpaperDB();
    await new Promise<void>((resolve, reject) => {
      const tx = db.transaction(IDB_STORE, "readwrite");
      const store = tx.objectStore(IDB_STORE);
      if (value === null) store.delete(WALLPAPER_KEY);
      else store.put(value, WALLPAPER_KEY);
      tx.oncomplete = () => resolve();
      tx.onerror = () => reject(tx.error);
    });
  } catch {
    // Best-effort: if IndexedDB is unavailable the wallpaper still applies for
    // the current session.
  }
}

interface SettingsContextValue {
  settings: Settings;
  update: (patch: Partial<Settings>) => void;
}

const SettingsContext = createContext<SettingsContextValue | null>(null);

export function SettingsProvider({ children }: { children: ReactNode }) {
  // Read the scalar prefs (and any legacy wallpaper) synchronously, once.
  const [initial] = useState(loadPrefs);
  const [settings, setSettings] = useState<Settings>(() => ({
    wallpaper: null,
    ...initial.prefs,
  }));
  // Guards writing the wallpaper store until the async hydrate has run, so we
  // never delete the persisted image before reading it back on startup.
  const [hydrated, setHydrated] = useState(false);

  // Hydrate the wallpaper from IndexedDB on mount, migrating a legacy wallpaper
  // out of the old localStorage blob if present.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      let stored = await loadWallpaper();
      if (stored == null && initial.legacyWallpaper) stored = initial.legacyWallpaper;
      if (!cancelled && stored) {
        // Only apply if the user hasn't already chosen one this session.
        setSettings((prev) => (prev.wallpaper == null ? { ...prev, wallpaper: stored } : prev));
      }
      if (!cancelled) setHydrated(true);
    })();
    return () => {
      cancelled = true;
    };
  }, [initial]);

  // Persist the scalar prefs to localStorage (never the wallpaper, keeping the
  // blob tiny and well under quota). Running on mount also strips any legacy
  // wallpaper from the old combined blob.
  useEffect(() => {
    try {
      const prefs: Prefs = {
        showEdgeLabels: settings.showEdgeLabels,
        showGrid: settings.showGrid,
        layoutRepulsion: settings.layoutRepulsion,
        layoutEdgeLength: settings.layoutEdgeLength,
        layoutGravity: settings.layoutGravity,
      };
      localStorage.setItem(STORAGE_KEY, JSON.stringify(prefs));
    } catch {
      // ignore persistence failures
    }
  }, [
    settings.showEdgeLabels,
    settings.showGrid,
    settings.layoutRepulsion,
    settings.layoutEdgeLength,
    settings.layoutGravity,
  ]);

  // Persist the wallpaper to IndexedDB whenever it changes (once hydrated).
  useEffect(() => {
    if (!hydrated) return;
    void saveWallpaper(settings.wallpaper);
  }, [hydrated, settings.wallpaper]);

  const updateRef = useRef((patch: Partial<Settings>) =>
    setSettings((prev) => ({ ...prev, ...patch })),
  );

  return (
    <SettingsContext.Provider value={{ settings, update: updateRef.current }}>
      {children}
    </SettingsContext.Provider>
  );
}

export function useSettings(): SettingsContextValue {
  const ctx = useContext(SettingsContext);
  if (!ctx) throw new Error("useSettings must be used within a SettingsProvider");
  return ctx;
}
