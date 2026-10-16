import { describe, expect, it } from "vitest";
import { clampPeeks, defaultMaxPeeks, MAX_PEEKS, peeksValue, stepPeeks } from "../lib/peeks";

describe("defaultMaxPeeks", () => {
  it("is 3 for a personal practice exam and 0 when people are assigned", () => {
    expect(defaultMaxPeeks([])).toBe(3);
    expect(defaultMaxPeeks(["a@b.c"])).toBe(0);
    expect(defaultMaxPeeks(["a@b.c", "d@e.fg"])).toBe(0);
  });
  it("counts only real emails: empty and whitespace entries are nobody", () => {
    expect(defaultMaxPeeks([""])).toBe(3);
    expect(defaultMaxPeeks(["", "  ", "\n\t"])).toBe(3);
    expect(defaultMaxPeeks(["", " a@b.c "])).toBe(0);
  });
  it("text that is not an email does not count", () => {
    expect(defaultMaxPeeks(["nobody"])).toBe(3);
    expect(defaultMaxPeeks(["a@b"])).toBe(3);
    expect(defaultMaxPeeks(["nobody", "a@b.c"])).toBe(0);
  });
});

describe("clampPeeks", () => {
  it("clamps to 0..10", () => {
    expect(MAX_PEEKS).toBe(10);
    expect(clampPeeks(-1)).toBe(0);
    expect(clampPeeks(0)).toBe(0);
    expect(clampPeeks(7)).toBe(7);
    expect(clampPeeks(10)).toBe(10);
    expect(clampPeeks(11)).toBe(10);
  });
  it("respects a lower cap and bad numbers", () => {
    expect(clampPeeks(5, 4)).toBe(4);
    expect(clampPeeks(5, -2)).toBe(0);
    expect(clampPeeks(Number.NaN)).toBe(0);
    expect(clampPeeks(2.9)).toBe(2);
    expect(clampPeeks(Infinity)).toBe(10);
  });
});

describe("peeksValue (follows the default until edited)", () => {
  it("uses the default while not edited, and the edit once made", () => {
    expect(peeksValue(null, [], 10)).toBe(3);
    expect(peeksValue(null, ["a@b.c"], 10)).toBe(0);
    expect(peeksValue(5, ["a@b.c"], 10)).toBe(5);
    expect(peeksValue(0, [], 10)).toBe(0); // an edit to 0 is an edit, not "unset"
  });
  it("is capped by the question count", () => {
    expect(peeksValue(null, [], 2)).toBe(2);
    expect(peeksValue(8, [], 4)).toBe(4);
  });
});

describe("stepPeeks", () => {
  it("steps by one inside 0..cap", () => {
    expect(stepPeeks(3, 1, 10)).toBe(4);
    expect(stepPeeks(3, -1, 10)).toBe(2);
    expect(stepPeeks(0, -1, 10)).toBe(0);
    expect(stepPeeks(10, 1, 10)).toBe(10);
    expect(stepPeeks(4, 1, 4)).toBe(4);
  });
});
