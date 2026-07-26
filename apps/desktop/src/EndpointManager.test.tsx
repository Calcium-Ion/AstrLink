// @vitest-environment happy-dom

import { act, StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  type Mock,
  vi,
} from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  createEndpoint: vi.fn(),
  deleteEndpoint: vi.fn(),
  getEndpoint: vi.fn(),
  listEndpoints: vi.fn(),
  updateEndpoint: vi.fn(),
}));

vi.mock("./bridge", () => bridgeMocks);

import {
  EndpointManager,
  type EndpointCatalogStatus,
  type EndpointManagerView,
} from "./EndpointManager";
import type { Endpoint, EndpointRecord } from "./endpoint-model";
import { encodeModelEditorValue } from "./model-editor";

const endpointRecord = (
  name = "Existing subscription",
  id = "endpoint_01",
): EndpointRecord => ({
  endpoint: {
    id,
    name,
    kind: "custom",
    base_url: "https://existing.example/v1",
    auth: { scheme: "bearer" },
    credential_ref: `local://endpoint/${id}`,
    enabled: true,
    capabilities: [
      {
        protocol: "openai.responses",
        mode: "delegated",
        streaming: true,
      },
    ],
  },
  etag: `"sha256:${"a".repeat(64)}"`,
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((complete, fail) => {
    resolve = complete;
    reject = fail;
  });
  return { promise, reject, resolve };
}

function control<T extends Element>(selector: string): T {
  const element = document.querySelector<T>(selector);
  if (!element) throw new Error(`Missing test control: ${selector}`);
  return element;
}

function button(label: string): HTMLButtonElement {
  const match = [...document.querySelectorAll("button")].find(
    (candidate) => candidate.textContent?.trim() === label,
  );
  if (!(match instanceof HTMLButtonElement)) {
    throw new Error(`Missing test button: ${label}`);
  }
  return match;
}

async function setInput(selector: string, value: string): Promise<void> {
  const input = control<HTMLInputElement>(selector);
  const valueSetter = Object.getOwnPropertyDescriptor(
    HTMLInputElement.prototype,
    "value",
  )?.set;
  if (!valueSetter) throw new Error("Missing HTMLInputElement value setter");
  await act(async () => {
    valueSetter.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

async function typeInput(selector: string, value: string): Promise<void> {
  for (const character of value) {
    const input = control<HTMLInputElement>(selector);
    await setInput(selector, input.value + character);
  }
}

describe("EndpointManager page interactions", () => {
  let container: HTMLDivElement;
  let root: Root;
  let callbacks: {
    onDirtyChange: Mock<(dirty: boolean) => void>;
    onEndpointRemoved: Mock<(id: string) => void>;
    onEndpointSaved: Mock<(endpoint: Endpoint) => void>;
    onRefresh: Mock<() => void>;
    onViewChange: Mock<(view: EndpointManagerView) => void>;
  };

  beforeEach(() => {
    (
      globalThis as typeof globalThis & {
        IS_REACT_ACT_ENVIRONMENT?: boolean;
      }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    vi.clearAllMocks();
    callbacks = {
      onDirtyChange: vi.fn<(dirty: boolean) => void>(),
      onEndpointRemoved: vi.fn<(id: string) => void>(),
      onEndpointSaved: vi.fn<(endpoint: Endpoint) => void>(),
      onRefresh: vi.fn<() => void>(),
      onViewChange: vi.fn<(view: EndpointManagerView) => void>(),
    };
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  async function renderManager({
    catalogError = null,
    catalogStatus = "ready",
    endpoints = [endpointRecord().endpoint],
    isReady = true,
    strict = false,
    view = { kind: "create" },
  }: {
    catalogError?: string | null;
    catalogStatus?: EndpointCatalogStatus;
    endpoints?: Endpoint[];
    isReady?: boolean;
    strict?: boolean;
    view?: EndpointManagerView;
  } = {}): Promise<void> {
    const manager = (
      <EndpointManager
        catalogError={catalogError}
        catalogStatus={catalogStatus}
        endpoints={endpoints}
        isReady={isReady}
        onDirtyChange={callbacks.onDirtyChange}
        onEndpointRemoved={callbacks.onEndpointRemoved}
        onEndpointSaved={callbacks.onEndpointSaved}
        onRefresh={callbacks.onRefresh}
        onViewChange={callbacks.onViewChange}
        protocols={[]}
        view={view}
      />
    );
    await act(async () => {
      root.render(strict ? <StrictMode>{manager}</StrictMode> : manager);
    });
  }

  afterEach(async () => {
    vi.restoreAllMocks();
    await act(async () => {
      root.unmount();
    });
    container.remove();
  });

  it("keeps the catalog and editor on mutually exclusive pages", async () => {
    await renderManager({ view: { kind: "list" } });

    expect(container.textContent).toContain("Existing subscription");
    expect(document.querySelector("form.endpoint-form")).toBeNull();

    await act(async () => {
      button("编辑").click();
    });
    expect(callbacks.onViewChange).toHaveBeenLastCalledWith({
      kind: "edit",
      endpointId: "endpoint_01",
    });

    bridgeMocks.getEndpoint.mockResolvedValueOnce(endpointRecord());
    await renderManager({
      view: { kind: "edit", endpointId: "endpoint_01" },
    });

    expect(document.querySelector(".endpoint-list")).toBeNull();
    expect(control<HTMLInputElement>("#endpoint-name").value).toBe(
      "Existing subscription",
    );

  });

  it("does not report a blocked catalog as an empty service list", async () => {
    await renderManager({
      catalogStatus: "blocked",
      endpoints: [],
      isReady: false,
      view: { kind: "list" },
    });

    expect(container.textContent).toContain("Core 就绪后将读取已配置服务");
    expect(container.textContent).not.toContain("还没有 API 服务");
  });

  it("does not expose a submittable form while an edit record is loading", async () => {
    const pendingEndpoint = deferred<EndpointRecord>();
    bridgeMocks.getEndpoint.mockReturnValueOnce(pendingEndpoint.promise);

    await renderManager({
      view: { kind: "edit", endpointId: "endpoint_01" },
    });

    expect(container.textContent).toContain("正在载入服务配置");
    expect(document.querySelector("form.endpoint-form")).toBeNull();
    expect(bridgeMocks.createEndpoint).not.toHaveBeenCalled();
    expect(bridgeMocks.updateEndpoint).not.toHaveBeenCalled();

    await act(async () => {
      pendingEndpoint.resolve(endpointRecord());
      await pendingEndpoint.promise;
    });

    expect(control<HTMLFormElement>("form.endpoint-form")).not.toBeNull();
    expect(button("保存修改").disabled).toBe(false);
  });

  it("deduplicates StrictMode edit loads and ignores a stale record", async () => {
    const older = deferred<EndpointRecord>();
    const newer = deferred<EndpointRecord>();
    bridgeMocks.getEndpoint.mockImplementation((id: string) =>
      id === "endpoint_old" ? older.promise : newer.promise,
    );

    await renderManager({
      strict: true,
      view: { kind: "edit", endpointId: "endpoint_old" },
    });
    expect(bridgeMocks.getEndpoint).toHaveBeenCalledTimes(1);

    await renderManager({
      strict: true,
      view: { kind: "edit", endpointId: "endpoint_new" },
    });
    expect(bridgeMocks.getEndpoint).toHaveBeenCalledTimes(2);

    await act(async () => {
      newer.resolve(endpointRecord("Newest result", "endpoint_new"));
      await newer.promise;
    });
    expect(control<HTMLInputElement>("#endpoint-name").value).toBe(
      "Newest result",
    );

    await act(async () => {
      older.resolve(endpointRecord("Stale result", "endpoint_old"));
      await older.promise;
    });
    expect(control<HTMLInputElement>("#endpoint-name").value).toBe(
      "Newest result",
    );
  });

  it("opens advanced settings and focuses a hidden invalid capability", async () => {
    await renderManager();
    await setInput("#endpoint-base-url", "https://new.example/v1");
    await setInput("#endpoint-secret", "new-secret");
    const details = control<HTMLDetailsElement>("details.advanced-settings");

    await act(async () => {
      details.open = true;
      details.dispatchEvent(new Event("toggle"));
    });
    await setInput("#endpoint-capability-0-protocol", "");
    await act(async () => {
      details.open = false;
      details.dispatchEvent(new Event("toggle"));
    });

    await act(async () => {
      button("保存服务").click();
    });

    expect(document.body.textContent).toContain("协议 ID“空”格式无效");
    expect(details.open).toBe(true);
    expect(document.activeElement).toBe(
      control("#endpoint-capability-0-protocol"),
    );
    expect(bridgeMocks.createEndpoint).not.toHaveBeenCalled();
  });

  it("reports dirty changes and clears them when the parent returns to the list", async () => {
    await renderManager();
    expect(callbacks.onDirtyChange).toHaveBeenLastCalledWith(false);

    await setInput("#endpoint-base-url", "https://new.example/v1");
    expect(callbacks.onDirtyChange).toHaveBeenLastCalledWith(true);

    await renderManager({ view: { kind: "list" } });
    expect(callbacks.onDirtyChange).toHaveBeenLastCalledWith(false);
  });

  it("publishes create results directly without refreshing the catalog", async () => {
    const created = endpointRecord("Created subscription", "endpoint_created");
    bridgeMocks.createEndpoint.mockResolvedValueOnce(created);
    await renderManager({ endpoints: [] });
    await setInput("#endpoint-base-url", "https://new.example/v1");
    await setInput("#endpoint-secret", "new-secret");

    await act(async () => {
      button("保存服务").click();
      await Promise.resolve();
    });

    expect(callbacks.onEndpointSaved).toHaveBeenCalledWith(created.endpoint);
    expect(callbacks.onDirtyChange).toHaveBeenLastCalledWith(false);
    expect(callbacks.onViewChange).not.toHaveBeenCalled();
    expect(bridgeMocks.listEndpoints).not.toHaveBeenCalled();
  });

  it("publishes update results directly and returns to the catalog", async () => {
    const original = endpointRecord();
    const updated = endpointRecord("Renamed subscription");
    bridgeMocks.getEndpoint.mockResolvedValueOnce(original);
    bridgeMocks.updateEndpoint.mockResolvedValueOnce(updated);
    await renderManager({
      view: { kind: "edit", endpointId: original.endpoint.id },
    });
    await setInput("#endpoint-name", "Renamed subscription");

    await act(async () => {
      button("保存修改").click();
      await Promise.resolve();
    });

    expect(bridgeMocks.updateEndpoint).toHaveBeenCalledWith(
      original.endpoint.id,
      original.etag,
      { name: "Renamed subscription" },
    );
    expect(callbacks.onEndpointSaved).toHaveBeenCalledWith(updated.endpoint);
    expect(callbacks.onViewChange).not.toHaveBeenCalled();
    expect(bridgeMocks.listEndpoints).not.toHaveBeenCalled();
  });

  it("deletes with the current ETag and publishes the removed id", async () => {
    const record = endpointRecord();
    bridgeMocks.getEndpoint.mockResolvedValueOnce(record);
    bridgeMocks.deleteEndpoint.mockResolvedValueOnce(undefined);
    window.confirm = vi.fn(() => true);
    await renderManager({ view: { kind: "list" } });

    await act(async () => {
      button("删除").click();
      await Promise.resolve();
    });

    expect(bridgeMocks.deleteEndpoint).toHaveBeenCalledWith(
      record.endpoint.id,
      record.etag,
    );
    expect(callbacks.onEndpointRemoved).toHaveBeenCalledWith(
      record.endpoint.id,
    );
    expect(bridgeMocks.listEndpoints).not.toHaveBeenCalled();
  });

  it("preserves model whitespace and control characters exactly", async () => {
    await renderManager();
    await setInput("#endpoint-base-url", "https://new.example/v1");
    await setInput("#endpoint-secret", "new-secret");
    const details = control<HTMLDetailsElement>("details.advanced-settings");
    await act(async () => {
      details.open = true;
      details.dispatchEvent(new Event("toggle"));
    });

    await act(async () => {
      control<HTMLButtonElement>("#endpoint-capability-0-add-model").click();
    });
    const controlCharacterModel = "  model-a\r\nrevision\rtrailer\n";
    const encodedControlCharacterModel = encodeModelEditorValue(
      controlCharacterModel,
    );
    await typeInput(
      "#endpoint-capability-0-model-0",
      encodedControlCharacterModel,
    );
    const firstModelInput = control<HTMLInputElement>(
      "#endpoint-capability-0-model-0",
    );
    expect(firstModelInput.value).toBe(encodedControlCharacterModel);
    await setInput(
      "#endpoint-capability-0-model-0",
      encodedControlCharacterModel.slice(0, -1),
    );
    expect(firstModelInput.value).toBe(
      encodedControlCharacterModel.slice(0, -1),
    );
    await typeInput("#endpoint-capability-0-model-0", "n");
    await act(async () => {
      control<HTMLButtonElement>("#endpoint-capability-0-add-model").click();
    });
    const whitespaceModel = "\tmodel-b\t";
    await setInput(
      "#endpoint-capability-0-model-1",
      encodeModelEditorValue(whitespaceModel),
    );
    expect(
      control<HTMLInputElement>("#endpoint-capability-0-model-0").value,
    ).toBe(encodedControlCharacterModel);
    expect(
      control<HTMLInputElement>("#endpoint-capability-0-model-1").value,
    ).toBe(encodeModelEditorValue(whitespaceModel));
    bridgeMocks.createEndpoint.mockResolvedValueOnce(endpointRecord());

    await act(async () => {
      button("保存服务").click();
      await Promise.resolve();
    });

    expect(bridgeMocks.createEndpoint).toHaveBeenCalledOnce();
    expect(
      bridgeMocks.createEndpoint.mock.calls[0]?.[0].capabilities[0].models,
    ).toEqual([controlCharacterModel, whitespaceModel]);
  });
});
