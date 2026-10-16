"use client";
// Attempt state for the take screen: answers map (question id -> text), current index, dirty flag.
// No network here. F-4.2 plugs autosave into this: watch `answers`/`index`, PUT them, then call
// `markSaved(snapshot)` with what was sent; `dirty` stays true if the user changed anything since.
import { useCallback, useMemo, useReducer } from "react";
import { clampIndex } from "./take";

export interface AttemptState {
  answers: Record<string, string>;
  index: number;
  /** True when answers or index differ from the last saved (or loaded) snapshot. */
  dirty: boolean;
}
export interface Snapshot { answers: Record<string, string>; index: number }
export type AttemptAction =
  | { type: "answer"; id: string; value: string }
  | { type: "goto"; index: number; total: number }
  | { type: "saved"; snapshot: Snapshot };

export function initAttempt(saved: { answers: Record<string, string>; lastQuestionIndex: number }, total: number): AttemptState {
  return { answers: { ...saved.answers }, index: clampIndex(saved.lastQuestionIndex, total), dirty: false };
}

export function attemptReducer(s: AttemptState, a: AttemptAction): AttemptState {
  switch (a.type) {
    case "answer": {
      if ((s.answers[a.id] ?? "") === a.value) return s;
      const answers = { ...s.answers };
      // An empty answer is the same as no answer: drop the key so the API map stays small.
      if (a.value === "") delete answers[a.id];
      else answers[a.id] = a.value;
      return { ...s, answers, dirty: true };
    }
    case "goto": {
      const index = clampIndex(a.index, a.total);
      return index === s.index ? s : { ...s, index, dirty: true };
    }
    case "saved": {
      const same = a.snapshot.answers === s.answers && a.snapshot.index === s.index;
      return same ? { ...s, dirty: false } : s;
    }
  }
}

export function useAttempt(
  saved: { answers: Record<string, string>; lastQuestionIndex: number },
  total: number,
) {
  const [state, dispatch] = useReducer(attemptReducer, undefined, () => initAttempt(saved, total));
  const setAnswer = useCallback((id: string, value: string) => dispatch({ type: "answer", id, value }), []);
  const goTo = useCallback((index: number) => dispatch({ type: "goto", index, total }), [total]);
  const markSaved = useCallback((snapshot: Snapshot) => dispatch({ type: "saved", snapshot }), []);
  return useMemo(
    () => ({ answers: state.answers, index: state.index, dirty: state.dirty, setAnswer, goTo, markSaved }),
    [state, setAnswer, goTo, markSaved],
  );
}
