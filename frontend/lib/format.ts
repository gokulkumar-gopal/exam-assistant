// Pure display helpers (fs 8, 9). Fixed en-US locale; the user's local timezone is used.

const MIN = 60_000;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;

export function shortDate(ms: number): string {
  return new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric" }).format(new Date(ms));
}

/** "closes in 5h" / "closes in 2d" under 7 days, "closes Oct 18" after, "closed Oct 8" once past. */
export function deadlineText(closesAtIso: string, nowMs: number): string {
  const at = Date.parse(closesAtIso);
  const diff = at - nowMs;
  if (diff <= 0) return `closed ${shortDate(at)}`;
  if (diff >= 7 * DAY) return `closes ${shortDate(at)}`;
  if (diff >= DAY) return `closes in ${Math.floor(diff / DAY)}d`;
  if (diff >= HOUR) return `closes in ${Math.floor(diff / HOUR)}h`;
  return `closes in ${Math.max(1, Math.floor(diff / MIN))}m`;
}

/** Scores may end in .5. One decimal only when needed; never rounded. */
export function scoreText(n: number): string {
  return String(n);
}
