export type ModelGroup = {
  key: string;
  models: string[];
};

export type ModelCategory = {
  key: string;
  groups: ModelGroup[];
};

function modelHead(model: string): string[] {
  const slash = model.indexOf("/");
  if (slash > 0) {
    return [model.slice(0, slash)];
  }
  return model.split("-").filter(Boolean);
}

/** Vendor / org key above product families (claude, gpt, openai/…). */
export function modelCategoryKey(model: string): string {
  const parts = modelHead(model);
  return parts[0] ?? model;
}

/** Family key for denser allow-list browsing (claude-sonnet, gpt-5.6, org/…). */
export function modelGroupKey(model: string): string {
  const parts = modelHead(model);
  if (parts.length === 0) return model;
  if (parts.length === 1) return parts[0]!;
  return `${parts[0]}-${parts[1]}`;
}

export function filterModels(
  models: readonly string[],
  query: string,
): string[] {
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

export function groupModelsByCategory(
  models: readonly string[],
): ModelCategory[] {
  const buckets = new Map<string, ModelGroup[]>();
  for (const group of groupModels(models)) {
    const key = modelCategoryKey(group.models[0] ?? group.key);
    const bucket = buckets.get(key);
    if (bucket) {
      bucket.push(group);
    } else {
      buckets.set(key, [group]);
    }
  }
  return [...buckets.entries()]
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([key, groups]) => ({ key, groups }));
}
