import type { RsbuildPlugin } from "@rsbuild/core";

export const DEV_BUILD_ID_PATH = "/__astrlink_build";

export type BuildStats = {
  hasErrors: () => boolean;
};

export type BuildIdRequest = {
  url?: string;
};

export type BuildIdResponse = {
  statusCode: number;
  setHeader(name: string, value: string): void;
  end(body?: string): void;
};

export class BuildGeneration {
  #value = 0;

  get value(): number {
    return this.#value;
  }

  noteCompile({
    isFirstCompile,
    stats,
  }: {
    isFirstCompile: boolean;
    stats: BuildStats;
  }): boolean {
    // Failed compiles report here too. Bumping the generation on a failure
    // would reload the webview onto a broken bundle.
    if (isFirstCompile || stats.hasErrors()) {
      return false;
    }
    this.#value += 1;
    return true;
  }
}

export function createBuildGenerationMiddleware(generation: BuildGeneration) {
  return (req: BuildIdRequest, res: BuildIdResponse, next: () => void): void => {
    if (req.url?.split("?")[0] !== DEV_BUILD_ID_PATH) {
      next();
      return;
    }
    res.statusCode = 200;
    res.setHeader("Content-Type", "text/plain; charset=utf-8");
    res.setHeader("Cache-Control", "no-store");
    res.end(String(generation.value));
  };
}

export function createTauriDevReloadPlugin(
  generation: BuildGeneration,
): RsbuildPlugin {
  return {
    name: "astrlink-tauri-dev-reload",
    setup(api) {
      api.onAfterDevCompile(({ isFirstCompile, stats }) => {
        generation.noteCompile({ isFirstCompile, stats });
      });
    },
  };
}
