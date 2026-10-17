import { describe, expect, it } from "vitest";
import { canDecline, focusCandidates, countsLine, examMeta, rowView } from "../lib/exam-row";
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
    expect(rowView(base, before)).toEqual({ pill: { variant: "neutral", text: "Not started" }, action: "Start", retake: false });
  });
  it("in progress: warn and Resume", () => {
    expect(rowView({ ...base, attemptStatus: "in_progress" }, before))
      .toEqual({ pill: { variant: "warn", text: "In progress" }, action: "Resume", retake: false });
  });
  it("submitted: ok with score and View result", () => {
    expect(rowView({ ...base, attemptStatus: "submitted", score: 34, maxScore: 40 }, before))
      .toEqual({ pill: { variant: "ok", text: "Best 34/40" }, action: "View result", retake: false });
  });
  it("submitted with a .5 score", () => {
    expect(rowView({ ...base, attemptStatus: "submitted", score: 27.5, maxScore: 30 }, before).pill?.text).toBe("Best 27.5/30");
  });
  it("submitted free-text (no score): no suffix", () => {
    expect(rowView({ ...base, type: "free", attemptStatus: "submitted" }, before).pill)
      .toEqual({ variant: "ok", text: "Submitted" });
  });
  it("missed: bad and View result", () => {
    expect(rowView({ ...base, attemptStatus: "missed" }, CLOSES + 3_600_000))
      .toEqual({ pill: { variant: "bad", text: "Missed" }, action: "View result", retake: false });
  });
  it("past closesAt, before acceptingUntil: Closing… and Resume (even if not started)", () => {
    const v = { pill: { variant: "warn", text: "Closing…" }, action: "Resume", retake: false };
    expect(rowView({ ...base, attemptStatus: "in_progress" }, CLOSES + 30_000)).toEqual(v);
    expect(rowView(base, CLOSES + 30_000)).toEqual(v);
  });
  it("boundary: exactly closesAt is closing; exactly acceptingUntil is closed (missed)", () => {
    expect(rowView(base, CLOSES - 1).pill?.text).toBe("Not started");
    expect(rowView(base, CLOSES).pill?.text).toBe("Closing…");
    expect(rowView(base, UNTIL - 1).pill?.text).toBe("Closing…");
    expect(rowView(base, UNTIL)).toEqual({ pill: { variant: "bad", text: "Missed" }, action: "View result", retake: false });
  });
  it("a submitted exam stays submitted after the deadline", () => {
    expect(rowView({ ...base, attemptStatus: "submitted", score: 1, maxScore: 40 }, UNTIL + 1).pill?.text).toBe("Best 1/40");
  });
  it("declined: neutral pill and no action", () => {
    expect(rowView({ ...base, attemptStatus: "declined" }, before)).toEqual({ pill: { variant: "neutral", text: "Declined" }, action: null, retake: false });
  });
});

describe("rowView retakes", () => {
  const sub = { ...base, attemptStatus: "submitted" as const, score: 34, maxScore: 40 };
  it("best-score pill with the attempt count only above one", () => {
    expect(rowView({ ...sub, attemptCount: 1 }, before).pill?.text).toBe("Best 34/40");
    expect(rowView({ ...sub, attemptCount: 3, canRetake: true }, before).pill).toEqual({ variant: "ok", text: "Best 34/40 · 3 attempts" });
    expect(rowView({ ...sub, attemptCount: 2, score: 27.5, canRetake: true }, before).pill?.text).toBe("Best 27.5/40 · 2 attempts");
  });
  it("free text without a score: Submitted plus the count", () => {
    expect(rowView({ ...base, type: "free", attemptStatus: "submitted", attemptCount: 2, canRetake: true }, before).pill?.text).toBe("Submitted · 2 attempts");
  });
  it("Retake only while canRetake is true; View result stays the action", () => {
    expect(rowView({ ...sub, attemptCount: 3, canRetake: true }, before)).toMatchObject({ action: "View result", retake: true });
    expect(rowView({ ...sub, attemptCount: 1, canRetake: false }, before).retake).toBe(false);
    expect(rowView({ ...sub, attemptCount: 1 }, before).retake).toBe(false);
  });
  it("no Retake once the exam is no longer accepting, even if the server said so", () => {
    expect(rowView({ ...sub, attemptCount: 2, canRetake: true }, UNTIL).retake).toBe(false);
  });
  it("a retake in progress (submitted before, cannot retake, still accepting) offers Resume, not Retake", () => {
    const v = rowView({ ...sub, attemptCount: 2, canRetake: false }, before);
    expect(v.action).toBe("Resume");
    expect(v.retake).toBe(false);
    expect(v.pill?.text).toBe("Retake in progress · Best 34/40 · 2 attempts");
  });
  it("at the 50-attempt limit the row is plain View result", () => {
    expect(rowView({ ...sub, attemptCount: 50, canRetake: false }, before)).toMatchObject({ action: "View result", retake: false });
  });
  it("after the deadline a multi-attempt row is View result with no Retake", () => {
    expect(rowView({ ...sub, attemptCount: 3, canRetake: false }, UNTIL + 1)).toMatchObject({ action: "View result", retake: false });
  });
});

describe("examMeta", () => {
  const now = CLOSES - 2 * 86_400_000 - 1000;
  it("an exam being retaken keeps the best earlier score in the meta line", () => {
    const retaking = { ...base, attemptStatus: "in_progress" as const, score: 34, maxScore: 40, attemptCount: 2 };
    expect(examMeta(retaking, "u_me", now)).toBe("Assigned by Ms. Rao · closes in 2d · Best 34/40");
    expect(examMeta({ ...retaking, attemptCount: 1 }, "u_me", now)).toBe("Assigned by Ms. Rao · closes in 2d");
  });
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

describe("focusCandidates", () => {
  it("middle row: next, then previous", () => expect(focusCandidates(["a", "b", "c"], "b")).toEqual(["c", "a"]));
  it("last row: previous only", () => expect(focusCandidates(["a", "b", "c"], "c")).toEqual(["b"]));
  it("first row: next only", () => expect(focusCandidates(["a", "b"], "a")).toEqual(["b"]));
  it("only row or unknown id: none", () => {
    expect(focusCandidates(["a"], "a")).toEqual([]);
    expect(focusCandidates(["a"], "z")).toEqual([]);
  });
});
