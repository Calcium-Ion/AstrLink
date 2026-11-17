// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const tauri = vi.hoisted(() => ({
  invoke: vi.fn(),
  listen: vi.fn(),
  unlisten: vi.fn(),
}));
vi.mock("./native-host/core", () => ({
  isDesktopHost: () => true,
  invoke: tauri.invoke,
}));
vi.mock("./native-host/event", () => ({ listen: tauri.listen }));

import {
  parsePrivacyModelUpdate,
  usePrivacyModelUpdate,
} from "./use-privacy-model-update";

const update = {
  catalog_id: "astrlink-guard",
  name: "AstrLink Guard",
  version: "0.1.1",
  phase: "available",
};

function Probe() {
  const current = usePrivacyModelUpdate();
  return (
    <output>
      {current === null ? "none" : `${current.version} ${current.phase}`}
    </output>
  );
}

describe("usePrivacyModelUpdate", () => {
  let container: HTMLDivElement;
  let root: Root;
  let emit: ((event: { payload: unknown }) => void) | undefined;

  beforeEach(() => {
    (
      globalThis as typeof globalThis & {
        IS_REACT_ACT_ENVIRONMENT?: boolean;
      }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    tauri.invoke.mockReset().mockResolvedValue(update);
    tauri.unlisten.mockReset();
    tauri.listen.mockReset().mockImplementation(async (_event, callback) => {
      emit = callback;
      return tauri.unlisten;
    });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(() => {
    act(() => root.unmount());
    container.remove();
    emit = undefined;
  });

  it("reads the host status after subscribing and follows its events", async () => {
    await act(async () => root.render(<Probe />));
    expect(tauri.listen).toHaveBeenCalledWith(
      "privacy-model-update",
      expect.any(Function),
    );
    expect(tauri.invoke).toHaveBeenCalledWith("privacy_model_update_status");
    expect(container.textContent).toBe("0.1.1 available");

    await act(async () => emit!({ payload: { ...update, phase: "ready" } }));
    expect(container.textContent).toBe("0.1.1 ready");

    await act(async () => emit!({ payload: null }));
    expect(container.textContent).toBe("none");

    const error = vi.spyOn(console, "error").mockImplementation(() => {});
    await act(async () => emit!({ payload: { ...update, extra: true } }));
    expect(container.textContent).toBe("none");
    expect(error).toHaveBeenCalled();
    error.mockRestore();

    act(() => root.unmount());
    expect(tauri.unlisten).toHaveBeenCalledOnce();
    root = createRoot(container);
  });

  it("accepts only the host's exact shape", () => {
    expect(parsePrivacyModelUpdate(null)).toBeNull();
    expect(parsePrivacyModelUpdate(update)).toEqual(update);
    for (const invalid of [
      [],
      "0.1.1",
      { ...update, version: 1 },
      { ...update, phase: "installed" },
      {
        catalog_id: "astrlink-guard",
        name: "AstrLink Guard",
        version: "0.1.1",
      },
    ]) {
      expect(() => parsePrivacyModelUpdate(invalid)).toThrow();
    }
  });
});
