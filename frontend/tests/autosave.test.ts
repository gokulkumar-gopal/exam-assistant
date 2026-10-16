import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { autosaveText, createAutosaver, keepaliveOk, shouldFlushOnEvent, type AutosaveState } from "../lib/autosave";

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

describe("createAutosaver after dispose", () => {
  it("issues no put and arms no timer once disposed", async () => {
    const { saver, put } = setup();
    saver.change();
    saver.dispose();
    await vi.advanceTimersByTimeAsync(5000);
    expect(put).not.toHaveBeenCalled();
    saver.change();
    await vi.advanceTimersByTimeAsync(5000);
    expect(put).not.toHaveBeenCalled();
  });

  it("a failed in-flight put does not reschedule or report after dispose", async () => {
    let rej: (e: Error) => void = () => {};
    const { saver, put, states } = setup(() => new Promise<void>((_, r) => { rej = r; }));
    saver.change();
    await vi.advanceTimersByTimeAsync(500);
    expect(put).toHaveBeenCalledTimes(1);
    const before = states.length;
    saver.dispose();
    rej(new Error("net"));
    await vi.advanceTimersByTimeAsync(30_000);
    expect(put).toHaveBeenCalledTimes(1);
    expect(states.length).toBe(before);
  });

  it("a flush started before dispose still completes its single put, with no retry afterwards", async () => {
    let fail = true;
    const { saver, put, states } = setup(async () => { if (fail) throw new Error("net"); });
    saver.change();
    const f = saver.flush();
    saver.dispose();
    expect(await f).toBe("failed");
    expect(put).toHaveBeenCalledTimes(1);
    fail = false;
    await vi.advanceTimersByTimeAsync(30_000);
    expect(put).toHaveBeenCalledTimes(1);
    expect(states.length).toBe(0);
  });

  it("a flush that waits on an in-flight put still sends once after dispose", async () => {
    let release: () => void = () => {};
    let n = 0;
    const { saver, put, s } = setup(() => (n++ === 0 ? new Promise<void>((r) => { release = r; }) : Promise.resolve()));
    saver.change();
    await vi.advanceTimersByTimeAsync(500);
    s.answers = { q1: "newer" };
    saver.change();
    const f = saver.flush();
    saver.dispose();
    release();
    expect(await f).toBe("saved");
    expect(put).toHaveBeenCalledTimes(2);
    expect(put).toHaveBeenLastCalledWith({ q1: "newer" }, 0);
  });

  it("a flush after dispose does not put", async () => {
    const { saver, put } = setup();
    saver.change();
    saver.dispose();
    await saver.flush();
    expect(put).not.toHaveBeenCalled();
  });
});

describe("createAutosaver flush and immediate changes", () => {
  it("concurrent flush calls share one put", async () => {
    let release: () => void = () => {};
    const { saver, put } = setup(() => new Promise<void>((r) => { release = r; }));
    saver.change();
    const a = saver.flush();
    const b = saver.flush();
    await vi.advanceTimersByTimeAsync(0);
    expect(put).toHaveBeenCalledTimes(1);
    release();
    expect(await Promise.all([a, b])).toEqual(["saved", "saved"]);
    expect(put).toHaveBeenCalledTimes(1);
  });

  it("concurrent flush calls while failing do not overlap puts", async () => {
    let inFlight = 0, maxInFlight = 0;
    const { saver, put } = setup(async () => {
      inFlight++; maxInFlight = Math.max(maxInFlight, inFlight);
      await new Promise((r) => setTimeout(r, 100));
      inFlight--;
      throw new Error("net");
    });
    saver.change();
    const r = Promise.all([saver.flush(), saver.flush(), saver.flush()]);
    await vi.advanceTimersByTimeAsync(500);
    expect(await r).toEqual(["failed", "failed", "failed"]);
    expect(maxInFlight).toBe(1);
    expect(put).toHaveBeenCalledTimes(1);
  });

  it("an immediate change puts without the 500 ms wait", async () => {
    const { saver, put } = setup();
    saver.change({ immediate: true });
    await vi.advanceTimersByTimeAsync(0);
    expect(put).toHaveBeenCalledTimes(1);
  });

  it("an immediate change during an in-flight put waits for it, then sends the newest", async () => {
    let release: () => void = () => {};
    let n = 0;
    const { saver, put, s } = setup(() => (n++ === 0 ? new Promise<void>((r) => { release = r; }) : Promise.resolve()));
    saver.change({ immediate: true });
    await vi.advanceTimersByTimeAsync(0);
    s.answers = { q1: "pick" };
    saver.change({ immediate: true });
    await vi.advanceTimersByTimeAsync(100);
    expect(put).toHaveBeenCalledTimes(1);
    release();
    await vi.advanceTimersByTimeAsync(10);
    expect(put).toHaveBeenCalledTimes(2);
    expect(put).toHaveBeenLastCalledWith({ q1: "pick" }, 0);
  });
});

describe("leaving the page", () => {
  it("flushes on pagehide and when the page becomes hidden, not when visible", () => {
    expect(shouldFlushOnEvent("pagehide", "visible")).toBe(true);
    expect(shouldFlushOnEvent("visibilitychange", "hidden")).toBe(true);
    expect(shouldFlushOnEvent("visibilitychange", "visible")).toBe(false);
  });
  it("uses keepalive only while the body fits the 64 KB browser limit", () => {
    expect(keepaliveOk(JSON.stringify({ answers: { q1: "a" } }))).toBe(true);
    expect(keepaliveOk("x".repeat(70_000))).toBe(false);
    expect(keepaliveOk("é".repeat(40_000))).toBe(false); // 2 bytes each
  });
});
