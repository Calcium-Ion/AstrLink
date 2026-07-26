const decodedEscapes: Readonly<Record<string, string>> = {
  "\\": "\\",
  b: "\b",
  f: "\f",
  n: "\n",
  r: "\r",
  t: "\t",
};

export function encodeModelEditorValue(value: string): string {
  let encoded = "";
  for (const character of value) {
    const codePoint = character.codePointAt(0) ?? 0;
    switch (character) {
      case "\\":
        encoded += "\\\\";
        break;
      case "\b":
        encoded += "\\b";
        break;
      case "\f":
        encoded += "\\f";
        break;
      case "\n":
        encoded += "\\n";
        break;
      case "\r":
        encoded += "\\r";
        break;
      case "\t":
        encoded += "\\t";
        break;
      default:
        encoded +=
          codePoint < 0x20 || codePoint === 0x7f
            ? `\\u${codePoint.toString(16).padStart(4, "0")}`
            : character;
    }
  }
  return encoded;
}

export function decodeModelEditorValue(value: string): string {
  let decoded = "";
  for (let index = 0; index < value.length; index += 1) {
    const character = value[index] ?? "";
    if (character !== "\\") {
      decoded += character;
      continue;
    }
    const escaped = value[index + 1];
    if (escaped === undefined) {
      decoded += "\\";
      continue;
    }
    const simple = decodedEscapes[escaped];
    if (simple !== undefined) {
      decoded += simple;
      index += 1;
      continue;
    }
    if (escaped === "u") {
      const hexadecimal = value.slice(index + 2, index + 6);
      if (/^[0-9A-Fa-f]{4}$/.test(hexadecimal)) {
        decoded += String.fromCharCode(Number.parseInt(hexadecimal, 16));
        index += 5;
        continue;
      }
    }
    decoded += `\\${escaped}`;
    index += 1;
  }
  return decoded;
}
