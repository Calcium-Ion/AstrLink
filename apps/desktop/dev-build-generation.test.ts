import { describe, expect, it, vi } from "vitest";

import {
  BuildGeneration,
  createBuildGenerationMiddleware,
  DEV_BUILD_ID_PATH,
} from "./dev-build-generation";

describe("BuildGeneration", () => {
  it("does not increment on the first compile or on errors", () => {
    const generation = new BuildGeneration();
    expect(
      generation.noteCompile({
        isFirstCompile: true,
        stats: { hasErrors: () => false },
      }),
    ).toBe(false);
    expect(generation.value).toBe(0);

    expect(
      generation.noteCompile({
        isFirstCompile: false,
        stats: { hasErrors: () => true },
      }),
    ).toBe(false);
    expect(generation.value).toBe(0);
  });

  it("increments after a successful rebuild", () => {
    const generation = new BuildGeneration();
    expect(
      generation.noteCompile({
        isFirstCompile: false,
        stats: { hasErrors: () => false },
      }),
    ).toBe(true);
    expect(generation.value).toBe(1);
  });
});

describe("createBuildGenerationMiddleware", () => {
  it("returns the current generation with no-store on the build id path", () => {
    const generation = new BuildGeneration();
    generation.noteCompile({
      isFirstCompile: false,
      stats: { hasErrors: () => false },
    });
    const middleware = createBuildGenerationMiddleware(generation);
    const headers = new Map<string, string>();
    const res = {
      statusCode: 0,
      setHeader(name: string, value: string) {
        headers.set(name, value);
      },
      end: vi.fn(),
    };
    const next = vi.fn();

    middleware({ url: `${DEV_BUILD_ID_PATH}?cache=1` }, res, next);

    expect(next).not.toHaveBeenCalled();
    expect(res.statusCode).toBe(200);
    expect(headers.get("Content-Type")).toBe("text/plain; charset=utf-8");
    expect(headers.get("Cache-Control")).toBe("no-store");
    expect(res.end).toHaveBeenCalledWith("1");
  });

  it("calls next for unrelated URLs", () => {
    const middleware = createBuildGenerationMiddleware(new BuildGeneration());
    const res = {
      statusCode: 0,
      setHeader: vi.fn(),
      end: vi.fn(),
    };
    const next = vi.fn();

    middleware({ url: "/static/js/index.js" }, res, next);

    expect(next).toHaveBeenCalledTimes(1);
    expect(res.end).not.toHaveBeenCalled();
  });
});
