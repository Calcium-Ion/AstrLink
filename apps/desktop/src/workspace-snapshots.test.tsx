// @vitest-environment happy-dom

import { act, type Dispatch, type SetStateAction } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import {
  WorkspaceSnapshotProvider,
  useWorkspaceSnapshot,
} from "./workspace-snapshots";

describe("workspace snapshots", () => {
  let container: HTMLDivElement;
  let root: Root;
  let writeCore: Dispatch<SetStateAction<string>>;
  let writeDesktop: Dispatch<SetStateAction<string>>;

  function Page() {
    const [core, setCore] = useWorkspaceSnapshot("record", "loading");
    const [desktop, setDesktop] = useWorkspaceSnapshot(
      "preferences",
      "loading",
      "desktop",
    );
    writeCore = setCore;
    writeDesktop = setDesktop;
    return (
      <p>
        {core}/{desktop}
      </p>
    );
  }

  async function render(sessionKey: string, visible = true) {
    await act(async () =>
      root.render(
        <WorkspaceSnapshotProvider sessionKey={sessionKey}>
          {visible ? <Page /> : null}
        </WorkspaceSnapshotProvider>,
      ),
    );
  }

  beforeEach(() => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  it("restores data synchronously after a page unmounts", async () => {
    await render("core-1");
    await act(async () => {
      writeCore("records");
      writeDesktop("saved");
    });
    await render("core-1", false);
    expect(container.textContent).toBe("");
    await render("core-1");
    expect(container.textContent).toBe("records/saved");
  });

  it("isolates Core sessions and late responses while retaining desktop preferences", async () => {
    await render("core-1");
    await act(async () => {
      writeCore("old records");
      writeDesktop("saved");
    });
    const writeOldSession = writeCore!;
    await render("core-2");
    expect(container.textContent).toBe("loading/saved");
    await act(async () => writeOldSession("late old records"));
    expect(container.textContent).toBe("loading/saved");
    await act(async () => writeCore("new records"));
    expect(container.textContent).toBe("new records/saved");
  });
});
