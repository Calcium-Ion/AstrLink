import { describe, expect, it } from "vitest";

import {
  isResourceHeavyVariant,
  parsePrivacyDryRunResult,
  parsePrivacyModelCatalog,
  parsePrivacyModelInstallation,
  parsePrivacyModelInstallationList,
  parsePrivacyModelProbe,
  parsePrivacyPolicyPage,
  parsePrivacyPolicyRecord,
  validateLocalProbeInput,
  validatePrivacyDryRunInput,
  validatePrivacyModelInstallInput,
} from "./privacy-policy-model";

const revision = "53d55aa8dbb28efaa4e9cf6b4b6015d00e43c088";
const installationID = "model_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
const policy = {
  id: "policy_privacy_default",
  name: "隐私保护",
  enabled: false,
  priority: 0,
  detector: "regex",
  local_model_id: null,
  min_confidence: 0.6,
  request_action: "redact",
  response_action: "allow",
  response_restore: true,
  match: {},
} as const;
const variant = {
  id: "cpu_int8",
  name: "CPU INT8",
  quantization: "int8",
  bytes_total: 180_000_000,
  estimated_ram_bytes: 420_000_000,
  recommended: true,
  supported: true,
  unsupported_reason: null,
} as const;
const catalogModel = {
  id: "catalog_sheltron_ettin_32m",
  name: "Ettin Privacy 32M",
  summary: "轻量英文隐私实体检测模型。",
  source: "community",
  repo_id: "sheltron-ai/privacy-filter-ettin-32m",
  revision,
  license: "apache-2.0",
  languages: ["en"],
  adapter: "hf_token_classification",
  variants: [variant],
} as const;
const readyInstallation = {
  id: installationID,
  source: "catalog",
  catalog_id: catalogModel.id,
  catalog_source: catalogModel.source,
  name: catalogModel.name,
  license: catalogModel.license,
  languages: catalogModel.languages,
  repo_id: catalogModel.repo_id,
  revision,
  variant_id: variant.id,
  variant_name: variant.name,
  quantization: variant.quantization,
  adapter: catalogModel.adapter,
  status: "ready",
  bytes_downloaded: variant.bytes_total,
  bytes_total: variant.bytes_total,
  estimated_ram_bytes: variant.estimated_ram_bytes,
  error: null,
  label_mapping: { EMAIL: "email", OTHER: null },
  installed_at: "2026-07-24T10:30:00Z",
} as const;

describe("privacy-policy IPC contract", () => {
  it("strictly parses the singleton policy with a nullable local model selection", () => {
    expect(
      parsePrivacyPolicyPage({ items: [policy], next_cursor: null }),
    ).toEqual({ items: [policy], next_cursor: null });
    const localPolicy = {
      ...policy,
      detector: "local_model",
      local_model_id: readyInstallation.id,
    } as const;
    expect(
      parsePrivacyPolicyRecord({
        policy: localPolicy,
        etag: `"sha256:${"a".repeat(64)}"`,
      }),
    ).toEqual({
      policy: localPolicy,
      etag: `"sha256:${"a".repeat(64)}"`,
    });
  });

  it("strictly parses dry-run results and validates dry-run inputs", () => {
    const dryRun = {
      decision: "redact",
      findings_summary: "email=1",
      findings: [
        {
          kind: "email",
          path: "/messages/0/content",
          start: 6,
          end: 23,
          confidence: 0.93,
        },
      ],
      suppressed_findings: [],
      redactions: [
        {
          placeholder: "<PRIVATE_EMAIL_7f3a91c04d28be56>",
          kind: "email",
          value: "alice@example.com",
        },
      ],
      redacted_body: '{"messages":[{"content":"x","role":"user"}]}',
      inspected_body: '{"messages":[{"content":"y","role":"user"}]}',
    };
    expect(parsePrivacyDryRunResult(dryRun)).toEqual(dryRun);
    expect(
      validatePrivacyDryRunInput({
        protocol: "openai.chat",
        sample_text: "hello",
        policy: {
          enabled: true,
          min_confidence: 0.75,
          request_action: "warn",
        },
      }),
    ).toEqual({
      protocol: "openai.chat",
      sample_text: "hello",
      policy: {
        enabled: true,
        min_confidence: 0.75,
        request_action: "warn",
      },
    });
    expect(() =>
      validatePrivacyDryRunInput({
        protocol: "openai.models" as "openai.chat",
        sample_text: "x",
      }),
    ).toThrow("unsupported dry-run protocol");
    expect(() =>
      parsePrivacyDryRunResult({
        ...dryRun,
        findings: [{ ...dryRun.findings[0], kind: "ssn" }],
      }),
    ).toThrow("unknown privacy kind");
    expect(() =>
      parsePrivacyDryRunResult({
        ...dryRun,
        findings: [{ ...dryRun.findings[0], confidence: 1.01 }],
      }),
    ).toThrow("between 0 and 1");
    expect(() =>
      validatePrivacyDryRunInput({
        protocol: "openai.chat",
        sample_text: "hello",
        policy: { min_confidence: -0.01 },
      }),
    ).toThrow("between 0 and 1");
  });

  it("rejects policy drift and unknown detectors", () => {
    expect(() =>
      parsePrivacyPolicyPage({
        items: [{ ...policy, secret: "must-not-cross-ipc" }],
        next_cursor: null,
      }),
    ).toThrow("unexpected field");
    expect(() =>
      parsePrivacyPolicyPage({
        items: [{ ...policy, detector: "remote_model" }],
        next_cursor: null,
      }),
    ).toThrow("unknown detector");
    expect(() =>
      parsePrivacyPolicyPage({
        items: [{ ...policy, local_model_id: "../model" }],
        next_cursor: null,
      }),
    ).toThrow("local_model_id");
    expect(() =>
      parsePrivacyPolicyPage({
        items: [{ ...policy, min_confidence: 1.01 }],
        next_cursor: null,
      }),
    ).toThrow("min_confidence");
  });

  it("strictly parses catalog variants and custom probe label suggestions", () => {
    expect(parsePrivacyModelCatalog({ items: [catalogModel] })).toEqual({
      items: [catalogModel],
    });
    const probe = {
      repo_id: catalogModel.repo_id,
      requested_revision: "main",
      revision,
      name: catalogModel.name,
      license: catalogModel.license,
      languages: catalogModel.languages,
      adapter: catalogModel.adapter,
      variants: catalogModel.variants,
      labels: [
        { label: "EMAIL", suggested_kind: "email" },
        { label: "MISC", suggested_kind: null },
      ],
      requires_label_mapping: true,
    } as const;
    expect(parsePrivacyModelProbe(probe)).toEqual(probe);
    expect(parsePrivacyModelProbe({ ...probe, license: null })).toEqual({
      ...probe,
      license: null,
    });
    expect(() =>
      parsePrivacyModelCatalog({
        items: [
          {
            ...catalogModel,
            adapter: "remote_code",
          },
        ],
      }),
    ).toThrow("unknown model adapter");
    expect(() =>
      parsePrivacyModelCatalog({
        items: [
          {
            ...catalogModel,
            variants: [
              {
                ...variant,
                supported: false,
                unsupported_reason: null,
              },
            ],
          },
        ],
      }),
    ).toThrow("inconsistent");
    expect(() =>
      parsePrivacyModelCatalog({
        items: [
          {
            ...catalogModel,
            variants: [{ ...variant, name: " CPU INT8" }],
          },
        ],
      }),
    ).toThrow("trimmed");
    expect(() =>
      parsePrivacyModelCatalog({
        items: [
          {
            ...catalogModel,
            variants: [{ ...variant, quantization: "int8\u0000" }],
          },
        ],
      }),
    ).toThrow("control characters");
    expect(() =>
      parsePrivacyModelProbe({
        ...probe,
        labels: [],
        requires_label_mapping: false,
      }),
    ).toThrow("1 to 256 labels");
    expect(() =>
      parsePrivacyModelProbe({
        ...probe,
        requires_label_mapping: false,
      }),
    ).toThrow("inconsistent with label suggestions");
    expect(() =>
      parsePrivacyModelProbe({
        ...probe,
        license: "",
      }),
    ).toThrow("1 to 64 characters");
  });

  it("strictly parses installation progress, errors, and label mappings", () => {
    expect(parsePrivacyModelInstallation(readyInstallation)).toEqual(
      readyInstallation,
    );
    const customInstallation = {
      ...readyInstallation,
      source: "custom",
      catalog_id: null,
      catalog_source: null,
      license: null,
      languages: [],
    } as const;
    expect(parsePrivacyModelInstallation(customInstallation)).toEqual(
      customInstallation,
    );
    const localInstallation = {
      ...customInstallation,
      source: "local",
      repo_id: "local/model-aaaaaaaaaaaa",
    } as const;
    expect(parsePrivacyModelInstallation(localInstallation)).toEqual(
      localInstallation,
    );
    expect(() =>
      parsePrivacyModelInstallation({
        ...customInstallation,
        repo_id: "local/model-aaaaaaaaaaaa",
      }),
    ).toThrow("local provenance");
    expect(() =>
      parsePrivacyModelInstallation({
        ...customInstallation,
        source: "local",
      }),
    ).toThrow("local provenance");
    const downloading = {
      ...readyInstallation,
      status: "downloading",
      bytes_downloaded: 45,
      installed_at: null,
    } as const;
    expect(
      parsePrivacyModelInstallationList({ items: [downloading] }),
    ).toEqual({ items: [downloading] });
    const preparing = {
      ...downloading,
      bytes_downloaded: 0,
      bytes_total: 0,
    } as const;
    expect(parsePrivacyModelInstallation(preparing)).toEqual(preparing);
    expect(() =>
      parsePrivacyModelInstallation({
        ...readyInstallation,
        bytes_downloaded: 10,
      }),
    ).toThrow("lifecycle");
    expect(() =>
      parsePrivacyModelInstallation({
        ...readyInstallation,
        bytes_downloaded: 0,
        bytes_total: 0,
      }),
    ).toThrow("lifecycle");
    expect(() =>
      parsePrivacyModelInstallation({
        ...readyInstallation,
        variant_name: "x".repeat(65),
      }),
    ).toThrow("1 to 64 characters");
    expect(() =>
      parsePrivacyModelInstallation({
        ...readyInstallation,
        name: " Example Privacy",
      }),
    ).toThrow("trimmed");
    expect(() =>
      parsePrivacyModelInstallation({
        ...readyInstallation,
        variant_name: "CPU INT8\n",
      }),
    ).toThrow("control characters");
    expect(() =>
      parsePrivacyModelInstallation({
        ...readyInstallation,
        quantization: " int8",
      }),
    ).toThrow("trimmed");
    expect(() =>
      parsePrivacyModelInstallation({
        ...readyInstallation,
        catalog_source: null,
      }),
    ).toThrow("catalog provenance");
    expect(() =>
      parsePrivacyModelInstallation({
        ...readyInstallation,
        languages: ["en", "en"],
      }),
    ).toThrow("duplicate value");
    expect(() =>
      parsePrivacyModelInstallation({
        ...readyInstallation,
        installed_at: "2026-07-24T10:30:00Q",
      }),
    ).toThrow("RFC3339");
    expect(() =>
      parsePrivacyModelInstallation({
        ...readyInstallation,
        installed_at: "2026-02-30T10:30:00Z",
      }),
    ).toThrow("RFC3339");
    expect(
      parsePrivacyModelInstallation({
        ...readyInstallation,
        installed_at: "2026-07-24T18:30:00.123456789+08:00",
      }).installed_at,
    ).toBe("2026-07-24T18:30:00.123456789+08:00");
    expect(() =>
      parsePrivacyModelInstallation({
        ...readyInstallation,
        label_mapping: { EMAIL: "unknown_kind" },
      }),
    ).toThrow("canonical privacy kind");
    expect(() =>
      parsePrivacyModelInstallation({
        ...readyInstallation,
        local_path: "/private/model",
      }),
    ).toThrow("unexpected field");
  });

  it("validates install inputs and identifies resource-heavy variants", () => {
    expect(
      validatePrivacyModelInstallInput({
        repo_id: catalogModel.repo_id,
        revision,
        variant_id: variant.id,
        label_mapping: { EMAIL: "email", MISC: null },
      }),
    ).toEqual({
      repo_id: catalogModel.repo_id,
      revision,
      variant_id: variant.id,
      label_mapping: { EMAIL: "email", MISC: null },
    });
    expect(() =>
      validatePrivacyModelInstallInput({
        repo_id: catalogModel.repo_id,
        revision: "main",
        variant_id: variant.id,
        label_mapping: {},
      }),
    ).toThrow("lowercase commit");
    expect(isResourceHeavyVariant(variant)).toBe(false);
    expect(
      isResourceHeavyVariant({
        bytes_total: 1024 ** 3,
        estimated_ram_bytes: 1,
      }),
    ).toBe(true);
  });

  it("normalizes local probe paths and rejects URIs or control characters", () => {
    expect(
      validateLocalProbeInput({ path: "  /Volumes/models/privacy  " }),
    ).toEqual({ path: "/Volumes/models/privacy" });
    expect(
      validateLocalProbeInput({ path: "C:\\models\\privacy\\model.onnx" }),
    ).toEqual({ path: "C:\\models\\privacy\\model.onnx" });
    expect(() => validateLocalProbeInput({ path: "   " })).toThrow(
      "1 to 4096 characters",
    );
    expect(() =>
      validateLocalProbeInput({ path: "smb://host/share/model.onnx" }),
    ).toThrow("not a URI");
    expect(() =>
      validateLocalProbeInput({ path: "file:/Volumes/models/privacy" }),
    ).toThrow("not a URI");
    expect(() =>
      validateLocalProbeInput({ path: "/Volumes/models\u0000/privacy" }),
    ).toThrow("control characters");
    expect(() =>
      validateLocalProbeInput({ path: `/${"a".repeat(4096)}` }),
    ).toThrow("1 to 4096 characters");
  });
});
