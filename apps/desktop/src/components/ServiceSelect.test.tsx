// @vitest-environment happy-dom
import { act, useState } from "react";
import { createRoot } from "react-dom/client";
import { expect, it } from "vitest";
import { ServiceSelect } from "./ServiceSelect";

it("keeps provider icons in the trigger and dropdown while selecting and clearing a filter", async () => {
  (
    globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  function Harness() {
    const [value, setValue] = useState("service_newapi");
    return (
      <ServiceSelect
        ariaLabel="Provider"
        label="Provider"
        allLabel="All"
        value={value}
        onChange={setValue}
        services={[
          { id: "service_newapi", name: "My gateway", kind: "newapi" },
          { id: "service_openai", name: "My OpenAI", kind: "openai" },
        ]}
      />
    );
  }
  try {
    await act(async () => root.render(<Harness />));
    const trigger =
      container.querySelector<HTMLButtonElement>('[role="combobox"]')!;
    expect(trigger.textContent).toContain("My gateway");
    expect(trigger.querySelector('[role="img"] img')).toBeTruthy();
    await act(async () =>
      trigger.dispatchEvent(
        new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }),
      ),
    );
    const options = [
      ...document.querySelectorAll<HTMLElement>('[role="option"]'),
    ];
    expect(options).toHaveLength(3);
    expect(
      options.slice(1).every((option) => option.querySelector('[role="img"]')),
    ).toBe(true);
    await act(async () => options[2].click());
    expect(trigger.textContent).toContain("My OpenAI");
    expect(trigger.querySelector('[role="img"] svg')).toBeTruthy();
    await act(async () =>
      trigger.dispatchEvent(
        new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }),
      ),
    );
    await act(async () =>
      document.querySelector<HTMLElement>('[role="option"]')!.click(),
    );
    expect(trigger.textContent).toContain("All");
    expect(trigger.querySelector('[role="img"]')).toBeNull();
  } finally {
    await act(async () => root.unmount());
    container.remove();
  }
});
