import type { Graph } from "./api";
import { colorForKind } from "./kinds";

export interface KindCount {
  kind: string;
  count: number;
}

// kindCounts returns the distinct kinds present in a graph with their node
// counts, sorted alphabetically.
export function kindCounts(graph: Graph): KindCount[] {
  const counts = new Map<string, number>();
  for (const n of graph.nodes) {
    counts.set(n.kind, (counts.get(n.kind) ?? 0) + 1);
  }
  return [...counts.entries()]
    .map(([kind, count]) => ({ kind, count }))
    .sort((a, b) => a.kind.localeCompare(b.kind));
}

interface Props {
  kinds: KindCount[];
  // Kinds the user has hidden. Empty means everything is shown (the default).
  hiddenKinds: Set<string>;
  onToggleKind: (kind: string) => void;
  onShowAll: () => void;
  onHideAll: () => void;
  // Connection-distance filter (relative to the selected node).
  hasSelection: boolean;
  // Max hops from the selected node to keep visible; null = all (no fading).
  maxDistance: number | null;
  onChangeDistance: (value: number | null) => void;
}

const MAX_DISTANCE = 9;

export function FilterPanel({
  kinds,
  hiddenKinds,
  onToggleKind,
  onShowAll,
  onHideAll,
  hasSelection,
  maxDistance,
  onChangeDistance,
}: Props) {
  const visibleCount = kinds.filter((k) => !hiddenKinds.has(k.kind)).length;
  const filtering = hiddenKinds.size > 0;

  return (
    <aside className="filters">
      <div className="filters-header">
        <h2>Filters</h2>
      </div>

      <section className="filter-group">
        <div className="filter-group-header">
          <h3>
            Resource types
            {filtering && (
              <span className="filter-badge">
                {visibleCount}/{kinds.length}
              </span>
            )}
          </h3>
          <div className="filter-actions">
            <button type="button" onClick={onShowAll} disabled={hiddenKinds.size === 0}>
              All
            </button>
            <button type="button" onClick={onHideAll} disabled={visibleCount === 0}>
              None
            </button>
          </div>
        </div>

        {kinds.length === 0 ? (
          <p className="muted">No resources.</p>
        ) : (
          <ul className="filter-list">
            {kinds.map(({ kind, count }) => {
              const visible = !hiddenKinds.has(kind);
              return (
                <li key={kind}>
                  <label className={visible ? "" : "dimmed"}>
                    <input
                      type="checkbox"
                      checked={visible}
                      onChange={() => onToggleKind(kind)}
                    />
                    <span
                      className="kind-swatch"
                      style={{ background: colorForKind(kind) }}
                    />
                    <span className="kind-name">{kind}</span>
                    <span className="kind-count">{count}</span>
                  </label>
                </li>
              );
            })}
          </ul>
        )}
      </section>

      <section className="filter-group">
        <div className="filter-group-header">
          <h3>Connection distance</h3>
        </div>
        <label className="distance-all">
          <input
            type="checkbox"
            checked={maxDistance === null}
            onChange={(e) => onChangeDistance(e.target.checked ? null : 2)}
          />
          All connections
        </label>
        <div className={`distance-stepper${maxDistance === null ? " disabled" : ""}`}>
          <button
            type="button"
            aria-label="Fewer hops"
            disabled={maxDistance === null || maxDistance <= 1}
            onClick={() => onChangeDistance(Math.max(1, (maxDistance ?? 2) - 1))}
          >
            −
          </button>
          <span className="distance-value">
            {maxDistance === null ? "∞" : maxDistance}
            <small>{maxDistance === 1 ? " hop" : " hops"}</small>
          </span>
          <button
            type="button"
            aria-label="More hops"
            disabled={maxDistance === null || maxDistance >= MAX_DISTANCE}
            onClick={() => onChangeDistance(Math.min(MAX_DISTANCE, (maxDistance ?? 2) + 1))}
          >
            +
          </button>
        </div>
        {maxDistance !== null && !hasSelection && (
          <p className="muted hint">Select a node to apply.</p>
        )}
      </section>
    </aside>
  );
}
