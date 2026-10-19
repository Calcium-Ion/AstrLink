import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

import en from "./i18n/locales/en.json";
import zhCN from "./i18n/locales/zh-CN.json";
import {
  intelligentRoutingErrorText,
  routingErrorCodes,
} from "./intelligent-routing-errors";

/**
 * The table in `intelligent-routing-errors.ts` is a claim about what Core can
 * send to the four 智能路由 panels. It drifts silently otherwise: Core adds a
 * code, the panel shows the server's English, and nothing fails. This reads the
 * Go handlers and checks every code they emit is either translated or
 * deliberately left to the localized fallback.
 */

const controlAPIDirectory = fileURLToPath(
  new URL("../../../core/internal/controlapi/", import.meta.url),
);

function readSource(name: string): string {
  return readFileSync(controlAPIDirectory + name, "utf8");
}

/**
 * Codes a file passes to `writeError`/`writeErrorDetails`: the literal third
 * argument, after the writer and the status. Reading the argument itself keeps
 * a string on the following lines from passing for the code of a call whose
 * code is a variable.
 */
function emittedCodes(source: string): string[] {
  const codes = new Set<string>();
  for (const match of source.matchAll(
    /writeError(?:Details)?\(\s*\w+,\s*[^,()]+(?:\([^()]*\))?,\s*"([a-z][a-z0-9_]{2,})"/g,
  )) {
    codes.add(match[1]);
  }
  return [...codes];
}

const classifierSource = readSource("classifier_settings.go");
const routingSource = readSource("routing_settings.go");
const helpersSource = readSource("control_helpers.go");

/**
 * Codes reachable from the classifier settings resource, the routing settings
 * resource, and the shared decode/store helpers they both call.
 */
function reachableCodes(): readonly string[] {
  const codes = new Set([
    ...emittedCodes(classifierSource),
    ...emittedCodes(routingSource),
    // `routing_settings.go` chooses between two literals in a local variable.
    ...(routingSource.match(/code\s*:?=\s*"([a-z][a-z0-9_]{2,})"/g) ?? []).map(
      (entry) => /"([a-z][a-z0-9_]{2,})"/.exec(entry)?.[1] ?? "",
    ),
    // The store translation is reachable from both resources.
    ...emittedCodes(helpersSource),
  ]);
  codes.delete("");
  // The helper's own parameters are not codes.
  codes.delete("code");
  return [...codes].sort();
}

function messageKeys(): Set<string> {
  return new Set(Object.values(routingErrorCodes));
}

describe("intelligent routing error translation", () => {
  it("translates every code these endpoints can emit", () => {
    const missing = reachableCodes().filter(
      (code) => !(code in routingErrorCodes),
    );
    expect(missing).toEqual([]);
  });

  it("has a message for every entry in both catalogs", () => {
    for (const suffix of messageKeys()) {
      const path = `intelligentRouting.errors.${suffix}`;
      const read = (catalog: unknown): string | undefined =>
        path
          .split(".")
          .reduce<unknown>(
            (node, part) =>
              typeof node === "object" && node !== null
                ? (node as Record<string, unknown>)[part]
                : undefined,
            catalog,
          ) as string | undefined;
      expect(typeof read(zhCN), `${path} (zh-CN)`).toBe("string");
      expect(typeof read(en), `${path} (en)`).toBe("string");
    }
  });

  it("renders a control-API envelope from its code, never from the server's English", () => {
    const failure =
      'PUT /control/v1/classifier/settings returned 422: {"error":{"code":"invalid_classifier_settings","message":"classifier timeout_ms must be between 1 and 5000"}}';
    const text = intelligentRoutingErrorText(new Error(failure), "fallback");
    expect(text).not.toContain("returned 422");
    expect(text).not.toContain("must be between");
    expect(text).toBe(zhCN.intelligentRouting.errors.invalidSettings);
  });

  it("keeps the code visible when this build has no text for it", () => {
    const failure =
      'GET /control/v1/routing/settings returned 500: {"error":{"code":"brand_new_code"}}';
    const text = intelligentRoutingErrorText(failure, "fallback");
    expect(text).toContain("brand_new_code");
    expect(text).not.toContain("returned 500");
  });

  it("names a transport failure in the reader's language", () => {
    expect(
      intelligentRoutingErrorText(new Error("error sending request"), "f"),
    ).toBe(zhCN.intelligentRouting.errors.connection);
    expect(
      intelligentRoutingErrorText(new Error("operation timed out"), "f"),
    ).toBe(zhCN.intelligentRouting.errors.timeout);
  });

  it("falls back to the caller's wording when there is no error text", () => {
    expect(intelligentRoutingErrorText(new Error("  "), "加载失败")).toBe(
      "加载失败",
    );
    expect(intelligentRoutingErrorText(undefined, "加载失败")).toBe("加载失败");
  });

  it("passes through text that is not a control-API failure", () => {
    // A host-side bug is for the reader to report, not a known condition.
    expect(
      intelligentRoutingErrorText(new Error("$.dimensions[0].name"), "f"),
    ).toBe("$.dimensions[0].name");
  });
});
