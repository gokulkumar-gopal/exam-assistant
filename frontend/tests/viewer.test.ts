import { describe, expect, it } from "vitest";
import {
  FileLoadError, ZOOM_DEFAULT, ZOOM_MAX, ZOOM_MIN, nextZoom, openFile, pageWidthPx,
} from "../lib/viewer";

const res = (status: number, body = "x") => new Response(body, { status });

describe("nextZoom", () => {
  it("defaults to 100 and moves in steps of 15", () => {
    expect(ZOOM_DEFAULT).toBe(100);
    expect(nextZoom(100, 1)).toBe(115);
    expect(nextZoom(100, -1)).toBe(85);
  });
  it("clamps to 50..300", () => {
    expect(ZOOM_MIN).toBe(50);
    expect(ZOOM_MAX).toBe(300);
    expect(nextZoom(295, 1)).toBe(300);
    expect(nextZoom(300, 1)).toBe(300);
    expect(nextZoom(55, -1)).toBe(50);
    expect(nextZoom(50, -1)).toBe(50);
  });
  it("reaches both ends from the default", () => {
    let z = ZOOM_DEFAULT;
    for (let i = 0; i < 30; i++) z = nextZoom(z, 1);
    expect(z).toBe(300);
    for (let i = 0; i < 30; i++) z = nextZoom(z, -1);
    expect(z).toBe(50);
  });
});

describe("pageWidthPx", () => {
  it("is 560 at 100% and scales with zoom", () => {
    expect(pageWidthPx(100)).toBe(560);
    expect(pageWidthPx(50)).toBe(280);
    expect(pageWidthPx(300)).toBe(1680);
  });
  it("never exceeds the available width at 100%", () => {
    expect(pageWidthPx(100, 303)).toBe(303);
    expect(pageWidthPx(150, 303)).toBe(455);
    expect(pageWidthPx(100, 900)).toBe(560);
  });
});

describe("openFile", () => {
  function setup(statuses: number[]) {
    const urls: string[] = [];
    let presigns = 0;
    const getUrl = async () => `https://s3.example/obj?sig=${++presigns}`;
    const fetcher = async (u: string) => {
      urls.push(u);
      return res(statuses[urls.length - 1] ?? 200);
    };
    return { getUrl, fetcher, urls, presigns: () => presigns };
  }

  it("returns the response with a single presign when the first fetch works", async () => {
    const s = setup([200]);
    const r = await openFile(s.getUrl, s.fetcher);
    expect(r.status).toBe(200);
    expect(s.presigns()).toBe(1);
    expect(s.urls).toHaveLength(1);
  });

  it("on a first 403 refetches the presigned URL exactly once, then succeeds", async () => {
    const s = setup([403, 200]);
    const r = await openFile(s.getUrl, s.fetcher);
    expect(r.status).toBe(200);
    expect(s.presigns()).toBe(2);
    expect(s.urls).toEqual(["https://s3.example/obj?sig=1", "https://s3.example/obj?sig=2"]);
  });

  it("two 403s in a row fail with no third attempt", async () => {
    const s = setup([403, 403, 200]);
    await expect(openFile(s.getUrl, s.fetcher)).rejects.toBeInstanceOf(FileLoadError);
    expect(s.presigns()).toBe(2);
    expect(s.urls).toHaveLength(2);
  });

  it("non-403 errors do not trigger the refetch", async () => {
    for (const status of [404, 500, 503]) {
      const s = setup([status, 200]);
      await expect(openFile(s.getUrl, s.fetcher)).rejects.toBeInstanceOf(FileLoadError);
      expect(s.presigns()).toBe(1);
      expect(s.urls).toHaveLength(1);
    }
  });

  it("a network failure is a FileLoadError and is not retried", async () => {
    let calls = 0;
    const getUrl = async () => "https://s3.example/obj?sig=1";
    const fetcher = async () => { calls++; throw new TypeError("Failed to fetch"); };
    await expect(openFile(getUrl, fetcher)).rejects.toBeInstanceOf(FileLoadError);
    expect(calls).toBe(1);
  });

  it("never puts the URL in an error message", async () => {
    const s = setup([403, 403]);
    const err = await openFile(s.getUrl, s.fetcher).catch((e: Error) => e);
    expect((err as Error).message).not.toContain("s3.example");
    expect((err as Error).message).not.toContain("sig=");
  });

  it("propagates a presign failure from getUrl as is", async () => {
    const boom = new Error("api down");
    await expect(openFile(async () => { throw boom; }, async () => res(200))).rejects.toBe(boom);
  });
});
