import { describe, expect, it } from "vitest";

import type { RequestSession } from "./request-record-model";
import { mergeLiveSessions } from "./session-live-model";

function session(
  id: string,
  overrides: Partial<RequestSession> = {},
): RequestSession {
  return {
    id,
    title: id,
    started_at: "2026-07-25T10:00:00Z",
    last_started_at: "2026-07-25T10:00:00Z",
    completed_at: "2026-07-25T10:00:02Z",
    turn_count: 1,
    call_count: 1,
    status: "succeeded",
    requested_model: "gpt-4.1",
    input_protocol: "openai.responses",
    service_id: "service_01",
    local_access_token_id: null,
    ...overrides,
  };
}

describe("mergeLiveSessions", () => {
  it("updates the model badge when only reasoning effort changes or clears", () => {
    const before = session("sess_a", { reasoning_effort: "low" });
    const after = session("sess_a", { reasoning_effort: "high" });
    const changed = mergeLiveSessions([before], [], [after], false);
    expect(changed.items[0]).toBe(after);
    const cleared = session("sess_a", { reasoning_effort: null });
    expect(mergeLiveSessions(changed.items, [], [cleared], false).items[0]).toBe(cleared);
  });

  // The monitor polls once a second, and an idle gateway answers with the same
  // rows decoded into new objects. Passing those on re-rendered the list, the
  // open detail and every trajectory row behind it, so an empty merge has to be
  // indistinguishable from no merge at all.
  it("returns the original arrays when the poll brought nothing new", () => {
    const items = [session("sess_a"), session("sess_b")];
    const queued = [session("sess_c")];

    const merged = mergeLiveSessions(
      items,
      queued,
      [session("sess_a"), session("sess_b"), session("sess_c")],
      true,
    );

    expect(merged.items).toBe(items);
    expect(merged.queued).toBe(queued);
    expect(merged.added).toBe(0);
  });

  it("adopts only the row that moved", () => {
    const settled = session("sess_a");
    const live = session("sess_b", { status: "pending", completed_at: null });

    const merged = mergeLiveSessions(
      [settled, live],
      [],
      [
        session("sess_a"),
        session("sess_b", {
          status: "pending",
          completed_at: null,
          call_count: 2,
        }),
      ],
      true,
    );

    expect(merged.items).not.toBe(settled);
    expect(merged.items[0]).toBe(settled);
    expect(merged.items[1]).not.toBe(live);
    expect(merged.items[1]?.call_count).toBe(2);
  });

  it("prepends new sessions when following the top and queues them otherwise", () => {
    const known = session("sess_a");
    const fresh = session("sess_b", {
      started_at: "2026-07-25T10:05:00Z",
      last_started_at: "2026-07-25T10:05:00Z",
    });

    const following = mergeLiveSessions([known], [], [fresh, known], false);
    expect(following.items.map((entry) => entry.id)).toEqual([
      "sess_b",
      "sess_a",
    ]);
    expect(following.added).toBe(1);

    const parked = mergeLiveSessions([known], [], [fresh, known], true);
    expect(parked.items.map((entry) => entry.id)).toEqual(["sess_a"]);
    expect(parked.queued.map((entry) => entry.id)).toEqual(["sess_b"]);
    expect(parked.added).toBe(1);
  });
});
