import { describe, expect, it } from "vitest";
import { takeDecision, takeRedirect, answersByIndex, closesText, clampIndex, dotLabel, metaText, optionLetter } from "../lib/take";

describe("metaText", () => {
  it("is 1-based with the marks", () => {
    expect(metaText(1, 12, 3)).toBe("Question 2 of 12 · 3 marks");
  });
  it("uses the singular for 1 mark", () => {
    expect(metaText(0, 5, 1)).toBe("Question 1 of 5 · 1 mark");
  });
});

describe("clampIndex", () => {
  it("keeps an in-range index", () => {
    expect(clampIndex(4, 12)).toBe(4);
    expect(clampIndex(0, 12)).toBe(0);
    expect(clampIndex(11, 12)).toBe(11);
  });
  it("sends a negative, fractional-NaN or missing index to 0", () => {
    expect(clampIndex(-3, 12)).toBe(0);
    expect(clampIndex(Number.NaN, 12)).toBe(0);
    expect(clampIndex(undefined, 12)).toBe(0);
  });
  it("sends an index past the end to the last question", () => {
    expect(clampIndex(12, 12)).toBe(11);
    expect(clampIndex(99, 12)).toBe(11);
  });
  it("floors a fraction and handles an empty exam", () => {
    expect(clampIndex(2.9, 12)).toBe(2);
    expect(clampIndex(5, 0)).toBe(0);
  });
});

describe("closesText", () => {
  it("formats the absolute deadline in the given locale and zone", () => {
    expect(closesText("2026-10-18T18:00:00Z", "en-US", "UTC")).toBe("Closes Oct 18, 6:00 PM");
  });
  it("is empty for an unusable timestamp", () => {
    expect(closesText("nope", "en-US", "UTC")).toBe("");
  });
});

describe("optionLetter and dotLabel", () => {
  it("letters options", () => {
    expect([0, 1, 2, 3].map(optionLetter)).toEqual(["A.", "B.", "C.", "D."]);
  });
  it("labels dots with the state in words", () => {
    expect(dotLabel(2, true)).toBe("Question 3, answered");
    expect(dotLabel(2, false)).toBe("Question 3, blank");
  });
});

describe("answersByIndex", () => {
  it("maps the id-keyed answers onto question order", () => {
    const qs = [{ id: "a" }, { id: "b" }, { id: "c" }];
    expect(answersByIndex(qs, { c: "x", a: "y", zzz: "ignored" })).toEqual(["y", undefined, "x"]);
  });
});

describe("takeRedirect", () => {
  const until = "2026-10-18T18:01:00Z";
  const before = Date.parse("2026-10-18T12:00:00Z");
  const after = Date.parse("2026-10-18T18:01:00Z");
  it("stays when open and not submitted", () => {
    expect(takeRedirect("not_started", until, before)).toBeNull();
    expect(takeRedirect("in_progress", until, before)).toBeNull();
  });
  it("goes to the result once the exam is over (never started or not)", () => {
    expect(takeRedirect("not_started", until, after)).toBe("result");
    expect(takeRedirect("in_progress", until, after)).toBe("result");
  });
  it("goes to the result when already submitted", () => {
    expect(takeRedirect("submitted", until, before)).toBe("result");
  });
});

describe("takeDecision (open time)", () => {
  const closes = Date.parse("2026-10-18T12:00:00Z");
  const accepts = closes + 60_000;
  const at = (s: number) => closes + s * 1000;
  it("takes the exam before the deadline, whatever the status", () => {
    for (const st of [undefined, "not_started", "in_progress"]) expect(takeDecision(st, at(-1), closes, accepts)).toBe("take");
  });
  it("a submitted attempt always goes to the result", () => {
    expect(takeDecision("submitted", at(-100), closes, accepts)).toBe("result");
    expect(takeDecision("submitted", at(10), closes, accepts)).toBe("result");
  });
  it("past acceptingUntil goes to the result for every status", () => {
    for (const st of [undefined, "not_started", "in_progress"]) expect(takeDecision(st, accepts, closes, accepts)).toBe("result");
  });
  it("inside the grace window an in-progress attempt submits its resumed answers", () => {
    expect(takeDecision("in_progress", closes, closes, accepts)).toBe("submit-resumed");
    expect(takeDecision("in_progress", accepts - 1, closes, accepts)).toBe("submit-resumed");
  });
  it("inside the grace window a never-started exam (or no attempt) goes Home as closed, never auto-submitted", () => {
    expect(takeDecision("not_started", at(10), closes, accepts)).toBe("home-closed");
    expect(takeDecision(undefined, at(10), closes, accepts)).toBe("home-closed");
  });
});
