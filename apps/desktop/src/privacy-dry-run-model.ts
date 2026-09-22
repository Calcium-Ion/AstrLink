import type {
  PrivacyDryRunFinding,
  PrivacyDryRunProtocol,
} from "./privacy-policy-model";

const samplePaths: Record<PrivacyDryRunProtocol, string> = {
  "openai.chat": "/messages/0/content",
  "anthropic.messages": "/messages/0/content",
  "openai.completions": "/prompt",
  "openai.responses": "/input",
  "openai.responses.compact": "/input",
  "google.generate_content": "/contents/0/parts/0/text",
};

function textAtPath(value: unknown, path: string): string | null {
  if (!path.startsWith("/")) return null;
  for (const part of path.slice(1).split("/")) {
    const key = part.replace(/~1/g, "/").replace(/~0/g, "~");
    if (
      typeof value !== "object" ||
      value === null ||
      !Object.hasOwn(value, key)
    )
      return null;
    value = (value as Record<string, unknown>)[key];
  }
  return typeof value === "string" ? value : null;
}

/** Resolve the decoded string, never offsets into serialized JSON. */
export function dryRunTextAtPath(body: string, path: string): string | null {
  try {
    return textAtPath(JSON.parse(body), path);
  } catch {
    return null;
  }
}

export function dryRunSampleText(
  body: string,
  protocol: PrivacyDryRunProtocol,
): string | null {
  return dryRunTextAtPath(body, samplePaths[protocol]);
}

export interface DryRunTextSpan {
  text: string;
  value: string;
  /** UTF-16 offsets for textarea selection and JS string slicing. */
  start: number;
  end: number;
}

/** Core reports UTF-8 byte offsets; Chinese and emoji must not use String.slice directly. */
export function dryRunFindingSpan(
  body: string,
  finding: PrivacyDryRunFinding,
): DryRunTextSpan | null {
  return createDryRunFindingResolver(body)(finding);
}

/** Parse/encode each request field once even when a large sample has many matches. */
export function createDryRunFindingResolver(body: string) {
  let document: unknown;
  try {
    document = JSON.parse(body);
  } catch {
    document = null;
  }
  const fields = new Map<string, { text: string; bytes: Uint8Array } | null>();
  return (finding: PrivacyDryRunFinding): DryRunTextSpan | null => {
    if (!fields.has(finding.path)) {
      const text = textAtPath(document, finding.path);
      fields.set(
        finding.path,
        text === null ? null : { text, bytes: new TextEncoder().encode(text) },
      );
    }
    const field = fields.get(finding.path);
    if (!field) return null;
    const { text, bytes } = field;
    const { start, end } = finding;
    if (
      !Number.isInteger(start) ||
      !Number.isInteger(end) ||
      start < 0 ||
      end <= start ||
      end > bytes.length
    )
      return null;
    try {
      const decoder = new TextDecoder("utf-8", {
        fatal: true,
        ignoreBOM: true,
      });
      const prefix = decoder.decode(bytes.subarray(0, start));
      const value = decoder.decode(bytes.subarray(start, end));
      return {
        text,
        value,
        start: prefix.length,
        end: prefix.length + value.length,
      };
    } catch {
      return null;
    }
  };
}
