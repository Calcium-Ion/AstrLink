import { useState } from "react";

export const OVERVIEW_LAYOUT_STORAGE_KEY = "astrlink.overview.layout.v1";

export const DEFAULT_OVERVIEW_LAYOUT = [
  "usage",
  "live-crew",
  "request-sky",
  "providers-models",
  "tokens",
  "access",
  "system",
] as const;

export type OverviewModuleId = (typeof DEFAULT_OVERVIEW_LAYOUT)[number];

/**
 * Optional modules: hidden in the default layout, and hidden when they first
 * join a layout saved before them. Showing one is up to the layout editor.
 */
export const DEFAULT_HIDDEN_MODULES: readonly OverviewModuleId[] = [
  "request-sky",
];

interface OverviewLayout {
  order: OverviewModuleId[];
  hidden: OverviewModuleId[];
}

function knownModules(value: unknown): OverviewModuleId[] {
  if (!Array.isArray(value)) return [];
  return [
    ...new Set(
      value.filter((id): id is OverviewModuleId =>
        DEFAULT_OVERVIEW_LAYOUT.includes(id),
      ),
    ),
  ];
}

/**
 * Puts modules a saved order lacks where the default layout has them: after
 * the nearest module that precedes them by default, or first.
 */
export function withNewModules(
  order: readonly OverviewModuleId[],
): OverviewModuleId[] {
  const merged = [...order];
  DEFAULT_OVERVIEW_LAYOUT.forEach((id, index) => {
    if (merged.includes(id)) return;
    let at = 0;
    for (let before = index - 1; before >= 0; before -= 1) {
      const found = merged.indexOf(DEFAULT_OVERVIEW_LAYOUT[before]);
      if (found >= 0) {
        at = found + 1;
        break;
      }
    }
    merged.splice(at, 0, id);
  });
  return merged;
}

function readLayout(): OverviewLayout {
  try {
    const saved: unknown = JSON.parse(
      localStorage.getItem(OVERVIEW_LAYOUT_STORAGE_KEY) ?? "null",
    );
    if (saved !== null && typeof saved === "object") {
      // Older preferences stored only the order. Newly added modules join in
      // their default place, and optional ones join hidden.
      const order = knownModules(
        Array.isArray(saved) ? saved : "order" in saved ? saved.order : null,
      );
      const hidden = knownModules("hidden" in saved ? saved.hidden : null);
      const joined = DEFAULT_HIDDEN_MODULES.filter((id) => !order.includes(id));
      return {
        order: withNewModules(order),
        hidden: [...new Set([...hidden, ...joined])],
      };
    }
  } catch {
    // Malformed preferences or unavailable storage must not block the overview.
  }
  return defaultLayout();
}

function defaultLayout(): OverviewLayout {
  return {
    order: [...DEFAULT_OVERVIEW_LAYOUT],
    hidden: [...DEFAULT_HIDDEN_MODULES],
  };
}

export function useOverviewLayout() {
  const [layout, setLayout] = useState(readLayout);
  const [saveFailed, setSaveFailed] = useState(false);
  const save = (next: OverviewLayout) => {
    setLayout(next);
    try {
      localStorage.setItem(OVERVIEW_LAYOUT_STORAGE_KEY, JSON.stringify(next));
      setSaveFailed(false);
    } catch {
      setSaveFailed(true);
    }
  };
  return {
    ...layout,
    save: (order: OverviewModuleId[]) => save({ ...layout, order }),
    setVisible: (id: OverviewModuleId, visible: boolean) =>
      save({
        ...layout,
        hidden: visible
          ? layout.hidden.filter((hidden) => hidden !== id)
          : [...new Set([...layout.hidden, id])],
      }),
    saveFailed,
    isDefault:
      layout.hidden.length === DEFAULT_HIDDEN_MODULES.length &&
      DEFAULT_HIDDEN_MODULES.every((id) => layout.hidden.includes(id)) &&
      layout.order.every((id, index) => id === DEFAULT_OVERVIEW_LAYOUT[index]),
    reset: () => save(defaultLayout()),
  };
}
