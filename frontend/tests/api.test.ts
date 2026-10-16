import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError, createApi } from "../lib/api";

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
const noSleep = () => Promise.resolve();

describe("api client", () => {
  it("maps the error envelope to ApiError", async () => {
    const f = vi.fn(async () =>
      json(422, { error: { code: "validation", message: "Bad", fields: { title: "Required" } } }),
    );
    const { api } = createApi({ fetch: f as never, sleep: noSleep });
    const err = await api("POST", "/exams", {}).catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 422, code: "validation", message: "Bad", fields: { title: "Required" } });
  });

  it("retries reads on 503 with 1/2/4 s backoff then throws", async () => {
    const waits: number[] = [];
    const f = vi.fn(async () => json(503, { error: { code: "upstream_unavailable", message: "x" } }));
    const { api } = createApi({ fetch: f as never, sleep: async (ms) => void waits.push(ms) });
    await expect(api("GET", "/documents")).rejects.toMatchObject({ status: 503 });
    expect(f).toHaveBeenCalledTimes(4);
    expect(waits).toEqual([1000, 2000, 4000]);
  });

  it("retries reads on network error and succeeds", async () => {
    const f = vi
      .fn()
      .mockRejectedValueOnce(new TypeError("net"))
      .mockResolvedValueOnce(json(200, { ok: 1 }));
    const { api } = createApi({ fetch: f as never, sleep: noSleep });
    await expect(api("GET", "/x")).resolves.toEqual({ ok: 1 });
  });

  it("never retries writes", async () => {
    const f = vi.fn(async () => json(502, { error: { code: "upstream_unavailable", message: "x" } }));
    const { api } = createApi({ fetch: f as never, sleep: noSleep });
    await expect(api("POST", "/exams/1/submit", {})).rejects.toMatchObject({ status: 502 });
    expect(f).toHaveBeenCalledTimes(1);
  });

  it("redirects on 401 with a return path, except on /login and /signup", async () => {
    const f = vi.fn(async () => json(401, { error: { code: "unauthenticated", message: "no" } }));
    for (const [path, expected] of [
      ["/exams/1/take", "/login?next=%2Fexams%2F1%2Ftake"],
      ["/login", null],
      ["/signup", null],
    ] as const) {
      const redirect = vi.fn();
      const { api } = createApi({ fetch: f as never, sleep: noSleep, redirect, currentPath: () => path });
      await expect(api("GET", "/me")).rejects.toMatchObject({ status: 401 });
      if (expected) expect(redirect).toHaveBeenCalledWith(expected);
      else expect(redirect).not.toHaveBeenCalled();
    }
  });

  it("sends Idempotency-Key and updates the server clock from serverTime", async () => {
    const f = vi.fn(async () => json(201, { id: "e1", serverTime: "2030-01-01T00:00:00Z" }));
    const { api, serverNow } = createApi({ fetch: f as never, sleep: noSleep });
    await api("POST", "/exams", { a: 1 }, { idempotencyKey: "key-12345" });
    const init = (f.mock.calls[0] as unknown as [string, RequestInit])[1];
    expect((init.headers as Record<string, string>)["Idempotency-Key"]).toBe("key-12345");
    expect(Math.abs(serverNow() - Date.parse("2030-01-01T00:00:00Z"))).toBeLessThan(2000);
  });

  it("prefixes /api and returns undefined for 204", async () => {
    const f = vi.fn(async () => new Response(null, { status: 204 }));
    const { api } = createApi({ fetch: f as never, sleep: noSleep });
    await expect(api("DELETE", "/documents/1")).resolves.toBeUndefined();
    expect((f.mock.calls[0] as unknown as [string])[0]).toBe("/api/documents/1");
  });

  it("does not retry reads on statuses outside 502/503/504", async () => {
    for (const status of [500, 404]) {
      const f = vi.fn(async () => json(status, { error: { code: "internal", message: "x" } }));
      const { api } = createApi({ fetch: f as never, sleep: noSleep });
      await expect(api("GET", "/documents")).rejects.toMatchObject({ status });
      expect(f).toHaveBeenCalledTimes(1);
    }
  });

  it("does not retry PUT or DELETE on 503", async () => {
    for (const method of ["PUT", "DELETE"] as const) {
      const f = vi.fn(async () => json(503, { error: { code: "upstream_unavailable", message: "x" } }));
      const { api } = createApi({ fetch: f as never, sleep: noSleep });
      await expect(api(method, "/exams/1/attempt", {})).rejects.toMatchObject({ status: 503 });
      expect(f).toHaveBeenCalledTimes(1);
    }
  });

  describe("default sleep", () => {
    afterEach(() => vi.useRealTimers());

    it("waits 1 s, 2 s, 4 s between read retries, advancing only with timers", async () => {
      vi.useFakeTimers();
      const f = vi.fn(async () => json(503, { error: { code: "upstream_unavailable", message: "x" } }));
      const { api } = createApi({ fetch: f as never });
      const p = api("GET", "/documents").catch((e) => e);
      await vi.advanceTimersByTimeAsync(0);
      expect(f).toHaveBeenCalledTimes(1);
      await vi.advanceTimersByTimeAsync(999);
      expect(f).toHaveBeenCalledTimes(1);
      await vi.advanceTimersByTimeAsync(1);
      expect(f).toHaveBeenCalledTimes(2);
      await vi.advanceTimersByTimeAsync(1999);
      expect(f).toHaveBeenCalledTimes(2);
      await vi.advanceTimersByTimeAsync(1);
      expect(f).toHaveBeenCalledTimes(3);
      await vi.advanceTimersByTimeAsync(3999);
      expect(f).toHaveBeenCalledTimes(3);
      await vi.advanceTimersByTimeAsync(1);
      expect(f).toHaveBeenCalledTimes(4);
      expect(await p).toMatchObject({ status: 503 });
    });
  });
});

describe("api client reachability (fs 8.1 'Can't reach the server. Retrying…')", () => {
  it("marks a read as retrying while it waits and clears it afterwards", async () => {
    const { createReachability } = await import("../lib/reachability");
    const reach = createReachability();
    const seen: boolean[] = [];
    const f = vi.fn().mockRejectedValueOnce(new TypeError("net")).mockResolvedValueOnce(json(200, { ok: 1 }));
    const { api } = createApi({ fetch: f as never, sleep: async () => void seen.push(reach.retrying()), reach });
    await api("GET", "/x");
    expect(seen).toEqual([true]);
    expect(reach.retrying()).toBe(false);
  });
  it("clears after the retries are exhausted, and never marks a write", async () => {
    const { createReachability } = await import("../lib/reachability");
    const reach = createReachability();
    const f = vi.fn(async () => json(503, { error: { code: "x", message: "x" } }));
    const { api } = createApi({ fetch: f as never, sleep: noSleep, reach });
    await expect(api("GET", "/x")).rejects.toMatchObject({ status: 503 });
    expect(reach.retrying()).toBe(false);
    const seen: boolean[] = [];
    const w = createApi({ fetch: f as never, sleep: async () => void seen.push(true), reach });
    await expect(w.api("POST", "/x", {})).rejects.toBeDefined();
    expect(seen).toEqual([]);
  });
});
