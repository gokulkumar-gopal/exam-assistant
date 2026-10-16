import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { autosaveText, createAutosaver, type AutosaveState } from "../lib/autosave";

const snap = { answers: { q1: "a" } as Record<string, string>, lastQuestionIndex: 0 };

function setup(putImpl?: (a: Record<string, string>, i: number) => Promise<void>) {
  const calls: [Record<string, string>, number][] = [];
  const states: AutosaveState[] = [];
  const put = vi.fn(async (a: Record<string, string>, i: number) => {
    calls.push([a, i]);
    if (putImpl) await putImpl(a, i);
  });
  const s = { ...snap, answers: { ...snap.answers } };
  const saver = createAutosaver({
    put, debounceMs: 500, retryMs: 3000, onState: (st) => states.push(st),
    snapshot: () => ({ answers: s.answers, lastQuestionIndex: s.lastQuestionIndex }),
  });
  return { saver, put, calls, states, s };
}
const conflict = () => Object.assign(new Error("closed"), { status: 409 });

beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(new Date("2026-10-18T10:42:00")); });
afterEach(() => { vi.useRealTimers(); });

describe("createAutosaver", () => {
  it("debounces rapid changes to one put 500 ms after the last", async () => {
    const { saver, put, s } = setup();
    saver.change();
    await vi.advanceTimersByTimeAsync(300);
    s.answers = { q1: "ab" };
    saver.change();
    await vi.advanceTimersByTimeAsync(499);
    expect(put).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(put).toHaveBeenCalledTimes(1);
    expect(put).toHaveBeenCalledWith({ q1: "ab" }, 0);
  });

  it("reports the last-success time", async () => {
    const { saver, states } = setup();
    saver.change();
    await vi.advanceTimersByTimeAsync(500);
    const last = states[states.length - 1];
    expect(last.status).toBe("saved");
    expect(last.savedAt).toBe(Date.now());
    expect(autosaveText(last, (ms) => `${new Date(ms).getHours()}:${new Date(ms).getMinutes()}`)).toBe("Saved 10:42");
  });

  it("retries every 3000 ms after a failure with Not saved. Retrying… and keeps the old success time", async () => {
    let fails = 3;
    const { saver, put, states } = setup(async () => { if (fails-- > 0) throw new Error("net"); });
    saver.change();
    await vi.advanceTimersByTimeAsync(500);
    expect(put).toHaveBeenCalledTimes(1);
    expect(states.at(-1)).toEqual({ status: "retrying", savedAt: null });
    expect(autosaveText(states.at(-1)!, () => "x")).toBe("Not saved. Retrying…");
    await vi.advanceTimersByTimeAsync(2999);
    expect(put).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(put).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(3000);
    expect(put).toHaveBeenCalledTimes(3);
    await vi.advanceTimersByTimeAsync(3000);
    expect(put).toHaveBeenCalledTimes(4); // this one succeeds
    expect(states.at(-1)!.status).toBe("saved");
    await vi.advanceTimersByTimeAsync(10_000);
    expect(put).toHaveBeenCalledTimes(4); // stops retrying once saved
  });

  it("a 409 stops retrying and reports closed", async () => {
    const { saver, put, states } = setup(async () => { throw conflict(); });
    saver.change();
    await vi.advanceTimersByTimeAsync(500);
    expect(states.at(-1)!.status).toBe("closed");
    await vi.advanceTimersByTimeAsync(30_000);
    expect(put).toHaveBeenCalledTimes(1);
    saver.change();
    await vi.advanceTimersByTimeAsync(5000);
    expect(put).toHaveBeenCalledTimes(1);
    expect(await saver.flush()).toBe("closed");
  });

  it("flush saves a pending change now and resolves saved", async () => {
    const { saver, put } = setup();
    saver.change();
    expect(await saver.flush()).toBe("saved");
    expect(put).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(2000);
    expect(put).toHaveBeenCalledTimes(1); // the debounce timer was cancelled
  });

  it("flush with nothing pending does not call put", async () => {
    const { saver, put } = setup();
    expect(await saver.flush()).toBe("saved");
    expect(put).not.toHaveBeenCalled();
  });

  it("flush that fails resolves failed, shows retrying and keeps retrying", async () => {
    let fail = true;
    const { saver, put, states } = setup(async () => { if (fail) throw new Error("net"); });
    saver.change();
    expect(await saver.flush()).toBe("failed");
    expect(states.at(-1)!.status).toBe("retrying");
    fail = false;
    await vi.advanceTimersByTimeAsync(3000);
    expect(put).toHaveBeenCalledTimes(2);
    expect(states.at(-1)!.status).toBe("saved");
    expect(await saver.flush()).toBe("saved");
  });

  it("a change during an in-flight put is saved afterwards, never concurrently", async () => {
    let release: () => void = () => {};
    let n = 0;
    const { saver, put, s } = setup(() => (n++ === 0 ? new Promise<void>((r) => { release = r; }) : Promise.resolve()));
    saver.change();
    await vi.advanceTimersByTimeAsync(500);
    expect(put).toHaveBeenCalledTimes(1);
    s.answers = { q1: "abc" };
    saver.change();
    await vi.advanceTimersByTimeAsync(600);
    expect(put).toHaveBeenCalledTimes(1); // still waiting on the first
    release();
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(600);
    expect(put).toHaveBeenCalledTimes(2);
    expect(put).toHaveBeenLastCalledWith({ q1: "abc" }, 0);
  });
});
