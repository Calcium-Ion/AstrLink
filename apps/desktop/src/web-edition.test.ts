// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from "vitest";
vi.mock("./edition", () => ({ isWebEdition: true }));
import { getCoreStatus, listServices, restartCore } from "./bridge";
import { parseAppSnapshot } from "./core-model";
import { webClientSnippet } from "./WebClientSetup";
import { readFileSync } from "node:fs";
// Use the contract fixture already used by Core rather than fabricating a
// different browser protocol contract.
const contract = JSON.parse(
  readFileSync("../../contracts/examples/capabilities.alpha.json", "utf8"),
);
afterEach(() => vi.unstubAllGlobals());
it("uses fetch without a Tauri bridge in the web edition", async () => {
  const fetch = vi
    .fn()
    .mockResolvedValue(
      new Response(JSON.stringify({ items: [], next_cursor: null })),
    );
  vi.stubGlobal("fetch", fetch);
  expect(await listServices()).toEqual({ items: [], next_cursor: null });
  await expect(restartCore()).rejects.toThrow();
});
it("handshakes with same-origin Core and accepts no synthetic process ID", async () => {
  const version = {
    core_version: "0.1.0",
    control_api_version: "v1",
    protocol_contract_version: "v1",
    build_commit: "unknown",
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async (path) =>
        new Response(
          JSON.stringify(
            String(path).endsWith("health")
              ? { status: "ok" }
              : String(path).endsWith("version")
                ? version
                : contract,
          ),
        ),
    ),
  );
  const snapshot = await getCoreStatus();
  expect(snapshot.ready?.inference_url).toBe(window.location.origin);
  expect(snapshot.pid).toBeNull();
  expect(snapshot.phase).toBe("ready");
  expect(() =>
    parseAppSnapshot({
      ...snapshot,
      ready: { ...snapshot.ready, control_url: "https://other.example" },
    }),
  ).toThrow();
});
it("uses the browser origin and the correct API version suffix in client snippets", () => {
  const base = "https://gateway.example:9443";
  expect(
    JSON.parse(webClientSnippet("claude", base)).env.ANTHROPIC_BASE_URL,
  ).toBe(base);
  expect(webClientSnippet("codex", base)).toContain(`base_url = "${base}/v1"`);
  expect(webClientSnippet("gemini", base)).toContain(
    `GOOGLE_GEMINI_BASE_URL=${base}\n`,
  );
  for (const client of ["opencode", "openclaw", "pi"] as const)
    expect(webClientSnippet(client, base)).toContain(`${base}/v1`);
});
