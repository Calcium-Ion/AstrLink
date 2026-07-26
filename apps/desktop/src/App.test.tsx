import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import App from "./App";

describe("desktop application shell", () => {
  it("renders task pages instead of the former marketing homepage", () => {
    const markup = renderToStaticMarkup(<App />);

    expect(markup).toContain("主要导航");
    expect(markup).toContain("概览");
    expect(markup).toContain("API 服务");
    expect(markup).toContain("访问令牌");
    expect(markup).toContain("路由与模型");
    expect(markup).toContain("即将推出");
    expect(markup).toContain("连接 AstrLink");
    expect(markup).toContain("今日用量");
    expect(markup).toContain("预估费用");
    expect(markup).toContain("请求记录");
    expect(markup).toContain("上游服务");
    expect(markup).toContain("系统详情");
    expect(markup).toContain("Core 就绪后显示已配置服务");

    expect(markup).not.toContain("统一 API 网关");
    expect(markup).not.toContain("运行在你的桌面");
    expect(markup).not.toContain("本地 AI 网关");
    expect(markup).not.toContain("Alpha 将使用");
    expect(markup).not.toContain("新手引导");
    expect(markup).not.toContain("尚未添加 API 服务");
  });
});
