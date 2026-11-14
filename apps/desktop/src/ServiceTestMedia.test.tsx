// @vitest-environment happy-dom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const bridge = vi.hoisted(() => ({
  saveGatewayFile: vi.fn(),
  openExternalURL: vi.fn(),
}));
vi.mock("./bridge", () => bridge);
const notify = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock("./notify", () => ({ notify }));

import { applyLocale } from "./i18n";
import { mediaFileName, ServiceTestMediaView } from "./ServiceTestMedia";
import type { ServiceTestMedia } from "./service-test-model";

const media: ServiceTestMedia[] = [
  {
    kind: "image",
    url: "http://127.0.0.1:8324/v1/tasks/resp_1/artifacts/image-1/content?access=a",
    name: "image-1",
  },
  {
    kind: "video",
    url: "http://127.0.0.1:8324/v1/tasks/resp_2/artifacts/video/content?access=b",
    name: "video",
    mime_type: "video/webm",
  },
  {
    kind: "audio",
    url: "http://127.0.0.1:8324/v1/tasks/resp_3/artifacts/song/content?access=c",
    name: "song",
    mime_type: "audio/mpeg",
  },
  {
    kind: "file",
    url: "http://127.0.0.1:8324/v1/tasks/resp_4/artifacts/lyrics/content?access=d",
    name: "lyrics",
  },
];

let root: Root;
let container: HTMLDivElement;

beforeEach(async () => {
  (
    globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  vi.clearAllMocks();
  await applyLocale("zh-CN");
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
});

it("names a generated file by its key and its media type", () => {
  expect(media.map(mediaFileName)).toEqual([
    "image-1.png",
    "video.webm",
    "song.mp3",
    "lyrics",
  ]);
});

it("shows images, plays video and music, and downloads each where the user chooses", async () => {
  bridge.saveGatewayFile.mockResolvedValue("/home/me/Downloads/image-1.png");
  await act(async () => root.render(<ServiceTestMediaView media={media} />));
  const figures = [...container.querySelectorAll("figure")];
  expect(figures.map((figure) => figure.dataset.kind)).toEqual([
    "image",
    "video",
    "audio",
    "file",
  ]);
  expect(figures[0].querySelector("img")?.getAttribute("src")).toBe(
    media[0].url,
  );
  expect(figures[1].querySelector("video")?.hasAttribute("controls")).toBe(
    true,
  );
  expect(figures[2].querySelector("audio")?.getAttribute("src")).toBe(
    media[2].url,
  );
  expect(figures[3].querySelector("img, video, audio")).toBeNull();
  expect(figures[0].textContent).toContain("图片 · image-1.png");
  const download = [...figures[0].querySelectorAll("button")].find(
    (button) => button.textContent === "下载",
  );
  await act(async () => download?.click());
  expect(bridge.saveGatewayFile).toHaveBeenCalledWith(
    media[0].url,
    "image-1.png",
  );
  expect(notify.success).toHaveBeenCalledWith(
    "已保存到 /home/me/Downloads/image-1.png",
  );
  // A cancelled save says nothing; a failed one says so.
  bridge.saveGatewayFile.mockResolvedValueOnce(null);
  await act(async () => download?.click());
  expect(notify.success).toHaveBeenCalledTimes(1);
  bridge.saveGatewayFile.mockRejectedValueOnce(new Error("disk full"));
  await act(async () => download?.click());
  expect(notify.error).toHaveBeenCalled();
});
