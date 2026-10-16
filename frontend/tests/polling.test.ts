import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createPoller, type VisibilityDoc } from "../lib/usePolling";

function fakeDoc(hidden = false) {
  const listeners = new Set<() => void>();
  const doc: VisibilityDoc & { hidden: boolean } = {
    hidden,
    addEventListener: (_t, fn) => { listeners.add(fn); },
    removeEventListener: (_t, fn) => { listeners.delete(fn); },
  };
  return { doc, listeners, setHidden(h: boolean) { doc.hidden = h; listeners.forEach((l) => l()); } };
}

beforeEach(() => { vi.useFakeTimers(); });
afterEach(() => { vi.useRealTimers(); });

describe("createPoller", () => {
  it("fetches at once, then every interval", async () => {
    const fn = vi.fn(async () => {});
    const d = fakeDoc();
    const p = createPoller(fn, 5000, d.doc);
    p.start();
    expect(fn).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(5000);
    expect(fn).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(10_000);
    expect(fn).toHaveBeenCalledTimes(4);
    p.stop();
  });

  it("pauses while hidden and refreshes once when visible again", async () => {
    const fn = vi.fn(async () => {});
    const d = fakeDoc();
    const p = createPoller(fn, 5000, d.doc);
    p.start();
    await vi.advanceTimersByTimeAsync(0);
    d.setHidden(true);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(fn).toHaveBeenCalledTimes(1);
    d.setHidden(false);
    expect(fn).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(5000);
    expect(fn).toHaveBeenCalledTimes(3);
    p.stop();
  });

  it("does not fetch at start when the tab is hidden, but does when it shows", async () => {
    const fn = vi.fn(async () => {});
    const d = fakeDoc(true);
    const p = createPoller(fn, 5000, d.doc);
    p.start();
    await vi.advanceTimersByTimeAsync(20_000);
    expect(fn).toHaveBeenCalledTimes(0);
    d.setHidden(false);
    expect(fn).toHaveBeenCalledTimes(1);
    p.stop();
  });

  it("never overlaps: a slow request delays the next one", async () => {
    let inflight = 0;
    let max = 0;
    const fn = vi.fn(async () => {
      inflight++; max = Math.max(max, inflight);
      await new Promise((r) => setTimeout(r, 12_000));
      inflight--;
    });
    const d = fakeDoc();
    const p = createPoller(fn, 5000, d.doc);
    p.start();
    await vi.advanceTimersByTimeAsync(11_000);
    expect(fn).toHaveBeenCalledTimes(1);
    // becoming visible while a request is in flight does not start a second one
    d.setHidden(true); d.setHidden(false);
    expect(fn).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1000 + 5000);
    expect(fn).toHaveBeenCalledTimes(2);
    expect(max).toBe(1);
    p.stop();
  });

  it("keeps polling after a failed refresh", async () => {
    const fn = vi.fn(async () => { throw new Error("x"); });
    const d = fakeDoc();
    const p = createPoller(fn, 5000, d.doc);
    p.start();
    await vi.advanceTimersByTimeAsync(10_000);
    expect(fn).toHaveBeenCalledTimes(3);
    p.stop();
  });

  it("stop cancels the timer and the listener: no fetch after unmount", async () => {
    const fn = vi.fn(async () => {});
    const d = fakeDoc();
    const p = createPoller(fn, 5000, d.doc);
    p.start();
    await vi.advanceTimersByTimeAsync(0);
    p.stop();
    expect(d.listeners.size).toBe(0);
    await vi.advanceTimersByTimeAsync(60_000);
    d.setHidden(false);
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it("stop during an in-flight request schedules nothing afterwards", async () => {
    const fn = vi.fn(() => new Promise<void>((r) => setTimeout(r, 3000)));
    const d = fakeDoc();
    const p = createPoller(fn, 5000, d.doc);
    p.start();
    p.stop();
    await vi.advanceTimersByTimeAsync(60_000);
    expect(fn).toHaveBeenCalledTimes(1);
  });
});
