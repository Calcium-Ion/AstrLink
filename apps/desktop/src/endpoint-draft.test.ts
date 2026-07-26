import { describe, expect, it } from "vitest";

import {
  activeDraft,
  bindDraftSecret,
  buildCreateInput,
  buildPatch,
  canKeepStoredCredential,
  createDraftSession,
  editDraftSession,
  hasCurrentDraftSecret,
  switchDraftProfile,
  updateActiveDraft,
  updateDraftIdentity,
  validateDraft,
  validateDraftIssue,
} from "./endpoint-draft";
import type { EndpointRecord } from "./endpoint-model";

const record = (): EndpointRecord => ({
  endpoint: {
    id: "endpoint_01",
    name: "My gateway",
    kind: "custom",
    base_url: "https://gateway.example/v1",
    auth: { scheme: "custom_header", header_name: "X-Subscription-Key" },
    credential_ref: "local://endpoint/endpoint_01",
    enabled: true,
    capabilities: [
      {
        protocol: "vendor.unknown",
        mode: "native",
        streaming: true,
        models: ["model-a", "model-b"],
      },
      {
        protocol: "vendor.unknown",
        mode: "delegated",
        streaming: false,
      },
    ],
  },
  etag: `"sha256:${"a".repeat(64)}"`,
});

describe("endpoint draft session", () => {
  it("keeps complete per-profile drafts while isolating secrets", () => {
    let session = createDraftSession();
    session = updateActiveDraft(session, (draft) => {
      const identified = updateDraftIdentity(draft, (current) => ({
        ...current,
        name: "私人 NewAPI",
        baseURL: "https://newapi.example",
        enabled: false,
      }));
      return bindDraftSecret(identified, "newapi-secret");
    });
    session = switchDraftProfile(session, "subscription_openai");

    expect(activeDraft(session)).toMatchObject({
      name: "Codex 订阅",
      baseURL: "",
      secret: "",
      enabled: true,
    });

    session = updateActiveDraft(session, (draft) => {
      const identified = updateDraftIdentity(draft, (current) => ({
        ...current,
        name: "团队订阅",
        baseURL: "https://subscription.example",
        enabled: true,
      }));
      return bindDraftSecret(identified, "subscription-secret");
    });
    session = switchDraftProfile(session, "newapi");

    expect(activeDraft(session)).toMatchObject({
      name: "私人 NewAPI",
      baseURL: "https://newapi.example",
      secret: "newapi-secret",
      enabled: false,
    });

    session = switchDraftProfile(session, "subscription_openai");
    expect(activeDraft(session)).toMatchObject({
      name: "团队订阅",
      baseURL: "https://subscription.example",
      secret: "subscription-secret",
      enabled: true,
    });
  });

  it("round-trips every stored capability without flattening unknown or dual-mode rows", () => {
    const session = editDraftSession(record());

    expect(activeDraft(session).capabilities).toEqual(
      record().endpoint.capabilities,
    );
    expect(buildPatch(session)).toEqual({});
  });

  it("emits only fields that actually changed", () => {
    const session = updateActiveDraft(editDraftSession(record()), (draft) => ({
      ...draft,
      name: "Renamed",
    }));

    expect(buildPatch(session)).toEqual({ name: "Renamed" });
  });

  it("keeps a stored key for path-only URL changes but requires one after identity changes", () => {
    let session = editDraftSession(record());
    session = updateActiveDraft(session, (draft) =>
      updateDraftIdentity(draft, (current) => ({
        ...current,
        baseURL: "https://gateway.example/v1/tenant",
      })),
    );
    expect(canKeepStoredCredential(session)).toBe(true);
    expect(validateDraft(session)).toBeNull();

    session = updateActiveDraft(session, (draft) =>
      updateDraftIdentity(draft, (current) => ({
        ...current,
        baseURL: "https://other.example/v1",
      })),
    );
    expect(canKeepStoredCredential(session)).toBe(false);
    expect(validateDraft(session)).toBe(
      "服务地址或认证方式已改变，请重新填写 API Key。",
    );
  });

  it("binds a typed key to its service identity and never submits a stale key", () => {
    let session = editDraftSession(record());
    session = updateActiveDraft(session, (draft) =>
      bindDraftSecret(draft, "replacement-secret"),
    );
    expect(hasCurrentDraftSecret(activeDraft(session))).toBe(true);
    expect(buildPatch(session)).toMatchObject({
      credential: { secret: "replacement-secret" },
    });

    session = updateActiveDraft(session, (draft) => ({
      ...draft,
      baseURL: "https://other.example/v1",
    }));
    expect(hasCurrentDraftSecret(activeDraft(session))).toBe(false);
    expect(buildPatch(session)).not.toHaveProperty("credential");
    expect(validateDraft(session)).toBe(
      "服务地址或认证方式已改变，请重新填写 API Key。",
    );
  });

  it("clears a typed key immediately when an identity field changes", () => {
    let session = editDraftSession(record());
    session = updateActiveDraft(session, (draft) =>
      bindDraftSecret(draft, "replacement-secret"),
    );
    session = updateActiveDraft(session, (draft) =>
      updateDraftIdentity(draft, (current) => ({
        ...current,
        headerName: "X-New-Key",
      })),
    );

    expect(activeDraft(session)).toMatchObject({
      headerName: "X-New-Key",
      secret: "",
      secretIdentity: null,
    });
  });

  it("never carries a stored key into a selected preset even when the origin matches", () => {
    const original = record();
    original.endpoint.auth = { scheme: "bearer" };
    let session = editDraftSession(original);
    session = switchDraftProfile(session, "subscription_openai");
    session = updateActiveDraft(session, (draft) =>
      updateDraftIdentity(draft, (current) => ({
        ...current,
        baseURL: original.endpoint.base_url,
      })),
    );

    expect(canKeepStoredCredential(session)).toBe(false);
    expect(validateDraft(session)).toBe(
      "服务地址或认证方式已改变，请重新填写 API Key。",
    );
  });

  it("treats whitespace-only secrets as absent in validation and payloads", () => {
    let editingSession = editDraftSession(record());
    editingSession = updateActiveDraft(editingSession, (draft) =>
      bindDraftSecret(draft, "   "),
    );
    expect(validateDraft(editingSession)).toBeNull();
    expect(buildPatch(editingSession)).toEqual({});

    let createSession = createDraftSession();
    createSession = updateActiveDraft(createSession, (draft) => {
      const identified = updateDraftIdentity(draft, (current) => ({
        ...current,
        baseURL: "https://gateway.example",
      }));
      return bindDraftSecret(identified, "\t ");
    });
    expect(validateDraft(createSession)).toBe("请填写 API Key。");
    expect(buildCreateInput(createSession)).not.toHaveProperty("credential");
  });

  it("drops empty model lines from serialized capabilities", () => {
    let session = createDraftSession();
    session = updateActiveDraft(session, (draft) => {
      const identified = updateDraftIdentity(draft, (current) => ({
        ...current,
        baseURL: "https://gateway.example",
        capabilities: [
          {
            protocol: "openai.responses",
            mode: "delegated",
            streaming: true,
            models: [
              "  model-a\r\nrevision\rtrailer\n",
              "",
              "\tmodel-b\t",
            ],
          },
        ],
      }));
      return bindDraftSecret(identified, "secret");
    });

    expect(validateDraft(session)).toBeNull();
    expect(buildCreateInput(session).capabilities).toEqual([
      {
        protocol: "openai.responses",
        mode: "delegated",
        streaming: true,
        models: ["  model-a\r\nrevision\rtrailer\n", "\tmodel-b\t"],
      },
    ]);
  });

  it("only clears a stored key through an explicit no-auth draft", () => {
    let session = editDraftSession(record());
    session = updateActiveDraft(session, (draft) =>
      updateDraftIdentity(draft, (current) => ({
        ...current,
        authScheme: "none",
        headerName: "",
        removeCredential: true,
      })),
    );

    expect(buildPatch(session)).toMatchObject({
      auth: { scheme: "none" },
      credential: null,
    });
  });

  it("does not infer credential removal from a pre-existing no-auth record", () => {
    const original = record();
    original.endpoint.auth = { scheme: "none" };
    const session = editDraftSession(original);

    expect(buildPatch(session)).toEqual({});
  });

  it("rejects empty, duplicate, and malformed protocol capabilities", () => {
    let session = createDraftSession();
    session = updateActiveDraft(session, (draft) => ({
      ...draft,
      baseURL: "https://gateway.example",
      capabilities: [],
    }));
    expect(validateDraft(session)).toContain("至少添加一项");

    session = updateActiveDraft(session, (draft) => ({
      ...draft,
      capabilities: [
        {
          protocol: "openai.responses",
          mode: "delegated",
          streaming: true,
        },
        {
          protocol: "openai.responses",
          mode: "delegated",
          streaming: false,
        },
      ],
    }));
    expect(validateDraft(session)).toContain("模式重复");

    session = updateActiveDraft(session, (draft) => ({
      ...draft,
      capabilities: [
        {
          protocol: "x",
          mode: "delegated",
          streaming: false,
        },
      ],
    }));
    expect(validateDraft(session)).toContain("格式无效");
  });

  it("identifies hidden advanced controls for application validation", () => {
    let session = createDraftSession();
    session = updateActiveDraft(session, (draft) => ({
      ...draft,
      baseURL: "https://gateway.example",
      capabilities: [],
    }));
    expect(validateDraftIssue(session)).toMatchObject({
      targetID: "endpoint-add-capability",
      advanced: true,
    });

    session = updateActiveDraft(session, (draft) => ({
      ...draft,
      capabilities: [
        {
          protocol: "",
          mode: "delegated",
          streaming: true,
        },
      ],
    }));
    expect(validateDraftIssue(session)).toMatchObject({
      targetID: "endpoint-capability-0-protocol",
      advanced: true,
    });

    session = updateActiveDraft(session, (draft) => ({
      ...draft,
      capabilities: [
        {
          protocol: "openai.responses",
          mode: "delegated",
          streaming: true,
          models: ["duplicate", "duplicate"],
        },
      ],
    }));
    expect(validateDraftIssue(session)).toMatchObject({
      targetID: "endpoint-capability-0-model-1",
      advanced: true,
    });
  });
});
