// Pure helpers for the result page (fs 6.7). The answer key is only ever read through resultCardView and
// resultCardData: when the key is hidden nothing here returns, or lets a caller reach, correct-answer material.
import { PEEKS_UI } from "./features";
import { scoreText } from "./format";
import { submittedCell } from "./status";
import type { ExamType } from "./types";

/** A result question as the API sends it. The key fields may be present even when they must not be shown. */
export interface ResultCardInput {
  id?: string;
  marks?: number;
  text?: string;
  options?: string[];
  yourAnswer?: string;
  correct?: boolean;
  correctAnswer?: string;
  correctIndex?: number;
  modelAnswer?: string | null;
  revealed?: boolean;
}

export interface CardView { showPill: boolean; showCorrect: boolean }

const hasModel = (q: ResultCardInput) => typeof q.modelAnswer === "string" && q.modelAnswer !== "";
const hasCorrect = (q: ResultCardInput) =>
  (typeof q.correctAnswer === "string" && q.correctAnswer !== "") || typeof q.correctIndex === "number";

/**
 * Whether the correctness pill and the correct-answer line may be drawn. Both are always false while the key is hidden.
 * The card kind comes from the exam type only: payload fields (options, correct, correctIndex) may carry zero values
 * for free text and never decide it.
 */
export function resultCardView(q: ResultCardInput, keyVisible: boolean, type: ExamType): CardView {
  if (!keyVisible) return { showPill: false, showCorrect: false };
  return type === "mcq" ? { showPill: typeof q.correct === "boolean" || hasCorrect(q), showCorrect: hasCorrect(q) } : { showPill: false, showCorrect: hasModel(q) };
}

export interface ResultCardData {
  number: number;
  kind: "mcq" | "free";
  text: string;
  yourAnswer: string;
  /** Text label plus colour (never colour alone). Null while the key is hidden. */
  pill: { variant: "ok" | "bad"; text: string } | null;
  correctText?: string;
  modelAnswer?: string;
  /** Free text with a visible model answer: the I got it / I missed it pair is offered. */
  canSelfCheck: boolean;
  /** (P1) The user revealed this answer mid-exam: a neutral "Answer revealed" marker, never a ✓ or ✗. */
  revealed: boolean;
  /**
   * (P1) The answer the user revealed, only while the key is hidden (the one answer they have already seen).
   * With a visible key the normal correct-answer line is used instead. Never set for an unrevealed question.
   */
  revealedText?: string;
}

/** Marker text on a revealed question. */
export const REVEALED_MARKER = "Answer revealed";

const mcqAnswerText = (q: ResultCardInput): string | undefined =>
  q.correctAnswer || (typeof q.correctIndex === "number" ? q.options?.[q.correctIndex] : undefined) || undefined;

/** The only shape the card component receives. Key fields are copied in only when resultCardView allows them. */
export function resultCardData(q: ResultCardInput, keyVisible: boolean, number: number, type: ExamType, peeksUi: boolean = PEEKS_UI): ResultCardData {
  const view = resultCardView(q, keyVisible, type);
  const kind = type;
  const out: ResultCardData = {
    number,
    kind,
    text: q.text ?? "",
    yourAnswer: q.yourAnswer?.trim() ? q.yourAnswer : "No answer",
    pill: null,
    canSelfCheck: false,
    // Only a real `true` counts, and only while the peeks UI is on.
    revealed: peeksUi && q.revealed === true,
  };
  if (view.showPill && typeof q.correct === "boolean") {
    out.pill = q.correct ? { variant: "ok", text: "✓ Correct" } : { variant: "bad", text: "✗ Incorrect" };
  }
  if (view.showCorrect) {
    if (kind === "mcq") {
      out.correctText = mcqAnswerText(q);
    } else {
      out.modelAnswer = q.modelAnswer as string;
      out.canSelfCheck = true;
    }
  } else if (out.revealed) {
    // Hidden key: the one answer the user already saw. Copied only for a revealed question, never anything else.
    const text = type === "mcq" ? mcqAnswerText(q) : hasModel(q) ? (q.modelAnswer as string) : undefined;
    if (text !== undefined) out.revealedText = text;
  }
  return out;
}

export interface PeekLines { withoutPenalty: string | null; used: string | null; noPeeks: boolean }

/**
 * fs 6.7 peek lines. `scoreBeforePeeks` is the score as if nothing was revealed (null when not known).
 * Used (> 0): "Score without the peek penalty: 36 / 40" and "Peeks used: N of M". Allowed (maxPeeks > 0) and none used:
 * the "No peeks" badge. Nothing for an attempt that was not submitted, when peeking was off, or with the peeks UI cut.
 */
export function peekLines(
  a: { maxPeeks: number; peeksUsed: number; maxScore: number; scoreBeforePeeks: number | null; submitted: boolean },
  peeksUi: boolean = PEEKS_UI,
): PeekLines {
  const none: PeekLines = { withoutPenalty: null, used: null, noPeeks: false };
  if (!peeksUi || !a.submitted) return none;
  if (a.peeksUsed > 0) {
    return {
      withoutPenalty: a.scoreBeforePeeks === null ? null : `Score without the peek penalty: ${scoreFraction(a.scoreBeforePeeks, a.maxScore)}`,
      used: `Peeks used: ${a.peeksUsed} of ${a.maxPeeks}`,
      noPeeks: false,
    };
  }
  return { ...none, noPeeks: a.maxPeeks > 0 };
}

/** "27.5 / 40"; integers never carry ".0". */
export function scoreFraction(score: number, max: number): string {
  return `${scoreText(score)} / ${scoreText(max)}`;
}

/** "Score 34 / 40". */
export function scoreSubtitle(score: number, max: number): string {
  return `Score ${scoreFraction(score, max)}`;
}

/** "Answers available after Oct 18, 6:01 PM." Never an empty gap: an unusable time reads "after the exam closes". */
export function keyAvailableLine(iso: string | null | undefined): string {
  const when = iso ? submittedCell(iso) : "—";
  return when === "—" ? "Answers available after the exam closes." : `Answers available after ${when}.`;
}

export type Check = "got" | "missed";
export type Checks = Record<string, Check>;

/** Latest choice wins; the input is not mutated. */
export function setCheck(checks: Checks, id: string, value: Check): Checks {
  return { ...checks, [id]: value };
}

/**
 * Local score for self-checked free text (bs 9.3 / fs 6.7: no endpoint, nothing is sent to the server).
 * `base` is the server score (auto-graded part), or null when nothing is graded. "I got it" earns the question's
 * marks, or half of them when the answer was revealed (peeked). The total never exceeds maxScore.
 * Null while there is no server score and no valid check.
 */
export function selfCheckScore(args: {
  base: number | null;
  maxScore: number;
  questions: readonly { id: string; marks: number; revealed: boolean }[];
  checks: Checks;
}): number | null {
  let checked = 0;
  let earned = 0;
  for (const q of args.questions) {
    const c = args.checks[q.id];
    if (!c) continue;
    checked++;
    if (c === "got") earned += q.revealed ? q.marks / 2 : q.marks;
  }
  if (args.base === null && checked === 0) return null;
  return Math.min(args.maxScore, (args.base ?? 0) + earned);
}
