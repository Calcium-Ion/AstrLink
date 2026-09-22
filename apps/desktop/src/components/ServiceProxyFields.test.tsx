// @vitest-environment happy-dom

import { act, useState } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import { ServiceProxyFields } from "./ServiceProxyFields";
import {
  proxyDraft,
  type ServiceProxyProbeResult,
} from "../service-proxy-model";

it("hides stale probe results and supports retrying failures without exposing raw errors", async () => {
  let resolve!: (result: ServiceProxyProbeResult) => void;
  const onTest = vi.fn().mockReturnValueOnce(
    new Promise<ServiceProxyProbeResult>((done) => {
      resolve = done;
    }),
  );
  function Form() {
    const [draft, setDraft] = useState(
      proxyDraft({ mode: "custom", url: "socks5://localhost:1080" }),
    );
    return (
      <ServiceProxyFields
        value={draft}
        onChange={setDraft}
        onTest={onTest}
        testTarget="https://provider.example"
      />
    );
  }
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  try {
    await act(async () => root.render(<Form />));
    const button = [...container.querySelectorAll("button")].find(
      (item) => item.textContent === "测试连通性",
    )!;
    await act(async () => button.click());
    expect(button.disabled).toBe(true);
    const input = container.querySelector<HTMLInputElement>(
      'input[aria-label="代理地址"]',
    )!;
    await act(async () => {
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(input, "socks5://localhost:1081");
      input.dispatchEvent(new Event("input", { bubbles: true }));
      resolve({ status_code: 200, latency_ms: 10 });
    });
    expect(container.querySelector('[role="status"]')).toBeNull();
    expect(button.disabled).toBe(false);
    onTest.mockRejectedValueOnce(new Error("proxy-user:proxy-secret"));
    await act(async () => button.click());
    expect(container.querySelector('[role="status"]')?.textContent).toContain(
      "连接失败",
    );
    expect(container.textContent).not.toContain("proxy-secret");
    onTest.mockResolvedValueOnce({ status_code: 401, latency_ms: 20 });
    await act(async () => button.click());
    expect(container.querySelector('[role="status"]')?.textContent).toContain(
      "HTTP 401",
    );
  } finally {
    await act(async () => root.unmount());
    container.remove();
  }
});
