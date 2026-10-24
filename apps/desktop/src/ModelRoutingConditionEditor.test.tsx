// @vitest-environment happy-dom

import { act, useState, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { RoutingPhraseEditor } from "./ModelRoutingConditionEditor";
import { applyLocale, i18n } from "./i18n";
import type { RoutingPhrase } from "./model-routing-editor-model";

/** Holds the phrase the way the rule editor does, so every change re-renders. */
function Harness({
  initial,
  changes,
}: {
  initial: RoutingPhrase;
  changes: RoutingPhrase[];
}) {
  const [phrase, setPhrase] = useState(initial);
  return (
    <RoutingPhraseEditor
      phrase={phrase}
      options={[]}
      dimensions={[]}
      onChange={(next) => {
        changes.push(next);
        setPhrase(next);
      }}
      onRemove={() => {}}
    />
  );
}

describe("count condition editor", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(async () => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    await applyLocale("zh-CN");
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  async function mount(node: ReactNode): Promise<void> {
    await act(async () => {
      root.render(node);
    });
  }

  /** React reads the tracked value, so a raw assignment is not seen as input. */
  async function type(input: HTMLInputElement, value: string): Promise<void> {
    await act(async () => {
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(input, value);
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
  }

  function boxes(): HTMLInputElement[] {
    return [
      ...container.querySelectorAll<HTMLInputElement>("input[type='number']"),
    ];
  }

  function mode(): string | null | undefined {
    return container.querySelector(
      '[data-slot="segmented-control-item"][data-state="on"]',
    )?.textContent;
  }

  it("keeps an upper bound an upper bound while its box is retyped", async () => {
    const changes: RoutingPhrase[] = [];
    await mount(
      <Harness
        initial={{ kind: "count", field: "turn_count", max: 5 }}
        changes={changes}
      />,
    );
    const atMost = i18n.t("routingConditions.editor.atMost");
    expect(mode()).toBe(atMost);
    await type(boxes()[0], "");
    expect(mode()).toBe(atMost);
    await type(boxes()[0], "7");
    expect(changes.at(-1)).toEqual({
      kind: "count",
      field: "turn_count",
      max: 7,
    });
  });

  it("keeps both boxes of a range while the lower one is retyped", async () => {
    const changes: RoutingPhrase[] = [];
    await mount(
      <Harness
        initial={{ kind: "count", field: "turn_count", min: 2, max: 9 }}
        changes={changes}
      />,
    );
    await type(boxes()[0], "");
    expect(mode()).toBe(i18n.t("routingConditions.editor.between"));
    expect(boxes()).toHaveLength(2);
    await type(boxes()[0], "3");
    expect(changes.at(-1)).toEqual({
      kind: "count",
      field: "turn_count",
      min: 3,
      max: 9,
    });
  });
});
