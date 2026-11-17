// @vitest-environment happy-dom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";

import { MarkdownContent } from "./MarkdownContent";

vi.mock("../native-host/core", () => ({ isDesktopHost: () => false }));

// Its own file, so this is the first reply this module ever mounts. A lazy
// component suspends on its first mount even once its import has settled,
// and React holds the fallback for about 300 ms: opening a request record
// flashed the first reply as raw monospace text.
it("renders the first reply formatted, without a plain-text flash", async () => {
  (
    globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  await act(async () => {
    await vi.dynamicImportSettled();
  });
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);

  act(() => root.render(<MarkdownContent content="**OK**" />));
  expect(container.querySelector("strong")?.textContent).toBe("OK");
  expect(container.querySelector("pre")).toBeNull();

  act(() => root.unmount());
  container.remove();
});
