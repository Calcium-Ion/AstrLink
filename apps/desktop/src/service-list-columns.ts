import { useState } from "react";

import {
  SERVICE_LIST_COLUMNS,
  type ServiceListColumn,
} from "@/components/ServiceListRow";

export const SERVICE_LIST_COLUMNS_STORAGE_KEY = "astrlink.services.columns.v1";

// Model counts are one click away in the editor; keep the default table lean.
export const DEFAULT_HIDDEN_SERVICE_COLUMNS: readonly ServiceListColumn[] = [
  "models",
];

function knownColumns(value: unknown): ServiceListColumn[] {
  if (!Array.isArray(value)) return [];
  return SERVICE_LIST_COLUMNS.filter((id) => value.includes(id));
}

function readHidden(): ServiceListColumn[] {
  try {
    const saved: unknown = JSON.parse(
      localStorage.getItem(SERVICE_LIST_COLUMNS_STORAGE_KEY) ?? "null",
    );
    if (saved !== null && typeof saved === "object" && "hidden" in saved) {
      return knownColumns(saved.hidden);
    }
  } catch {
    // Malformed preferences or unavailable storage must not block the list.
  }
  return [...DEFAULT_HIDDEN_SERVICE_COLUMNS];
}

export function useServiceListColumns() {
  const [hidden, setHidden] = useState(readHidden);
  const save = (next: ServiceListColumn[]) => {
    setHidden(next);
    try {
      localStorage.setItem(
        SERVICE_LIST_COLUMNS_STORAGE_KEY,
        JSON.stringify({ hidden: next }),
      );
    } catch {
      // The choice still applies to this session.
    }
  };
  return {
    hidden,
    setVisible: (id: ServiceListColumn, visible: boolean) =>
      save(
        SERVICE_LIST_COLUMNS.filter((column) =>
          column === id ? !visible : hidden.includes(column),
        ),
      ),
    isDefault:
      hidden.length === DEFAULT_HIDDEN_SERVICE_COLUMNS.length &&
      DEFAULT_HIDDEN_SERVICE_COLUMNS.every((id) => hidden.includes(id)),
    reset: () => save([...DEFAULT_HIDDEN_SERVICE_COLUMNS]),
  };
}
