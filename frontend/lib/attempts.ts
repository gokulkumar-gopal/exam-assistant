// Retakes and attempt history (fs 6.1, 6.5, 6.6, 6.7). Pure helpers plus the one retake call.
import { api as defaultApi, ApiError, isNetworkError, NETWORK_TOAST } from "./api";
import { scoreText } from "./format";
import { setCheck, type Check, type Checks } from "./key";
import type { AttemptStatus, RetakeResponse } from "./types";

/** bs 8.5: the technical safety limit on attempts per person per exam. */
export const MAX_ATTEMPTS = 50;
export const CLOSED_TOAST = "This exam has closed.";
export const LIMIT_TOAST = `You have reached the limit of ${MAX_ATTEMPTS} attempts for this exam.`;
export const RETAKE_FAILED_TOAST = "Couldn't start a new attempt. Try again.";

interface Scored { attemptNo: number; status: AttemptStatus; score?: number | null }

/** The highest-scoring submitted attempt (the earliest on a tie); undefined when none has a numeric score (free text). */
export function bestAttemptNo(attempts: readonly Scored[]): number | undefined {
  let best: Scored | undefined;
  for (const a of attempts) {
    if (a.status !== "submitted" || typeof a.score !== "number") continue;
    if (!best || a.score > (best.score as number)) best = a;
  }
  return best?.attemptNo;
}

/** "3 attempts" when more than one, else an empty string. */
export const attemptsLabel = (count: number | undefined): string => (count !== undefined && count > 1 ? `${count} attempts` : "");

/** "Best 34/40 · 3 attempts" (the count only above one); "Best" alone for an ungraded free-text exam. */
export function bestPillText(score: number | undefined, maxScore: number | undefined, count: number | undefined): string {
  const best = typeof score === "number" && typeof maxScore === "number" ? `Best ${scoreText(score)}/${maxScore}` : "Best";
  const n = attemptsLabel(count);
  return n ? `${best} · ${n}` : best;
}

/** Header text on the take screen: "Attempt 2" once there is more than one attempt. */
export function attemptHeaderLabel(attemptCount: number | undefined, attemptNo: number | undefined): string | null {
  return attemptCount !== undefined && attemptCount > 1 && attemptNo !== undefined ? `Attempt ${attemptNo}` : null;
}

/** `?attempt=N` from the URL: a positive whole number, else undefined (the page then shows the latest submitted attempt). */
export function parseAttemptParam(raw: string | null | undefined): number | undefined {
  if (raw === null || raw === undefined || !/^[1-9]\d{0,5}$/.test(raw)) return undefined;
  return Number(raw);
}

/** Path for the result fetch. */
export const resultPath = (encodedId: string, attempt: number | undefined): string =>
  `/exams/${encodedId}/result${attempt === undefined ? "" : `?attempt=${attempt}`}`;

/** The result page can offer Retake: the latest attempt is submitted, it is before acceptingUntil and under the limit. */
export function canRetakeFrom(
  attempts: readonly { attemptNo: number; status: AttemptStatus }[] | undefined,
  acceptingUntilIso: string,
  nowMs: number,
): boolean {
  if (!attempts || attempts.length === 0 || attempts.length >= MAX_ATTEMPTS) return false;
  const latest = attempts.reduce((a, b) => (b.attemptNo > a.attemptNo ? b : a));
  const until = Date.parse(acceptingUntilIso);
  return latest.status === "submitted" && !Number.isNaN(until) && nowMs < until;
}

/** "Mar 3, 2:30 PM" style time for an attempt row; an em dash when missing. */
export function attemptTime(iso: string | undefined): string {
  const t = iso ? Date.parse(iso) : NaN;
  if (Number.isNaN(t)) return "—";
  return new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" }).format(new Date(t));
}

/** "34/40" for a submitted, graded attempt; an em dash otherwise. */
export function attemptScore(a: { status: AttemptStatus; score?: number | null }, maxScore: number): string {
  return a.status === "submitted" && typeof a.score === "number" ? `${scoreText(a.score)}/${maxScore}` : "—";
}

/** Self-check choices belong to one attempt: switching attempts starts from nothing (fs 6.7). */
export interface ScopedChecks { attemptNo: number | undefined; checks: Checks }
export const checksForAttempt = (s: ScopedChecks, attemptNo: number | undefined): Checks => (s.attemptNo === attemptNo ? s.checks : {});
export function setScopedCheck(s: ScopedChecks, attemptNo: number | undefined, id: string, v: Check): ScopedChecks {
  return { attemptNo, checks: setCheck(checksForAttempt(s, attemptNo), id, v) };
}

type Call = (method: "POST", path: string, body?: undefined, opts?: { noRetry: boolean }) => Promise<unknown>;

/** POST /exams/:id/retake. Idempotent on the server while the latest attempt is unfinished. */
export async function retakeExam(id: string, call: Call = defaultApi as Call): Promise<RetakeResponse> {
  return (await call("POST", `/exams/${encodeURIComponent(id)}/retake`, undefined, { noRetry: true })) as RetakeResponse;
}

/** Toast text for a failed retake: closed, the limit, a dropped network, or a generic line. */
export function retakeErrorMessage(e: unknown): string {
  if (e instanceof ApiError && e.status === 409) return e.code === "exam_closed" ? CLOSED_TOAST : LIMIT_TOAST;
  if (isNetworkError(e) || e instanceof TypeError) return NETWORK_TOAST;
  return RETAKE_FAILED_TOAST;
}
