import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

import { modelRoutingWireContract } from "./model-routing-wire-contract";
import { parseRequestRecord } from "./request-record-model";

/**
 * The desktop half of the model routing contract was written from the design
 * document while the Go half was written from the same document — each side
 * tested against its own assumption, and `execution.status` vs `state` shipped
 * unnoticed. This file reads the Go struct tags and compares them with what the
 * parser actually reads, so the next such drift fails here.
 */

const contractSource = readFileSync(
  fileURLToPath(
    new URL("../../../core/contract/model_routing.go", import.meta.url),
  ),
  "utf8",
);

/** JSON names a Go struct declares, split into required and `omitempty`. */
function goWireFields(structName: string): {
  required: string[];
  optional: string[];
} {
  const body = contractSource.match(
    new RegExp(`type ${structName} struct \\{([\\s\\S]*?)\\n\\}`),
  )?.[1];
  if (body === undefined) throw new Error(`no Go struct ${structName}`);
  const required: string[] = [];
  const optional: string[] = [];
  for (const match of body.matchAll(/json:"([^",]+)(,omitempty)?"/g)) {
    (match[2] ? optional : required).push(match[1]);
  }
  return { required, optional };
}

const goStructs = {
  decision: "ModelRoutingDecision",
  fieldObservation: "ModelRoutingFieldObservation",
  candidateObservation: "ModelRoutingCandidateObservation",
  ruleVerdict: "ModelRoutingRuleVerdict",
  classifierAnswers: "ModelRoutingClassifierAnswers",
  dimensionAnswer: "ModelRoutingDimensionAnswer",
  bindingReset: "ModelRoutingBindingReset",
  execution: "ModelRoutingExecution",
  privacyAttempt: "ModelRoutingPrivacyAttempt",
} as const;

describe("model routing wire contract vs core/contract/model_routing.go", () => {
  it("names every Go field the desktop expects, with matching optionality", () => {
    for (const [slot, structName] of Object.entries(goStructs)) {
      const go = goWireFields(structName);
      const desktop = modelRoutingWireContract[slot as keyof typeof goStructs];
      expect({ [slot]: desktop.required }, `${structName} required`).toEqual({
        [slot]: [...go.required],
      });
      expect({ [slot]: desktop.optional }, `${structName} omitempty`).toEqual({
        [slot]: [...go.optional],
      });
    }
  });

  it("keeps the desktop from inventing fields the backend never writes", () => {
    // `result_code` was one such field: optional on the desktop, absent from
    // the contract. An optional field that can never arrive reads as a real
    // capability to whoever comes next.
    const declared = new Set(
      Object.values(goWireFields("ModelRoutingDecision")).flat(),
    );
    for (const slot of ["decision", "execution"] as const) {
      for (const key of modelRoutingWireContract[slot].optional)
        expect(declared.has(key), `${key} is not in the Go contract`).toBe(
          true,
        );
    }
  });
});

describe("model routing record written in the Go wire shape", () => {
  // Only the tags the Go contract declares; nothing invented on this side.
  const backendRecord = {
    id: "req_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    started_at: "2026-07-25T10:00:00Z",
    completed_at: "2026-07-25T10:00:01Z",
    status: "succeeded",
    input_protocol: "openai.responses",
    requested_model: "astrlink/superclaude",
    streaming: true,
    route_id: "route_01",
    service_id: "service_01",
    local_access_token_id: "token_01",
    plan: { kind: "native" },
    http_status: 200,
    latency_ms: 120,
    usage: {
      input_tokens: 10,
      output_tokens: 20,
      total_tokens: 30,
      cache_read_tokens: 2,
    },
    error: null,
    audit: {
      request_body_captured: true,
      response_content_captured: false,
      request_body_truncated: false,
      response_content_truncated: false,
    },
    privacy_restore: {
      enabled: true,
      mapping_count: 4,
      restored_count: 5,
      visible_restored_count: 3,
      tool_argument_restored_count: 2,
      fallback_count: 0,
    },
    extensions: {},
    model_routing: {
      schema_version: 1,
      virtual_model_id: "astrlink/superclaude",
      group_id: "superclaude",
      config_snapshot_version:
        "6f1c0b2a4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f8",
      selection_source: "rule",
      rule_id: "coding",
      rule_priority: 100,
      used_default: false,
      // `match_count` is a plain Go int with no omitempty, so it is on the
      // wire for every observation, including the unset ones.
      fields: [
        { field: "streaming", source: "local", known: true, match_count: 0 },
        {
          field: "task_type",
          source: "classifier",
          known: true,
          match_count: 1,
          classifier_id: "classifier_ab0123456789abcdef0123456789abcd",
        },
        {
          field: "literal_keyword",
          source: "local",
          known: false,
          match_count: 0,
          skip_reason: "local_unknown",
          error_code: "classifier_timeout",
        },
      ],
      member_id: "strong",
      member_service_id: "anthropic",
      member_model: "claude-sonnet",
      fallback_reason: "primary unavailable",
      execution: { status: "succeeded", attempt_count: 1 },
      privacy_attempts: [
        {
          attempt_index: 0,
          service_id: "anthropic",
          decision: "allow",
          finding_count: 0,
          duration_ms: 3,
        },
        {
          attempt_index: 1,
          service_id: "openai",
          decision: "reject",
          finding_count: 2,
          duration_ms: 4,
          error_code: "model_routing_privacy_blocked",
        },
      ],
      duration_ms: 12,
      classifier_duration_ms: 7,
    },
  };

  it("parses the whole record instead of throwing the detail page away", () => {
    const parsed = parseRequestRecord(backendRecord);
    expect(parsed.model_routing).toBeDefined();
    expect(parsed.model_routing?.execution).toStrictEqual({
      status: "succeeded",
      attempt_count: 1,
    });
  });

  it("keeps a classifier ID under the name the backend sends", () => {
    const parsed = parseRequestRecord(backendRecord);
    const classified = parsed.model_routing?.fields.find(
      (field) => field.field === "task_type",
    );
    expect(classified?.classifier_id).toBe(
      "classifier_ab0123456789abcdef0123456789abcd",
    );
  });

  it("rejects a record whose execution uses the desktop-only key", () => {
    const { model_routing: routing } = backendRecord;
    const drifted = {
      ...backendRecord,
      model_routing: {
        ...routing,
        execution: { state: "succeeded", attempt_count: 1 },
      },
    };
    expect(() => parseRequestRecord(drifted)).toThrow(
      "execution.status）：未知状态",
    );
  });
});
