export type ModelGroup = {
  key: string;
  models: string[];
};

/** Family key for denser allow-list browsing (claude-sonnet, gpt-5.6, org/…). */
export function modelGroupKey(model: string): string {
  const slash = model.indexOf("/");
  if (slash > 0) {
    return model.slice(0, slash);
  }

  const parts = model.split("-").filter(Boolean);
  if (parts.length === 0) return model;
  if (parts.length === 1) return parts[0]!;

  const second = parts[1]!;
  if (/^\d/.test(second)) {
    return `${parts[0]}-${second}`;
  }
  return `${parts[0]}-${second}`;
}

export function filterModels(models: readonly string[], query: string): string[] {
  const normalized = query.trim().toLowerCase();
  if (!normalized) return [...models];
  return models.filter((model) => model.toLowerCase().includes(normalized));
}

export function groupModels(models: readonly string[]): ModelGroup[] {
  const buckets = new Map<string, string[]>();
  for (const model of models) {
    const key = modelGroupKey(model);
    const bucket = buckets.get(key);
    if (bucket) {
      bucket.push(model);
    } else {
      buckets.set(key, [model]);
    }
  }
  return [...buckets.entries()]
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([key, items]) => ({
      key,
      models: [...items].sort((left, right) => left.localeCompare(right)),
    }));
}
