import { expect, it } from "vitest";
import { parseServiceTestResult } from "./service-test-model";

const result = { service_id: "service_test", protocol: "openai.chat", model: "test", stream: true, ok: true, status_code: 200, duration_ms: 300, output: "OK" };

it("accepts measured zero, nullable timings and older Core responses", () => {
  expect(parseServiceTestResult(result).first_token_ms).toBeUndefined();
  expect(parseServiceTestResult({ ...result, response_headers_ms: null, first_token_ms: null }).first_token_ms).toBeNull();
  expect(parseServiceTestResult({ ...result, response_headers_ms: 0, first_token_ms: 0 }).first_token_ms).toBe(0);
  expect(parseServiceTestResult({ ...result, response_headers_ms: 30, first_token_ms: 200 }).first_token_ms).toBe(200);
});

it("rejects invalid timing data at the bridge boundary", () => {
  for (const timing of [
    { response_headers_ms: -1 }, { response_headers_ms: "30" }, { response_headers_ms: NaN },
    { response_headers_ms: 301 }, { response_headers_ms: 0.5 },
    { first_token_ms: 20 }, { response_headers_ms: 30, first_token_ms: 20 },
    { response_headers_ms: 30, first_token_ms: 400 },
    { response_headers_ms: 30, first_token_ms: 40, stream: false },
  ]) {
    expect(() => parseServiceTestResult({ ...result, ...timing })).toThrow("Invalid provider test timing");
  }
});

it("accepts raw bodies without parsing them and rejects invalid envelope field types", () => {
  const raw = 'data: malformed <script>bad()</script>\r\n\r\n';
  expect(parseServiceTestResult({ ...result, raw_response: raw, raw_response_truncated: true, response_content_type: "text/event-stream" }).raw_response).toBe(raw);
  for (const fields of [{ raw_response: {} }, { raw_response_truncated: "true" }, { response_content_type: 1 }]) {
    expect(() => parseServiceTestResult({ ...result, ...fields })).toThrow("Invalid provider test result");
  }
});
