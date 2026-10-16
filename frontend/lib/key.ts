// Pure helpers for the result page (fs 6.7). The answer key is only ever read through resultCardView and
// resultCardData: when the key is hidden nothing here returns, or lets a caller reach, correct-answer material.
import { scoreText } from "./format";
import { submittedCell } from "./status";

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

const isMcq = (q: ResultCardInput) => Array.isArray(q.options) || typeof q.correct === "boolean" || typeof q.correctIndex === "number";
const hasModel = (q: ResultCardInput) => typeof q.modelAnswer === "string" && q.modelAnswer !== "";
const hasCorrect = (q: ResultCardInput) =>
  (typeof q.correctAnswer === "string" && q.correctAnswer !== "") || typeof q.correctIndex === "number";

/** Whether the correctness pill and the correct-answer line may be drawn. Both are always false while the key is hidden. */
export function resultCardView(q: ResultCardInput, keyVisible: boolean): CardView {
  if (!keyVisible) return { showPill: false, showCorrect: false };
  return { showPill: isMcq(q), showCorrect: isMcq(q) ? hasCorrect(q) : hasModel(q) };
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
}

/** The only shape the card component receives. Key fields are copied in only when resultCardView allows them. */
export function resultCardData(q: ResultCardInput, keyVisible: boolean, number: number): ResultCardData {
  const view = resultCardView(q, keyVisible);
  const kind = isMcq(q) ? "mcq" : "free";
  const out: ResultCardData = {
    number,
    kind,
    text: q.text ?? "",
    yourAnswer: q.yourAnswer ? q.yourAnswer : "No answer",
    pill: null,
    canSelfCheck: false,
  };
  if (view.showPill && typeof q.correct === "boolean") {
    out.pill = q.correct ? { variant: "ok", text: "✓ Correct" } : { variant: "bad", text: "✗ Incorrect" };
  }
  if (view.showCorrect) {
    if (kind === "mcq") {
      out.correctText = q.correctAnswer || (typeof q.correctIndex === "number" ? q.options?.[q.correctIndex] : undefined);
    } else {
      out.modelAnswer = q.modelAnswer as string;
      out.canSelfCheck = true;
    }
  }
  return out;
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
