import { describe, expect, it } from "vitest";
import { gapNotice, orderQuestions } from "../lib/exam-order";
import type { Marks, Question } from "../lib/types";

const mk = (marks: Marks[]): Question[] => marks.map((m, i) => ({ id: `q${i}`, marks: m, text: `t${i}` }));

describe("orderQuestions", () => {
  it("is ascending by marks and stable for equal marks", () => {
    const out = orderQuestions(mk([3, 1, 2, 1]), false, 7);
    expect(out.map((q) => q.marks)).toEqual([1, 1, 2, 3]);
    expect(out.map((q) => q.id)).toEqual(["q1", "q3", "q2", "q0"]);
  });
  it("shuffle reorders the same set with no loss or duplication", () => {
    const qs = mk([1, 1, 2, 2, 3, 3, 4, 5]);
    const out = orderQuestions(qs, true, 12345);
    expect(out.map((q) => q.id).sort()).toEqual(qs.map((q) => q.id).sort());
    expect(out.map((q) => q.id)).not.toEqual(orderQuestions(qs, false, 12345).map((q) => q.id));
  });
  it("is deterministic per seed and can differ between seeds", () => {
    const qs = mk([1, 1, 2, 2, 3, 3, 4, 5]);
    const ids = (s: number) => orderQuestions(qs, true, s).map((q) => q.id).join();
    expect(ids(5)).toBe(ids(5));
    const all = new Set([1, 2, 3, 4, 5, 6, 7, 8].map(ids));
    expect(all.size).toBeGreaterThan(1);
  });
  it("leaves the input array unmodified", () => {
    const qs = mk([3, 1, 2, 1]);
    const copy = qs.map((q) => q.id);
    orderQuestions(qs, false, 1);
    orderQuestions(qs, true, 1);
    expect(qs.map((q) => q.id)).toEqual(copy);
  });
});

describe("gapNotice", () => {
  it("caps k at 5 and is null at or over target", () => {
    expect(gapNotice(30, 27)).toEqual({ k: 3 });
    expect(gapNotice(30, 20)).toEqual({ k: 5 });
    expect(gapNotice(30, 30)).toBeNull();
    expect(gapNotice(30, 35)).toBeNull();
  });
});
