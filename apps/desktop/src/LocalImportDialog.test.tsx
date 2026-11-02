// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  confirmLocalImport: vi.fn(),
  dismissLocalImport: vi.fn(),
  getService: vi.fn(),
  openLocalImport: vi.fn(),
  probeLocalImportModels: vi.fn(),
  probeServiceModels: vi.fn(),
  readLocalImport: vi.fn(),
  updateService: vi.fn(),
}));
vi.mock("./bridge", () => mocks);

import { settleClosedLayer } from "@/lib/test-layer-settle";

import { LocalImportDialog } from "./LocalImportDialog";
import type { LocalImportOpened, LocalImportRead } from "./local-import-model";
import { ServiceKindPickerDialog } from "./ServiceManager";
import type { HTTPServiceCreateInput, Service } from "./service-model";

const session = "0123456789abcdef0123456789abcdef";
const pastedKey = "fake-pasted-key-D33p";

const opened: LocalImportOpened = {
  session,
  sources: [
    { id: "s0", tool: "claude", wsl: null, paths: ["~/.claude/settings.json"] },
    {
      id: "s1",
      tool: "codex",
      wsl: null,
      paths: ["~/.codex/config.toml", "~/.codex/auth.json"],
    },
    { id: "s2", tool: "pi", wsl: null, paths: ["~/.pi/agent/models.json"] },
    {
      id: "s3",
      tool: "codex",
      wsl: "Ubuntu",
      paths: ["~/.codex/config.toml", "~/.codex/auth.json"],
    },
  ],
};

const found: LocalImportRead = {
  candidates: [
    {
      id: "c0",
      name: "Relay Hub",
      address: "https://api.relayhub.example/v1",
      protocols: ["openai.responses", "anthropic.messages"],
      models: ["relay-large"],
      key: { status: "found", hint: "…k2Qe" },
      sources: [
        { tool: "codex", wsl: null },
        { tool: "claude", wsl: null },
      ],
    },
    {
      id: "c1",
      name: null,
      address: "https://open.bigmodel.cn/api/anthropic",
      protocols: ["anthropic.messages"],
      models: [],
      key: { status: "found", hint: "…x81P" },
      sources: [{ tool: "claude", wsl: null }],
    },
    {
      id: "c2",
      name: "deepseek",
      address: "https://api.deepseek.com/v1",
      protocols: ["openai.chat"],
      models: [],
      key: { status: "env", name: "DEEPSEEK_API_KEY" },
      sources: [{ tool: "codex", wsl: "Ubuntu" }],
    },
    {
      id: "c3",
      name: "Old relay",
      address: "https://api.oldrelay.example",
      protocols: ["openai.chat"],
      models: ["old-model"],
      key: { status: "found", hint: "…T3bA" },
      sources: [{ tool: "codex", wsl: null }],
    },
    {
      id: "c4",
      name: "LAN box",
      address: "http://192.168.1.20:3000/v1",
      protocols: ["openai.chat"],
      models: ["lan-model"],
      key: { status: "found", hint: "…L4N0" },
      sources: [{ tool: "codex", wsl: null }],
    },
  ],
  skipped: [
    {
      reason: "chatgpt_login",
      tool: "codex",
      wsl: null,
      name: null,
      path: null,
      detail: null,
    },
    {
      reason: "unreadable",
      tool: "codex",
      wsl: "Ubuntu",
      name: null,
      path: "~/.codex/auth.json",
      detail: "permission",
    },
  ],
};

function service(
  id: string,
  name: string,
  baseURL: string,
  credentialHint?: string,
): Service {
  return {
    id,
    name,
    kind: "newapi",
    enabled: true,
    models: ["old-model"],
    capabilities: [
      { protocol: "openai.chat", mode: "native", streaming: true },
    ],
    http: {
      base_url: baseURL,
      auth: { scheme: "bearer" },
      ...(credentialHint ? { credential_hint: credentialHint } : {}),
    },
    created_at: "2026-10-31T00:00:00Z",
    updated_at: "2026-10-31T00:00:00Z",
  };
}

// The list omits the saved-key hint; a single read carries it.
const oldRelay = service(
  "svc_old",
  "Old relay",
  "https://api.oldrelay.example/v1",
);
const teamRelay = service(
  "svc_team",
  "Team relay",
  "https://api.relayhub.example",
  "…zzzz",
);

function created(candidate: string, input: HTTPServiceCreateInput) {
  return {
    service: {
      id: `svc_${candidate}`,
      name: input.name,
      kind: input.kind,
      enabled: true,
      models: input.models,
      capabilities: input.capabilities,
      http: { base_url: input.http.base_url, auth: input.http.auth },
      created_at: "2026-10-31T00:00:00Z",
      updated_at: "2026-10-31T00:00:00Z",
    },
    etag: `"${candidate}"`,
  };
}

let root: Root;
let container: HTMLDivElement;

beforeEach(() => {
  (
    globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  for (const mock of Object.values(mocks)) mock.mockReset();
  mocks.openLocalImport.mockResolvedValue(opened);
  mocks.readLocalImport.mockResolvedValue(found);
  mocks.dismissLocalImport.mockResolvedValue(undefined);
  mocks.getService.mockImplementation(async (id: string) => ({
    service: {
      ...oldRelay,
      http: { ...oldRelay.http!, credential_hint: "…T3bA" },
    },
    etag: `"${id}"`,
  }));
  mocks.confirmLocalImport.mockImplementation(
    async (
      _session: string,
      candidate: string,
      input: HTTPServiceCreateInput,
    ) => created(candidate, input),
  );
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  vi.restoreAllMocks();
});

async function flush(times = 8) {
  for (let index = 0; index < times; index += 1) {
    await act(async () => await Promise.resolve());
  }
}

const dialog = () => document.querySelector<HTMLElement>('[role="dialog"]')!;
const text = () => dialog().textContent ?? "";
const button = (label: string) =>
  [...dialog().querySelectorAll<HTMLButtonElement>("button")].find(
    (item) => item.textContent?.trim() === label,
  )!;
const sourceBox = (id: string) =>
  dialog().querySelector<HTMLButtonElement>(`#local-import-${id}`)!;
const list = () =>
  dialog().querySelector<HTMLElement>('[data-testid="local-import-list"]')!;
const rowBoxes = () => [
  ...list().querySelectorAll<HTMLButtonElement>('[role="checkbox"]'),
];
const checked = (box: HTMLElement) => box.getAttribute("aria-checked");

async function click(element: HTMLElement) {
  await act(async () => element.click());
  await flush();
}

async function type(field: HTMLInputElement, value: string) {
  await act(async () => {
    Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )!.set!.call(field, value);
    field.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

async function render(
  handlers: { onClose?: () => void; onImported?: (item: Service) => void } = {},
) {
  await act(async () =>
    root.render(
      <LocalImportDialog
        onClose={handlers.onClose ?? (() => {})}
        onImported={handlers.onImported ?? (() => {})}
        protocols={[]}
        services={[oldRelay, teamRelay]}
      />,
    ),
  );
  await flush();
}

/** Reads Claude Code and both Codex homes, leaving Pi unread. */
async function readWithoutPi() {
  await render();
  await click(sourceBox("s2"));
  await click(button("读取配置"));
}

async function chooseKind(option: string) {
  const trigger = dialog().querySelector<HTMLButtonElement>(
    'button[role="combobox"][aria-label="提供商类型"]',
  )!;
  await act(async () => {
    trigger.dispatchEvent(
      new PointerEvent("pointerdown", {
        bubbles: true,
        button: 0,
        pointerType: "mouse",
      }),
    );
    await Promise.resolve();
  });
  const item = [
    ...document.querySelectorAll<HTMLElement>('[role="option"]'),
  ].find((candidate) => candidate.textContent?.trim() === option)!;
  await act(async () => {
    item.click();
    await Promise.resolve();
  });
  await settleClosedLayer();
}

it("lists the import first in the kind picker without locating anything", async () => {
  const onLocalImport = vi.fn();
  const onSelect = vi.fn();
  await act(async () =>
    root.render(
      <ServiceKindPickerDialog
        onLocalImport={onLocalImport}
        onOpenChange={() => {}}
        onSelect={onSelect}
        open
      />,
    ),
  );
  const groups = [...dialog().querySelectorAll("section")];
  expect(groups[0].getAttribute("aria-label")).toBe("已有配置");
  expect(groups[0].textContent).toContain("从本机导入");
  expect(groups[0].textContent).toContain("Claude Code、Codex、Pi");
  expect(mocks.openLocalImport).not.toHaveBeenCalled();

  await act(async () =>
    [...groups[0].querySelectorAll("button")]
      .find((item) => item.textContent?.includes("从本机导入"))!
      .click(),
  );
  expect(onLocalImport).toHaveBeenCalledTimes(1);
  expect(onSelect).not.toHaveBeenCalled();

  await act(async () =>
    root.render(
      <ServiceKindPickerDialog
        onOpenChange={() => {}}
        onSelect={onSelect}
        open
      />,
    ),
  );
  expect(dialog().querySelector('section[aria-label="已有配置"]')).toBeNull();
});

it("reads nothing until the user asks, and only the tools left checked", async () => {
  await render();

  expect(mocks.openLocalImport).toHaveBeenCalledTimes(1);
  expect(mocks.readLocalImport).not.toHaveBeenCalled();
  expect(mocks.getService).not.toHaveBeenCalled();
  expect(text()).toContain("从下面的配置文件读取 API 地址和密钥。");
  expect(text()).toContain("~/.claude/settings.json");
  expect(text()).toContain("~/.pi/agent/models.json");
  expect(text()).toContain("WSL · Ubuntu");
  expect(text()).toContain(
    "从 config.toml 读取地址，从 auth.json 读取 API 密钥。",
  );
  expect(["s0", "s1", "s2", "s3"].map((id) => checked(sourceBox(id)))).toEqual([
    "true",
    "true",
    "true",
    "true",
  ]);

  for (const id of ["s0", "s1", "s2", "s3"]) await click(sourceBox(id));
  expect(button("读取配置").disabled).toBe(true);
  for (const id of ["s0", "s1", "s3"]) await click(sourceBox(id));
  expect(mocks.readLocalImport).not.toHaveBeenCalled();

  await click(button("读取配置"));
  expect(mocks.readLocalImport).toHaveBeenCalledTimes(1);
  expect(mocks.readLocalImport).toHaveBeenCalledWith(session, [
    "s0",
    "s1",
    "s3",
  ]);
  expect(mocks.getService.mock.calls).toEqual([["svc_old"]]);
  expect(text()).toContain(
    "在 Claude Code、Codex 里找到 5 个提供商，选择要导入的。",
  );
});

it("starts every provider unchecked and marks what is already there", async () => {
  await readWithoutPi();

  expect(rowBoxes().map(checked)).toEqual([
    "false",
    "false",
    "false",
    "false",
    "false",
  ]);
  expect(text()).toContain("可导入 3 个，已选 0 个");
  expect(button("导入").disabled).toBe(true);
  expect(text()).toContain("导入时会获取每个提供商的模型列表。");

  const [relay, glm, deepseek, old, lan] = rowBoxes();
  expect(relay.disabled).toBe(false);
  expect(glm.disabled).toBe(false);
  expect(text()).toContain("和已有的「Team relay」地址相同");
  // The same address and key as an existing provider: nothing to import.
  expect(old.disabled).toBe(true);
  expect(text()).toContain("已添加");
  expect(text()).toContain("和已有的「Old relay」相同");
  expect(deepseek.disabled).toBe(true);
  expect(text()).toContain("需要密钥");
  expect(text()).toContain("密钥在环境变量 DEEPSEEK_API_KEY 里");
  expect(text()).toContain(
    "这个地址同时用于 Coding Plan 和按量付费 API，按你的密钥选择类型。",
  );
  expect(text()).toContain("这个地址用未加密的 http 连接");
  expect(lan.disabled).toBe(false);
  expect(text()).toContain("跳过的 2 项");
  expect(text()).toContain("Codex 的 ChatGPT 登录：请用「登录订阅账户」添加。");
  expect(text()).toContain("~/.codex/auth.json（WSL · Ubuntu）：没有读取权限");
  expect(text()).not.toContain("fake-");

  await type(
    dialog().querySelector<HTMLInputElement>('input[placeholder="粘贴密钥"]')!,
    pastedKey,
  );
  expect(deepseek.disabled).toBe(false);
  expect(checked(deepseek)).toBe("false");
  expect(text()).toContain("可导入 4 个，已选 0 个");

  await click(dialog().querySelector<HTMLButtonElement>("#local-import-all")!);
  expect(text()).toContain("可导入 4 个，已选 4 个");
  expect(button("导入 4 个").disabled).toBe(false);
  await click(lan);
  expect(text()).toContain("已选 3 个");
  expect(button("导入 3 个")).toBeTruthy();
  expect(mocks.probeLocalImportModels).not.toHaveBeenCalled();
  expect(mocks.confirmLocalImport).not.toHaveBeenCalled();
});

it("imports only the chosen providers, one model list each", async () => {
  const onImported = vi.fn();
  mocks.probeLocalImportModels.mockImplementation(
    async (_session: string, candidate: string) => {
      if (candidate === "c0") {
        throw new Error("upstream model discovery timed out");
      }
      return {
        protocol: "openai.models",
        model_ids: ["deepseek-v4-pro", "deepseek-v4-flash"],
      };
    },
  );
  await render({ onImported });
  await click(sourceBox("s2"));
  await click(button("读取配置"));
  await type(
    dialog().querySelector<HTMLInputElement>('input[placeholder="粘贴密钥"]')!,
    pastedKey,
  );
  const [relay, glm, deepseek] = rowBoxes();
  for (const box of [relay, glm, deepseek]) await click(box);
  await click(button("导入 3 个"));

  // GLM Coding Plan has no model list to fetch; it keeps the preset's.
  expect(mocks.probeLocalImportModels.mock.calls).toEqual([
    [
      session,
      "c0",
      {
        kind: "newapi",
        http: {
          base_url: "https://api.relayhub.example/v1",
          auth: { scheme: "bearer" },
        },
        protocol: "openai.models",
      },
    ],
    [
      session,
      "c2",
      {
        kind: "deepseek",
        http: {
          base_url: "https://api.deepseek.com/v1",
          auth: { scheme: "bearer" },
          credential: { secret: pastedKey },
        },
        protocol: "openai.models",
      },
    ],
  ]);
  const confirmed = mocks.confirmLocalImport.mock.calls.map((call) => {
    const [id, candidate, input] = call as [
      string,
      string,
      HTTPServiceCreateInput,
    ];
    return [id, candidate, input.kind, input.models, input.http];
  });
  expect(confirmed).toEqual([
    [
      session,
      "c0",
      "newapi",
      ["relay-large"],
      {
        base_url: "https://api.relayhub.example/v1",
        auth: { scheme: "bearer" },
      },
    ],
    [
      session,
      "c1",
      "glm_coding",
      ["glm-5.3", "glm-5.3-flash"],
      {
        base_url: "https://open.bigmodel.cn/api/coding/paas/v4",
        auth: { scheme: "bearer" },
      },
    ],
    [
      session,
      "c2",
      "deepseek",
      ["deepseek-v4-pro", "deepseek-v4-flash"],
      {
        base_url: "https://api.deepseek.com/v1",
        auth: { scheme: "bearer" },
        credential: { secret: pastedKey },
      },
    ],
  ]);
  expect(mocks.confirmLocalImport.mock.calls[0][2].name).toBe("Relay Hub");
  expect(onImported).toHaveBeenCalledTimes(3);
  // The host drops the keys as soon as the import ends.
  expect(mocks.dismissLocalImport.mock.calls).toEqual([[session]]);

  expect(text()).toContain("已导入 3 个提供商。");
  expect(text()).toContain(
    "获取模型列表失败（超时），先使用配置里的 1 个模型。",
  );
  expect(text()).toContain("2 个模型，来自 GLM Coding Plan 预设");
  expect(text()).toContain("获取到 2 个模型");
  expect(text()).not.toContain(pastedKey);

  // A second model-list request only when the user asks for it.
  mocks.probeServiceModels.mockResolvedValue({
    protocol: "openai.models",
    model_ids: ["relay-large", "relay-mini"],
  });
  mocks.updateService.mockImplementation(
    async (id: string, _etag: string, patch: { models: string[] }) => ({
      service: {
        ...service(id, "Relay Hub", "https://api.relayhub.example/v1"),
        models: patch.models,
      },
      etag: '"c0-2"',
    }),
  );
  await click(button("重新获取"));
  expect(mocks.probeServiceModels).toHaveBeenCalledWith(
    "svc_c0",
    "openai.models",
  );
  expect(mocks.updateService).toHaveBeenCalledWith("svc_c0", '"c0"', {
    models: ["relay-large", "relay-mini"],
  });
  expect(onImported).toHaveBeenCalledTimes(4);
  expect(text()).not.toContain("先用配置里的");
  expect(button("重新获取")).toBeUndefined();

  const onClose = vi.fn();
  await act(async () =>
    root.render(
      <LocalImportDialog
        onClose={onClose}
        onImported={onImported}
        protocols={[]}
        services={[oldRelay, teamRelay]}
      />,
    ),
  );
  await click(button("完成"));
  expect(onClose).toHaveBeenCalledTimes(1);
  expect(mocks.dismissLocalImport).toHaveBeenCalledTimes(1);
});

it("lets the key decide between a Coding Plan and the API", async () => {
  await readWithoutPi();
  const glm = () => rowBoxes()[1];
  await click(glm());
  expect(checked(glm())).toBe("true");

  // The API preset lists no models and this config names none.
  await chooseKind("智谱 GLM API");
  expect(text()).toContain("请手动添加这个提供商，并填上要用的模型。");
  expect(glm().disabled).toBe(true);
  expect(checked(glm())).toBe("false");
  expect(button("导入").disabled).toBe(true);

  await chooseKind("GLM Coding Plan");
  expect(glm().disabled).toBe(false);
  expect(checked(glm())).toBe("false");
});

it("drops the keys whenever the dialog closes", async () => {
  const onClose = vi.fn();
  await render({ onClose });
  await click(button("取消"));
  expect(onClose).toHaveBeenCalledTimes(1);
  expect(mocks.dismissLocalImport.mock.calls).toEqual([[session]]);
  expect(mocks.readLocalImport).not.toHaveBeenCalled();
  await act(async () => root.unmount());
  expect(mocks.dismissLocalImport).toHaveBeenCalledTimes(1);

  // Unmounted by its host without a close: the cleanup still ends it.
  mocks.dismissLocalImport.mockClear();
  root = createRoot(container);
  await readWithoutPi();
  await act(async () => root.unmount());
  expect(mocks.dismissLocalImport.mock.calls).toEqual([[session]]);
  root = createRoot(container);
});

it("asks to read again once the host has dropped an idle import", async () => {
  mocks.readLocalImport.mockRejectedValue(
    new Error("this import has closed; read the configs again"),
  );
  await readWithoutPi();
  expect(text()).toContain("闲置超过 10 分钟，读到的密钥已清除。");

  mocks.readLocalImport.mockResolvedValue({
    candidates: [],
    skipped: [
      {
        reason: "missing",
        tool: "claude",
        wsl: null,
        name: null,
        path: "~/.claude/settings.json",
        detail: null,
      },
    ],
  });
  await click(button("重新读取"));
  expect(mocks.openLocalImport).toHaveBeenCalledTimes(2);
  await click(sourceBox("s2"));
  await click(button("读取配置"));
  expect(text()).toContain("Claude Code、Codex 的配置里没有找到提供商。");
  expect(text()).toContain("没找到 ~/.claude/settings.json");
  expect(text()).not.toContain("导入时会获取每个提供商的模型列表。");
  expect(button("关闭")).toBeTruthy();
});
