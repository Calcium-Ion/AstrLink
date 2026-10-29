// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { RawAccessGrant, RawAccessList } from "./raw-access-model";
import type { RawSealingState } from "./raw-sealing-model";

const mocks = vi.hoisted(() => ({
  decideRawAccess: vi.fn(),
  getRawSealingStatus: vi.fn(),
  getRequestRecord: vi.fn(),
  listRawAccess: vi.fn(),
  listenRawSealingChanged: vi.fn(),
  revokeRawGrant: vi.fn(),
  toastError: vi.fn(),
  toastInfo: vi.fn(),
  toastSuccess: vi.fn(),
}));

vi.mock("./bridge", () => ({
  decideRawAccess: mocks.decideRawAccess,
  getRawSealingStatus: mocks.getRawSealingStatus,
  getRequestRecord: mocks.getRequestRecord,
  listRawAccess: mocks.listRawAccess,
  listenRawSealingChanged: mocks.listenRawSealingChanged,
  revokeRawGrant: mocks.revokeRawGrant,
}));
vi.mock("sonner", () => ({
  toast: {
    error: mocks.toastError,
    info: mocks.toastInfo,
    success: mocks.toastSuccess,
  },
}));

import { RawAccessApprovalWindow } from "./RawAccessApprovalWindow";

let container: HTMLDivElement;
let root: Root;

function grant(overrides: Partial<RawAccessGrant> = {}): RawAccessGrant {
  return {
    grant_id: "rawgrant_0123456789abcdef",
    request_id: "req_raw_01",
    status: "pending",
    decision: null,
    scope: null,
    reason: "需要核对上游返回的原始 JSON",
    client_name: "Claude Code",
    created_at: "2026-09-28T10:00:00Z",
    expires_at: new Date(Date.now() + 10 * 60_000).toISOString(),
    ...overrides,
  };
}

function list(overrides: Partial<RawAccessList> = {}): RawAccessList {
  return { pending: [], active: [], ...overrides };
}

function sealing(overrides: Partial<RawSealingState> = {}): RawSealingState {
  return {
    raw_available: true,
    configured: true,
    password_set: true,
    password_required: overrides.password_set === false,
    envelopes: ["password"],
    key_verified: true,
    unlocked: false,
    unlock_expires_at: null,
    unlock_idle_seconds: 900,
    retry_after_seconds: 0,
    password_min_length: 8,
    password_max_length: 128,
    key_replaced: false,
    biometric_available: false,
    ...overrides,
  };
}

/** Touch ID offered on this Mac and set up for raw content. */
const touchID = sealing({
  envelopes: ["password", "biometric"],
  biometric_available: true,
});

const passwordProof = { kind: "password", password: "correct horse" };
const biometricProof = { kind: "biometric" };

const laterGrant = grant({
  grant_id: "rawgrant_fedcba9876543210",
  request_id: "req_raw_02",
  client_name: "Codex",
  created_at: "2026-09-28T10:05:00Z",
});

const runningGrant = grant({
  grant_id: "rawgrant_2222222222222222",
  status: "approved",
  decision: "window_1h",
  scope: "all_requests",
  client_name: "Codex",
});

beforeEach(() => {
  (
    globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  for (const mock of Object.values(mocks)) mock.mockReset();
  mocks.listRawAccess.mockResolvedValue(list());
  mocks.listenRawSealingChanged.mockResolvedValue(() => {});
  mocks.getRawSealingStatus.mockResolvedValue(sealing());
  mocks.getRequestRecord.mockImplementation(async (requestId: string) => ({
    id: requestId,
    requested_model: requestId === "req_raw_01" ? "gpt-5" : "claude-opus",
    started_at: "2026-09-28T09:58:00Z",
  }));
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  vi.useRealTimers();
});

async function render() {
  await act(async () => root.render(<RawAccessApprovalWindow />));
  await act(async () => {});
}

async function settle() {
  await act(async () => {});
  await act(async () => {});
}

function button(label: string): HTMLButtonElement {
  const match = [
    ...document.querySelectorAll<HTMLButtonElement>("button"),
  ].find((candidate) => candidate.textContent?.trim() === label);
  if (!match) throw new Error(`Missing button: ${label}`);
  return match;
}

function passwordInput(): HTMLInputElement | null {
  return document.querySelector<HTMLInputElement>('input[type="password"]');
}

async function typePassword(value: string) {
  const input = passwordInput();
  if (!input) throw new Error("Missing password input");
  await act(async () => {
    Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

describe("RawAccessApprovalWindow", () => {
  it("shows the oldest request and says what lasting approvals reach", async () => {
    mocks.listRawAccess.mockResolvedValue(
      list({ pending: [laterGrant, grant()] }),
    );

    await render();

    const text = container.textContent ?? "";
    expect(text).toContain("「Claude Code」请求查看");
    expect(text).toContain("req_raw_01");
    expect(text).toContain("gpt-5");
    expect(text).toContain("需要核对上游返回的原始 JSON");
    expect(text).toContain("之后还有 1 个申请在等待");
    expect(
      document.querySelector('[data-slot="raw-access-timed-warning"]')
        ?.textContent,
    ).toBe(
      "「批准到锁屏」和「批准 1 小时」会允许它读取所有请求的原文，包括之后新产生的请求，次数不限。「批准到锁屏」在锁屏、电脑睡眠、退出 AstrLink 或点击「锁定」时结束。",
    );
    expect(text).toContain("关闭窗口不会批准或拒绝");
    // Nothing takes focus and no form submits on Enter.
    expect(document.activeElement).toBe(document.body);
    expect(document.querySelector("form")).toBeNull();
    expect(
      [...document.querySelectorAll("button[data-decision]")].map(
        (node) => node.textContent,
      ),
    ).toEqual(["批准一次", "批准到锁屏", "批准 1 小时", "拒绝"]);
    for (const node of document.querySelectorAll("button")) {
      expect(node.type).toBe("button");
    }
  });

  it("approves until the screen locks with Touch ID", async () => {
    mocks.listRawAccess.mockResolvedValueOnce(list({ pending: [grant()] }));
    mocks.getRawSealingStatus.mockResolvedValue(touchID);
    mocks.decideRawAccess.mockResolvedValue({
      outcome: "decided",
      grant: grant({ status: "approved", decision: "until_lock" }),
    });
    await render();

    // The host prompts for Touch ID; no password field is needed.
    expect(passwordInput()).toBeNull();
    expect(container.textContent).toContain("点击批准后，验证 Touch ID 即可。");
    await act(async () => button("批准到锁屏").click());
    await settle();

    expect(mocks.decideRawAccess).toHaveBeenCalledExactlyOnceWith(
      "rawgrant_0123456789abcdef",
      "until_lock",
      biometricProof,
    );
    expect(mocks.toastSuccess).toHaveBeenCalledWith(
      "已允许「Claude Code」在锁屏前读取所有请求的原文",
    );
    expect(container.textContent).toContain("没有待批准的申请");
  });

  it("falls back to the raw password when Touch ID cannot answer", async () => {
    mocks.listRawAccess.mockResolvedValue(list({ pending: [grant()] }));
    mocks.getRawSealingStatus.mockResolvedValue(touchID);
    mocks.decideRawAccess.mockResolvedValueOnce({
      outcome: "biometric_cancelled",
    });
    await render();

    await act(async () => button("批准一次").click());
    await settle();
    expect(container.textContent).toContain("Touch ID 已取消。");
    expect(passwordInput()).toBeNull();

    mocks.decideRawAccess.mockResolvedValueOnce({
      outcome: "biometric_unavailable",
    });
    await act(async () => button("批准一次").click());
    await settle();
    expect(container.textContent).toContain(
      "Touch ID 现在不可用，请输入原文口令。",
    );
    expect(button("批准一次").disabled).toBe(true);

    mocks.decideRawAccess.mockResolvedValueOnce({
      outcome: "decided",
      grant: grant({ status: "approved", decision: "once" }),
    });
    await typePassword("correct horse");
    await act(async () => button("批准一次").click());
    await settle();
    expect(mocks.decideRawAccess).toHaveBeenLastCalledWith(
      "rawgrant_0123456789abcdef",
      "once",
      passwordProof,
    );
  });

  it("lets the operator choose the raw password over Touch ID", async () => {
    mocks.listRawAccess.mockResolvedValue(list({ pending: [grant()] }));
    mocks.getRawSealingStatus.mockResolvedValue(touchID);
    await render();

    await act(async () => button("改用原文口令").click());
    expect(passwordInput()).not.toBeNull();
    expect(button("批准一次").disabled).toBe(true);
    await act(async () => button("改用 Touch ID").click());
    expect(passwordInput()).toBeNull();
    expect(button("批准一次").disabled).toBe(false);
  });

  it("does not approve on Enter", async () => {
    mocks.listRawAccess.mockResolvedValue(list({ pending: [grant()] }));
    await render();

    const approve = button("批准一次");
    const event = new KeyboardEvent("keydown", {
      bubbles: true,
      cancelable: true,
      key: "Enter",
    });
    await act(async () => approve.dispatchEvent(event));

    expect(event.defaultPrevented).toBe(true);
    expect(mocks.decideRawAccess).not.toHaveBeenCalled();
  });

  it("asks for the password even while raw content is unlocked", async () => {
    mocks.listRawAccess.mockResolvedValueOnce(
      list({ pending: [grant(), laterGrant] }),
    );
    mocks.listRawAccess.mockResolvedValue(list({ pending: [laterGrant] }));
    // An open unlock is no proof: UI automation could click through it.
    mocks.getRawSealingStatus.mockResolvedValue(sealing({ unlocked: true }));
    mocks.decideRawAccess.mockResolvedValue({
      outcome: "decided",
      grant: grant({ status: "approved", decision: "once" }),
    });
    await render();

    expect(container.textContent).toContain("批准需要输入原文口令。");
    expect(button("批准一次").disabled).toBe(true);
    expect(button("拒绝").disabled).toBe(false);
    await typePassword("correct horse");
    await act(async () => button("批准一次").click());
    await settle();

    expect(mocks.decideRawAccess).toHaveBeenCalledExactlyOnceWith(
      "rawgrant_0123456789abcdef",
      "once",
      passwordProof,
    );
    expect(mocks.toastSuccess).toHaveBeenCalledWith(
      "已允许「Claude Code」读取一次原文",
    );
    expect(container.textContent).toContain("「Codex」请求查看");
    expect(passwordInput()?.value).toBe("");
  });

  it("asks for the password when Core takes no Touch ID", async () => {
    mocks.listRawAccess.mockResolvedValue(list({ pending: [grant()] }));
    mocks.getRawSealingStatus.mockResolvedValue(touchID);
    mocks.decideRawAccess.mockResolvedValueOnce({
      outcome: "proof_required",
    });
    await render();

    await act(async () => button("批准 1 小时").click());
    await settle();

    expect(container.textContent).toContain("批准需要输入原文口令。");
    mocks.decideRawAccess.mockResolvedValueOnce({
      outcome: "decided",
      grant: grant({ status: "approved", decision: "window_1h" }),
    });
    await typePassword("correct horse");
    await act(async () => button("批准 1 小时").click());
    await settle();

    expect(mocks.decideRawAccess).toHaveBeenLastCalledWith(
      "rawgrant_0123456789abcdef",
      "window_1h",
      passwordProof,
    );
    expect(mocks.toastSuccess).toHaveBeenCalledWith(
      "已允许「Claude Code」在 1 小时内读取所有请求的原文",
    );
  });

  it("keeps the request after a wrong password or a backoff", async () => {
    // A frozen clock keeps a slow runner from counting the backoff down.
    vi.useFakeTimers({ toFake: ["Date"] });
    mocks.listRawAccess.mockResolvedValue(list({ pending: [grant()] }));
    mocks.decideRawAccess.mockResolvedValueOnce({
      outcome: "password_invalid",
    });
    await render();
    await typePassword("wrong horse");
    await act(async () => button("批准一次").click());
    await settle();

    expect(container.textContent).toContain("原文口令不正确。");
    expect(mocks.toastSuccess).not.toHaveBeenCalled();

    mocks.decideRawAccess.mockResolvedValueOnce({
      outcome: "backoff",
      retry_after_seconds: 8,
    });
    await typePassword("wrong horse");
    await act(async () => button("批准一次").click());
    await settle();

    expect(
      document.querySelector('[data-slot="proof-backoff"]')?.textContent,
    ).toContain("8 秒");
    await typePassword("correct horse");
    expect(button("批准一次").disabled).toBe(true);
  });

  it("denies without a proof", async () => {
    mocks.listRawAccess.mockResolvedValueOnce(list({ pending: [grant()] }));
    mocks.decideRawAccess.mockResolvedValue({
      outcome: "decided",
      grant: grant({ status: "denied", decision: "deny" }),
    });
    await render();

    await act(async () => button("拒绝").click());
    await settle();

    expect(mocks.decideRawAccess).toHaveBeenCalledExactlyOnceWith(
      "rawgrant_0123456789abcdef",
      "deny",
    );
    expect(mocks.toastInfo).toHaveBeenCalledWith("已拒绝原文申请");
  });

  it("leaves only denying when no raw password is set", async () => {
    mocks.listRawAccess.mockResolvedValue(list({ pending: [grant()] }));
    mocks.getRawSealingStatus.mockResolvedValue(
      sealing({ password_set: false }),
    );
    await render();

    expect(
      document.querySelector('[data-testid="raw-access-unreachable"]'),
    ).not.toBeNull();
    expect(passwordInput()).toBeNull();
    expect(button("批准一次").disabled).toBe(true);
    expect(button("拒绝").disabled).toBe(false);
  });

  it("lists running grants and revokes one", async () => {
    mocks.listRawAccess.mockResolvedValueOnce(list({ active: [runningGrant] }));
    mocks.listRawAccess.mockResolvedValue(list());
    mocks.revokeRawGrant.mockResolvedValue({
      ...runningGrant,
      status: "revoked",
    });
    await render();

    const grants = document.querySelector('[data-slot="active-raw-grants"]');
    expect(grants?.textContent).toContain("可读取原文的 Agent");
    expect(grants?.textContent).toContain("Codex");
    expect(grants?.textContent).toContain("所有请求 · 剩余 ");
    expect(grants?.textContent).toContain(
      "关闭窗口后，这些 Agent 照常可以读取原文。",
    );
    expect(container.textContent).toContain("没有待批准的申请");

    await act(async () => button("撤销").click());
    await settle();

    expect(mocks.revokeRawGrant).toHaveBeenCalledExactlyOnceWith(
      "rawgrant_2222222222222222",
    );
    expect(mocks.toastSuccess).toHaveBeenCalledWith(
      "「Codex」已不能再读取原文",
    );
    expect(
      document.querySelector('[data-slot="active-raw-grants"]'),
    ).toBeNull();
  });

  it("shows a grant that runs until the screen locks", async () => {
    mocks.listRawAccess.mockResolvedValue(
      list({
        active: [{ ...runningGrant, decision: "until_lock", expires_at: null }],
      }),
    );
    await render();

    expect(
      document.querySelector('[data-slot="active-raw-grants"]')?.textContent,
    ).toContain("所有请求 · 到锁屏为止");
  });
});
