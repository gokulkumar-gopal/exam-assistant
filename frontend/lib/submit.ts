// Submit, auto-submit at zero and the review summary (fs 6.5; backend-spec 8 POST /exams/{id}/submit).
import { api as defaultApi, ApiError } from "./api";
import { isAnswered } from "./dots";
import type { SubmitBody } from "./types";

export type SubmitOutcome = "ok" | "closed";
type Call = (method: "POST", path: string, body: SubmitBody, opts: { noRetry: boolean }) => Promise<unknown>;

/**
 * Submit the attempt with the given answers. `409 exam_closed` (the grace window ended first) is the
 * "closed" outcome; every other failure is rethrown so the caller keeps the user's answers and can retry.
 */
export async function submitExam(
  id: string,
  answers: Record<string, string>,
  lastQuestionIndex: number,
  call: Call = defaultApi as Call,
): Promise<SubmitOutcome> {
  try {
    await call("POST", `/exams/${encodeURIComponent(id)}/submit`, { answers, lastQuestionIndex }, { noRetry: true });
    return "ok";
  } catch (e) {
    if (e instanceof ApiError && e.status === 409 && e.code === "exam_closed") return "closed";
    throw e;
  }
}

/** Zero-based positions with no answer, over `n` questions (entries missing from `answers` count as blank). */
export function unansweredIndexes(answers: readonly (string | undefined)[], n: number = answers.length): number[] {
  const out: number[] = [];
  for (let i = 0; i < n; i++) if (!isAnswered(answers[i])) out.push(i);
  return out;
}

/** "2 unanswered: question 3, 7" or "All questions answered." (`answers` in question order). */
export function unansweredLine(answers: readonly (string | undefined)[], n: number = answers.length): string {
  const missing = unansweredIndexes(answers, n);
  if (missing.length === 0) return "All questions answered.";
  return `${missing.length} unanswered: question ${missing.map((i) => i + 1).join(", ")}`;
}

/** AlertDialog title when submitting with blanks. */
export const submitDialogTitle = (count: number): string =>
  `Submit with ${count} unanswered question${count === 1 ? "" : "s"}?`;

/** Where a submit stands: nothing started, one running (manual or deadline), or the attempt is over. */
export type SubmitPhase = "idle" | "submitting" | "done";

/** Save & exit makes no sense once a submit has started (it would claim a save and send the user Home). */
export const canSaveAndExit = (phase: SubmitPhase): boolean => phase === "idle";

/** Reject with Error("timeout") if `p` has not settled in `ms`; the request itself is not cancelled (submit is idempotent). */
export function withTimeout<T>(p: Promise<T>, ms: number): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const id = setTimeout(() => reject(new Error("timeout")), ms);
    p.then((v) => { clearTimeout(id); resolve(v); }, (e) => { clearTimeout(id); reject(e); });
  });
}

/** Wait for `p` for at most `ms`; resolves either way (a rejection is swallowed). Used to let an in-flight reveal finish before a submit, but never stall it. */
export function awaitWithTimeout(p: Promise<unknown>, ms: number): Promise<void> {
  return new Promise<void>((resolve) => {
    const id = setTimeout(resolve, ms);
    const done = () => { clearTimeout(id); resolve(); };
    p.then(done, done);
  });
}

export interface AutoSubmitDeps {
  submit: (answers: Record<string, string>, lastQuestionIndex: number) => Promise<SubmitOutcome>;
  /** The newest in-memory answers and index; read once, at the first trigger. */
  snapshot: () => { answers: Record<string, string>; lastQuestionIndex: number };
  /** Stops autosave for good (dispose). Called before the submit, so a late put cannot race it. */
  stopAutosave: () => void;
  /** Whether a failed submit may still be retried (false once the grace window is over). Default: no retry. */
  canRetry?: () => boolean;
  retryMs?: number;
  sleep?: (ms: number) => Promise<void>;
}

/**
 * The deadline submit (fs 6.5): no autosave first, the answers go in the submit body. Returns a trigger that is
 * safe to call any number of times: the work runs once and every call gets the same outcome. A network failure
 * is retried every 3 s with the same answers while `canRetry()`; "gave-up" means retrying is over.
 */
export function autoSubmitAtZero(d: AutoSubmitDeps): () => Promise<SubmitOutcome | "gave-up"> {
  let run: Promise<SubmitOutcome | "gave-up"> | null = null;
  const sleep = d.sleep ?? ((ms: number) => new Promise<void>((r) => setTimeout(r, ms)));
  return () => {
    d.stopAutosave(); // idempotent; also on repeat triggers (a remounted saver must not outlive the deadline)
    run ??= (async () => {
      const { answers, lastQuestionIndex } = d.snapshot();
      for (;;) {
        try {
          return await d.submit(answers, lastQuestionIndex);
        } catch {
          if (!d.canRetry?.()) return "gave-up";
          await sleep(d.retryMs ?? 3000);
        }
      }
    })();
    return run;
  };
}
