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
  const fields = new Map<string, { text: string; units: Int32Array } | null>();
  return (finding: PrivacyDryRunFinding): DryRunTextSpan | null => {
    if (!fields.has(finding.path)) {
      const text = textAtPath(document, finding.path);
      fields.set(
        finding.path,
        text === null ? null : { text, units: utf16Units(text) },
      );
    }
    const field = fields.get(finding.path);
    if (!field) return null;
    const { text, units } = field;
    const { start, end } = finding;
    if (
      !Number.isInteger(start) ||
      !Number.isInteger(end) ||
      start < 0 ||
      end <= start ||
      end >= units.length
    )
      return null;
    const from = units[start];
    const to = units[end];
    if (from < 0 || to < 0) return null;
    return { text, value: text.slice(from, to), start: from, end: to };
  };
}

/**
 * Maps each UTF-8 byte offset of text to its UTF-16 index, or -1 inside a
 * character. Decoding the text before every finding instead takes time
 * proportional to the matches times the length of the text.
 */
function utf16Units(text: string): Int32Array {
  const units = new Int32Array(new TextEncoder().encode(text).length + 1);
  units.fill(-1);
  let byte = 0;
  let unit = 0;
  for (const character of text) {
    units[byte] = unit;
    const point = character.codePointAt(0) ?? 0;
    byte += point < 0x80 ? 1 : point < 0x800 ? 2 : point < 0x10000 ? 3 : 4;
    unit += character.length;
  }
  units[byte] = unit;
  return units;
}
