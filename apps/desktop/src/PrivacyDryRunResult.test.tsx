// @vitest-environment happy-dom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  PrivacyDryRunResult,
  type CompletedPrivacyDryRun,
} from "./PrivacyDryRunResult";

describe("privacy dry-run explanations", () => {
  let container: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  const sample = "alice@example.com";
  const finding = {
    kind: "email" as const,
    path: "/input",
    start: 0,
    end: sample.length,
    confidence: 0.99,
  };
  const base: CompletedPrivacyDryRun = {
    protocol: "openai.responses",
    detector: "local_model",
    minConfidence: 0.6,
    decision: "allow",
    findings_summary: "",
    findings: [],
    suppressed_findings: [],
    inspected_body: JSON.stringify({ input: sample }),
  };
  const render = async (result: CompletedPrivacyDryRun) =>
    act(async () => {
      root.render(
        <PrivacyDryRunResult
          result={result}
          summary=""
          sample={sample}
          onLocate={vi.fn()}
        />,
      );
    });

  it("shows the original for every suppression cause without confusing policy exclusions with low confidence", async () => {
    await render({
      ...base,
      suppressed_findings: [
        { ...finding, reason: "low_confidence", confidence: 0.5999 },
        ...(
          [
            "kind_disabled",
            "allowlisted",
            "placeholder",
            "unrepresentable",
          ] as const
        ).map((reason) => ({ ...finding, reason })),
      ],
    });
    const rows = [
      ...container.querySelectorAll('[data-testid="dry-run-finding"]'),
    ];
    expect(rows.map((row) => row.querySelector("mark")?.textContent)).toEqual(
      Array(5).fill(sample),
    );
    expect(rows[0].textContent).toContain("置信度 59.99%，低于 60% 门槛");
    expect(rows[1].textContent).toContain("该信息类型已关闭");
    expect(rows[2].textContent).toContain("匹配免脱敏名单");
    expect(rows[3].textContent).toContain("已有占位符");
    expect(rows[4].textContent).toContain("无法生成可用的替换值");
    for (const row of rows.slice(1))
      expect(row.textContent).not.toContain("门槛");
    expect(container.querySelector("details")?.open).toBe(false);
  });

  it("explains a blocked request and keeps a sendable text preview out of the result", async () => {
    await render({ ...base, decision: "block", findings: [finding] });
    expect(container.textContent).toContain("不会发送到上游");
    expect(container.querySelector("mark")?.textContent).toBe(sample);
    expect(container.querySelector('[aria-label="处理后文本"]')).toBeNull();
  });

  it("matches replacements by both kind and original text", async () => {
    await render({
      ...base,
      decision: "redact",
      findings: [finding],
      redactions: [
        {
          kind: "phone",
          value: sample,
          placeholder: "wrong-kind",
          style: "token",
        },
        {
          kind: "email",
          value: "bob@example.com",
          placeholder: "wrong-value",
          style: "token",
        },
        {
          kind: "email",
          value: sample,
          placeholder: "replacement@example.invalid",
          style: "natural",
        },
      ],
      redacted_body: JSON.stringify({ input: "replacement@example.invalid" }),
    });
    const row = container.querySelector('[data-testid="dry-run-finding"]')!;
    expect(row.textContent).toContain("replacement@example.invalid");
    expect(row.textContent).not.toContain("wrong-");
    expect(
      container.querySelector('[aria-label="处理后文本"]')?.textContent,
    ).toContain("replacement@example.invalid");
  });
});
