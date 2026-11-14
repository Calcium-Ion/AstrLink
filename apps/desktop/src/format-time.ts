import { currentLocale } from "./i18n";

type Shape = "time" | "day" | "date";

const dateParts: Record<Shape, Intl.DateTimeFormatOptions> = {
  time: {},
  day: { month: "numeric", day: "numeric" },
  date: { year: "numeric", month: "numeric", day: "numeric" },
};

// Same output as toLocaleString / toLocaleTimeString with `hour12: false`,
// which build a new formatter per call; lists format a time or two per row.
const formats = new Map<string, Intl.DateTimeFormat>();

function format(value: string | number | Date, shape: Shape): string {
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  const locale = currentLocale();
  const key = `${locale}:${shape}`;
  let formatter = formats.get(key);
  if (!formatter) {
    formatter = new Intl.DateTimeFormat(locale, {
      ...dateParts[shape],
      hour: "numeric",
      minute: "numeric",
      second: "numeric",
      hour12: false,
    });
    formats.set(key, formatter);
  }
  return formatter.format(date);
}

/** A time of day on the 24-hour clock, as request records show it. */
export function formatClockTime(value: string | number | Date): string {
  return format(value, "time");
}

/** A date and a time on the 24-hour clock, as request records show them. */
export function formatDateTime(value: string | number | Date): string {
  return format(value, "date");
}

/** A moment in a list: its time of day today, else its day and time. */
export function formatMoment(
  value: string | number | Date,
  now: Date = new Date(),
): string {
  const date = value instanceof Date ? value : new Date(value);
  return format(
    value,
    date.toDateString() === now.toDateString() ? "time" : "day",
  );
}
