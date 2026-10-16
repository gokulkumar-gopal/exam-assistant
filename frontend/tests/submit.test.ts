import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../lib/api";
import { createAutosaver } from "../lib/autosave";
import { autoSubmitAtZero, submitExam, submitDialogTitle, unansweredIndexes, unansweredLine } from "../lib/submit";

beforeEach(() => { vi.useFakeTimers(); });
afterEach(() => { vi.useRealTimers(); });

describe("submitExam", () => {
  it("posts answers and the last index to the submit endpoint and returns ok", async () => {
    const call = vi.fn(async () => ({ status: "submitted" }));
    const r = await submitExam("e 1", { q1: "a" }, 2, call as never);
    expect(r).toBe("ok");
    expect(call).toHaveBeenCalledTimes(1);
    expect(call).toHaveBeenCalledWith("POST", "/exams/e%201/submit", { answers: { q1: "a" }, lastQuestionIndex: 2 }, { noRetry: true });
  });
  it("maps 409 exam_closed to closed", async () => {
    const call = vi.fn(async () => { throw new ApiError(409, "exam_closed", "closed"); });
    expect(await submitExam("e1", {}, 0, call as never)).toBe("closed");
  });
  it("rethrows any other failure so the page can keep the answers and retry", async () => {
    await expect(submitExam("e1", {}, 0, (async () => { throw new ApiError(0, "network", "x"); }) as never)).rejects.toMatchObject({ status: 0 });
    await expect(submitExam("e1", {}, 0, (async () => { throw new ApiError(409, "conflict", "x"); }) as never)).rejects.toMatchObject({ code: "conflict" });
    await expect(submitExam("e1", {}, 0, (async () => { throw new ApiError(500, "internal", "x"); }) as never)).rejects.toMatchObject({ status: 500 });
  });
});

describe("unansweredLine", () => {
  it("lists the unanswered question numbers (1-based)", () => {
    expect(unansweredLine(["a", "b", undefined, "d", "e", "f", undefined], 7)).toBe("2 unanswered: question 3, 7");
  });
  it("one unanswered", () => {
    expect(unansweredLine(["a", "b", undefined], 3)).toBe("1 unanswered: question 3");
  });
  it("all answered", () => {
    expect(unansweredLine(["a", "b"], 2)).toBe("All questions answered.");
  });
  it("blank and whitespace-only answers count as unanswered; missing trailing entries too", () => {
    expect(unansweredLine(["a", "", "   "], 4)).toBe("3 unanswered: question 2, 3, 4");
    expect(unansweredIndexes(["a", "", "   "], 4)).toEqual([1, 2, 3]);
  });
});

describe("submitDialogTitle", () => {
  it("matches the spec for 2 and is singular for 1", () => {
    expect(submitDialogTitle(2)).toBe("Submit with 2 unanswered questions?");
    expect(submitDialogTitle(1)).toBe("Submit with 1 unanswered question?");
  });
});

describe("autoSubmitAtZero", () => {
  const snap = () => ({ answers: { q1: "now" }, lastQuestionIndex: 4 });

  it("submits once with the current answers and makes no autosave call before it", async () => {
    const put = vi.fn(async () => undefined);
    const order: string[] = [];
    const saver = createAutosaver({ put, snapshot: snap, onState: () => undefined });
    saver.change(); // a debounced save is pending when zero hits
    const submit = vi.fn(async () => { order.push("submit"); return "ok" as const; });
    const trigger = autoSubmitAtZero({ submit, snapshot: snap, stopAutosave: () => { order.push("stop"); saver.dispose(); } });
    expect(await trigger()).toBe("ok");
    await vi.advanceTimersByTimeAsync(5000);
    expect(submit).toHaveBeenCalledTimes(1);
    expect(submit).toHaveBeenCalledWith({ q1: "now" }, 4);
    expect(put).not.toHaveBeenCalled();
    expect(order).toEqual(["stop", "submit"]);
  });

  it("a double trigger submits once and both callers get the same outcome", async () => {
    const submit = vi.fn(async () => "ok" as const);
    const trigger = autoSubmitAtZero({ submit, snapshot: snap, stopAutosave: () => undefined });
    const [a, b] = await Promise.all([trigger(), trigger()]);
    expect(await trigger()).toBe("ok");
    expect([a, b]).toEqual(["ok", "ok"]);
    expect(submit).toHaveBeenCalledTimes(1);
  });

  it("reports closed when the server says the exam closed, without retrying", async () => {
    const submit = vi.fn(async () => "closed" as const);
    const trigger = autoSubmitAtZero({ submit, snapshot: snap, stopAutosave: () => undefined });
    expect(await trigger()).toBe("closed");
    expect(submit).toHaveBeenCalledTimes(1);
  });

  it("retries a network failure with the same frozen answers every 3 s while allowed", async () => {
    const s = { answers: { q1: "first" }, lastQuestionIndex: 1 };
    let n = 0;
    const submit = vi.fn(async () => { if (++n < 3) throw new ApiError(0, "network", "x"); return "ok" as const; });
    const trigger = autoSubmitAtZero({ submit, snapshot: () => s, stopAutosave: () => undefined, canRetry: () => true });
    const p = trigger();
    s.answers = { q1: "changed after zero" };
    await vi.advanceTimersByTimeAsync(2999);
    expect(submit).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(submit).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(3000);
    expect(await p).toBe("ok");
    expect(submit).toHaveBeenCalledTimes(3);
    for (const c of submit.mock.calls) expect(c).toEqual([{ q1: "first" }, 1]);
  });

  it("gives up (no more retries) once retrying is no longer allowed", async () => {
    const submit = vi.fn(async () => { throw new ApiError(0, "network", "x"); });
    let allowed = true;
    const trigger = autoSubmitAtZero({ submit, snapshot: snap, stopAutosave: () => undefined, canRetry: () => allowed });
    const p = trigger();
    await vi.advanceTimersByTimeAsync(0);
    allowed = false;
    await vi.advanceTimersByTimeAsync(3000);
    expect(await p).toBe("gave-up");
    expect(submit).toHaveBeenCalledTimes(2);
  });
});
