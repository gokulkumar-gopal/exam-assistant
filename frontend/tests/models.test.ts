import { describe, expect, it } from "vitest";
import { defaultModelId, isCostlier, modelOptionLabel } from "../lib/models";
import type { AiModel } from "../lib/types";

const m = (id: string, name: string, tier: string, small: number, def = false): AiModel => ({
  id, name, provider: "x", tier, priceInPerMTok: 1, priceOutPerMTok: 1, approxJobCost: { small, heavy: small * 3 }, default: def,
});
const list = [
  m("a", "Gemini 3.1 Flash-Lite", "cheapest", 0.03, true),
  m("b", "Claude Haiku 4.5", "cheap", 0.08),
  m("c", "Claude Opus 5.5", "most capable", 0.33),
];

describe("modelOptionLabel", () => {
  it("formats name, tier and small cost with 2 decimals", () => {
    expect(modelOptionLabel(list[0])).toBe("Gemini 3.1 Flash-Lite · cheapest · about $0.03 per set");
    expect(modelOptionLabel(list[2])).toBe("Claude Opus 5.5 · most capable · about $0.33 per set");
  });
  it("rounds to 2 decimals", () => {
    expect(modelOptionLabel(m("z", "Z", "t", 0.1))).toBe("Z · t · about $0.10 per set");
    expect(modelOptionLabel(m("z", "Z", "t", 1))).toBe("Z · t · about $1.00 per set");
  });
});

describe("defaultModelId", () => {
  it("is the entry marked default", () => {
    expect(defaultModelId([list[0], { ...list[1], default: true }].map((x, i) => ({ ...x, default: i === 1 })))).toBe("b");
    expect(defaultModelId(list)).toBe("a");
  });
  it("is undefined for an empty list or no default flag", () => {
    expect(defaultModelId([])).toBeUndefined();
    expect(defaultModelId(list.map((x) => ({ ...x, default: false })))).toBeUndefined();
  });
});

describe("isCostlier", () => {
  it("is false for the first (cheapest) entry", () => {
    expect(isCostlier(list, "a")).toBe(false);
  });
  it("is true for any later entry", () => {
    expect(isCostlier(list, "b")).toBe(true);
    expect(isCostlier(list, "c")).toBe(true);
  });
  it("is false for undefined, unknown id or empty list", () => {
    expect(isCostlier(list, undefined)).toBe(false);
    expect(isCostlier(list, "nope")).toBe(false);
    expect(isCostlier([], "a")).toBe(false);
  });
});
