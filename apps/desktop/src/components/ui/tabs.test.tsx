// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { Tabs, TabsContent, TabsList, TabsTrigger } from "./tabs";

describe("TabsContent", () => {
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

  it("restores ancestor workspace scroll when a panel is focused", async () => {
    await act(async () => {
      root.render(
        <div data-slot="workspace">
          <Tabs defaultValue="models">
            <TabsList>
              <TabsTrigger type="button" value="models">
                支持模型
              </TabsTrigger>
              <TabsTrigger type="button" value="protocols">
                入口协议
              </TabsTrigger>
            </TabsList>
            <TabsContent
              className="overflow-y-auto"
              data-tab-scroller=""
              value="models"
            >
              models
            </TabsContent>
            <TabsContent
              className="overflow-y-auto"
              data-tab-scroller=""
              value="protocols"
            >
              protocols
            </TabsContent>
          </Tabs>
        </div>,
      );
    });

    const workspace = container.querySelector(
      "[data-slot='workspace']",
    ) as HTMLElement;
    workspace.scrollTop = 96;
    workspace.scrollLeft = 12;

    const panel = container.querySelector(
      "[data-slot='tabs-content']",
    ) as HTMLElement;
    await act(async () => {
      panel.dispatchEvent(new FocusEvent("focus", { bubbles: true }));
      await new Promise<void>((resolve) => {
        requestAnimationFrame(() => resolve());
      });
    });

    expect(workspace.scrollTop).toBe(96);
    expect(workspace.scrollLeft).toBe(12);
  });
});
