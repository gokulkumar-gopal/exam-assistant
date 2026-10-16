import { describe, expect, it } from "vitest";
import { timerBucket, timerView } from "../lib/timer";

const closes = "2026-10-18T12:00:00.000Z";
const T = Date.parse(closes);

describe("timerView", () => {
  it("shows minutes and seconds left", () => {
    expect(timerView(closes, T - (42 * 60 + 10) * 1000)).toEqual({ text: "42:10 left", low: false });
  });
  it("uses server time: a skewed device clock does not change the remaining time", () => {
    const deviceNow = T - 10 * 60_000 + 2 * 3_600_000; // device runs 2 h fast
    const offset = -2 * 3_600_000; // serverTime - deviceTime, learned once
    expect(timerView(closes, deviceNow + offset)).toEqual({ text: "10:00 left", low: false });
    // Without the offset the same device clock would read as already closed.
    expect(timerView(closes, deviceNow).text).toBe("Closing…");
  });
  it("is low under 5:00 and not at exactly 5:00", () => {
    expect(timerView(closes, T - 300_000)).toEqual({ text: "5:00 left", low: false });
    expect(timerView(closes, T - 299_000)).toEqual({ text: "4:59 left", low: true });
  });
  it("rounds a part second up so it never shows 0:00 before Closing…", () => {
    expect(timerView(closes, T - 400)).toEqual({ text: "0:01 left", low: true });
  });
  it("reads Closing… at 0 and after", () => {
    expect(timerView(closes, T)).toEqual({ text: "Closing…", low: true });
    expect(timerView(closes, T + 30_000)).toEqual({ text: "Closing…", low: true });
  });
  it("shows hours when an hour or more is left", () => {
    expect(timerView(closes, T - (3600 + 125) * 1000).text).toBe("1:02:05 left");
  });
  it("is neutral for an unusable timestamp", () => {
    expect(timerView("nope", T)).toEqual({ text: "--:--", low: false });
  });
});

describe("timerBucket (screen reader announcements only at thresholds)", () => {
  it("changes only at 5:00, 1:00 and 0", () => {
    const b = (secLeft: number) => timerBucket(closes, T - secLeft * 1000);
    expect(b(1800)).toBe("");
    expect(b(301)).toBe("");
    expect(b(300)).toBe("5 minutes left");
    expect(b(61)).toBe("5 minutes left");
    expect(b(60)).toBe("1 minute left");
    expect(b(1)).toBe("1 minute left");
    expect(b(0)).toBe("Time is up. Closing…");
  });
  it("is the same string for every second inside a band", () => {
    const set = new Set<string>();
    for (let s = 299; s > 60; s--) set.add(timerBucket(closes, T - s * 1000));
    expect(set.size).toBe(1);
  });
});
