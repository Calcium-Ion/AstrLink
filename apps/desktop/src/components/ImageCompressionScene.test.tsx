// @vitest-environment happy-dom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import type { ImageCompression } from "@/failure-policy-model";
import { applyLocale } from "@/i18n";
import { settleClosedLayer } from "@/lib/test-layer-settle";
import { ImageCompressionEditor } from "./ImageCompressionEditor";

let container: HTMLDivElement;
let root: Root;

beforeEach(async () => {
  await applyLocale("zh-CN");
  (
    globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
});

async function render(value: Partial<ImageCompression> = {}) {
  await act(async () =>
    root.render(
      <ImageCompressionEditor
        disabled={false}
        onChange={vi.fn()}
        value={{ enabled: true, mode: "lossless", quality: 85, ...value }}
      />,
    ),
  );
}

function scene() {
  return container.querySelector<HTMLElement>('[data-visual="compression"]')!;
}

/** The example image as it leaves the client and reaches the model; * marks one a WebP replaced. */
function image() {
  const row = scene().querySelector<HTMLElement>("[data-image]")!;
  return `${row.textContent}${row.dataset.compressed ? "*" : ""}`;
}

it("shows what the saved mode does to its example image", async () => {
  await render();
  expect(scene().getAttribute("aria-hidden")).toBe("true");
  expect(scene().dataset.mode).toBe("lossless");
  expect(scene().querySelectorAll("[data-image]")).toHaveLength(1);
  expect(image()).toBe("截图.png1.8 MB截图.webp1.1 MB*");

  await render({ mode: "lossy" });
  expect(scene().dataset.mode).toBe("lossy");
  expect(image()).toBe("照片.jpg2.4 MB照片.webp1.1 MB*");
});

it("shrinks the lossy example further at a lower quality", async () => {
  await render({ mode: "lossy", quality: 60 });
  expect(image()).toBe("照片.jpg2.4 MB照片.webp383.1 KB*");
  await render({ mode: "lossy", quality: 95 });
  expect(image()).toBe("照片.jpg2.4 MB照片.webp1.6 MB*");
});

it("sends the image as it left when compression is off", async () => {
  await render({ enabled: false, mode: "lossy" });
  expect(scene().dataset.mode).toBe("off");
  expect(image()).toBe("照片.jpg2.4 MB照片.jpg2.4 MB");
  await render({ enabled: false, mode: "lossless" });
  expect(image()).toBe("截图.png1.8 MB截图.png1.8 MB");
});

it("keeps the facts the picture cannot show behind the help button", async () => {
  await render();
  const help = container.querySelector<HTMLButtonElement>(
    'button[aria-label="请求图片压缩说明"]',
  )!;
  await act(async () => help.click());
  const popover = document.querySelector<HTMLElement>(
    '[data-slot="popover-content"]',
  )!;
  expect([...popover.querySelectorAll("p")].map((p) => p.textContent)).toEqual([
    "无损模式下 JPEG 原样发送，转换只会变大。同一张图每次压缩结果相同，提示缓存照常命中。大截图第一次压缩要零点几秒。模型按像素尺寸计算 Token，用量不变。",
    "累计只算发往上游的请求，一次请求重试或换提供商重发只计一次。累计单独保存，删除或清理请求记录不会减少。",
  ]);
  expect(popover.querySelector("[data-visual]")).toBeNull();
  await act(async () =>
    document.dispatchEvent(
      new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
    ),
  );
  await settleClosedLayer();
});
