import { describe, expect, it } from "vitest";

import type { ConversionEngineCapability } from "./core-model";
import { routingFormatReport } from "./model-routing-format-model";
import type { ModelRoutingGroup } from "./model-routing-group-model";
import type { RoutableService } from "./service-model";

const engine = {
  available: true,
  edges: [
    { from: "openai.responses", to: "anthropic.messages", quality: "good" },
    { from: "openai.chat", to: "anthropic.messages", quality: "good" },
  ],
} as unknown as ConversionEngineCapability;

function service(
  id: string,
  protocols: string[],
  enabled = true,
): RoutableService {
  return {
    id,
    name: id === "claude" ? "Claude 订阅" : id,
    kind: "openai_compatible",
    enabled,
    models: [],
    capabilities: protocols.map((protocol) => ({
      protocol,
      mode: "native",
      streaming: true,
    })),
  } as unknown as RoutableService;
}

const group: ModelRoutingGroup = {
  id: "program",
  enabled: true,
  members: [
    { id: "strong", service_id: "claude", model: "claude-opus-5-5" },
    { id: "translator", service_id: "glm", model: "glm-5.3" },
    { id: "fast", service_id: "subapi", model: "gpt-6.1-sol" },
    { id: "unused", service_id: "gemini-only", model: "gemini" },
  ],
  default_member_id: "fast",
  rules: [
    {
      id: "coding",
      priority: 1,
      when: { field: "task_type", in: ["coding"] },
      target_member_id: "strong",
    },
    {
      id: "translate",
      priority: 2,
      when: { field: "literal_keyword", contains_any: ["翻译"] },
      target_member_id: "translator",
    },
  ],
};

const services = [
  service("claude", ["anthropic.messages"]),
  service("glm", ["anthropic.messages", "openai.chat", "openai.responses"]),
  service("subapi", ["anthropic.messages", "openai.chat", "openai.responses"]),
  service("gemini-only", ["google.generate_content"]),
];

describe("routingFormatReport", () => {
  it("lists the formats some models refuse, with the rules that would fail", () => {
    const report = routingFormatReport(group, services, engine);
    expect(report.mixed).toBe(true);
    expect(report.rows.map((row) => row.protocol)).toEqual([
      "anthropic.messages",
      "openai.responses",
      "openai.chat",
    ]);
    expect(report.rows[0].gaps).toEqual([]);
    const responses = report.rows[1].gaps;
    expect(responses.map((gap) => gap.member.id)).toEqual(["strong"]);
    expect(responses[0]).toMatchObject({ rules: [1], fallback: false });
    expect(responses[0].convertible).toBe(true);
    expect(report.memberGaps.get("strong")).toEqual([
      "openai.responses",
      "openai.chat",
    ]);
    // An unused member is cleared on save; it never counts.
    expect(report.memberGaps.has("unused")).toBe(false);
    expect([...report.fixable.keys()]).toEqual(["claude"]);
    expect(report.fixable.get("claude")?.protocols).toEqual([
      "openai.responses",
      "openai.chat",
    ]);
  });

  it("stays quiet when every model accepts the same formats", () => {
    const uniform = { ...group, rules: [group.rules[1]] };
    expect(routingFormatReport(uniform, services, engine).mixed).toBe(false);
  });

  it("marks a gap a provider cannot convert, and a disabled provider", () => {
    const report = routingFormatReport(
      group,
      [
        service("claude", ["anthropic.messages"]),
        service("glm", ["anthropic.messages", "openai.chat"], false),
        service("subapi", ["anthropic.messages", "openai.chat"]),
      ],
      { available: false, edges: [] } as unknown as ConversionEngineCapability,
    );
    const chat = report.rows.find((row) => row.protocol === "openai.chat");
    expect(chat?.gaps.map((gap) => [gap.member.id, gap.convertible])).toEqual([
      ["strong", false],
      ["translator", false],
    ]);
    expect(report.fixable.size).toBe(0);
  });

  it("names the fallback when the Otherwise model is the one that refuses", () => {
    const report = routingFormatReport(
      { ...group, default_member_id: "strong", rules: [group.rules[1]] },
      services,
      engine,
    );
    const responses = report.rows.find(
      (row) => row.protocol === "openai.responses",
    );
    expect(responses?.gaps[0]).toMatchObject({ rules: [], fallback: true });
  });

  it("counts an entry that cannot stream as incompatible, and not as something to convert", () => {
    const noStream = {
      ...service("glm", ["anthropic.messages", "openai.responses"]),
      capabilities: [
        { protocol: "anthropic.messages", mode: "native", streaming: true },
        { protocol: "openai.responses", mode: "native", streaming: true },
        { protocol: "openai.chat", mode: "native", streaming: false },
      ],
    } as unknown as RoutableService;
    const report = routingFormatReport(
      group,
      [services[0], noStream, services[2]],
      engine,
    );
    const chat = report.rows.find((row) => row.protocol === "openai.chat");
    const translator = chat?.gaps.find((gap) => gap.member.id === "translator");
    expect(translator).toMatchObject({ convertible: false, rules: [2] });
  });
});
