// @vitest-environment happy-dom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { beforeEach, afterEach, expect, it, vi } from "vitest";
const bridge = vi.hoisted(() => ({
  getServiceOrder: vi.fn(),
  updateServiceOrder: vi.fn(),
}));
vi.mock("./bridge", () => bridge);
import { useServiceOrder } from "./use-service-order";
import { OrderedList } from "./components/OrderedList";
import type { Service } from "./service-model";

const services = ["service_a", "service_b", "service_c"].map(
  (id) => ({ id }) as Service,
);
const initial = {
  service_ids: services.map((item) => item.id),
  etag: '"initial"',
};
let root: Root, container: HTMLDivElement;
const refresh = vi.fn();
function Harness({ filtered = false }: { filtered?: boolean }) {
  const order = useServiceOrder(services, true, refresh);
  return (
    <>
      <OrderedList
        items={order.ordered.filter(
          (item) => !filtered || item.id !== "service_b",
        )}
        label="order"
        compact
        disabled={!order.complete || order.saving}
        positionOf={(item) =>
          order.ordered.findIndex((service) => service.id === item.id) + 1
        }
        onChange={(items) => void order.save(items)}
      >
        {(item, _index, controls) => (
          <div data-id={item.id}>
            {controls}
            {item.id}
          </div>
        )}
      </OrderedList>
      <output>{order.error}</output>
    </>
  );
}
beforeEach(() => {
  (
    globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  vi.clearAllMocks();
  bridge.getServiceOrder.mockResolvedValue(initial);
  bridge.updateServiceOrder.mockImplementation(async (service_ids) => ({
    service_ids,
    etag: '"saved"',
  }));
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  vi.restoreAllMocks();
});
const ids = () =>
  [...container.querySelectorAll<HTMLElement>("[data-id]")].map(
    (item) => item.dataset.id,
  );
const handle = (id: string) =>
  container.querySelector<HTMLButtonElement>(`[data-id="${id}"] button`)!;
function measureRows(compactHeight = 100) {
  const list = container.querySelector("ol")!;
  const height = () => (list.dataset.sorting === "true" ? compactHeight : 100);
  list.setPointerCapture = vi.fn();
  list.hasPointerCapture = () => false;
  vi.spyOn(list, "getBoundingClientRect").mockReturnValue(
    new DOMRect(0, 0, 400, 300),
  );
  for (const row of list.querySelectorAll<HTMLElement>("[data-ordered-item]")) {
    Object.defineProperty(row, "offsetTop", {
      configurable: true,
      get: () =>
        [...list.querySelectorAll("[data-ordered-item]")].indexOf(row) *
          height() +
        (parseFloat(list.style.paddingTop) || 0),
    });
    Object.defineProperty(row, "offsetHeight", {
      configurable: true,
      get: height,
    });
    vi.spyOn(row, "getBoundingClientRect").mockImplementation(
      () => new DOMRect(0, row.offsetTop, 400, height()),
    );
    vi.spyOn(
      row.querySelector("button")!,
      "getBoundingClientRect",
    ).mockImplementation(() => new DOMRect(0, row.offsetTop + 10, 28, 28));
  }
  return list;
}
async function dragToFirst() {
  const list = measureRows();
  const startY = handle("service_c").getBoundingClientRect().top + 10;
  await act(async () =>
    handle("service_c").dispatchEvent(
      new PointerEvent("pointerdown", {
        bubbles: true,
        button: 0,
        pointerId: 1,
        clientX: 20,
        clientY: startY,
      }),
    ),
  );
  await act(async () =>
    list.dispatchEvent(
      new PointerEvent("pointermove", {
        bubbles: true,
        pointerId: 1,
        clientX: 20,
        clientY: 10,
      }),
    ),
  );
  return list;
}

it("persists keyboard order once, blocks changes during saving and uses the new ETag", async () => {
  let resolve!: (value: unknown) => void;
  bridge.updateServiceOrder.mockReturnValueOnce(
    new Promise((done) => {
      resolve = done;
    }),
  );
  await act(async () => root.render(<Harness />));
  await act(async () =>
    handle("service_a").dispatchEvent(
      new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }),
    ),
  );
  expect(ids()).toEqual(["service_b", "service_a", "service_c"]);
  expect(handle("service_b").disabled).toBe(true);
  expect(bridge.updateServiceOrder).toHaveBeenCalledExactlyOnceWith(
    ["service_b", "service_a", "service_c"],
    '"initial"',
  );
  await act(async () =>
    resolve({
      service_ids: ["service_b", "service_a", "service_c"],
      etag: '"saved"',
    }),
  );
  await act(async () =>
    handle("service_a").dispatchEvent(
      new KeyboardEvent("keydown", { key: "ArrowUp", bubbles: true }),
    ),
  );
  expect(bridge.updateServiceOrder).toHaveBeenLastCalledWith(
    initial.service_ids,
    '"saved"',
  );
});

it("rolls back a failed save and refreshes after conflict", async () => {
  bridge.updateServiceOrder.mockRejectedValueOnce(Error("412 order changed"));
  await act(async () => root.render(<Harness />));
  await act(async () =>
    handle("service_a").dispatchEvent(
      new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }),
    ),
  );
  expect(ids()).toEqual(initial.service_ids);
  expect(container.textContent).toContain("412 order changed");
  expect(refresh).toHaveBeenCalledOnce();
});

it("commits drag/drop and keeps reordering available after filtering", async () => {
  await act(async () => root.render(<Harness />));
  const list = await dragToFirst();
  expect(ids()).toEqual(["service_c", "service_a", "service_b"]);
  expect(
    container
      .querySelector("[data-dragging]")
      ?.getAttribute("data-ordered-item"),
  ).toBe("service_c");
  expect(
    container.querySelector<HTMLElement>("[data-drop-slot]")?.style.top,
  ).toBe("0px");
  expect(bridge.updateServiceOrder).not.toHaveBeenCalled();
  await act(async () =>
    list.dispatchEvent(
      new PointerEvent("pointerup", {
        bubbles: true,
        pointerId: 1,
        clientY: 20,
      }),
    ),
  );
  expect(ids()).toEqual(["service_c", "service_a", "service_b"]);
  expect(bridge.updateServiceOrder).toHaveBeenCalledExactlyOnceWith(
    ["service_c", "service_a", "service_b"],
    '"initial"',
  );
  await act(async () => root.render(<Harness filtered />));
  expect(handle("service_c").disabled).toBe(false);
  expect(ids()).toEqual(["service_c", "service_a"]);
});

it("merges filtered drag/drop into global slots and preserves hidden services", async () => {
  await act(async () => root.render(<Harness filtered />));
  expect(ids()).toEqual(["service_a", "service_c"]);
  expect(handle("service_c").getAttribute("aria-label")).toMatch(/3$/);
  const list = await dragToFirst();
  expect(ids()).toEqual(["service_c", "service_a"]);
  expect(handle("service_c").getAttribute("aria-label")).toMatch(/1$/);
  expect(handle("service_a").getAttribute("aria-label")).toMatch(/3$/);
  expect(bridge.updateServiceOrder).not.toHaveBeenCalled();
  await act(async () =>
    list.dispatchEvent(
      new PointerEvent("pointerup", {
        bubbles: true,
        pointerId: 1,
      }),
    ),
  );
  expect(bridge.updateServiceOrder).toHaveBeenCalledExactlyOnceWith(
    ["service_c", "service_b", "service_a"],
    '"initial"',
  );
  await act(async () => root.render(<Harness />));
  expect(ids()).toEqual(["service_c", "service_b", "service_a"]);
});

it("restores the full order when a filtered save conflicts", async () => {
  bridge.updateServiceOrder.mockRejectedValueOnce(Error("412 order changed"));
  bridge.getServiceOrder.mockResolvedValueOnce(initial).mockResolvedValueOnce({
    service_ids: ["service_b", "service_a", "service_c"],
    etag: '"fresh"',
  });
  await act(async () => root.render(<Harness filtered />));
  await act(async () =>
    handle("service_a").dispatchEvent(
      new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }),
    ),
  );
  expect(bridge.updateServiceOrder).toHaveBeenCalledWith(
    ["service_c", "service_b", "service_a"],
    '"initial"',
  );
  expect(ids()).toEqual(["service_a", "service_c"]);
  expect(refresh).toHaveBeenCalledOnce();
  expect(container.textContent).toContain("412 order changed");
  await act(async () => root.render(<Harness />));
  expect(ids()).toEqual(["service_b", "service_a", "service_c"]);
});

it("cancels an active drag when the filter changes without saving a stale subset", async () => {
  await act(async () => root.render(<Harness />));
  await dragToFirst();
  expect(ids()).toEqual(["service_c", "service_a", "service_b"]);
  await act(async () => root.render(<Harness filtered />));
  expect(ids()).toEqual(["service_a", "service_c"]);
  expect(container.querySelector("[data-drop-slot]")).toBeNull();
  expect(bridge.updateServiceOrder).not.toHaveBeenCalled();
});

it.each(["Escape", "pointercancel"])(
  "restores the preview without saving when cancelled by %s",
  async (reason) => {
    await act(async () => root.render(<Harness />));
    const list = await dragToFirst();
    expect(ids()).toEqual(["service_c", "service_a", "service_b"]);
    await act(async () => {
      if (reason === "Escape") {
        handle("service_c").dispatchEvent(
          new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
        );
      } else {
        list.dispatchEvent(
          new PointerEvent("pointercancel", { bubbles: true, pointerId: 1 }),
        );
      }
    });
    expect(ids()).toEqual(initial.service_ids);
    expect(container.querySelector("[data-drop-slot]")).toBeNull();
    expect(bridge.updateServiceOrder).not.toHaveBeenCalled();
  },
);

it("anchors the grabbed supplier when rows become compact without changing priority", async () => {
  await act(async () => root.render(<Harness />));
  const list = measureRows(52);
  await act(async () =>
    handle("service_c").dispatchEvent(
      new PointerEvent("pointerdown", {
        bubbles: true,
        button: 0,
        pointerId: 1,
        clientX: 20,
        clientY: 220,
      }),
    ),
  );
  await act(async () =>
    list.dispatchEvent(
      new PointerEvent("pointermove", {
        bubbles: true,
        pointerId: 1,
        clientX: 20,
        clientY: 226,
      }),
    ),
  );
  expect(ids()).toEqual(initial.service_ids);
  expect(
    container.querySelector<HTMLElement>("[data-drop-slot]")?.style.top,
  ).toBe("200px");
  await act(async () =>
    list.dispatchEvent(
      new PointerEvent("pointerup", { bubbles: true, pointerId: 1 }),
    ),
  );
  expect(bridge.updateServiceOrder).not.toHaveBeenCalled();
  expect(list.style.paddingTop).toBe("");
});
