/** Locale-aware formatting helpers. The host tells us the user's locale/timezone. */

let locale = "pt-BR";
let timeZone: string | undefined;

export function setLocale(l?: string, tz?: string): void {
  if (l) locale = l;
  if (tz) timeZone = tz;
}

const DAY = 86_400_000;

/** WhatsApp-style relative stamp: 14:32 today, "ontem", weekday, then date. */
export function chatTime(ms: number): string {
  if (!ms) return "";
  const d = new Date(ms);
  if (Number.isNaN(d.getTime())) return "";
  const now = new Date();
  const startOfToday = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  const opts: Intl.DateTimeFormatOptions = { timeZone };
  if (ms >= startOfToday) {
    return d.toLocaleTimeString(locale, { ...opts, hour: "2-digit", minute: "2-digit" });
  }
  if (ms >= startOfToday - DAY) return relativeDay(1);
  if (ms >= startOfToday - 6 * DAY) return d.toLocaleDateString(locale, { ...opts, weekday: "short" });
  return d.toLocaleDateString(locale, { ...opts, day: "2-digit", month: "2-digit", year: "2-digit" });
}

/** Full timestamp for message bubbles and tooltips. */
export function fullTime(ms: number): string {
  if (!ms) return "";
  const d = new Date(ms);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleString(locale, {
    timeZone,
    day: "2-digit",
    month: "2-digit",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function relativeDay(daysAgo: number): string {
  try {
    return new Intl.RelativeTimeFormat(locale, { numeric: "auto" }).format(-daysAgo, "day");
  } catch {
    return `${daysAgo}d`;
  }
}

export function timeOnly(ms: number): string {
  if (!ms) return "";
  const d = new Date(ms);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleTimeString(locale, { timeZone, hour: "2-digit", minute: "2-digit" });
}

export function count(n: number): string {
  try {
    return new Intl.NumberFormat(locale).format(n);
  } catch {
    return String(n);
  }
}

export function bytes(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return "";
  const units = ["B", "KB", "MB", "GB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v >= 10 || i === 0 ? 0 : 1)} ${units[i]}`;
}

/** +55 21 96886-5678 from 5521968865678 (Brazilian shapes; passthrough otherwise). */
export function phone(digits: string): string {
  const d = String(digits ?? "").replace(/\D/g, "");
  if (!d) return "";
  if (d.startsWith("55") && (d.length === 12 || d.length === 13)) {
    const ddd = d.slice(2, 4);
    const rest = d.slice(4);
    const head = rest.length === 9 ? rest.slice(0, 5) : rest.slice(0, 4);
    return `+55 ${ddd} ${head}-${rest.slice(head.length)}`;
  }
  return `+${d}`;
}
