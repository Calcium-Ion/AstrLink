// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// The shared activity poll, answered by hand.
const poll = vi.hoisted(() => {
  let activity: unknown = null;
  const listeners = new Set<() => void>();
  return {
    read: () => activity,
    answer(next: unknown) {
      activity = next;
      listeners.forEach((listener) => listener());
    },
    subscribe(listener: () => void) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
  };
});
vi.mock("./RequestActivityBoard", async () => {
  const { useSyncExternalStore } = await import("react");
  return {
    useRequestActivityPoll: () =>
      useSyncExternalStore(poll.subscribe, poll.read, poll.read),
  };
});

import { CrewEngine, LiveCrew, resetCrewEngineForTests } from "./LiveCrew";
import type { Crew } from "./live-crew-model";
import type {
  RequestActivity,
  RequestActivityCall,
} from "./request-record-model";

const NOW = "2026-11-13T08:10:00Z";

/** A call still running, so its conversation is in progress. */
function running(
  session: string,
  started: string,
  output: RequestActivityCall["output"],
): RequestActivityCall {
  return {
    session_id: session,
    client_type: "claude_code",
    started_at: `2026-11-13T08:${started}Z`,
    completed_at: null,
    status: "pending",
    output,
  };
}
const text = { kind: "text" as const, tools: [] };
const tool = (name: string) => ({ kind: "tool" as const, tools: [name] });
const activity = (...calls: RequestActivityCall[]): RequestActivity => ({
  now: NOW,
  calls,
});

describe("LiveCrew", () => {
  let container: HTMLDivElement;
  let root: Root;
  let engines: CrewEngine[];

  beforeEach(() => {
    (
      globalThis as typeof globalThis & {
        IS_REACT_ACT_ENVIRONMENT?: boolean;
      }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    resetCrewEngineForTests();
    poll.answer(null);
    // Frames step the crew; these tests move it only by reads.
    vi.stubGlobal("requestAnimationFrame", () => 1);
    vi.stubGlobal("cancelAnimationFrame", () => {});
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue(
      new DOMRect(0, 0, 1006, 280),
    );
    engines = [];
    const attach = CrewEngine.prototype.attach;
    vi.spyOn(CrewEngine.prototype, "attach").mockImplementation(function (
      this: CrewEngine,
      canvas,
    ) {
      engines.push(this);
      attach.call(this, canvas);
    });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    resetCrewEngineForTests();
  });

  const mount = () =>
    act(async () => {
      root.render(
        <LiveCrew coreSessionKey="core" isReady todayTokens={null} />,
      );
    });
  /** Another page replaces the overview. */
  const leave = async () => {
    await act(async () => root.unmount());
    root = createRoot(container);
  };
  const answer = (next: RequestActivity) => act(async () => poll.answer(next));
  const crewOf = (engine: CrewEngine) =>
    (engine as unknown as { crew: Crew }).crew;
  const where = (engine: CrewEngine) =>
    crewOf(engine).members.map((member) => ({
      id: member.id,
      x: member.x,
      level: member.level,
      act: member.act,
      arrived: member.arrived,
      waiting: member.waiting,
    }));
  const header = () => ({
    count: container.querySelector('[data-slot="live-crew-count"]')
      ?.textContent,
    rate: container.querySelector('[data-slot="live-crew-rate"]')?.textContent,
  });

  it("opens with everyone already at work", async () => {
    await mount();
    await answer(
      activity(
        running("a", "05:00", tool("Edit")),
        running("b", "06:00", text),
      ),
    );
    const [engine] = engines;
    expect(where(engine)).toMatchObject([
      { id: "a", act: "edit", arrived: true, waiting: false },
      { id: "b", act: "write", arrived: true, waiting: false },
    ]);
    expect(where(engine).every((member) => member.x > 0)).toBe(true);
  });

  it("keeps the crew as it was when the overview comes back", async () => {
    await mount();
    const first = activity(
      running("a", "05:00", tool("Edit")),
      running("b", "06:00", text),
      running("c", "07:00", tool("Read")),
    );
    await answer(first);
    const [engine] = engines;
    const before = where(engine);
    const shown = header();

    await leave();
    await mount();

    // The same engine draws on the new canvas, and the same read changes
    // nothing: nobody walks in again.
    expect(engines).toHaveLength(2);
    expect(engines[1]).toBe(engine);
    expect(where(engine)).toEqual(before);
    expect(header()).toEqual(shown);
    expect(crewOf(engine).members.every((member) => member.alpha === 1)).toBe(
      true,
    );
  });

  it("only brings in the conversations that started while away", async () => {
    await mount();
    await answer(
      activity(
        running("a", "05:00", tool("Edit")),
        running("b", "06:00", text),
        running("c", "07:00", tool("Read")),
      ),
    );
    const [engine] = engines;
    const before = where(engine);

    await leave();
    // While away: a goes on as it was, b ended, c moved on, d and e started.
    const away = activity(
      running("a", "05:00", tool("Edit")),
      running("c", "07:00", tool("WebFetch")),
      running("d", "08:00", text),
      running("e", "09:00", tool("Bash")),
    );
    await mount();
    await answer(away);

    const after = where(engine);
    expect(after.find((member) => member.id === "a")).toEqual(before[0]);
    expect(after.some((member) => member.id === "b")).toBe(false);
    expect(after.find((member) => member.id === "c")).toMatchObject({
      act: "search",
      arrived: true,
    });
    // The newcomers wait their turn at the door, one after the other.
    const fresh = after.filter((member) => member.x < 0);
    expect(fresh.map((member) => member.id)).toEqual(["d", "e"]);
    expect(fresh.every((member) => member.waiting)).toBe(true);
    expect(header().count).toContain("4");

    // Watching again, changes act out as usual.
    await answer(
      activity(
        running("a", "05:00", tool("Read")),
        running("c", "07:00", tool("WebFetch")),
        running("d", "08:00", text),
        running("e", "09:00", tool("Bash")),
      ),
    );
    expect(
      crewOf(engine).members.find((member) => member.id === "a")?.queue,
    ).toMatchObject([{ act: "read" }]);
  });

  it("stops drawing while away and lets go of the canvas", async () => {
    const cancel = vi.fn();
    vi.stubGlobal("cancelAnimationFrame", cancel);
    const frames = vi.fn(() => 7);
    vi.stubGlobal("requestAnimationFrame", frames);
    await mount();
    await answer(activity(running("a", "05:00", text)));
    expect(frames).toHaveBeenCalled();
    await leave();
    expect(cancel).toHaveBeenCalledWith(7);
    const engine = engines[0] as unknown as {
      canvas: unknown;
      frame: number;
    };
    expect(engine.canvas).toBeNull();
    expect(engine.frame).toBe(0);
    // A read while away does not start drawing.
    frames.mockClear();
    poll.answer(activity(running("a", "05:00", tool("Read"))));
    expect(frames).not.toHaveBeenCalled();
  });
});
