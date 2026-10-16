// Pure helpers for the take screen (fs 6.5).

/** "Question 2 of 12 · 3 marks" (index is 0-based). */
export function metaText(index: number, total: number, marks: number): string {
  return `Question ${index + 1} of ${total} · ${marks} mark${marks === 1 ? "" : "s"}`;
}

/** Resume index: missing, NaN or negative -> 0; past the end -> the last question; fractions floor. */
export function clampIndex(i: number | undefined, total: number): number {
  if (total <= 0 || typeof i !== "number" || !Number.isFinite(i) || i < 0) return 0;
  return Math.min(Math.floor(i), total - 1);
}

/**
 * "Closes Oct 18, 6:00 PM" from the server deadline, in the viewer's locale and zone by default.
 * Empty string when the timestamp is unusable.
 */
export function closesText(iso: string, locale?: string, timeZone?: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  const f = new Intl.DateTimeFormat(locale, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit", timeZone });
  return `Closes ${f.format(new Date(t))}`;
}

export const optionLetter = (i: number): string => `${String.fromCharCode(65 + i)}.`;

export const dotLabel = (i: number, answered: boolean): string => `Question ${i + 1}, ${answered ? "answered" : "blank"}`;

/** id-keyed answers (the API shape) -> answers in question order (what the dots read). */
export function answersByIndex(questions: readonly { id: string }[], answers: Record<string, string>): (string | undefined)[] {
  return questions.map((q) => answers[q.id]);
}

/**
 * Where an opened exam should go instead of the take screen: the result page when the exam is over
 * (from acceptingUntil, the one server cutoff) or already submitted. Otherwise null (stay and take it).
 * A never-started attempt on a closed exam is the spec case; a submitted one cannot be edited either.
 */
export function takeRedirect(status: string | undefined, acceptingUntilIso: string, nowMs: number): "result" | null {
  const until = Date.parse(acceptingUntilIso);
  if (status === "submitted") return "result";
  if (!Number.isNaN(until) && nowMs >= until) return "result";
  return null;
}

export type TakeDecision = "take" | "submit-resumed" | "home-closed" | "result";

/**
 * What opening the take/review screen does (fs 6.5 "Open after deadline"). Submitted, or past acceptingUntil
 * (the one server cutoff): the result. Inside the 60 s grace window after closesAt: an in-progress attempt submits
 * its resumed answers; a never-started one (or no attempt) is NOT submitted blank, it goes Home as closed.
 */
export function takeDecision(status: string | undefined, nowMs: number, closesMs: number, acceptingMs: number): TakeDecision {
  if (status === "submitted") return "result";
  if (!Number.isNaN(acceptingMs) && nowMs >= acceptingMs) return "result";
  if (!Number.isNaN(closesMs) && nowMs >= closesMs) return status === "in_progress" ? "submit-resumed" : "home-closed";
  return "take";
}

export { timerView } from "./timer";
