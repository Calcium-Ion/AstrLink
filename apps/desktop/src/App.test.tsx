// @vitest-environment happy-dom

import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, it, vi } from "vitest";

import App from "./App";
import logo from "./assets/astrlink-logo.svg";
import developmentLogo from "./assets/astrlink-dev-logo.svg";

describe("desktop application shell", () => {
  afterEach(() => {
    vi.unstubAllEnvs();
    delete window.__ASTRLINK_ISOLATED__;
  });

  it.each([
    { isolated: true, environment: "production", development: true },
    { isolated: false, environment: "development", development: false },
    { isolated: undefined, environment: "development", development: true },
    { isolated: undefined, environment: "production", development: false },
  ])(
    "shows the instance branding for isolated=$isolated, NODE_ENV=$environment",
    ({ isolated, environment, development }) => {
      vi.stubEnv("NODE_ENV", environment);
      window.__ASTRLINK_ISOLATED__ = isolated;

      const markup = renderToStaticMarkup(<App />);
      const container = document.createElement("div");
      container.innerHTML = markup;
      const brand = container.querySelector('[data-slot="app-brand"]');
      expect(brand?.getAttribute("aria-label")).toBe(
        development ? "AstrLink Dev" : "AstrLink",
      );
      expect(brand?.querySelector("img")?.getAttribute("src")).toBe(
        development ? developmentLogo : logo,
      );
      expect(brand?.querySelector('[data-slot="badge"]')?.textContent).toBe(
        development ? "DEV" : undefined,
      );
    },
  );

  it("renders task pages instead of the former marketing homepage", () => {
    const markup = renderToStaticMarkup(<App />);

    expect(markup).toContain("主要导航");
    expect(markup).toContain("概览");
    expect(markup).toContain("提供商");
    expect(markup).toContain("令牌");
    expect(markup).toContain("路由");
    expect(markup).toContain("Agent 工具");
    expect(markup).toContain("设置");
    expect(markup).toContain("usage-heading");
    expect(markup).toContain("连接中");
    expect(markup).toContain("用量概览");
    expect(markup).toContain("请求");
    expect(markup).toContain("系统详情");
    expect(markup).not.toContain("欢迎使用 AstrLink");
    expect(markup).not.toContain("当前为浏览器预览");

    expect(markup).not.toContain("统一 API 网关");
    expect(markup).not.toContain("运行在你的桌面");
    expect(markup).not.toContain("本地 AI 网关");
    expect(markup).not.toContain("Alpha 将使用");
    expect(markup).not.toContain("新手引导");
    expect(markup).not.toContain("尚未添加 API 提供商");
  });
});
