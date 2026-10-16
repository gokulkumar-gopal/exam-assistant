import { describe, expect, it } from "vitest";
import { scoreCell, statusPill, statusSubtitle, submittedCell } from "../lib/status";
import { parseEmails, assignHelper } from "../lib/publish";

describe("statusSubtitle", () => {
  it("lists the three counts", () => {
    expect(statusSubtitle({ submitted: 2, inProgress: 1, notStarted: 1, declined: 0 })).toBe("2 submitted, 1 in progress, 1 not started");
  });
  it("adds declined only above 0", () => {
    expect(statusSubtitle({ submitted: 2, inProgress: 1, notStarted: 1, declined: 1 })).toBe("2 submitted, 1 in progress, 1 not started, 1 declined");
  });
  it("keeps zero counts and never changes the wording for 1 (spec text is literal)", () => {
    expect(statusSubtitle({ submitted: 0, inProgress: 0, notStarted: 0, declined: 0 })).toBe("0 submitted, 0 in progress, 0 not started");
    expect(statusSubtitle({ submitted: 1, inProgress: 0, notStarted: 0, declined: 3 })).toBe("1 submitted, 0 in progress, 0 not started, 3 declined");
  });
});

describe("scoreCell", () => {
  it("is an em dash until graded", () => {
    expect(scoreCell({ status: "not_started" }, 40)).toBe("—");
    expect(scoreCell({ status: "in_progress" }, 40)).toBe("—");
    expect(scoreCell({ status: "declined" }, 40)).toBe("—");
    expect(scoreCell({ status: "submitted" }, 40)).toBe("—");
    expect(scoreCell({ status: "submitted", score: undefined }, 40)).toBe("—");
  });
  it("shows score/total, never rounding .5", () => {
    expect(scoreCell({ status: "submitted", score: 34 }, 40)).toBe("34/40");
    expect(scoreCell({ status: "submitted", score: 27.5 }, 30)).toBe("27.5/30");
    expect(scoreCell({ status: "submitted", score: 0 }, 30)).toBe("0/30");
  });
});

describe("statusPill", () => {
  it("maps each status; declined is a bad outline pill", () => {
    expect(statusPill("not_started")).toEqual({ variant: "neutral", text: "Not started", outline: false });
    expect(statusPill("in_progress")).toEqual({ variant: "warn", text: "In progress", outline: false });
    expect(statusPill("submitted")).toEqual({ variant: "ok", text: "Submitted", outline: false });
    expect(statusPill("declined")).toEqual({ variant: "bad", text: "Declined", outline: true });
  });
});

describe("submittedCell", () => {
  it("is an em dash without a timestamp", () => {
    expect(submittedCell(undefined)).toBe("—");
    expect(submittedCell("garbage")).toBe("—");
  });
  it("formats a timestamp as short date and time", () => {
    expect(submittedCell("2026-10-16T12:00:00Z")).toMatch(/^Oct 1[56], \d{1,2}:\d{2}\s?[AP]M$/);
  });
});

describe("parseEmails for Assign", () => {
  it("splits on commas, newlines and spaces; trims, lowercases, dedupes", () => {
    expect(parseEmails("A@x.com b@y.org\nc@z.io,\n a@X.com  ", "teacher")).toEqual({ emails: ["a@x.com", "b@y.org", "c@z.io"], error: null });
  });
  it("counts people already on the exam against the cap but not re-assigned ones", () => {
    const existing = Array.from({ length: 9 }, (_, i) => `e${i}@x.com`);
    expect(parseEmails("new1@x.com", "student", existing).error).toBeNull();
    expect(parseEmails("new1@x.com new2@x.com", "student", existing).error).toMatch(/10/);
    expect(parseEmails("e0@x.com e1@x.com new1@x.com", "student", existing).error).toBeNull();
  });
});

describe("assignHelper", () => {
  it("shows the role cap text (stricter student text until the role is known)", () => {
    expect(assignHelper("student")).toBe("Student emails only · up to 10 people");
    expect(assignHelper(undefined)).toBe("Student emails only · up to 10 people");
    expect(assignHelper("teacher")).toBe("Up to 50 people");
    expect(assignHelper("parent")).toBe("Up to 50 people");
  });
});
