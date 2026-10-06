// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { FindRequest } from "@/find-model";
import { parseJsonTree } from "@/json-tree-model";

import { JsonTreeView } from "./JsonTreeView";

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  (
    globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
});

const longText = `${"请帮我总结这段对话。".repeat(40)}结尾标记`;
const imageData = `data:image/png;base64,${"iVBORw0KGgoAAAANSUhEUgAA".repeat(200)}`;
const request = {
  model: "gpt-4.1",
  tools: [{ type: "function", name: "lookup" }],
  input: [
    {
      role: "user",
      content: [
        { type: "input_text", text: longText },
        { type: "input_image", image_url: imageData },
      ],
    },
  ],
};

async function renderTree(text: string) {
  await act(async () => {
    root.render(<JsonTreeView root={parseJsonTree(text).root!} />);
  });
}

function button(label: string): HTMLButtonElement {
  const found = [...container.querySelectorAll("button")].find(
    (item) =>
      item.textContent === label || item.getAttribute("aria-label") === label,
  );
  if (!found) throw new Error(`no button ${label}`);
  return found;
}

describe("JsonTreeView", () => {
  it("folds long text, inline files and tool definitions by default", async () => {
    await renderTree(JSON.stringify(request));
    const text = container.textContent ?? "";
    expect(text).toContain('"model": "gpt-4.1"');
    expect(text).toContain('"type": "input_text"');
    // The prompt shows as a one-line preview with its length.
    expect(text).toContain("请帮我总结这段对话。");
    expect(text).not.toContain("结尾标记");
    expect(text).toContain(`${longText.length.toLocaleString()} 字符`);
    // The image shows what it is, not its payload.
    expect(text).toContain("base64 · image/png");
    expect(text).not.toContain("iVBORw0KGgoAAAANSUhEUgAAiVBORw0KGgo");
    // Tools collapse to a preview.
    expect(text).toContain('"tools": [{…}]');
    expect(text).not.toContain('"name": "lookup"');
  });

  it("unfolds a string and a container on demand", async () => {
    await renderTree(JSON.stringify(request));
    const toggle = button("展开 text");
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    await act(async () => toggle.click());
    expect(container.textContent).toContain("结尾标记");
    expect(button("折叠 text").getAttribute("aria-expanded")).toBe("true");

    // Opening a container shows its children, each folded to a preview.
    await act(async () => button("展开 tools").click());
    expect(container.textContent).toContain(
      '0: {type: "function", name: "lookup"}',
    );
  });

  it("expands and collapses everything from the toolbar", async () => {
    await renderTree(JSON.stringify(request));
    await act(async () => button("全部展开").click());
    expect(container.textContent).toContain('"name": "lookup"');
    // Strings stay folded even when every container opens.
    expect(container.textContent).not.toContain("结尾标记");
    // Collapsing keeps the top level listed.
    await act(async () => button("全部折叠").click());
    expect(container.textContent).toContain('"input": [{…}]');
    expect(container.textContent).not.toContain('"role"');
  });

  it("pages long arrays", async () => {
    await renderTree(
      JSON.stringify({ items: Array.from({ length: 150 }, (_, i) => i * 10) }),
    );
    expect(container.textContent).toContain("已显示 100 / 150");
    expect(container.textContent).not.toContain("1490");
    await act(async () => button("再显示 50 项").click());
    expect(container.textContent).toContain("1490");
    expect(container.textContent).not.toContain("已显示");
  });

  it("marks where a truncated capture ends", async () => {
    await renderTree(
      '{"model":"gpt-4.1","input":[{"role":"user","content":"hel',
    );
    const text = container.textContent ?? "";
    expect(text).toContain('"content": "hel');
    expect(text.match(/此处截断/g)).toHaveLength(1);
  });

  it("starts over when it receives another document", async () => {
    await renderTree(JSON.stringify(request));
    await act(async () => button("全部折叠").click());
    await renderTree(JSON.stringify({ other: { nested: true } }));
    expect(container.textContent).toContain('"nested": true');
  });

  it("finds into folded text and paged arrays and marks the hit in view", async () => {
    const items = Array.from({ length: 130 }, (_, index) =>
      index === 120 ? "the 结尾标记 again" : `item ${index}`,
    );
    const onResult = vi.fn();
    const find = (active: number, seq: number): FindRequest => ({
      query: "结尾标记",
      active,
      seq,
      onResult,
    });
    const tree = parseJsonTree(JSON.stringify({ ...request, items })).root!;
    const scrolled = vi
      .spyOn(Element.prototype, "scrollIntoView")
      .mockImplementation(() => {});

    await act(async () => {
      root.render(<JsonTreeView find={find(0, 1)} root={tree} />);
    });

    // Both hits count, though one is folded in the prompt and one is paged out.
    expect(onResult).toHaveBeenLastCalledWith({ count: 2, capped: false });
    // The first hit unfolds the prompt, is marked as the one in view, and is
    // scrolled to.
    const active = container.querySelector('mark[data-find-active="true"]');
    expect(active?.textContent).toBe("结尾标记");
    expect(button("折叠 text").getAttribute("aria-expanded")).toBe("true");
    expect(scrolled).toHaveBeenCalled();

    await act(async () => {
      root.render(<JsonTreeView find={find(1, 2)} root={tree} />);
    });

    // The second hit pages the array far enough to show item 120.
    const marks = [...container.querySelectorAll('[data-testid="find-mark"]')];
    expect(marks).toHaveLength(2);
    expect(
      container
        .querySelector('mark[data-find-active="true"]')
        ?.closest("[data-path]")?.textContent,
    ).toContain("120:");
    scrolled.mockRestore();
  });

  it("marks matching keys as printed", async () => {
    await act(async () => {
      root.render(
        <JsonTreeView
          find={{ query: "MODEL", active: 0, seq: 1, onResult: () => {} }}
          root={parseJsonTree(JSON.stringify(request)).root!}
        />,
      );
    });

    const mark = container.querySelector('[data-testid="find-mark"]');
    expect(mark?.textContent).toBe("model");
    expect(mark?.parentElement?.textContent).toBe('"model"');
  });
});
