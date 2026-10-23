// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { renderToString } from "react-dom/server";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import { useMediaQuery } from "./use-media-query";

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
  vi.restoreAllMocks();
});

function mediaQuery(query: string, initialMatches: boolean) {
  let matches = initialMatches;
  const media = new EventTarget() as MediaQueryList;
  Object.defineProperties(media, {
    media: { value: query },
    matches: { get: () => matches },
  });
  const add = vi.spyOn(media, "addEventListener");
  const remove = vi.spyOn(media, "removeEventListener");
  return {
    media,
    add,
    remove,
    change(next: boolean) {
      matches = next;
      media.dispatchEvent(new Event("change"));
    },
  };
}

function Probe({ query }: { query: string }) {
  return <output>{String(useMediaQuery(query))}</output>;
}

it("tracks media changes and releases subscriptions when the query changes or unmounts", () => {
  const short = mediaQuery("(max-height: 640px)", true);
  const narrow = mediaQuery("(max-width: 480px)", false);
  vi.spyOn(window, "matchMedia").mockImplementation((query) =>
    query === short.media.media ? short.media : narrow.media,
  );

  expect(renderToString(<Probe query={short.media.media} />)).toBe(
    "<output>false</output>",
  );
  expect(short.add).not.toHaveBeenCalled();

  act(() => root.render(<Probe query={short.media.media} />));
  expect(container.textContent).toBe("true");
  act(() => short.change(false));
  expect(container.textContent).toBe("false");
  act(() => root.render(<Probe query={short.media.media} />));
  expect(short.add).toHaveBeenCalledTimes(1);

  act(() => root.render(<Probe query={narrow.media.media} />));
  expect(short.remove).toHaveBeenCalledWith(
    "change",
    short.add.mock.calls[0]![1],
  );
  act(() => short.change(true));
  expect(container.textContent).toBe("false");
  act(() => narrow.change(true));
  expect(container.textContent).toBe("true");

  act(() => root.render(null));
  expect(narrow.remove).toHaveBeenCalledWith(
    "change",
    narrow.add.mock.calls[0]![1],
  );
});
