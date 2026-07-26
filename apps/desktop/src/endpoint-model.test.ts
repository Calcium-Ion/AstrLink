import { describe, expect, it } from "vitest";

import { parseEndpointPage, parseEndpointRecord } from "./endpoint-model";

const endpoint = (credentialRef = "local://endpoint/endpoint_01") => ({
  id: "endpoint_01",
  name: "Primary",
  kind: "openai",
  base_url: "https://api.example/v1",
  auth: { scheme: "bearer" },
  credential_ref: credentialRef,
  enabled: true,
  capabilities: [
    { protocol: "openai.responses", mode: "native", streaming: true },
    {
      protocol: "vendor.custom",
      mode: "native",
      streaming: true,
      models: ["vendor-model"],
    },
    {
      protocol: "vendor.custom",
      mode: "delegated",
      streaming: false,
    },
  ],
});

describe("endpoint IPC contract", () => {
  it("accepts the default local reference and optional keyring reference", () => {
    expect(
      parseEndpointPage({ items: [endpoint()], next_cursor: null }).items[0]
        ?.credential_ref,
    ).toBe("local://endpoint/endpoint_01");
    expect(
      parseEndpointPage({
        items: [endpoint("keyring://endpoint/endpoint_01")],
        next_cursor: "cursor",
      }).next_cursor,
    ).toBe("cursor");
  });

  it("parses strong ETags without allowing extra fields", () => {
    const record = parseEndpointRecord({
      endpoint: endpoint(),
      etag: `"sha256:${"a".repeat(64)}"`,
    });
    expect(record.endpoint.id).toBe("endpoint_01");
    expect(record.endpoint.capabilities).toHaveLength(3);
    expect(record.endpoint.capabilities[1]?.models).toEqual(["vendor-model"]);
    expect(() =>
      parseEndpointRecord({ ...record, secret: "must-not-cross-ipc" }),
    ).toThrow("unexpected field");
  });

  it("uses Unicode code points for frozen character limits", () => {
    const valid = {
      ...endpoint(),
      name: "😀".repeat(128),
      capabilities: endpoint().capabilities.map((capability, index) =>
        index === 1
          ? { ...capability, models: ["😀".repeat(256)] }
          : capability,
      ),
    };
    const parsed = parseEndpointPage({ items: [valid], next_cursor: null });
    expect(parsed.items[0]?.name).toBe(valid.name);
    expect(parsed.items[0]?.capabilities[1]?.models?.[0]).toBe(
      "😀".repeat(256),
    );

    expect(() =>
      parseEndpointPage({
        items: [{ ...valid, name: "😀".repeat(129) }],
        next_cursor: null,
      }),
    ).toThrow("expected 1 to 128 characters");
    expect(() =>
      parseEndpointPage({
        items: [
          {
            ...valid,
            capabilities: valid.capabilities.map((capability, index) =>
              index === 1
                ? { ...capability, models: ["😀".repeat(257)] }
                : capability,
            ),
          },
        ],
        next_cursor: null,
      }),
    ).toThrow("expected 1 to 256 characters");
  });

  it.each([
    ["unsafe URL", { ...endpoint(), base_url: "https://user:secret@example.com" }],
    ["invalid reference", endpoint("file:///tmp/secret")],
    ["unknown endpoint field", { ...endpoint(), credential: "secret" }],
  ])("rejects %s", (_name, invalidEndpoint) => {
    expect(() =>
      parseEndpointPage({ items: [invalidEndpoint], next_cursor: null }),
    ).toThrow("Invalid Endpoint IPC response");
  });
});
