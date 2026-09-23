import { useState } from "react";

export const OVERVIEW_LAYOUT_STORAGE_KEY = "astrlink.overview.layout.v1";

export const DEFAULT_OVERVIEW_LAYOUT = [
  "usage",
  "providers-models",
  "tokens",
  "access",
  "system",
] as const;

export type OverviewModuleId = (typeof DEFAULT_OVERVIEW_LAYOUT)[number];

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

function readLayout(): OverviewLayout {
  try {
    const saved: unknown = JSON.parse(
      localStorage.getItem(OVERVIEW_LAYOUT_STORAGE_KEY) ?? "null",
    );
    if (saved !== null && typeof saved === "object") {
      // Older preferences stored only the order. Newly added modules stay visible.
      const order = knownModules(
        Array.isArray(saved) ? saved : "order" in saved ? saved.order : null,
      );
      const hidden = knownModules("hidden" in saved ? saved.hidden : null);
      return {
        order: [...new Set([...order, ...DEFAULT_OVERVIEW_LAYOUT])],
        hidden,
      };
    }
  } catch {
    // Malformed preferences or unavailable storage must not block the overview.
  }
  return { order: [...DEFAULT_OVERVIEW_LAYOUT], hidden: [] };
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
      layout.hidden.length === 0 &&
      layout.order.every((id, index) => id === DEFAULT_OVERVIEW_LAYOUT[index]),
    reset: () => save({ order: [...DEFAULT_OVERVIEW_LAYOUT], hidden: [] }),
  };
}
