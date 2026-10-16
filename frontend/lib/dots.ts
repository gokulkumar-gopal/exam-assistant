// Question dots (fs 6.5). Pure. Answers are indexed by question position.

export type DotState = "answered" | "current" | "blank";
export type DotFill = "answered" | "blank";
export interface DotFlags { fill: DotFill; current: boolean }

/** An answer counts once it has non-whitespace text (an MCQ answer is the option text). */
export function isAnswered(a: string | undefined): boolean {
  return typeof a === "string" && a.trim() !== "";
}

/** Both facts a dot needs: its fill, and whether it is the current question (outline). */
export function dotFlags(i: number, answers: readonly (string | undefined)[], current: number): DotFlags {
  return { fill: isAnswered(answers[i]) ? "answered" : "blank", current: i === current };
}

/**
 * Single-word state. Precedence: current > answered > blank, so the current dot reports "current"
 * even when answered. Use dotFlags when the answered fill must also be drawn under the outline.
 */
export function dotState(i: number, answers: readonly (string | undefined)[], current: number): DotState {
  const f = dotFlags(i, answers, current);
  return f.current ? "current" : f.fill;
}
