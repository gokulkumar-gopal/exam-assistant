import { describe, expect, it } from "vitest";
import { attemptReducer, initAttempt } from "../lib/attempt";

const init = () => initAttempt({ answers: { q1: "A" }, lastQuestionIndex: 3 }, 12);

describe("attemptReducer", () => {
  it("starts from the saved attempt, clean, with the index clamped", () => {
    const s = init();
    expect(s).toMatchObject({ answers: { q1: "A" }, index: 3, dirty: false });
    expect(initAttempt({ answers: {}, lastQuestionIndex: 40 }, 12).index).toBe(11);
  });
  it("setAnswer stores the value and marks dirty", () => {
    const s = attemptReducer(init(), { type: "answer", id: "q2", value: "B" });
    expect(s.answers).toEqual({ q1: "A", q2: "B" });
    expect(s.dirty).toBe(true);
  });
  it("setting the same value again is not a change", () => {
    const s = attemptReducer(init(), { type: "answer", id: "q1", value: "A" });
    expect(s.dirty).toBe(false);
  });
  it("clearing text to empty removes the key", () => {
    const s = attemptReducer(init(), { type: "answer", id: "q1", value: "" });
    expect(s.answers).toEqual({});
    expect(s.dirty).toBe(true);
  });
  it("moving changes the index (clamped) and marks dirty", () => {
    const s = attemptReducer(init(), { type: "goto", index: 99, total: 12 });
    expect(s.index).toBe(11);
    expect(s.dirty).toBe(true);
    expect(attemptReducer(init(), { type: "goto", index: 3, total: 12 }).dirty).toBe(false);
  });
  it("saved clears dirty only when nothing changed since the snapshot", () => {
    const dirty = attemptReducer(init(), { type: "answer", id: "q2", value: "B" });
    const snap = { answers: dirty.answers, index: dirty.index };
    expect(attemptReducer(dirty, { type: "saved", snapshot: snap }).dirty).toBe(false);
    const later = attemptReducer(dirty, { type: "answer", id: "q2", value: "C" });
    expect(attemptReducer(later, { type: "saved", snapshot: snap }).dirty).toBe(true);
  });
});
