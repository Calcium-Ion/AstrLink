import { describe, expect, it } from "vitest";

import { placeFloatingCard } from "./place-floating-card";

describe("placeFloatingCard", () => {
  it("centers the card above the anchor when there is room", () => {
    expect(
      placeFloatingCard({
        anchorX: 200,
        anchorY: 180,
        height: 80,
        viewportHeight: 400,
        viewportWidth: 400,
        width: 160,
      }),
    ).toEqual({ left: 120, top: 92 });
  });

  it("flips to the left when the right edge would clip", () => {
    expect(
      placeFloatingCard({
        anchorX: 380,
        anchorY: 180,
        height: 80,
        viewportHeight: 400,
        viewportWidth: 400,
        width: 160,
      }),
    ).toEqual({ left: 212, top: 92 });
  });

  it("flips to the right when the left edge would clip", () => {
    expect(
      placeFloatingCard({
        anchorX: 20,
        anchorY: 180,
        height: 80,
        viewportHeight: 400,
        viewportWidth: 400,
        width: 160,
      }),
    ).toEqual({ left: 28, top: 92 });
  });

  it("flips below the anchor when the top edge would clip", () => {
    expect(
      placeFloatingCard({
        anchorX: 200,
        anchorY: 40,
        height: 80,
        viewportHeight: 400,
        viewportWidth: 400,
        width: 160,
      }),
    ).toEqual({ left: 120, top: 48 });
  });
});
