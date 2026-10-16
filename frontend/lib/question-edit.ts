// Validation for the inline question editor (fs 6.4 Edit).
import type { ExamType } from "./types";

export interface QuestionDraft { text: string; options?: string[]; correctIndex?: number; modelAnswer?: string }

/** An error message, or null when the draft can be saved. */
export function validateQuestionEdit(d: QuestionDraft, type: ExamType): string | null {
  if (!d.text.trim()) return "Write the question.";
  if (type !== "mcq") {
    // A free-text question that has a model answer must keep a non-empty one.
    return d.modelAnswer !== undefined && !d.modelAnswer.trim() ? "Write the model answer." : null;
  }
  const opts = d.options ?? [];
  if (opts.length !== 4 || opts.some((o) => !o.trim())) return "Fill in all four options.";
  const norm = opts.map((o) => o.trim().toLowerCase());
  if (new Set(norm).size !== 4) return "Make the four options different.";
  if (d.correctIndex === undefined || !Number.isInteger(d.correctIndex) || d.correctIndex < 0 || d.correctIndex > 3) {
    return "Mark one option as correct.";
  }
  return null;
}
