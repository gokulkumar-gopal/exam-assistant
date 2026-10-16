import { describe, expect, it } from "vitest";
import { canDecline, countsLine, examMeta, rowView } from "../lib/exam-row";
import type { ExamListItem } from "../lib/types";

const CLOSES = Date.parse("2026-10-16T12:00:00Z");
const UNTIL = CLOSES + 60_000;
const base: ExamListItem = {
  id: "e1", title: "Quiz", createdBy: { id: "u_rao", name: "Ms. Rao" },
  closesAt: new Date(CLOSES).toISOString(), acceptingUntil: new Date(UNTIL).toISOString(),
  totalMarks: 40, type: "mcq", attemptStatus: "not_started",
};
const before = CLOSES - 3_600_000;

describe("rowView", () => {
  it("not started, open: neutral 'Not started' label and Start", () => {
    expect(rowView(base, before)).toEqual({ pill: { variant: "neutral", text: "Not started" }, action: "Start" });
  });
  it("in progress: warn and Resume", () => {
    expect(rowView({ ...base, attemptStatus: "in_progress" }, before))
      .toEqual({ pill: { variant: "warn", text: "In progress" }, action: "Resume" });
  });
  it("submitted: ok with score and View result", () => {
    expect(rowView({ ...base, attemptStatus: "submitted", score: 34, maxScore: 40 }, before))
      .toEqual({ pill: { variant: "ok", text: "Submitted · 34/40" }, action: "View result" });
  });
  it("submitted with a .5 score", () => {
    expect(rowView({ ...base, attemptStatus: "submitted", score: 27.5, maxScore: 30 }, before).pill?.text).toBe("Submitted · 27.5/30");
  });
  it("submitted free-text (no score): no suffix", () => {
    expect(rowView({ ...base, type: "free", attemptStatus: "submitted" }, before).pill)
      .toEqual({ variant: "ok", text: "Submitted" });
  });
  it("missed: bad and View result", () => {
    expect(rowView({ ...base, attemptStatus: "missed" }, CLOSES + 3_600_000))
      .toEqual({ pill: { variant: "bad", text: "Missed" }, action: "View result" });
  });
  it("past closesAt, before acceptingUntil: Closing… and Resume (even if not started)", () => {
    const v = { pill: { variant: "warn", text: "Closing…" }, action: "Resume" };
    expect(rowView({ ...base, attemptStatus: "in_progress" }, CLOSES + 30_000)).toEqual(v);
    expect(rowView(base, CLOSES + 30_000)).toEqual(v);
  });
  it("boundary: exactly closesAt is closing; exactly acceptingUntil is closed (missed)", () => {
    expect(rowView(base, CLOSES - 1).pill?.text).toBe("Not started");
    expect(rowView(base, CLOSES).pill?.text).toBe("Closing…");
    expect(rowView(base, UNTIL - 1).pill?.text).toBe("Closing…");
    expect(rowView(base, UNTIL)).toEqual({ pill: { variant: "bad", text: "Missed" }, action: "View result" });
  });
  it("a submitted exam stays submitted after the deadline", () => {
    expect(rowView({ ...base, attemptStatus: "submitted", score: 1, maxScore: 40 }, UNTIL + 1).pill?.text).toBe("Submitted · 1/40");
  });
  it("declined: neutral pill and no action", () => {
    expect(rowView({ ...base, attemptStatus: "declined" }, before)).toEqual({ pill: { variant: "neutral", text: "Declined" }, action: null });
  });
});

describe("examMeta", () => {
  const now = CLOSES - 2 * 86_400_000 - 1000;
  it("names the creator and the deadline", () => {
    expect(examMeta(base, "u_me", now)).toBe("Assigned by Ms. Rao · closes in 2d");
  });
  it("says You when self-created", () => {
    expect(examMeta({ ...base, createdBy: { id: "u_me", name: "Me" } }, "u_me", now)).toBe("Assigned by You · closes in 2d");
  });
  it("hours and minutes", () => {
    expect(examMeta(base, "x", CLOSES - 3 * 3_600_000)).toContain("closes in 3h");
    expect(examMeta(base, "x", CLOSES - 12 * 60_000)).toContain("closes in 12m");
  });
  it("closed after the deadline", () => {
    expect(examMeta(base, "x", CLOSES + 1)).toMatch(/ · closed Oct 1[56]$/);
  });
});

describe("countsLine", () => {
  it("lists all parts, declined only above 0", () => {
    expect(countsLine({ submitted: 12, inProgress: 3, notStarted: 5, declined: 1 })).toBe("12 submitted, 3 in progress, 5 not started, 1 declined");
    expect(countsLine({ submitted: 12, inProgress: 3, notStarted: 5, declined: 0 })).toBe("12 submitted, 3 in progress, 5 not started");
  });
  it("keeps zero counts", () => {
    expect(countsLine({ submitted: 0, inProgress: 0, notStarted: 0, declined: 0 })).toBe("0 submitted, 0 in progress, 0 not started");
  });
  it("missing counts give an empty line", () => {
    expect(countsLine(undefined)).toBe("");
  });
});

describe("canDecline", () => {
  it("open, not started, assigned by someone else: yes", () => {
    expect(canDecline(base, "u_me", before)).toBe(true);
  });
  it("self-created: never", () => {
    expect(canDecline({ ...base, createdBy: { id: "u_me", name: "Me" } }, "u_me", before)).toBe(false);
  });
  it("unknown viewer: no", () => {
    expect(canDecline(base, undefined, before)).toBe(false);
  });
  it("in progress, submitted, missed: no", () => {
    for (const attemptStatus of ["in_progress", "submitted", "missed"] as const) {
      expect(canDecline({ ...base, attemptStatus }, "u_me", before)).toBe(false);
    }
  });
  it("already declined: no", () => {
    expect(canDecline({ ...base, attemptStatus: "declined" }, "u_me", before)).toBe(false);
  });
  it("closing (past closesAt, before acceptingUntil) or past acceptingUntil: no", () => {
    expect(canDecline(base, "u_me", CLOSES + 1000)).toBe(false);
    expect(canDecline(base, "u_me", UNTIL + 1000)).toBe(false);
    expect(canDecline(base, "u_me", CLOSES)).toBe(false);
  });
  it("created-list rows (no attemptStatus): no", () => {
    expect(canDecline({ ...base, attemptStatus: undefined }, "u_me", before)).toBe(false);
  });
});
