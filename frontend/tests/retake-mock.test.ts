import { beforeEach, describe, expect, it } from "vitest";
import { mockFetch } from "../lib/mock";

const call = async (m: string, path: string, body?: unknown) => {
  const r = await mockFetch(m, path, body);
  return { status: r.status, body: r.status === 204 ? null : await r.json() };
};
beforeEach(() => { (globalThis as { __mockFlags?: unknown }).__mockFlags = {}; });

describe("mock retake contract", () => {
  it("list shows best score, count and canRetake", async () => {
    const { body } = await call("GET", "/exams?scope=closed");
    const e = body.exams.find((x: { id: string }) => x.id === "e24");
    expect(e).toMatchObject({ attemptStatus: "submitted", attemptCount: 2, canRetake: true, maxScore: 7 });
    expect(e.score).toBe(7);
  });
  it("result defaults to the latest submitted attempt and ?attempt=N returns that attempt's own answers", async () => {
    const latest = (await call("GET", "/exams/e24/result")).body;
    expect(latest.attemptNo).toBe(2);
    expect(latest.bestAttemptNo).toBe(2);
    expect(latest.attempts.map((a: { attemptNo: number }) => a.attemptNo)).toEqual([1, 2]);
    const first = (await call("GET", "/exams/e24/result?attempt=1")).body;
    expect(first.attemptNo).toBe(1);
    expect(first.score).toBeLessThan(latest.score);
    expect(first.questions.map((q: { yourAnswer: string }) => q.yourAnswer)).not.toEqual(latest.questions.map((q: { yourAnswer: string }) => q.yourAnswer));
    expect((await call("GET", "/exams/e24/result?attempt=9")).status).toBe(404);
  });
  it("retake creates attempt N+1, is idempotent while unfinished, and keeps earlier attempts", async () => {
    const a = await call("POST", "/exams/e24/retake");
    expect(a.status).toBe(200);
    const n = a.body.attemptNo;
    expect((await call("POST", "/exams/e24/retake")).body.attemptNo).toBe(n);
    const exam = (await call("GET", "/exams/e24")).body;
    expect(exam.attemptCount).toBe(n);
    expect(exam.attempt).toMatchObject({ attemptNo: n, status: "not_started" });
    // an unfinished retake never turns the default result into a 409
    expect((await call("GET", "/exams/e24/result")).body.attemptNo).toBe(2);
    expect((await call("GET", "/exams/e24/result?attempt=1")).status).toBe(200);
  });
  it("triggers: closed, limit and fail", async () => {
    await call("POST", "/exams/e3/retake"); // closed exam
    expect((await call("POST", "/exams/e3/retake")).body.error.code).toBe("exam_closed");
    const g = globalThis as { __mockFlags?: Record<string, string> };
    g.__mockFlags = { mockRetake: "limit" };
    const lim = await call("POST", "/exams/e3/retake");
    expect(lim.status).toBe(409);
    g.__mockFlags = { mockRetake: "fail" };
    expect(() => mockFetch("POST", "/exams/e3/retake", undefined)).toThrow(TypeError);
  });
  it("status rows carry attemptCount and history with marks and times, never answers", async () => {
    const { body } = await call("GET", "/exams/e4/status");
    const row = body.attempts[0];
    expect(row.attemptCount).toBe(3);
    expect(row.history).toHaveLength(3);
    expect(row.score).toBe(Math.max(...row.history.map((h: { score: number }) => h.score)));
    expect(JSON.stringify(body).toLowerCase()).not.toContain("answers");
  });
});
