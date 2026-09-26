import { intelligence, type IntelligenceRun } from "./intelligence-bridge";
import { renderQuestionSVG } from "./lib/question-image";

type Listener = (run: IntelligenceRun) => void;
const listeners = new Map<string, Set<Listener>>();
const watching = new Set<string>();

export function subscribeIntelligence(serviceId: string, listener: Listener) {
  const group = listeners.get(serviceId) ?? new Set<Listener>();
  listeners.set(serviceId, group);
  group.add(listener);
  return () => {
    group.delete(listener);
    if (group.size === 0) listeners.delete(serviceId);
  };
}

// Owned by the app session, not a dialog or page. Unsubscribing the UI must not
// stop polling or the SVG -> PNG -> judge handoff.
export function watchIntelligenceRun(run: IntelligenceRun) {
  if (run.status !== "running" || watching.has(run.id)) return;
  watching.add(run.id);
  const rendered = new Set<string>();
  const poll = async () => {
    let finished = false;
    try {
      const next = await intelligence<IntelligenceRun>(
        "run",
        undefined,
        undefined,
        run.id,
      );
      for (const listener of listeners.get(next.service_id) ?? [])
        listener(next);
      finished = next.status !== "running";
      await Promise.all(
        next.items.map(async (item) => {
          if (item.status !== "rendering" || rendered.has(item.question.id))
            return;
          rendered.add(item.question.id);
          let payload: { question_id: string; png?: string; error?: string };
          try {
            payload = {
              question_id: item.question.id,
              png: await renderQuestionSVG(item.output),
            };
          } catch (error) {
            payload = {
              question_id: item.question.id,
              error: String(error).slice(0, 150),
            };
          }
          try {
            await intelligence("render", undefined, payload, next.id);
          } catch {
            rendered.delete(item.question.id);
          }
        }),
      );
    } catch {
      // Temporary bridge failures are retried even when no page is watching.
    } finally {
      if (finished) watching.delete(run.id);
      else setTimeout(() => void poll(), 700);
    }
  };
  setTimeout(() => void poll(), 100);
}

export async function resumeIntelligenceRuns(serviceIds: string[]) {
  await Promise.all(
    serviceIds.map(async (id) => {
      try {
        const history = await intelligence<IntelligenceRun[]>("history", id);
        for (const run of history) watchIntelligenceRun(run);
      } catch {
        // Services can disappear while their catalog is refreshing.
      }
    }),
  );
}
