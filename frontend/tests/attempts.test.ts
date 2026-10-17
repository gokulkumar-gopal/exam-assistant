import { describe, expect, it, vi } from "vitest";
import {
  attemptHeaderLabel, attemptScore, attemptsLabel, bestAttemptNo, bestPillText, canRetakeFrom, checksForAttempt,
  CLOSED_TOAST, LIMIT_TOAST, parseAttemptParam, resultPath, retakeErrorMessage, retakeExam, setScopedCheck,
} from "../lib/attempts";
import { ApiError, NETWORK_TOAST } from "../lib/api";
import { takeDecision } from "../lib/take";

describe("bestAttemptNo", () => {
  it("is the highest submitted score, the earliest on a tie", () => {
    expect(bestAttemptNo([
      { attemptNo: 1, status: "submitted", score: 28 },
      { attemptNo: 2, status: "submitted", score: 34.5 },
      { attemptNo: 3, status: "submitted", score: 34.5 },
    ])).toBe(2);
  });
  it("ignores unfinished attempts and ungraded (free text) ones", () => {
    expect(bestAttemptNo([{ attemptNo: 1, status: "submitted", score: 10 }, { attemptNo: 2, status: "in_progress", score: 99 }])).toBe(1);
    expect(bestAttemptNo([{ attemptNo: 1, status: "submitted" }, { attemptNo: 2, status: "submitted", score: null }])).toBeUndefined();
    expect(bestAttemptNo([])).toBeUndefined();
  });
  it("a zero score still counts", () => {
    expect(bestAttemptNo([{ attemptNo: 1, status: "submitted", score: 0 }])).toBe(1);
  });
});

describe("labels", () => {
  it("attemptsLabel only above one", () => {
    expect(attemptsLabel(undefined)).toBe("");
    expect(attemptsLabel(1)).toBe("");
    expect(attemptsLabel(2)).toBe("2 attempts");
  });
  it("bestPillText", () => {
    expect(bestPillText(34, 40, 3)).toBe("Best 34/40 · 3 attempts");
    expect(bestPillText(27.5, 30, 1)).toBe("Best 27.5/30");
    expect(bestPillText(undefined, 30, 2)).toBe("Best · 2 attempts");
  });
  it("header shows Attempt N only with more than one attempt", () => {
    expect(attemptHeaderLabel(2, 2)).toBe("Attempt 2");
    expect(attemptHeaderLabel(1, 1)).toBeNull();
    expect(attemptHeaderLabel(undefined, 1)).toBeNull();
    expect(attemptHeaderLabel(3, undefined)).toBeNull();
  });
  it("attemptScore is graded submitted attempts only", () => {
    expect(attemptScore({ status: "submitted", score: 34.5 }, 40)).toBe("34.5/40");
    expect(attemptScore({ status: "submitted" }, 40)).toBe("—");
    expect(attemptScore({ status: "in_progress", score: 3 }, 40)).toBe("—");
  });
});

describe("?attempt parsing and path", () => {
  it("accepts positive whole numbers only", () => {
    expect(parseAttemptParam("2")).toBe(2);
    for (const bad of [null, undefined, "", "0", "-1", "1.5", "abc", "1e3", "02", "9999999"]) expect(parseAttemptParam(bad)).toBeUndefined();
  });
  it("resultPath adds the query only for a chosen attempt", () => {
    expect(resultPath("e1", undefined)).toBe("/exams/e1/result");
    expect(resultPath("e1", 2)).toBe("/exams/e1/result?attempt=2");
  });
});

describe("canRetakeFrom", () => {
  const until = "2026-10-16T12:01:00Z";
  const before = Date.parse(until) - 1000;
  const sub = (n: number) => ({ attemptNo: n, status: "submitted" as const });
  it("latest submitted, before the deadline", () => {
    expect(canRetakeFrom([sub(1), sub(2)], until, before)).toBe(true);
  });
  it("not while the latest attempt is unfinished", () => {
    expect(canRetakeFrom([sub(1), { attemptNo: 2, status: "in_progress" }], until, before)).toBe(false);
  });
  it("not at or after acceptingUntil", () => {
    expect(canRetakeFrom([sub(1)], until, Date.parse(until))).toBe(false);
  });
  it("not at 50 attempts, nor with no attempts or a bad deadline", () => {
    expect(canRetakeFrom(Array.from({ length: 50 }, (_, i) => sub(i + 1)), until, before)).toBe(false);
    expect(canRetakeFrom([], until, before)).toBe(false);
    expect(canRetakeFrom(undefined, until, before)).toBe(false);
    expect(canRetakeFrom([sub(1)], "garbage", before)).toBe(false);
  });
});

describe("self-check marks are per attempt", () => {
  it("marks made on one attempt are not shown on another", () => {
    let s = setScopedCheck({ attemptNo: undefined, checks: {} }, 1, "q1", "got");
    expect(checksForAttempt(s, 1)).toEqual({ q1: "got" });
    expect(checksForAttempt(s, 2)).toEqual({});
    s = setScopedCheck(s, 2, "q2", "missed");
    expect(checksForAttempt(s, 2)).toEqual({ q2: "missed" });
    expect(checksForAttempt(s, 1)).toEqual({});
  });
  it("marks on the same attempt accumulate", () => {
    let s = setScopedCheck({ attemptNo: undefined, checks: {} }, 1, "q1", "got");
    s = setScopedCheck(s, 1, "q2", "missed");
    expect(checksForAttempt(s, 1)).toEqual({ q1: "got", q2: "missed" });
  });
});

describe("retakeExam", () => {
  it("POSTs /exams/:id/retake with no body and returns the new attempt number", async () => {
    const call = vi.fn().mockResolvedValue({ attemptNo: 3, serverTime: "t" });
    await expect(retakeExam("e 1", call)).resolves.toEqual({ attemptNo: 3, serverTime: "t" });
    expect(call).toHaveBeenCalledWith("POST", "/exams/e%201/retake", undefined, { noRetry: true });
  });
  it("rethrows the API error", async () => {
    const err = new ApiError(409, "exam_closed", "closed");
    await expect(retakeExam("e1", vi.fn().mockRejectedValue(err))).rejects.toBe(err);
  });
});

describe("retakeErrorMessage", () => {
  it("409 exam_closed", () => {
    expect(retakeErrorMessage(new ApiError(409, "exam_closed", "x"))).toBe("This exam has closed.");
    expect(CLOSED_TOAST).toBe("This exam has closed.");
  });
  it("409 at the limit", () => {
    expect(retakeErrorMessage(new ApiError(409, "conflict", "x"))).toBe("You have reached the limit of 50 attempts for this exam.");
    expect(LIMIT_TOAST).toBe("You have reached the limit of 50 attempts for this exam.");
  });
  it("network failures and anything else", () => {
    expect(retakeErrorMessage(new ApiError(503, "unavailable", "x"))).toBe(NETWORK_TOAST);
    expect(retakeErrorMessage(new TypeError("net"))).toBe(NETWORK_TOAST);
    expect(retakeErrorMessage(new ApiError(500, "internal", "x"))).toBe("Couldn't start a new attempt. Try again.");
  });
});

describe("takeDecision with retakes", () => {
  const closes = Date.parse("2026-10-16T12:00:00Z");
  const accepting = closes + 60_000;
  it("a submitted latest attempt still goes to the result page, a fresh retake takes", () => {
    expect(takeDecision("submitted", closes - 3600_000, closes, accepting)).toBe("result");
    expect(takeDecision("not_started", closes - 3600_000, closes, accepting)).toBe("take");
    expect(takeDecision("in_progress", closes - 3600_000, closes, accepting)).toBe("take");
  });
});
