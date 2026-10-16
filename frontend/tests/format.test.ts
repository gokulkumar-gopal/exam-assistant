import { describe, expect, it } from "vitest";
import { deadlineText, scoreText } from "../lib/format";

// Fixed zone so the absolute date form is deterministic.
process.env.TZ = "UTC";

const NOW = Date.parse("2026-10-10T12:00:00Z");
const H = 3600_000;
const iso = (offsetMs: number) => new Date(NOW + offsetMs).toISOString();

describe("deadlineText", () => {
  it("shows hours under a day", () => {
    expect(deadlineText(iso(5 * H), NOW)).toBe("closes in 5h");
  });
  it("shows days under a week", () => {
    expect(deadlineText(iso(2 * 24 * H), NOW)).toBe("closes in 2d");
  });
  it("shows an absolute date from 7 days", () => {
    expect(deadlineText(iso(8 * 24 * H), NOW)).toBe("closes Oct 18");
  });
  it("shows minutes under an hour", () => {
    expect(deadlineText(iso(30 * 60_000), NOW)).toBe("closes in 30m");
  });
  it("shows closed with a date once past", () => {
    expect(deadlineText("2026-10-08T09:00:00Z", NOW)).toBe("closed Oct 8");
  });
});

describe("scoreText", () => {
  it("keeps a half mark", () => expect(scoreText(27.5)).toBe("27.5"));
  it("keeps a whole mark without decimals", () => expect(scoreText(34)).toBe("34"));
  it("never rounds", () => expect(scoreText(0.5)).toBe("0.5"));
});
