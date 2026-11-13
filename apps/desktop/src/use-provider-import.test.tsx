// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import type {
  ProviderImportLink,
  ProviderImportNotice,
} from "./provider-import-model";

const mocks = vi.hoisted(() => ({
  listener: null as ((notice: unknown) => void) | null,
  getPendingProviderImport: vi.fn(),
  dismissProviderImport: vi.fn(),
  listenProviderImport: vi.fn(),
  error: vi.fn(),
}));
vi.mock("./native-host/core", () => ({ isDesktopHost: () => true }));
vi.mock("./bridge", () => ({
  getPendingProviderImport: mocks.getPendingProviderImport,
  dismissProviderImport: mocks.dismissProviderImport,
  listenProviderImport: mocks.listenProviderImport,
}));
vi.mock("./notify", () => ({ notify: { error: mocks.error } }));

import {
  providerImportRejection,
  useProviderImport,
} from "./use-provider-import";

const id = "0123456789abcdef0123456789abcdef";
const provider: ProviderImportLink = {
  kind: "newapi",
  name: "Relay",
  base_url: "https://relay.example.com",
  auth: null,
  auth_header: null,
  protocols: [],
  models: [],
  has_api_key: true,
  api_key_hint: "…cdef",
};
let root: Root;
let container: HTMLDivElement;
let latest: ReturnType<typeof useProviderImport>;

function Harness({ enabled }: { enabled: boolean }) {
  latest = useProviderImport({ enabled, protocols: [] });
  return null;
}

async function render(enabled: boolean) {
  await act(async () => root.render(<Harness enabled={enabled} />));
}

async function deliver(notice: ProviderImportNotice) {
  await act(async () => mocks.listener?.(notice));
}

beforeEach(() => {
  (
    globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  mocks.listener = null;
  mocks.getPendingProviderImport.mockReset().mockResolvedValue(null);
  mocks.dismissProviderImport.mockReset().mockResolvedValue(undefined);
  mocks.listenProviderImport
    .mockReset()
    .mockImplementation(async (listener: (notice: unknown) => void) => {
      mocks.listener = listener;
      return () => {
        mocks.listener = null;
      };
    });
  mocks.error.mockReset();
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
});

it("holds a launch link until the app can add providers", async () => {
  mocks.getPendingProviderImport.mockResolvedValue({
    status: "pending",
    id,
    provider,
  });
  await render(false);
  expect(latest.active).toBeNull();

  await render(true);
  expect(latest.active?.id).toBe(id);
  expect(latest.active?.plan.defaultName).toBe("Relay");

  // The open event can repeat the launch link; the dialog keeps its state.
  const plan = latest.active?.plan;
  await deliver({ status: "pending", id, provider });
  expect(latest.active?.plan).toBe(plan);

  await act(async () => latest.close());
  expect(latest.active).toBeNull();
  expect(mocks.dismissProviderImport).not.toHaveBeenCalled();
});

it("reports a refused link once and releases it", async () => {
  const refused: ProviderImportNotice = {
    status: "invalid",
    id,
    reason: "missing_parameter",
    field: "base_url",
  };
  mocks.getPendingProviderImport.mockResolvedValue(refused);
  await render(true);
  await deliver(refused);

  expect(mocks.error).toHaveBeenCalledTimes(1);
  expect(mocks.error).toHaveBeenCalledWith(
    "无法通过链接添加 API 提供商：链接缺少“API 地址”。",
  );
  expect(mocks.dismissProviderImport).toHaveBeenCalledTimes(1);
  expect(mocks.dismissProviderImport).toHaveBeenCalledWith(id);
  expect(latest.active).toBeNull();
});

it("refuses a link its preset cannot complete", async () => {
  await render(true);
  await deliver({
    status: "pending",
    id,
    provider: { ...provider, kind: "acme" },
  });

  expect(latest.active).toBeNull();
  expect(mocks.error).toHaveBeenCalledWith(
    "无法通过链接添加 API 提供商：AstrLink 不支持链接中的“API 提供商类型”。",
  );
  expect(mocks.dismissProviderImport).toHaveBeenCalledWith(id);
});

it("names only fields the user can recognize", () => {
  expect(providerImportRejection("unknown_parameter", "token")).toBe(
    "无法通过链接添加 API 提供商：链接包含无法识别的参数“token”。",
  );
  expect(providerImportRejection("unknown_parameter", null)).toBe(
    "无法通过链接添加 API 提供商：链接包含无法识别的参数。",
  );
  expect(providerImportRejection("unsupported", null)).toBe(
    "无法通过链接添加 API 提供商：AstrLink 不支持这种链接。",
  );
  expect(providerImportRejection("invalid_parameter", "api_key")).toBe(
    "无法通过链接添加 API 提供商：链接中的“API Key”无效。",
  );
  expect(providerImportRejection("malformed", null)).toBe(
    "无法通过链接添加 API 提供商：无法识别这个链接。",
  );
});
