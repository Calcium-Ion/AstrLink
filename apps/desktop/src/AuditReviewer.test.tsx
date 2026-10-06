// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  AuditPartSection,
  AuditResultSection,
  AuditWireView,
  HTTPMetaSection,
} from "./AuditReviewer";
import type { CopyFeedback } from "./copy-feedback";
import type {
  AuditContentPart,
  AuditHTTPMeta,
  AuditWithheldPart,
} from "./request-record-model";

const noopFeedback: CopyFeedback = {
  activeKey: null,
  state: "idle",
  copy: () => undefined,
};

describe("AuditReviewer sections", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    (
      globalThis as typeof globalThis & {
        IS_REACT_ACT_ENVIRONMENT?: boolean;
      }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  it("defaults the client result to merged output and keeps raw capture accessible", async () => {
    const content =
      'data: {"type":"response.output_text.delta","delta":"Readable reply"}\n\n';
    await act(async () => {
      root.render(
        <AuditResultSection
          part={{
            content,
            media_type: "text/event-stream",
            captured_bytes: content.length,
            truncated: false,
          }}
        />,
      );
    });
    expect(
      container.querySelector('[data-testid="audit-result-preview"]')
        ?.textContent,
    ).toContain("Readable reply");
    expect(container.textContent).not.toContain("response.output_text.delta");
    expect(container.querySelector('[data-testid="audit-raw"]')).toBeNull();
    const raw = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "原文",
    );
    expect(raw).toBeDefined();
    await act(async () => raw!.click());
    expect(
      container.querySelector('[data-testid="audit-raw"] pre')?.textContent,
    ).toBe(content);
  });

  it("lays the client response out as a JSON tree or stream events beside the original", async () => {
    const json = '{"choices":[{"message":{"content":"Tree reply"}}]}';
    const viewButton = (label: string) =>
      [...container.querySelectorAll("button")].find(
        (button) => button.textContent === label,
      );
    await act(async () => {
      root.render(
        <AuditResultSection
          part={{
            content: json,
            media_type: "application/json",
            captured_bytes: json.length,
            truncated: false,
          }}
        />,
      );
    });
    await act(async () => viewButton("格式化")!.click());
    const tree = container.querySelector('[data-testid="json-tree"]');
    expect(tree?.textContent).toContain('"content": "Tree reply"');
    expect(container.querySelector('[data-testid="audit-raw"]')).toBeNull();

    const stream =
      'data: {"type":"response.output_text.delta","delta":"Event reply"}\n\n';
    await act(async () => {
      root.render(
        <AuditResultSection
          key="stream"
          part={{
            content: stream,
            media_type: "text/event-stream",
            captured_bytes: stream.length,
            truncated: false,
          }}
        />,
      );
    });
    await act(async () => viewButton("格式化")!.click());
    await vi.waitFor(() =>
      expect(
        container.querySelector('[data-testid="audit-event"]')?.textContent,
      ).toContain("Event reply"),
    );
  });

  it("opens a stream event as the JSON tree and finds into it", async () => {
    const stream = [
      'event: response.created\ndata: {"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}',
      'event: response.output_text.delta\ndata: {"type":"response.output_text.delta","delta":"needle here"}',
    ]
      .map((event) => `${event}\n\n`)
      .join("");
    const part: AuditContentPart = {
      content: stream,
      media_type: "text/event-stream",
      captured_bytes: stream.length,
      truncated: false,
    };
    await act(async () => {
      root.render(<AuditWireView mode="structured" part={part} />);
    });
    await vi.waitFor(() =>
      expect(
        container.querySelectorAll('[data-testid="audit-event"]'),
      ).toHaveLength(2),
    );
    // Closed events build nothing; opening one lays its data out as a tree.
    expect(container.querySelector('[data-testid="json-tree"]')).toBeNull();
    const first = container.querySelector<HTMLDetailsElement>(
      '[data-testid="audit-event"]',
    )!;
    await act(async () => {
      first.open = true;
      first.dispatchEvent(new Event("toggle"));
    });
    expect(
      first.querySelector('[data-testid="json-tree"]')?.textContent,
    ).toContain('"status": "in_progress"');

    const onResult = vi.fn();
    await act(async () => {
      root.render(
        <AuditWireView
          find={{ query: "needle", active: 0, seq: 1, onResult }}
          mode="structured"
          part={part}
        />,
      );
    });
    expect(onResult).toHaveBeenLastCalledWith({ count: 1, capped: false });
    const mark = container.querySelector('mark[data-find-active="true"]');
    expect(mark?.textContent).toBe("needle");
    expect(mark?.closest('[data-testid="json-tree"]')).not.toBeNull();
  });

  it("shows an empty-output explanation for lifecycle-only streams", async () => {
    const content =
      'data: {"type":"response.created","response":{"output":[]}}\n\n';
    await act(async () => {
      root.render(
        <AuditResultSection
          part={{
            content,
            media_type: "text/event-stream",
            captured_bytes: content.length,
            truncated: false,
          }}
        />,
      );
    });
    expect(container.textContent).toContain("未捕获到回复或工具调用");
    expect(container.querySelector('[data-testid="audit-raw"]')).toBeNull();
  });

  it("bounds large reconstructed replies and lets the reader load the remainder", async () => {
    const content = `${"a".repeat(64 * 1024)}tail-of-reply`;
    await act(async () => {
      root.render(
        <AuditResultSection
          part={{
            content,
            media_type: "text/plain",
            captured_bytes: content.length,
            truncated: false,
          }}
        />,
      );
    });
    const preview = () =>
      container.querySelector('[data-testid="audit-result-preview"]');
    expect(preview()?.textContent).not.toContain("tail-of-reply");
    const next = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "加载下一段",
    );
    await act(async () => next!.click());
    expect(preview()?.textContent).toContain("tail-of-reply");
  });

  it("ignores an obsolete multi-batch parse when selecting another response", async () => {
    const content =
      'data: {"type":"response.output_text.delta","delta":"old"}\n\n'.repeat(
        1200,
      );
    await act(async () => {
      root.render(
        <AuditResultSection
          part={{
            content,
            media_type: "text/event-stream",
            captured_bytes: content.length,
            truncated: false,
          }}
        />,
      );
    });
    await act(async () => {
      root.render(
        <AuditResultSection
          part={{
            content: "New response",
            media_type: "text/plain",
            captured_bytes: 12,
            truncated: false,
          }}
        />,
      );
      await new Promise((resolve) => setTimeout(resolve, 20));
    });
    expect(
      container.querySelector('[data-testid="audit-result-preview"]')
        ?.textContent,
    ).toBe("New response");
  });

  it("shows an 85KB stream as raw text by default and parses events only when the tab is opened", async () => {
    const delta = "readable-output-".repeat(12);
    const eventCount = 460;
    const stream = Array.from(
      { length: eventCount },
      (_, index) =>
        `event: response.output_text.delta\ndata: ${JSON.stringify({
          type: "response.output_text.delta",
          delta: `${index}:${delta}`,
        })}\n\n`,
    ).join("");
    expect(stream.length).toBeGreaterThan(85 * 1024);
    const part: AuditContentPart = {
      media_type: "text/event-stream",
      content: stream,
      truncated: false,
      captured_bytes: stream.length,
    };

    await act(async () => {
      root.render(
        <AuditPartSection
          copyFeedback={noopFeedback}
          part={part}
          protocol="openai.responses"
          sectionKey="response-content"
          title="响应内容"
        />,
      );
      await Promise.resolve();
    });

    // Raw is the default view; nothing is parsed yet.
    expect(container.querySelector('[data-testid="audit-raw"]')).not.toBeNull();
    expect(container.querySelector('[data-testid="audit-event"]')).toBeNull();
    const rawText =
      container.querySelector('[data-testid="audit-raw"] pre')?.textContent ??
      "";
    expect(rawText.startsWith("event: response.output_text.delta")).toBe(true);

    const eventsTab = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "格式化",
    );
    expect(eventsTab).toBeDefined();
    await act(async () => {
      (eventsTab as HTMLButtonElement).dispatchEvent(
        new MouseEvent("mousedown", { bubbles: true, button: 0 }),
      );
    });
    // Incremental parsing yields between batches.
    for (let round = 0; round < 20; round += 1) {
      // eslint-disable-next-line no-await-in-loop
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 0));
      });
      if (container.querySelectorAll('[data-testid="audit-event"]').length > 0)
        break;
    }
    expect(
      container.querySelectorAll('[data-testid="audit-event"]'),
    ).toHaveLength(300);
    const loadMore = [...container.querySelectorAll("button")].find((button) =>
      button.textContent?.includes("再显示 160 个事件"),
    );
    await act(async () => (loadMore as HTMLButtonElement).click());
    expect(
      container.querySelectorAll('[data-testid="audit-event"]'),
    ).toHaveLength(eventCount);
  });

  it("highlights privacy placeholders without marking originals", async () => {
    const copied: string[] = [];
    const copyFeedback: CopyFeedback = {
      activeKey: null,
      state: "idle",
      copy: (_key, text) => {
        copied.push(text);
      },
    };
    const part: AuditContentPart = {
      media_type: "application/json",
      content: `{"input":"alice@example.com <PRIVATE_EMAIL_aaaaaaaaaaaaaaaa>"}`,
      truncated: false,
      captured_bytes: 64,
    };
    await act(async () => {
      root.render(
        <AuditPartSection
          copyFeedback={copyFeedback}
          part={part}
          protocol="openai.responses"
          sectionKey="upstream-request"
          title="脱敏后请求"
        />,
      );
      await Promise.resolve();
    });

    const marks = [
      ...container.querySelectorAll('[data-testid="privacy-mark"]'),
    ];
    expect(marks).toHaveLength(1);
    expect(marks[0]?.textContent).toBe("<PRIVATE_EMAIL_aaaaaaaaaaaaaaaa>");
    expect(marks[0]?.getAttribute("data-kind")).toBe("email");
    expect(
      marks[0]?.closest('[data-testid="json-tree"]')?.textContent,
    ).toContain("alice@example.com");
    expect(marks[0]?.textContent).not.toContain("alice@");

    const copyButton = [...container.querySelectorAll("button")].find(
      (button) => button.textContent?.includes("复制"),
    );
    expect(copyButton).toBeDefined();
    await act(async () => {
      (copyButton as HTMLButtonElement).click();
    });
    expect(copied[0]).toBe(part.content);
    expect(copied[0]).not.toContain("<mark");
  });

  it("pages large raw content in 256KB segments", async () => {
    const content = "y".repeat(300 * 1024);
    const part: AuditContentPart = {
      media_type: "text/plain",
      content,
      truncated: false,
      captured_bytes: content.length,
    };
    await act(async () => {
      root.render(
        <AuditPartSection
          copyFeedback={noopFeedback}
          part={part}
          protocol="openai.responses"
          sectionKey="response-content"
          title="响应内容"
        />,
      );
      await Promise.resolve();
    });

    expect(
      container.querySelectorAll('[data-testid="audit-raw-segment"]'),
    ).toHaveLength(1);
    const loadNext = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "加载下一段",
    );
    await act(async () => (loadNext as HTMLButtonElement).click());
    expect(
      container.querySelectorAll('[data-testid="audit-raw-segment"]'),
    ).toHaveLength(2);
  });

  it("parses a large truncated JSON body in slices into a folded tree", async () => {
    const prompt = `${"long prompt ".repeat(40_000)}prompt-tail`;
    const content = JSON.stringify({
      model: "gpt-4.1",
      input: [{ role: "user", content: prompt }],
      metadata: { note: "cut-before-this" },
    }).slice(0, -30);
    const part: AuditContentPart = {
      media_type: "application/json",
      content,
      truncated: true,
      captured_bytes: content.length,
    };
    await act(async () => {
      root.render(<AuditWireView mode="structured" part={part} />);
    });
    // Past the one-shot size the parse runs in slices off the render.
    for (let round = 0; round < 50; round += 1) {
      // eslint-disable-next-line no-await-in-loop
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 0));
      });
      if (container.querySelector('[data-testid="json-tree"]')) break;
    }
    const tree = container.querySelector('[data-testid="json-tree"]');
    expect(tree?.textContent).toContain('"model": "gpt-4.1"');
    expect(tree?.textContent).toContain(
      `${prompt.length.toLocaleString()} 字符`,
    );
    expect(tree?.textContent).not.toContain("prompt-tail");
    expect(tree?.textContent).toContain("此处截断");
    expect(container.querySelector('[data-testid="audit-raw"]')).toBeNull();
  });

  it("falls back to the original text for malformed JSON", async () => {
    const content = '{"model": "gpt-4.1",, "input": []}';
    const part: AuditContentPart = {
      media_type: "application/json",
      content,
      truncated: false,
      captured_bytes: content.length,
    };
    await act(async () => {
      root.render(<AuditWireView mode="structured" part={part} />);
    });
    expect(container.textContent).toContain("JSON 无效");
    expect(container.querySelector('[data-testid="json-tree"]')).toBeNull();
    expect(
      container.querySelector('[data-testid="audit-raw"] pre')?.textContent,
    ).toBe(content);
  });

  it("renders redacted headers distinctly and reports missing capture", async () => {
    const meta: AuditHTTPMeta = {
      method: "POST",
      url: "/v1/responses?stream=true",
      http_version: "HTTP/1.1",
      request_headers: [
        {
          name: "authorization",
          value: "Bearer <redacted:51 chars>",
          redacted: true,
        },
        { name: "content-type", value: "application/json", redacted: false },
      ],
      response_status: 200,
      response_headers: [
        { name: "x-request-id", value: "req_1", redacted: false },
      ],
    };
    await act(async () => {
      root.render(<HTTPMetaSection copyFeedback={noopFeedback} meta={meta} />);
      await Promise.resolve();
    });

    expect(container.textContent).toContain("POST /v1/responses?stream=true");
    expect(container.textContent).toContain("Bearer <redacted:51 chars>");
    expect(container.querySelector('[data-redacted="true"]')).not.toBeNull();
    expect(container.textContent).toContain("x-request-id");

    await act(async () => {
      root.render(<HTTPMetaSection copyFeedback={noopFeedback} meta={null} />);
      await Promise.resolve();
    });
    expect(container.textContent).toContain("此记录未捕获 HTTP 元数据");
  });

  it("marks a raw-locked part and offers the unlock", async () => {
    const onUnlock = vi.fn();
    const withheld: AuditWithheldPart = {
      reason: "raw_locked",
      raw_available: true,
      media_type: "application/json",
      truncated: false,
      captured_bytes: 64,
    };
    await act(async () => {
      root.render(
        <AuditPartSection
          copyFeedback={noopFeedback}
          onUnlock={onUnlock}
          part={null}
          protocol="openai.responses"
          sectionKey="request-body"
          title="客户端请求"
          withheld={withheld}
        />,
      );
    });

    const body = container.querySelector('[data-slot="audit-part-withheld"]');
    expect(body?.textContent).toContain("已锁定");
    expect(body?.textContent).toContain(
      "原文已封存，输入原文口令解锁后才能查看。",
    );
    const unlock = [...container.querySelectorAll("button")].find(
      (candidate) => candidate.textContent?.trim() === "解锁",
    );
    await act(async () => unlock?.click());
    expect(onUnlock).toHaveBeenCalledOnce();
  });

  it("tells a withheld part apart from one that was never captured", async () => {
    await act(async () => {
      root.render(
        <AuditPartSection
          copyFeedback={noopFeedback}
          onUnlock={() => undefined}
          part={null}
          protocol="openai.responses"
          sectionKey="request-body"
          title="客户端请求"
          withheld={{
            reason: "privacy_redacted",
            raw_available: false,
            media_type: "application/json",
            truncated: false,
            captured_bytes: 64,
          }}
        />,
      );
    });
    const body = container.querySelector('[data-slot="audit-part-withheld"]');
    expect(body?.textContent).toContain("此部分已捕获，当前视图不显示。");
    expect(body?.textContent).not.toContain("已锁定");
    expect(container.querySelectorAll("button")).toHaveLength(0);

    await act(async () => {
      root.render(
        <AuditPartSection
          copyFeedback={noopFeedback}
          part={null}
          protocol="openai.responses"
          sectionKey="request-body"
          title="客户端请求"
        />,
      );
    });
    expect(
      container.querySelector('[data-slot="audit-part-withheld"]'),
    ).toBeNull();
  });
});
