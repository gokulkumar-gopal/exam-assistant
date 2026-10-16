// Exam countdown helpers (fs 6.5, 8.1). Pure: the caller passes server-based "now"
// (device clock plus the offset learned from serverTime), so a wrong device clock changes nothing.

export const LOW_SECONDS = 300;

export interface TimerView { text: string; low: boolean }

/** Whole seconds left, rounded up; NaN when the deadline is unusable. */
function secondsLeft(closesAtIso: string, nowMs: number): number {
  const t = Date.parse(closesAtIso);
  if (Number.isNaN(t)) return NaN;
  return Math.ceil((t - nowMs) / 1000);
}

const pad = (n: number) => String(n).padStart(2, "0");

export function timerView(closesAtIso: string, nowMs: number): TimerView {
  const s = secondsLeft(closesAtIso, nowMs);
  if (Number.isNaN(s)) return { text: "--:--", low: false };
  if (s <= 0) return { text: "Closing…", low: true };
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  const clock = h > 0 ? `${h}:${pad(m)}:${pad(sec)}` : `${m}:${pad(sec)}`;
  return { text: `${clock} left`, low: s < LOW_SECONDS };
}

/**
 * Screen-reader announcement for the current band. It changes only at 5:00, 1:00 and 0, so a live
 * region holding it speaks three times in total, never every second. Empty above 5:00.
 */
export function timerBucket(closesAtIso: string, nowMs: number): string {
  const s = secondsLeft(closesAtIso, nowMs);
  if (Number.isNaN(s) || s > LOW_SECONDS) return "";
  if (s > 60) return "5 minutes left";
  if (s > 0) return "1 minute left";
  return "Time is up. Closing…";
}

/** setTimeout fires immediately above 2^31-1 ms (about 24.8 days); stay safely below it. */
export const MAX_DELAY = 2 ** 31 - 1 - 1000;
export const armDelay = (ms: number): number => Math.max(0, Math.min(ms, MAX_DELAY));

/**
 * Run `cb` once when `now()` reaches `targetMs`, re-arming in chunks for far deadlines.
 * `now` should be server-based (device clock plus offset). Returns a cancel function.
 */
export function scheduleAt(targetMs: number, now: () => number, cb: () => void): () => void {
  let id: ReturnType<typeof setTimeout> | null = null;
  const arm = () => {
    const left = targetMs - now();
    if (left <= 0) { id = null; cb(); return; }
    id = setTimeout(arm, armDelay(left));
  };
  arm();
  return () => { if (id !== null) clearTimeout(id); id = null; };
}
