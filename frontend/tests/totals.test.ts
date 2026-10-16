import { describe, expect, it } from "vitest";
import { footerState, parseSources, sourceLimitError, totalMarks, questionCount } from "../lib/totals";

const zero = { 1: 0, 2: 0, 3: 0, 4: 0, 5: 0 };

describe("totalMarks / questionCount", () => {
  it("sums marks times count", () => {
    expect(totalMarks({ ...zero, 1: 2, 3: 2 })).toBe(8);
    expect(totalMarks(zero)).toBe(0);
    expect(questionCount({ ...zero, 1: 2, 3: 2 })).toBe(4);
  });
});

describe("footerState", () => {
  it("under target", () => {
    expect(footerState(28, 30, 12, true)).toEqual({ tone: "accent", text: "2 to go · 12 questions", generateEnabled: false });
  });
  it("at target", () => {
    expect(footerState(30, 30, 12, true)).toEqual({ tone: "ok", text: "Ready to generate · 12 questions", generateEnabled: true });
  });
  it("over target", () => {
    expect(footerState(32, 30, 12, true)).toEqual({ tone: "bad", text: "2 over · 12 questions", generateEnabled: false });
  });
  it("no source forces disabled even at target", () => {
    const s = footerState(30, 30, 12, false);
    expect(s.generateEnabled).toBe(false);
    expect(s.tone).toBe("ok");
  });
  it("singular question", () => {
    expect(footerState(5, 30, 1, true).text).toBe("25 to go · 1 question");
  });
});

describe("parseSources", () => {
  const ready = ["a", "b", "c"];
  it("keeps known ids in param order, drops unknown and duplicates", () => {
    expect(parseSources("c,x,a,c, ,b", ready)).toEqual(["c", "a", "b"]);
  });
  it("null or empty gives none", () => {
    expect(parseSources(null, ready)).toEqual([]);
    expect(parseSources("", ready)).toEqual([]);
  });
});

describe("sourceLimitError", () => {
  const d = (n: number, bytes = 1000, pages = 1) =>
    Array.from({ length: n }, (_, i) => ({ id: `d${i}`, sizeBytes: bytes, pages }));
  it("ok within caps", () => {
    expect(sourceLimitError(d(10, 2097152, 4))).toBeNull();
  });
  it("too many files", () => {
    expect(sourceLimitError(d(11))).toMatch(/10 files/);
  });
  it("too many bytes", () => {
    expect(sourceLimitError(d(2, 10485761))).toMatch(/20 MB/);
  });
  it("too many pages", () => {
    expect(sourceLimitError(d(2, 1000, 21))).toMatch(/40 pages/);
  });
  it("exactly at caps is fine", () => {
    expect(sourceLimitError(d(2, 10485760, 20))).toBeNull();
  });
});
