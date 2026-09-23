// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ValueTransition } from "./ValueTransition";

let root: Root;
let container: HTMLDivElement;
let originalAnimate: PropertyDescriptor | undefined;
const cancel = vi.fn();
const animate = vi.fn(() => ({ cancel }));

beforeEach(() => {
  vi.clearAllMocks();
  originalAnimate = Object.getOwnPropertyDescriptor(
    HTMLElement.prototype,
    "animate",
  );
  Object.defineProperty(HTMLElement.prototype, "animate", {
    configurable: true,
    value: animate,
  });
  vi.spyOn(window, "matchMedia").mockReturnValue({
    matches: false,
  } as MediaQueryList);
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  if (originalAnimate)
    Object.defineProperty(HTMLElement.prototype, "animate", originalAnimate);
  else
    delete (HTMLElement.prototype as unknown as { animate?: unknown }).animate;
  vi.restoreAllMocks();
});

it("fades changed values without replaying on ordinary refreshes", async () => {
  await act(async () =>
    root.render(<ValueTransition valueKey="18.42">$18.42</ValueTransition>),
  );
  expect(animate).not.toHaveBeenCalled();
  await act(async () =>
    root.render(<ValueTransition valueKey="18.42">$18.42</ValueTransition>),
  );
  expect(animate).not.toHaveBeenCalled();
  await act(async () =>
    root.render(<ValueTransition valueKey="19.00">$19.00</ValueTransition>),
  );
  expect(container.textContent).toBe("$19.00");
  expect(animate).toHaveBeenCalledOnce();
  expect(animate).toHaveBeenCalledWith([{ opacity: 0.45 }, { opacity: 1 }], {
    duration: 180,
    easing: "ease-out",
  });
  await act(async () => root.render(null));
  expect(cancel).toHaveBeenCalledOnce();
});

it("transitions pages without adding a wrapper or remounting their controls", async () => {
  const render = async (page: string) =>
    act(async () =>
      root.render(
        <ValueTransition
          asChild
          valueKey={page}
          duration={280}
          initialOpacity={0}
          offsetY={8}
        >
          <main data-slot="workspace">
            <input defaultValue="saved" />
          </main>
        </ValueTransition>,
      ),
    );
  await render("list");
  const main = container.querySelector("main");
  const input = container.querySelector("input")!;
  input.value = "draft";
  await render("editor");
  expect(container.firstElementChild).toBe(main);
  expect(container.querySelector("input")).toBe(input);
  expect(input.value).toBe("draft");
  expect(animate).toHaveBeenCalledOnce();
  expect(animate).toHaveBeenLastCalledWith(
    [
      { opacity: 0, transform: "translateY(8px)" },
      { opacity: 1, transform: "none" },
    ],
    { duration: 280, easing: "cubic-bezier(0.22, 1, 0.36, 1)" },
  );
  // Rapid navigation cancels the previous reveal instead of stacking effects.
  await render("records");
  expect(cancel).toHaveBeenCalledOnce();
  expect(animate).toHaveBeenCalledTimes(2);
  await render("records");
  expect(animate).toHaveBeenCalledTimes(2);
  expect(container.querySelector("input")).toBe(input);
});

it("shows updated content immediately without motion when reduced motion is enabled", async () => {
  vi.mocked(window.matchMedia).mockReturnValue({
    matches: true,
  } as MediaQueryList);
  await act(async () =>
    root.render(
      <ValueTransition valueKey="one" offsetY={8}>
        One
      </ValueTransition>,
    ),
  );
  await act(async () =>
    root.render(
      <ValueTransition valueKey="two" offsetY={8}>
        Two
      </ValueTransition>,
    ),
  );
  expect(container.textContent).toBe("Two");
  expect(animate).not.toHaveBeenCalled();
});
