// Glob patterns for path.matchesGlob and fs.glob, as minimatch reads them
// for Node: *, ?, [...], {a,b} and ** across directories; a wildcard does
// not match a leading dot. Windows and macOS match without case.
(function (module, exports, require) {
  "use strict";
  const special = /[.+^${}()|\\]/g;

  // braces returns the index of the brace that closes the one at start.
  const closing = (pattern, start) => {
    let depth = 0;
    for (let index = start; index < pattern.length; index++) {
      if (pattern[index] === "\\") index++;
      else if (pattern[index] === "{") depth++;
      else if (pattern[index] === "}" && --depth === 0) return index;
    }
    return -1;
  };
  const alternatives = (body) => {
    const parts = [];
    let depth = 0;
    let last = 0;
    for (let index = 0; index < body.length; index++) {
      if (body[index] === "\\") index++;
      else if (body[index] === "{") depth++;
      else if (body[index] === "}") depth--;
      else if (body[index] === "," && depth === 0) {
        parts.push(body.slice(last, index));
        last = index + 1;
      }
    }
    parts.push(body.slice(last));
    return parts;
  };

  const translate = (pattern, escapes) => {
    let out = "";
    const segmentStart = () => out === "" || out.endsWith("/");
    for (let index = 0; index < pattern.length; index++) {
      const char = pattern[index];
      if (char === "*") {
        if (pattern[index + 1] === "*") {
          const whole =
            segmentStart() &&
            (index + 2 === pattern.length || pattern[index + 2] === "/");
          if (whole) {
            out +=
              index + 2 === pattern.length
                ? "(?:(?!\\.)[^/]*(?:/(?!\\.)[^/]*)*)?"
                : "(?:(?!\\.)[^/]*/)*";
            index += 2;
            continue;
          }
          index++;
        }
        out += (segmentStart() ? "(?!\\.)" : "") + "[^/]*";
      } else if (char === "?") {
        out += (segmentStart() ? "(?!\\.)" : "") + "[^/]";
      } else if (char === "[") {
        const end = pattern.indexOf("]", index + 2);
        if (end === -1) {
          out += "\\[";
          continue;
        }
        let set = pattern.slice(index + 1, end);
        if (set[0] === "!") set = "^" + set.slice(1);
        out += "[" + set.replace(/\\/g, "\\\\") + "]";
        index = end;
      } else if (char === "{") {
        const end = closing(pattern, index);
        if (end === -1) {
          out += "\\{";
          continue;
        }
        const parts = alternatives(pattern.slice(index + 1, end));
        if (parts.length === 1) {
          out += "\\{" + translate(parts[0], escapes) + "\\}";
        } else {
          out += "(?:" + parts.map((part) => translate(part, escapes)).join("|") + ")";
        }
        index = end;
      } else if (char === "\\" && escapes && index + 1 < pattern.length) {
        out += pattern[++index].replace(special, "\\$&");
      } else {
        out += char.replace(special, "\\$&");
      }
    }
    return out;
  };

  const compiled = new Map();
  exports.regExp = (pattern, windows) => {
    const key = `${windows ? 1 : 0}${pattern}`;
    let found = compiled.get(key);
    if (found === undefined) {
      const platform = require("process").platform;
      const caseless = platform === "win32" || platform === "darwin";
      const source = windows ? pattern.replace(/\\/g, "/") : pattern;
      found = new RegExp(`^${translate(source, !windows)}$`, caseless ? "i" : "");
      if (compiled.size > 256) compiled.clear();
      compiled.set(key, found);
    }
    return found;
  };
  exports.matches = (path, pattern, windows) =>
    exports.regExp(pattern, windows).test(windows ? path.replace(/\\/g, "/") : path);
})
