// @vitest-environment happy-dom

import { afterEach, describe, expect, it, vi } from "vitest";

import { downloadTextFile } from "./download-text-file";

describe("downloadTextFile", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("clicks a hidden download anchor backed by a blob URL", () => {
    const createObjectURL = vi.fn(() => "blob:astrlink-export");
    const revokeObjectURL = vi.fn();
    vi.spyOn(URL, "createObjectURL").mockImplementation(createObjectURL);
    vi.spyOn(URL, "revokeObjectURL").mockImplementation(revokeObjectURL);

    const clicks: Array<{ download: string; href: string }> = [];
    const createElement = document.createElement.bind(document);
    vi.spyOn(document, "createElement").mockImplementation(
      (tagName, options) => {
        const element = createElement(tagName, options);
        if (tagName === "a") {
          const anchor = element as HTMLAnchorElement;
          anchor.click = () => {
            clicks.push({ download: anchor.download, href: anchor.href });
          };
        }
        return element;
      },
    );

    downloadTextFile(
      "astrlink-req_bundle_test.txt",
      "plain body",
      "text/plain;charset=utf-8",
    );

    expect(createObjectURL).toHaveBeenCalledTimes(1);
    const blob = createObjectURL.mock.calls.at(0)?.at(0);
    expect(blob).toBeInstanceOf(Blob);
    expect(blob).toMatchObject({ type: "text/plain;charset=utf-8" });
    expect(clicks).toEqual([
      {
        download: "astrlink-req_bundle_test.txt",
        href: "blob:astrlink-export",
      },
    ]);
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:astrlink-export");
    expect(document.querySelector("a")).toBeNull();
  });
});
