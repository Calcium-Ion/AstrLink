// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import {
  ToolCallCard,
  ToolCallGroup,
  type ToolCallStatus,
} from "./tool-call-card";

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  (
    globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

function render(status: ToolCallStatus, group = false) {
  const card = (
    <ToolCallCard
      name="Read"
      summary="src/main.ts"
      status={status}
      statusLabel={status}
    >
      <button type="button">Copy exact content</button>
    </ToolCallCard>
  );
  act(() =>
    root.render(
      group ? (
        <ToolCallGroup
          label="2 tools"
          summary="Read · Search"
          errorLabel={status === "error" ? "1 failed" : undefined}
          icons={[]}
        >
          {card}
        </ToolCallGroup>
      ) : (
        card
      ),
    ),
  );
}

describe("ToolCallCard", () => {
  it("keeps closed payloads unmounted and exposes a linked disclosure", () => {
    render("unavailable");
    const trigger = container.querySelector<HTMLButtonElement>("button")!;
    expect(trigger.getAttribute("aria-expanded")).toBe("false");
    expect(container.textContent).not.toContain("Copy exact content");
    expect(container.querySelector('[data-status="running"]')).toBeNull();
    act(() => trigger.click());
    expect(trigger.getAttribute("aria-expanded")).toBe("true");
    expect(
      document.getElementById(trigger.getAttribute("aria-controls")!)
        ?.textContent,
    ).toContain("Copy exact content");
    expect(container.querySelector("button button")).toBeNull();
  });

  it("reveals a newly observed error but preserves the reader's later choice", () => {
    render("unavailable");
    render("error");
    const trigger = container.querySelector<HTMLButtonElement>("button")!;
    expect(trigger.getAttribute("aria-expanded")).toBe("true");
    act(() => trigger.click());
    render("error");
    expect(trigger.getAttribute("aria-expanded")).toBe("false");
    expect(container.textContent).not.toContain("Copy exact content");
  });

  it("opens both a group and its failed card when a result arrives", () => {
    render("unavailable", true);
    expect(container.querySelector('[data-slot="tool-call-card"]')).toBeNull();
    render("error", true);
    expect(
      container.querySelectorAll('button[aria-expanded="true"]').length,
    ).toBe(2);
    expect(container.textContent).toContain("Copy exact content");
  });
});
