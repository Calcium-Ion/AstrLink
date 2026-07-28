import { getCurrentWindow } from "@tauri-apps/api/window";
import {
  type MouseEvent as ReactMouseEvent,
  type ReactNode,
  useCallback,
  useEffect,
  useMemo,
  useState,
} from "react";

import {
  type DesktopPlatform,
  type WindowControl,
  type WindowControlLayout,
  getDesktopPlatform,
  loadLinuxWindowControlLayout,
  parseLinuxDecorationLayout,
} from "./window-chrome";

type ResizeDirection =
  | "East"
  | "North"
  | "NorthEast"
  | "NorthWest"
  | "South"
  | "SouthEast"
  | "SouthWest"
  | "West";

interface WindowChromeProps {
  platform?: DesktopPlatform;
}

interface WindowState {
  focused: boolean;
  fullscreen: boolean;
  maximized: boolean;
}

const resizeDirections: ResizeDirection[] = [
  "North",
  "NorthEast",
  "East",
  "SouthEast",
  "South",
  "SouthWest",
  "West",
  "NorthWest",
];

const initialWindowState: WindowState = {
  focused: true,
  fullscreen: false,
  maximized: false,
};

function ControlIcon({
  control,
  maximized,
}: {
  control: WindowControl;
  maximized: boolean;
}): ReactNode {
  if (control === "minimize") {
    return <path d="M4 11.5h8" />;
  }
  if (control === "close") {
    return <path d="m4.5 4.5 7 7m0-7-7 7" />;
  }
  if (maximized) {
    return (
      <>
        <path d="M5.5 6.5h6v6h-6z" />
        <path d="M4.5 10.5h-1v-7h7v1" />
      </>
    );
  }
  return <path d="M4 4h8v8H4z" />;
}

function controlLabel(control: WindowControl, maximized: boolean): string {
  if (control === "close") return "关闭窗口";
  if (control === "minimize") return "最小化窗口";
  return maximized ? "还原窗口" : "最大化窗口";
}

export function WindowChrome({
  platform: platformOverride,
}: WindowChromeProps) {
  const platform = platformOverride ?? getDesktopPlatform();
  const appWindow = useMemo(
    () => (platform === "browser" ? null : getCurrentWindow()),
    [platform],
  );
  const [windowState, setWindowState] =
    useState<WindowState>(initialWindowState);
  const [linuxLayout, setLinuxLayout] = useState<WindowControlLayout>(() =>
    parseLinuxDecorationLayout(null),
  );

  const syncWindowState = useCallback(async () => {
    if (!appWindow) return;
    const [focused, fullscreen, maximized] = await Promise.all([
      appWindow.isFocused(),
      appWindow.isFullscreen(),
      appWindow.isMaximized(),
    ]);
    setWindowState({ focused, fullscreen, maximized });
  }, [appWindow]);

  useEffect(() => {
    if (!appWindow) return;

    let active = true;
    const unlisten: Array<() => void> = [];
    const updateWindowState = async () => {
      try {
        const [focused, fullscreen, maximized] = await Promise.all([
          appWindow.isFocused(),
          appWindow.isFullscreen(),
          appWindow.isMaximized(),
        ]);
        if (active) setWindowState({ focused, fullscreen, maximized });
      } catch (error) {
        console.error("Unable to read AstrLink window state", error);
      }
    };

    void updateWindowState();
    void Promise.all([
      appWindow.onResized(() => {
        void updateWindowState();
      }),
      appWindow.onFocusChanged(({ payload }) => {
        if (active) {
          setWindowState((current) => ({ ...current, focused: payload }));
        }
      }),
    ])
      .then((listeners) => {
        if (active) {
          unlisten.push(...listeners);
        } else {
          listeners.forEach((listener) => listener());
        }
      })
      .catch((error: unknown) => {
        console.error("Unable to observe AstrLink window state", error);
      });

    return () => {
      active = false;
      unlisten.forEach((listener) => listener());
    };
  }, [appWindow]);

  useEffect(() => {
    if (platform !== "linux") return;
    let active = true;
    void loadLinuxWindowControlLayout()
      .then((layout) => {
        if (active) setLinuxLayout(layout);
      })
      .catch((error: unknown) => {
        console.error("Unable to read Linux window decoration layout", error);
      });
    return () => {
      active = false;
    };
  }, [platform]);

  useEffect(() => {
    if (platform === "browser") return;
    const root = document.documentElement;
    root.dataset.windowFullscreen = String(windowState.fullscreen);
    return () => {
      delete root.dataset.windowFullscreen;
    };
  }, [platform, windowState.fullscreen]);

  const runWindowAction = useCallback(
    (action: () => Promise<unknown>) => {
      void action().catch((error: unknown) => {
        console.error("AstrLink window action failed", error);
      });
    },
    [],
  );

  const activateControl = useCallback(
    (control: WindowControl) => {
      if (!appWindow) return;
      if (control === "close") {
        runWindowAction(() => appWindow.close());
      } else if (control === "minimize") {
        runWindowAction(() => appWindow.minimize());
      } else {
        runWindowAction(async () => {
          await appWindow.toggleMaximize();
          await syncWindowState();
        });
      }
    },
    [appWindow, runWindowAction, syncWindowState],
  );

  const beginResize = useCallback(
    (
      direction: ResizeDirection,
      event: ReactMouseEvent<HTMLDivElement>,
    ) => {
      if (!appWindow || event.button !== 0) return;
      event.preventDefault();
      event.stopPropagation();
      runWindowAction(() => appWindow.startResizeDragging(direction));
    },
    [appWindow, runWindowAction],
  );

  if (platform === "browser") return null;

  const layout =
    platform === "linux"
      ? linuxLayout
      : platform === "windows"
        ? {
            start: [],
            end: ["minimize", "maximize", "close"] satisfies WindowControl[],
          }
        : { start: [], end: [] };

  const renderControls = (
    controls: WindowControl[],
    placement: "end" | "start",
  ) =>
    controls.length > 0 ? (
      <div
        className={`window-chrome__controls window-chrome__controls--${placement}`}
      >
        {controls.map((control) => {
          const label = controlLabel(control, windowState.maximized);
          return (
            <button
              aria-label={label}
              className={`window-chrome__button window-chrome__button--${control}`}
              key={control}
              onClick={() => activateControl(control)}
              title={label}
              type="button"
            >
              <svg
                aria-hidden="true"
                fill="none"
                viewBox="0 0 16 16"
              >
                <ControlIcon
                  control={control}
                  maximized={windowState.maximized}
                />
              </svg>
            </button>
          );
        })}
      </div>
    ) : null;

  return (
    <>
      <header
        aria-label="窗口控制栏"
        className={`window-chrome window-chrome--${platform}`}
        data-focused={windowState.focused}
        data-maximized={windowState.maximized}
        data-platform={platform}
      >
        {renderControls(layout.start, "start")}
        <div
          className="window-chrome__drag"
          data-tauri-drag-region
        />
        {renderControls(layout.end, "end")}
      </header>
      {platform !== "macos" &&
      !windowState.maximized &&
      !windowState.fullscreen
        ? resizeDirections.map((direction) => (
            <div
              aria-hidden="true"
              className={`window-resize-handle window-resize-handle--${direction.toLowerCase()}`}
              key={direction}
              onMouseDown={(event) => beginResize(direction, event)}
            />
          ))
        : null}
    </>
  );
}
