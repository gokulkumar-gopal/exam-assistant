import { describe, expect, it } from "vitest";
import { dotFlags, dotState, isAnswered } from "../lib/dots";

// Answers are indexed by question position (undefined = never answered).
const answers = ["Option A", undefined, "", "  ", "text"];

describe("isAnswered", () => {
  it("is true only for a non-blank string", () => {
    expect(isAnswered("Option A")).toBe(true);
    expect(isAnswered(undefined)).toBe(false);
    expect(isAnswered("")).toBe(false);
    expect(isAnswered("   ")).toBe(false);
  });
});

describe("dotState", () => {
  it("answered index that is not current is answered", () => {
    expect(dotState(0, answers, 4)).toBe("answered");
  });
  it("unanswered gives blank", () => {
    expect(dotState(1, answers, 0)).toBe("blank");
    expect(dotState(2, answers, 0)).toBe("blank");
    expect(dotState(3, answers, 0)).toBe("blank");
    expect(dotState(9, answers, 0)).toBe("blank"); // beyond the array
  });
  it("current wins the string result, answered or not", () => {
    expect(dotState(0, answers, 0)).toBe("current");
    expect(dotState(1, answers, 1)).toBe("current");
  });
});

describe("dotFlags", () => {
  it("an answered current dot keeps the answered fill and gets the current flag", () => {
    expect(dotFlags(0, answers, 0)).toEqual({ fill: "answered", current: true });
  });
  it("an unanswered current dot is blank fill with the current flag", () => {
    expect(dotFlags(1, answers, 1)).toEqual({ fill: "blank", current: true });
  });
  it("others carry no current flag", () => {
    expect(dotFlags(0, answers, 4)).toEqual({ fill: "answered", current: false });
    expect(dotFlags(2, answers, 4)).toEqual({ fill: "blank", current: false });
  });
});
