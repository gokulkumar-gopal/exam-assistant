// Peeks allowed on Create (fs 6.4 card 4, bs 8.5): the default, the 0..10 clamp and the stepper value.
import { isEmail } from "./publish";

export const MAX_PEEKS = 10;

/** 3 for a personal practice exam (nobody assigned), 0 when people are assigned. Only real emails count as people. */
export function defaultMaxPeeks(assigneeEmails: readonly string[]): 0 | 3 {
  return assigneeEmails.some((e) => typeof e === "string" && isEmail(e.trim())) ? 0 : 3;
}

/** An integer in 0..cap (cap itself at most 10). Not-a-number reads as 0. */
export function clampPeeks(v: number, cap: number = MAX_PEEKS): number {
  const hi = Math.max(0, Math.min(MAX_PEEKS, Math.floor(Number.isFinite(cap) ? cap : MAX_PEEKS)));
  if (Number.isNaN(v)) return 0;
  return Math.max(0, Math.min(hi, Math.floor(v)));
}

/** The stepper value: the user's edit if there is one (null: never edited), else the default, within 0..cap. */
export function peeksValue(edited: number | null, assigneeEmails: readonly string[], cap: number = MAX_PEEKS): number {
  return clampPeeks(edited ?? defaultMaxPeeks(assigneeEmails), cap);
}

/** One step up or down from `current`, kept within 0..cap. */
export function stepPeeks(current: number, delta: 1 | -1, cap: number = MAX_PEEKS): number {
  return clampPeeks(current + delta, cap);
}
