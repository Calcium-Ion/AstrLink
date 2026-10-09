import { describe, expect, it } from "vitest";

import {
  httpServicePreset,
  payAsYouGoPresetIDs,
  codingPlanPresetIDs,
  httpServicePresetIDs,
  httpServicePresetLabel,
  localConversionTargets,
  protocolEntryPath,
  serviceSiteForBaseURL,
  supportsLocalConversion,
} from "./service-presets";

describe("HTTP service product presets", () => {
  it("configures new-api as a bearer-authenticated passthrough multi-protocol gateway", () => {
    const preset = httpServicePreset("newapi");

    expect(preset.kind).toBe("newapi");
    expect(preset.authScheme).toBe("bearer");
    expect(preset.baseURL).toBe("");
    expect(preset.capabilities).toHaveLength(8);
    expect(new Set(preset.capabilities.map(({ mode }) => mode))).toEqual(
      new Set(["native"]),
    );
    expect(preset.capabilities.map(({ protocol }) => protocol)).toEqual([
      "openai.responses",
      "openai.responses.compact",
      "anthropic.messages",
      "google.generate_content",
      "openai.chat",
      "openai.completions",
      "openai.models",
      "google.models",
    ]);
  });

  it("points magpie at the local gateway with its documented native surface", () => {
    const preset = httpServicePreset("magpie");

    expect(preset).toMatchObject({
      kind: "magpie",
      baseURL: "http://127.0.0.1:3425",
      authScheme: "bearer",
      advancedOnStart: false,
      sites: [{ id: "local", baseURL: "http://127.0.0.1:3425" }],
    });
    expect(preset.capabilities).toEqual([
      { protocol: "openai.responses", mode: "native", streaming: true },
      { protocol: "anthropic.messages", mode: "native", streaming: true },
      { protocol: "google.generate_content", mode: "native", streaming: true },
      { protocol: "openai.chat", mode: "native", streaming: true },
      { protocol: "openai.models", mode: "native", streaming: false },
      { protocol: "google.models", mode: "native", streaming: false },
    ]);
    // The OpenAI SDK base keeps its /v1; another host or port is a custom address.
    expect(serviceSiteForBaseURL(preset, "http://127.0.0.1:3425/v1")).toBe(
      "local",
    );
    expect(serviceSiteForBaseURL(preset, "http://192.168.1.8:3425")).toBeNull();
  });

  it("does not expose API-key services as Codex, Claude, or Gemini subscriptions", () => {
    expect(httpServicePresetIDs).toEqual([
      "opencode_go",
      "opencode_zen",
      "kimi_coding",
      "glm_coding",
      "minimax_coding",
      "newapi",
      "magpie",
      "openai_compatible",
      "openai",
      "anthropic",
      "gemini",
      "deepseek",
      "qwen",
      "moonshot",
      "glm",
      "minimax",
      "doubao",
      "xai",
      "custom",
    ]);
    expect(
      httpServicePresetIDs.map(httpServicePresetLabel).join(" "),
    ).not.toContain("订阅");
  });

  it("keeps OpenAI-compatible API services intentionally narrow and native", () => {
    expect(httpServicePreset("openai_compatible")).toMatchObject({
      kind: "openai_compatible",
      baseURL: "",
      authScheme: "bearer",
      capabilities: [
        { protocol: "openai.chat", mode: "native", streaming: true },
        {
          protocol: "openai.completions",
          mode: "native",
          streaming: true,
        },
        { protocol: "openai.models", mode: "native", streaming: false },
      ],
    });
  });

  it("uses exact provider-specific auth and native capabilities for official services", () => {
    expect(httpServicePreset("openai")).toMatchObject({
      baseURL: "https://api.openai.com/v1",
      authScheme: "bearer",
      capabilities: [
        { protocol: "openai.responses", mode: "native", streaming: true },
        {
          protocol: "openai.responses.compact",
          mode: "native",
          streaming: false,
        },
        { protocol: "openai.chat", mode: "native", streaming: true },
        {
          protocol: "openai.completions",
          mode: "native",
          streaming: true,
        },
        { protocol: "openai.models", mode: "native", streaming: false },
      ],
    });
    expect(httpServicePreset("anthropic")).toMatchObject({
      baseURL: "https://api.anthropic.com",
      authScheme: "anthropic_api_key",
      capabilities: [
        {
          protocol: "anthropic.messages",
          mode: "native",
          streaming: true,
        },
        { protocol: "openai.models", mode: "native", streaming: false },
      ],
    });
    expect(httpServicePreset("gemini")).toMatchObject({
      baseURL: "https://generativelanguage.googleapis.com",
      authScheme: "google_api_key",
      capabilities: [
        {
          protocol: "google.generate_content",
          mode: "native",
          streaming: true,
        },
        { protocol: "google.models", mode: "native", streaming: false },
        {
          protocol: "openai.chat",
          mode: "native",
          streaming: true,
          convert_to: "google.generate_content",
        },
      ],
    });
  });

  it("keeps usage-based providers separate from coding plans and advertises only supported protocols", () => {
    expect(payAsYouGoPresetIDs).toContain("opencode_zen");
    expect(codingPlanPresetIDs).toContain("opencode_go");
    expect(
      payAsYouGoPresetIDs.some((kind) => codingPlanPresetIDs.includes(kind)),
    ).toBe(false);
    for (const [kind, baseURL, discovery] of [
      ["deepseek", "https://api.deepseek.com/v1", true],
      ["qwen", "https://dashscope.aliyuncs.com/compatible-mode/v1", false],
      ["moonshot", "https://api.moonshot.cn/v1", true],
      ["glm", "https://open.bigmodel.cn/api/paas/v4", false],
      ["minimax", "https://api.minimax.cn/v1", true],
      ["doubao", "https://ark.cn-beijing.volces.com/api/v3", false],
      ["xai", "https://api.x.ai/v1", true],
    ] as const) {
      const preset = httpServicePreset(kind);
      expect(payAsYouGoPresetIDs).toContain(kind);
      expect(preset).toMatchObject({
        kind,
        baseURL,
        authScheme: "bearer",
        advancedOnStart: false,
      });
      expect(preset.capabilities).toContainEqual({
        protocol: "openai.chat",
        mode: "native",
        streaming: true,
      });
      expect(
        preset.capabilities.some(
          ({ protocol }) => protocol === "openai.models",
        ),
      ).toBe(discovery);
      expect(preset.capabilities).toContainEqual({
        protocol: "anthropic.messages",
        mode: "native",
        streaming: true,
      });
      expect(preset.capabilities).toContainEqual({
        protocol: "openai.responses",
        mode: "native",
        streaming: true,
      });
      expect(
        preset.capabilities.some(
          ({ protocol }) => protocol === "openai.responses.compact",
        ),
      ).toBe(kind === "xai");
      expect(
        preset.capabilities.some(
          ({ protocol }) => protocol === "openai.completions",
        ),
      ).toBe(kind === "xai");
      expect(
        preset.capabilities.every(
          ({ mode, convert_to }) =>
            mode === "native" && convert_to === undefined,
        ),
      ).toBe(true);
    }
  });

  it("gives coding plans every native surface behind one bearer key", () => {
    for (const [kind, baseURL, protocols] of [
      [
        "kimi_coding",
        "https://api.kimi.ai/coding",
        [
          "openai.responses",
          "anthropic.messages",
          "openai.chat",
          "openai.models",
        ],
      ],
      [
        "glm_coding",
        "https://open.bigmodel.cn/api/coding/paas/v4",
        ["openai.responses", "anthropic.messages", "openai.chat"],
      ],
      [
        "minimax_coding",
        "https://api.minimax.cn/v1",
        [
          "openai.responses",
          "anthropic.messages",
          "openai.chat",
          "openai.models",
        ],
      ],
    ] as const) {
      const preset = httpServicePreset(kind);
      expect(codingPlanPresetIDs).toContain(kind);
      expect(preset).toMatchObject({ baseURL, authScheme: "bearer" });
      expect(preset.capabilities.map(({ protocol }) => protocol)).toEqual(
        protocols,
      );
      expect(
        preset.capabilities.every(
          ({ mode, convert_to }) =>
            mode === "native" && convert_to === undefined,
        ),
      ).toBe(true);
    }
  });

  it("offers vendor sites so users pick a region instead of typing an address", () => {
    for (const [kind, global] of [
      ["glm_coding", "https://api.z.ai/api/coding/paas/v4"],
      ["minimax_coding", "https://api.minimax.io/v1"],
      ["qwen", "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"],
      ["moonshot", "https://api.moonshot.ai/v1"],
      ["glm", "https://api.z.ai/api/paas/v4"],
      ["minimax", "https://api.minimax.io/v1"],
    ] as const) {
      const preset = httpServicePreset(kind);
      expect(preset.sites.map(({ id }) => id)).toEqual(["cn", "global"]);
      expect(preset.sites[0]?.baseURL).toBe(preset.baseURL);
      expect(preset.sites[1]?.baseURL).toBe(global);
    }
    for (const kind of httpServicePresetIDs) {
      const preset = httpServicePreset(kind);
      expect(preset.sites.length > 0).toBe(preset.baseURL !== "");
      if (preset.sites.length > 0) {
        expect(preset.sites[0]?.baseURL).toBe(preset.baseURL);
      }
    }

    const minimax = httpServicePreset("minimax_coding");
    // Saved vendor paths and legacy hosts still belong to their site.
    expect(
      serviceSiteForBaseURL(minimax, "https://api.minimax.cn/anthropic"),
    ).toBe("cn");
    expect(
      serviceSiteForBaseURL(minimax, "https://api.minimaxi.com/anthropic"),
    ).toBe("cn");
    expect(serviceSiteForBaseURL(minimax, "https://api.minimax.io/v1")).toBe(
      "global",
    );
    expect(
      serviceSiteForBaseURL(minimax, "https://proxy.example/minimax"),
    ).toBeNull();
    expect(serviceSiteForBaseURL(minimax, "not a url")).toBeNull();
    expect(
      serviceSiteForBaseURL(
        httpServicePreset("kimi_coding"),
        "https://api.kimi.com/coding",
      ),
    ).toBe("official");
    expect(httpServicePreset("newapi").sites).toEqual([]);
  });

  it("lists local conversion targets and enables only advertised edges", () => {
    expect(supportsLocalConversion("openai.chat")).toBe(true);
    expect(supportsLocalConversion("openai.models")).toBe(false);
    // Neither variant has an advertised edge, so offering them would only ever
    // render permanently disabled options.
    expect(supportsLocalConversion("openai.responses.compact")).toBe(false);
    expect(supportsLocalConversion("openai.completions")).toBe(false);
    expect(
      localConversionTargets("anthropic.messages", {
        available: true,
        edges: [
          {
            from: "anthropic.messages",
            to: "openai.chat",
            quality: "fair",
            streaming: true,
          },
        ],
      }).filter((target) => target.enabled),
    ).toEqual([
      { id: "openai.chat", enabled: true, quality: "fair", streaming: true },
    ]);
    expect(
      localConversionTargets("openai.chat", {
        available: false,
        edges: [],
      }).every((target) => !target.enabled && target.quality === null),
    ).toBe(true);
  });

  it("maps protocol IDs to the inference-plane entry path", () => {
    expect(protocolEntryPath("anthropic.messages")).toBe("/v1/messages");
    expect(protocolEntryPath("openai.responses")).toBe("/v1/responses");
    expect(protocolEntryPath("openai.chat")).toBe("/v1/chat/completions");
    expect(protocolEntryPath("google.generate_content")).toBe(
      "/v1beta/models/:model:generateContent",
    );
    expect(
      protocolEntryPath("google.generate_content", { streaming: true }),
    ).toBe("/v1beta/models/:model:streamGenerateContent");
    expect(protocolEntryPath("vendor.custom")).toBe("vendor.custom");
  });

  it("opens advanced settings and requires an explicit capability for custom services", () => {
    expect(httpServicePreset("custom")).toMatchObject({
      authScheme: "bearer",
      capabilities: [],
      advancedOnStart: true,
    });
  });
});

describe("default protocol conversions", () => {
  // Today's core edge table, deliberately confined to the test fixture.
  const protocols = [
    "openai.responses",
    "openai.chat",
    "anthropic.messages",
    "google.generate_content",
  ];
  const engine = {
    available: true,
    edges: protocols.flatMap((from, i) =>
      protocols
        .filter((to) => to !== from)
        .map((to) => ({
          from,
          to,
          streaming: true,
          quality:
            i < 2 && protocols.indexOf(to) < 2
              ? ("good" as const)
              : i >= 2 && protocols.indexOf(to) >= 2
                ? ("discouraged" as const)
                : ("fair" as const),
        })),
    ),
  };
  it("snapshots every preset with the advertised engine", () => {
    expect(
      Object.fromEntries(
        httpServicePresetIDs.map((id) => [
          id,
          httpServicePreset(id, [], engine).capabilities.map(
            (row) =>
              `${row.protocol}:${row.mode}:${row.streaming}${row.convert_to ? ` → ${row.convert_to}` : ""}`,
          ),
        ]),
      ),
    ).toMatchInlineSnapshot(`
      {
        "anthropic": [
          "anthropic.messages:native:true",
          "openai.models:native:false",
          "openai.responses:native:true → anthropic.messages",
          "openai.chat:native:true → anthropic.messages",
        ],
        "custom": [],
        "deepseek": [
          "openai.responses:native:true",
          "anthropic.messages:native:true",
          "openai.chat:native:true",
          "openai.models:native:false",
          "google.generate_content:native:true → openai.responses",
        ],
        "doubao": [
          "openai.responses:native:true",
          "anthropic.messages:native:true",
          "openai.chat:native:true",
          "google.generate_content:native:true → openai.responses",
        ],
        "gemini": [
          "google.generate_content:native:true",
          "google.models:native:false",
          "openai.chat:native:true → google.generate_content",
          "openai.responses:native:true → google.generate_content",
        ],
        "glm": [
          "openai.responses:native:true",
          "anthropic.messages:native:true",
          "openai.chat:native:true",
          "google.generate_content:native:true → openai.responses",
        ],
        "glm_coding": [
          "openai.responses:native:true",
          "anthropic.messages:native:true",
          "openai.chat:native:true",
          "google.generate_content:native:true → openai.responses",
        ],
        "kimi_coding": [
          "openai.responses:native:true",
          "anthropic.messages:native:true",
          "openai.chat:native:true",
          "openai.models:native:false",
          "google.generate_content:native:true → openai.responses",
        ],
        "magpie": [
          "openai.responses:native:true",
          "anthropic.messages:native:true",
          "google.generate_content:native:true",
          "openai.chat:native:true",
          "openai.models:native:false",
          "google.models:native:false",
        ],
        "minimax": [
          "openai.responses:native:true",
          "anthropic.messages:native:true",
          "openai.chat:native:true",
          "openai.models:native:false",
          "google.generate_content:native:true → openai.responses",
        ],
        "minimax_coding": [
          "openai.responses:native:true",
          "anthropic.messages:native:true",
          "openai.chat:native:true",
          "openai.models:native:false",
          "google.generate_content:native:true → openai.responses",
        ],
        "moonshot": [
          "openai.responses:native:true",
          "anthropic.messages:native:true",
          "openai.chat:native:true",
          "openai.models:native:false",
          "google.generate_content:native:true → openai.responses",
        ],
        "newapi": [
          "openai.responses:native:true",
          "openai.responses.compact:native:false",
          "anthropic.messages:native:true",
          "google.generate_content:native:true",
          "openai.chat:native:true",
          "openai.completions:native:true",
          "openai.models:native:false",
          "google.models:native:false",
        ],
        "openai": [
          "openai.responses:native:true",
          "openai.responses.compact:native:false",
          "openai.chat:native:true",
          "openai.completions:native:true",
          "openai.models:native:false",
          "anthropic.messages:native:true → openai.responses",
          "google.generate_content:native:true → openai.responses",
        ],
        "openai_compatible": [
          "openai.chat:native:true",
          "openai.completions:native:true",
          "openai.models:native:false",
          "openai.responses:native:true → openai.chat",
          "anthropic.messages:native:true → openai.chat",
          "google.generate_content:native:true → openai.chat",
        ],
        "opencode_go": [
          "openai.responses:native:true",
          "openai.chat:native:true",
          "anthropic.messages:native:true",
          "openai.models:native:false",
        ],
        "opencode_zen": [
          "openai.responses:native:true",
          "openai.chat:native:true",
          "anthropic.messages:native:true",
          "openai.models:native:false",
        ],
        "qwen": [
          "openai.responses:native:true",
          "anthropic.messages:native:true",
          "openai.chat:native:true",
          "google.generate_content:native:true → openai.responses",
        ],
        "xai": [
          "openai.responses:native:true",
          "openai.responses.compact:native:false",
          "anthropic.messages:native:true",
          "openai.chat:native:true",
          "openai.completions:native:true",
          "openai.models:native:false",
          "google.generate_content:native:true → openai.responses",
        ],
      }
    `);
  });
  it.each(httpServicePresetIDs)(
    "only appends supported conversion rows to %s",
    (id) => {
      const original = httpServicePreset(id).capabilities;
      const current = httpServicePreset(id, [], engine).capabilities;
      expect(current.slice(0, original.length)).toEqual(original);
      expect(httpServicePreset(id, [], null).capabilities).toEqual(original);
      expect(
        httpServicePreset(id, [], { ...engine, available: false }).capabilities,
      ).toEqual(original);
      for (const row of current.slice(original.length)) {
        expect(engine.edges).toContainEqual({
          from: row.protocol,
          to: row.convert_to,
          quality: expect.stringMatching(/^(good|fair)$/),
          streaming: true,
        });
        expect(original).toContainEqual({
          protocol: row.convert_to,
          mode: "native",
          streaming: true,
        });
      }
      if (
        ["opencode_go", "opencode_zen", "custom", "newapi", "magpie"].includes(
          id,
        )
      )
        expect(current).toEqual(original);
    },
  );
  it("responds to revised Messages/Gemini ratings without changing presets", () => {
    const revised = {
      ...engine,
      edges: engine.edges.map((edge) => ({
        ...edge,
        quality:
          edge.quality === "discouraged" ? ("fair" as const) : edge.quality,
      })),
    };
    for (const [id, from, to] of [
      ["anthropic", "google.generate_content", "anthropic.messages"],
      ["gemini", "anthropic.messages", "google.generate_content"],
    ] as const) {
      expect(
        httpServicePreset(id, [], engine).capabilities.some(
          (row) => row.protocol === from,
        ),
      ).toBe(false);
      expect(httpServicePreset(id, [], revised).capabilities).toContainEqual({
        protocol: from,
        mode: "native",
        streaming: true,
        convert_to: to,
      });
    }
  });
});
