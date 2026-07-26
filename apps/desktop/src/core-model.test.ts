import { describe, expect, it } from "vitest";

import {
  alphaPlanCount,
  browserSnapshot,
  failedSnapshot,
  phaseLabel,
  phaseTone,
  type AppSnapshot,
  type CapabilitiesResponse,
} from "./core-model";

describe("core status presentation", () => {
  it("does not report the browser preview as a healthy Core", () => {
    const snapshot = browserSnapshot();

    expect(snapshot.phase).toBe("unavailable");
    expect(phaseTone(snapshot.phase)).toBe("neutral");
  });

  it("presents handshake progress separately from ready", () => {
    expect(phaseLabel("handshaking")).toBe("校验中");
    expect(phaseTone("handshaking")).toBe("pending");
    expect(phaseTone("ready")).toBe("positive");
  });

  it("counts only plans explicitly available in alpha", () => {
    const capabilities: CapabilitiesResponse = {
      protocol_contract_version: "v1",
      protocols: [],
      plan_types: [
        {
          id: "native",
          available_in_alpha: true,
          uses_local_conversion: false,
        },
        {
          id: "delegated",
          available_in_alpha: true,
          uses_local_conversion: false,
        },
        {
          id: "relaykit",
          available_in_alpha: false,
          uses_local_conversion: true,
        },
      ],
      conversion_engine: {
        name: "relaykit",
        version: null,
        available: false,
        edges: [],
      },
    };

    expect(alphaPlanCount(capabilities)).toBe(2);
  });

  it("clears all stale Core data after a refresh failure", () => {
    const previous = {
      ...browserSnapshot(),
      app_version: "0.1.0",
      phase: "ready",
      pid: 42,
      ready: {
        event: "ready",
        core_version: "0.1.0-dev",
        control_api_version: "v1",
        protocol_contract_version: "v1",
        inference_url: "http://127.0.0.1:8317",
        control_url: "http://127.0.0.1:49152",
      },
      health: { status: "ok" },
      version: {
        core_version: "0.1.0-dev",
        control_api_version: "v1",
        protocol_contract_version: "v1",
        build_commit: "unknown",
      },
      capabilities: {
        protocol_contract_version: "v1",
        protocols: [],
        plan_types: [],
        conversion_engine: {
          name: "relaykit",
          version: null,
          available: false,
          edges: [],
        },
      },
      last_error: null,
    } as AppSnapshot;

    expect(failedSnapshot(previous, "bridge failed")).toEqual({
      app_version: "0.1.0",
      phase: "error",
      pid: null,
      ready: null,
      health: null,
      version: null,
      capabilities: null,
      last_error: "bridge failed",
    });
    expect(failedSnapshot(null, "first query failed").app_version).toBe("Unknown");
  });
});
