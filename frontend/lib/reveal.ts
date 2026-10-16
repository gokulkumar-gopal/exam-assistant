// Reveal an answer (fs 6.5 Peeks, bs 8.5 / 9.6): pure state helpers. Answers come only from `attempt.revealed`
// and from the reveal response; the exam payload's questions are never read for a key, so an answer that was
// not revealed is never held on the client.
import { PEEKS_UI } from "./features";
import type { ExamType } from "./types";

export interface RevealedAnswer { correctIndex?: number; modelAnswer?: string }
export interface RevealState {
  maxPeeks: number;
  peeksLeft: number;
  /** question id -> the answer the user revealed. */
  revealed: Record<string, RevealedAnswer>;
  /** A 409 came back (no peeks left, or the key is already visible): the button stays disabled. */
  noPeeksLeft: boolean;
  /** A 403 came back, or the key is visible: peeking is off, nothing is shown. */
  off: boolean;
}
interface RevealedInput { revealed?: readonly { questionId: string; correctIndex?: number; modelAnswer?: string }[]; peeksLeft?: number }

export const ANSWER_LABEL = "Revealed · half marks at most";
export const answerNoticeText = (text: string): string => `Answer: ${text}`;
export const peeksLeftText = (n: number): string => `${n} peek${n === 1 ? "" : "s"}`;
/** Dialog body (fs 6.5). N is "1 peek" when it is one. */
export const revealDialogBody = (peeksLeft: number): string =>
  `Revealed questions score at most half marks. You have ${peeksLeftText(peeksLeft)} left.`;

const clampLeft = (v: number, max: number) => Math.max(0, Math.min(Math.max(0, max), Math.floor(v)));

function pick(r: { correctIndex?: unknown; modelAnswer?: unknown }): RevealedAnswer {
  const out: RevealedAnswer = {};
  if (typeof r.correctIndex === "number" && Number.isInteger(r.correctIndex)) out.correctIndex = r.correctIndex;
  if (typeof r.modelAnswer === "string") out.modelAnswer = r.modelAnswer;
  return out;
}

/** State when the take screen loads: revealed answers survive a reload (attempt.revealed). */
export function initReveal(maxPeeks: number, attempt: RevealedInput | null | undefined, keyVisible = false): RevealState {
  const revealed: Record<string, RevealedAnswer> = {};
  for (const r of attempt?.revealed ?? []) {
    if (r && typeof r.questionId === "string") revealed[r.questionId] = pick(r);
  }
  const count = Object.keys(revealed).length;
  const sent = attempt?.peeksLeft;
  const peeksLeft = typeof sent === "number" && Number.isFinite(sent) ? clampLeft(sent, maxPeeks) : clampLeft(maxPeeks - count, maxPeeks);
  return { maxPeeks, peeksLeft, revealed, noPeeksLeft: false, off: keyVisible };
}

/** Counter and controls are shown: maxPeeks above 0, not turned off by a 403, and the peeks UI is not cut. */
export function peeksOn(s: RevealState, ui: boolean = PEEKS_UI): boolean {
  return ui && s.maxPeeks > 0 && !s.off;
}

export const storedAnswer = (s: RevealState, qid: string): RevealedAnswer | undefined => s.revealed[qid];

/**
 * The Reveal answer control for a question: "none" (not shown), "disabled" or "enabled".
 * A revealed question shows its answer instead of the control. `busy` is a submit or another reveal in flight.
 */
export function revealControl(s: RevealState, qid: string, busy: boolean, ui: boolean = PEEKS_UI): "none" | "disabled" | "enabled" {
  if (!peeksOn(s, ui) || s.revealed[qid]) return "none";
  if (busy || s.noPeeksLeft || s.peeksLeft <= 0) return "disabled";
  return "enabled";
}

/** A successful reveal. A question already revealed keeps its stored answer and costs nothing (same state back). */
export function applyReveal(s: RevealState, r: { questionId: string; correctIndex?: number; modelAnswer?: string; peeksLeft: number }): RevealState {
  if (s.revealed[r.questionId]) return s;
  return { ...s, revealed: { ...s.revealed, [r.questionId]: pick(r) }, peeksLeft: clampLeft(r.peeksLeft, s.maxPeeks) };
}

/** 403: peeking is off (control gone). 409: no peeks left or key visible (button disabled). Anything else: unchanged. */
export function applyRevealError(s: RevealState, status: number): RevealState {
  if (status === 403) return { ...s, off: true };
  if (status === 409) return { ...s, noPeeksLeft: true };
  return s;
}

/** The text to show for a revealed answer: the option label (MCQ) or the model answer. Null when there is nothing usable. */
export function revealedAnswerText(type: ExamType, options: readonly string[] | undefined, a: RevealedAnswer): string | null {
  if (type === "mcq") {
    const i = a.correctIndex;
    return options && typeof i === "number" && Number.isInteger(i) && i >= 0 && i < options.length ? options[i] : null;
  }
  return typeof a.modelAnswer === "string" && a.modelAnswer !== "" ? a.modelAnswer : null;
}
