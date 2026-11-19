// @vitest-environment happy-dom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
const bridge = vi.hoisted(() => ({
  getSessionMetadata: vi.fn(),
  listMetadataAttributes: vi.fn(),
  listMetadataValues: vi.fn(),
  setSessionMetadata: vi.fn(),
  unbindSessionMetadata: vi.fn(),
}));
const notify = vi.hoisted(() => ({ success: vi.fn() }));
vi.mock("./bridge", () => bridge);
vi.mock("./notify", () => ({ notify }));
import { SessionMetadataChips } from "./SessionMetadata";
import { applyLocale } from "./i18n";
import { settleClosedLayer } from "./lib/test-layer-settle";

const project = {
  key: "project",
  label: "项目",
  description: "",
  pattern: "^[a-z0-9][a-z0-9._-]{0,63}$",
  marker: "memory-project",
  owner: "powercontext",
  created_at: "2026-10-18T00:00:00Z",
  updated_at: "2026-10-18T00:00:00Z",
  header: "X-Astrlink-Meta-Project",
};
const env = { ...project, key: "env", label: "环境", marker: undefined };
const bound = {
  values: {
    project: {
      value: "astrlink",
      set_by: "model_marker",
      turn: 2,
      updated_at: "2026-10-18T01:00:00Z",
    },
  },
};

let root: Root, container: HTMLDivElement;
beforeEach(async () => {
  await applyLocale("zh-CN");
  (
    globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  vi.resetAllMocks();
  bridge.listMetadataAttributes.mockResolvedValue([project, env]);
  bridge.getSessionMetadata.mockResolvedValue(bound);
  bridge.listMetadataValues.mockResolvedValue([
    {
      value: "astrlink",
      sessions: 3,
      last_used_at: "2026-10-18T01:00:00Z",
    },
    { value: "none", sessions: 1 },
  ]);
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
});

async function render() {
  await act(async () => {
    root.render(<SessionMetadataChips refreshKey="1" sessionId="session_1" />);
  });
}

function input(): HTMLInputElement {
  const field = document.querySelector<HTMLInputElement>(
    'input[aria-label="新的项目"]',
  );
  if (!field) throw new Error("no new project field");
  return field;
}

function picked(): string {
  return document.querySelector("#session-metadata-project")?.textContent ?? "";
}

/** Picks the option of the project picker whose text starts with option. */
async function pick(option: string) {
  const trigger = document.querySelector<HTMLButtonElement>(
    "#session-metadata-project",
  );
  if (!trigger) throw new Error("no project picker");
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
  ].find((candidate) => candidate.textContent?.startsWith(option));
  if (!item) throw new Error(`no option ${option}`);
  await act(async () => {
    item.click();
    await Promise.resolve();
  });
  await settleClosedLayer();
}

function button(label: string): HTMLButtonElement {
  const found = [...document.querySelectorAll("button")].find(
    (item) => item.textContent === label,
  );
  if (!found) throw new Error(`no ${label} button`);
  return found;
}

async function type(field: HTMLInputElement, value: string) {
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )?.set;
    setter?.call(field, value);
    field.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

it("shows the bindings a marker can make and where they came from", async () => {
  await render();
  const chips = container.querySelectorAll(
    "[data-testid=session-metadata-chip]",
  );
  expect(chips).toHaveLength(1);
  expect(chips[0].textContent).toBe("项目astrlink");
  expect(chips[0].getAttribute("title")).toBe("询问后选定 · 第 2 轮");
});

it("shows nothing while no attribute has a marker", async () => {
  bridge.listMetadataAttributes.mockResolvedValue([env]);
  bridge.getSessionMetadata.mockResolvedValue({ values: {} });
  await render();
  expect(container.textContent).toBe("");
});

it("binds by hand after checking the pattern, and unbinds to none", async () => {
  bridge.setSessionMetadata.mockResolvedValue({
    values: {
      project: {
        value: "paseo",
        set_by: "manual",
        turn: 3,
        updated_at: "2026-10-18T02:00:00Z",
      },
    },
  });
  bridge.unbindSessionMetadata.mockResolvedValue({
    values: {
      project: {
        value: "none",
        set_by: "manual",
        turn: 3,
        updated_at: "2026-10-18T03:00:00Z",
      },
    },
  });
  await render();
  const chip = container.querySelector<HTMLButtonElement>(
    "[data-testid=session-metadata-chip]",
  );
  await act(async () => chip?.click());
  expect(picked()).toBe("astrlink");
  // The picker offers the values sessions used, with their counts; none is
  // what unbinding stores, not a project.
  await pick("新项目…");
  const options = [...document.querySelectorAll('[role="option"]')];
  expect(options).toHaveLength(0);

  await type(input(), "Not Valid");
  await act(async () => button("保存").click());
  expect(bridge.setSessionMetadata).not.toHaveBeenCalled();
  expect(document.body.textContent).toContain("「项目」的值不符合校验规则。");

  await type(input(), "paseo");
  await act(async () => button("保存").click());
  expect(bridge.setSessionMetadata).toHaveBeenCalledWith(
    "session_1",
    "project",
    "paseo",
    false,
  );
  expect(container.textContent).toContain("paseo");

  await act(async () => chip?.click());
  await act(async () => button("解除").click());
  expect(bridge.unbindSessionMetadata).toHaveBeenCalledWith(
    "session_1",
    "project",
  );
  expect(notify.success).toHaveBeenCalledWith("已解除「项目」");
  expect(container.textContent).toContain("不纳入");
});

it("asks whether the workspace follows a session moved to another project", async () => {
  bridge.getSessionMetadata.mockResolvedValue({
    ...bound,
    workspace: { name: "astrlink", values: { project: "astrlink" } },
    choices: { project: ["astrlink", "paseo"] },
  });
  bridge.setSessionMetadata.mockResolvedValue({
    values: {
      project: {
        value: "paseo",
        set_by: "manual",
        turn: 3,
        updated_at: "2026-10-18T02:00:00Z",
      },
    },
  });
  await render();
  await act(async () =>
    container
      .querySelector<HTMLButtonElement>("[data-testid=session-metadata-chip]")
      ?.click(),
  );
  // Nothing to ask while the session keeps the workspace's project.
  expect(document.body.textContent).not.toContain("以后 astrlink 目录");
  await pick("paseo");
  const follow = [...document.querySelectorAll("label")].find((label) =>
    label.textContent?.startsWith("以后 astrlink 目录里的新会话也用这个"),
  );
  expect(follow?.textContent).toContain("现在是 astrlink");
  const box = document.getElementById(follow?.htmlFor ?? "");
  await act(async () => box?.click());
  await act(async () => button("保存").click());
  expect(bridge.setSessionMetadata).toHaveBeenCalledWith(
    "session_1",
    "project",
    "paseo",
    true,
  );
});
