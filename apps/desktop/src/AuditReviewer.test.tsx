// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { AuditPartSection, HTTPMetaSection } from "./AuditReviewer";
import type { CopyFeedback } from "./copy-feedback";
import type { AuditContentPart, AuditHTTPMeta } from "./request-record-model";

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
    const rawText = container.querySelector('[data-testid="audit-raw"] pre')?.textContent ?? "";
    expect(rawText.startsWith("event: response.output_text.delta")).toBe(true);

    const eventsTab = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "事件",
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
      if (container.querySelectorAll('[data-testid="audit-event"]').length > 0) break;
    }
    expect(container.querySelectorAll('[data-testid="audit-event"]')).toHaveLength(300);
    const loadMore = [...container.querySelectorAll("button")].find((button) =>
      button.textContent?.includes("再显示 160 个事件"),
    );
    await act(async () => (loadMore as HTMLButtonElement).click());
    expect(container.querySelectorAll('[data-testid="audit-event"]')).toHaveLength(
      eventCount,
    );
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

    const marks = [...container.querySelectorAll('[data-testid="privacy-mark"]')];
    expect(marks).toHaveLength(1);
    expect(marks[0]?.textContent).toBe("<PRIVATE_EMAIL_aaaaaaaaaaaaaaaa>");
    expect(marks[0]?.getAttribute("data-kind")).toBe("email");
    expect(marks[0]?.closest("pre")?.textContent).toContain("alice@example.com");
    expect(marks[0]?.textContent).not.toContain("alice@");

    const copyButton = [...container.querySelectorAll("button")].find((button) =>
      button.textContent?.includes("复制"),
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

    expect(container.querySelectorAll('[data-testid="audit-raw-segment"]')).toHaveLength(1);
    const loadNext = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "加载下一段",
    );
    await act(async () => (loadNext as HTMLButtonElement).click());
    expect(container.querySelectorAll('[data-testid="audit-raw-segment"]')).toHaveLength(2);
  });

  it("renders redacted headers distinctly and reports missing capture", async () => {
    const meta: AuditHTTPMeta = {
      method: "POST",
      url: "/v1/responses?stream=true",
      http_version: "HTTP/1.1",
      request_headers: [
        { name: "authorization", value: "Bearer <redacted:51 chars>", redacted: true },
        { name: "content-type", value: "application/json", redacted: false },
      ],
      response_status: 200,
      response_headers: [
        { name: "x-request-id", value: "req_1", redacted: false },
      ],
    };
    await act(async () => {
      root.render(
        <HTTPMetaSection copyFeedback={noopFeedback} meta={meta} />,
      );
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
});
