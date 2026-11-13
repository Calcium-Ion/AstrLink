/** One line of a line diff. */
export interface DiffLine {
  kind: "same" | "added" | "removed";
  text: string;
}

/** A run of unchanged lines a diff view folds away. */
export interface DiffFold {
  kind: "fold";
  count: number;
}

/**
 * Compares two texts line by line, keeping the longest run of lines they
 * share. The common head and tail are matched first; a middle too large to
 * compare within `maxCells` reads as removed, then added.
 */
export function lineDiff(
  before: string,
  after: string,
  maxCells = 4_000_000,
): DiffLine[] {
  const left = splitLines(before);
  const right = splitLines(after);
  let head = 0;
  while (
    head < left.length &&
    head < right.length &&
    left[head] === right[head]
  ) {
    head += 1;
  }
  let tail = 0;
  while (
    tail < left.length - head &&
    tail < right.length - head &&
    left[left.length - 1 - tail] === right[right.length - 1 - tail]
  ) {
    tail += 1;
  }
  const same = (text: string): DiffLine => ({ kind: "same", text });
  const middle = middleDiff(
    left.slice(head, left.length - tail),
    right.slice(head, right.length - tail),
    maxCells,
  );
  return [
    ...left.slice(0, head).map(same),
    ...middle,
    ...left.slice(left.length - tail).map(same),
  ];
}

function splitLines(text: string): string[] {
  if (text === "") return [];
  const lines = text.replace(/\r\n?/g, "\n").split("\n");
  // A final newline ends the last line rather than starting another.
  if (lines[lines.length - 1] === "") lines.pop();
  return lines;
}

function middleDiff(
  left: string[],
  right: string[],
  maxCells: number,
): DiffLine[] {
  const removed = (text: string): DiffLine => ({ kind: "removed", text });
  const added = (text: string): DiffLine => ({ kind: "added", text });
  if (
    left.length === 0 ||
    right.length === 0 ||
    left.length * right.length > maxCells
  ) {
    return [...left.map(removed), ...right.map(added)];
  }
  // lengths[i][j] is the longest common run of left[i..] and right[j..].
  const width = right.length + 1;
  const lengths = new Uint32Array((left.length + 1) * width);
  for (let i = left.length - 1; i >= 0; i -= 1) {
    for (let j = right.length - 1; j >= 0; j -= 1) {
      lengths[i * width + j] =
        left[i] === right[j]
          ? lengths[(i + 1) * width + j + 1]! + 1
          : Math.max(
              lengths[(i + 1) * width + j]!,
              lengths[i * width + j + 1]!,
            );
    }
  }
  const lines: DiffLine[] = [];
  let i = 0;
  let j = 0;
  while (i < left.length && j < right.length) {
    if (left[i] === right[j]) {
      lines.push({ kind: "same", text: left[i]! });
      i += 1;
      j += 1;
    } else if (lengths[(i + 1) * width + j]! >= lengths[i * width + j + 1]!) {
      lines.push(removed(left[i]!));
      i += 1;
    } else {
      lines.push(added(right[j]!));
      j += 1;
    }
  }
  return [
    ...lines,
    ...left.slice(i).map(removed),
    ...right.slice(j).map(added),
  ];
}

/**
 * Folds the unchanged lines of a diff that sit more than `context` lines
 * away from a change, so the view opens on what changed.
 */
export function foldDiff(
  lines: DiffLine[],
  context = 2,
): (DiffLine | DiffFold)[] {
  const near = new Uint8Array(lines.length);
  lines.forEach((line, index) => {
    if (line.kind === "same") return;
    const from = Math.max(0, index - context);
    const to = Math.min(lines.length - 1, index + context);
    near.fill(1, from, to + 1);
  });
  const folded: (DiffLine | DiffFold)[] = [];
  let skipped = 0;
  lines.forEach((line, index) => {
    if (line.kind === "same" && !near[index]) {
      skipped += 1;
      return;
    }
    if (skipped > 0) folded.push({ kind: "fold", count: skipped });
    skipped = 0;
    folded.push(line);
  });
  if (skipped > 0) folded.push({ kind: "fold", count: skipped });
  return folded;
}
