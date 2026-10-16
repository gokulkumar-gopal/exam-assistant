import { describe, expect, it } from "vitest";
import { ApiError } from "../lib/api";
import { generateErrorNotice, pollJob, startGeneration, type Send } from "../lib/generate";

type Call = { method: string; path: string };

function harness(script: Array<unknown | Error>) {
  let t = 0;
  const calls: Call[] = [];
  const sleeps: number[] = [];
  let i = 0;
  const send: Send = async (method, path) => {
    calls.push({ method, path });
    if (method === "DELETE") return undefined;
    const step = script[Math.min(i++, script.length - 1)];
    if (step instanceof Error) throw step;
    return step;
  };
  const opts = {
    now: () => t,
    sleep: async (ms: number) => { sleeps.push(ms); t += ms; },
    send,
  };
  return { calls, sleeps, opts, deletes: () => calls.filter((c) => c.method === "DELETE") };
}

const running = (done = 1) => ({ status: "running", model: "m1", progress: { done, total: 3 }, questions: null, failedSources: [], error: null });
const done = { status: "done", model: "m1", progress: { done: 3, total: 3 }, questions: [{ id: "q1" }], failedSources: [], error: null };

describe("pollJob", () => {
  it("(a) gives up after 3 consecutive failures, DELETE sent first and once", async () => {
    const h = harness([new Error("net")]);
    const r = await pollJob("j1", h.opts);
    expect(r).toEqual({ status: "gaveUp" });
    expect(h.calls.filter((c) => c.method === "GET")).toHaveLength(3);
    expect(h.deletes()).toEqual([{ method: "DELETE", path: "/generate/j1" }]);
    expect(h.calls[h.calls.length - 1].method).toBe("DELETE");
  });

  it("(b) gives up when runtime exceeds 360000 ms, with one DELETE", async () => {
    const h = harness([running()]);
    const r = await pollJob("j1", h.opts);
    expect(r).toEqual({ status: "gaveUp" });
    expect(h.deletes()).toHaveLength(1);
    expect(h.sleeps.reduce((a, b) => a + b, 0)).toBeGreaterThan(360000);
    expect(h.sleeps.reduce((a, b) => a + b, 0) - 1500).toBeLessThanOrEqual(360000);
  });

  it("(c) one failure then success does not give up", async () => {
    const h = harness([new Error("blip"), done]);
    const r = await pollJob("j1", h.opts);
    expect(r.status).toBe("done");
    expect(h.deletes()).toHaveLength(0);
  });

  it("a success between failures resets the failure count", async () => {
    const e = new Error("x");
    const h = harness([e, e, running(), e, e, done]);
    expect((await pollJob("j1", h.opts)).status).toBe("done");
    expect(h.deletes()).toHaveLength(0);
  });

  it("(d) done stops polling and returns the job's model and questions", async () => {
    const h = harness([running(1), running(2), done, running(9)]);
    const r = await pollJob("j1", h.opts);
    expect(r).toEqual({ status: "done", model: "m1", questions: [{ id: "q1" }], failedSources: [] });
    expect(h.calls.filter((c) => c.method === "GET")).toHaveLength(3);
  });

  it("passes failedSources through on done", async () => {
    const h = harness([{ ...done, failedSources: ["Notes.txt"] }]);
    const r = await pollJob("j1", h.opts);
    expect(r).toMatchObject({ status: "done", failedSources: ["Notes.txt"] });
  });

  it("returns failed with the error text verbatim", async () => {
    const h = harness([{ ...running(), status: "failed", error: "That took too long. Try again." }]);
    expect(await pollJob("j1", h.opts)).toEqual({ status: "failed", error: "That took too long. Try again." });
    expect(h.deletes()).toHaveLength(0);
  });

  it("returns cancelled when the server reports it, without a DELETE", async () => {
    const h = harness([{ ...running(), status: "cancelled" }]);
    expect(await pollJob("j1", h.opts)).toEqual({ status: "cancelled" });
    expect(h.deletes()).toHaveLength(0);
  });

  it("polls every 1500 ms and reports progress", async () => {
    const h = harness([running(1), running(2), done]);
    const seen: unknown[] = [];
    await pollJob("j1", { ...h.opts, onProgress: (p) => seen.push(p) });
    expect(h.sleeps).toEqual([1500, 1500]);
    expect(seen).toEqual([{ done: 1, total: 3 }, { done: 2, total: 3 }]);
  });

  it("abort sends one DELETE immediately and a later poll does not send another", async () => {
    const h = harness([running()]);
    const ctrl = new AbortController();
    const opts = { ...h.opts, signal: ctrl.signal, sleep: async (ms: number) => { h.sleeps.push(ms); ctrl.abort(); } };
    const r = await pollJob("j1", opts);
    expect(r).toEqual({ status: "cancelled" });
    expect(h.deletes()).toHaveLength(1);
    expect(h.calls.filter((c) => c.method === "GET")).toHaveLength(1);
  });

  it("an already-aborted signal sends one DELETE and no poll", async () => {
    const h = harness([running()]);
    const ctrl = new AbortController();
    ctrl.abort();
    expect(await pollJob("j1", { ...h.opts, signal: ctrl.signal })).toEqual({ status: "cancelled" });
    expect(h.deletes()).toHaveLength(1);
    expect(h.calls).toHaveLength(1);
  });
});

describe("startGeneration", () => {
  const base = { sourceIds: ["d1"], type: "mcq" as const, counts: { "1": 2 }, shuffle: false };
  it("sends model only when defined", async () => {
    const bodies: Record<string, unknown>[] = [];
    const post = async (b: Record<string, unknown>) => { bodies.push(b); return { jobId: "j9" }; };
    expect(await startGeneration({ ...base, model: "m2" }, post as never)).toEqual({ jobId: "j9" });
    await startGeneration({ ...base, model: undefined }, post as never);
    expect(bodies[0]).toMatchObject({ model: "m2" });
    expect("model" in bodies[1]).toBe(false);
  });
});

describe("generateErrorNotice", () => {
  const e = (status: number, message: string, fields?: Record<string, string>) => new ApiError(status, "x", message, fields);
  it("429 shows the message verbatim with no retry", () => {
    expect(generateErrorNotice(e(429, "The monthly AI budget has been used. Try again next month.")))
      .toEqual({ text: "The monthly AI budget has been used. Try again next month.", retry: false, resetModel: false });
  });
  it("422 with fields.model resets the model", () => {
    expect(generateErrorNotice(e(422, "Bad", { model: "unknown" })))
      .toEqual({ text: "That model isn't available. Pick another.", retry: false, resetModel: true });
  });
  it("422 naming a file shows the message verbatim", () => {
    expect(generateErrorNotice(e(422, "Biology.pdf is too long to use.", { sourceIds: "Biology.pdf" })))
      .toEqual({ text: "Biology.pdf is too long to use.", retry: false, resetModel: false });
  });
  it("other errors offer Try again", () => {
    expect(generateErrorNotice(e(0, "Couldn't reach the server. Try again."))).toMatchObject({ retry: true });
    expect(generateErrorNotice(new Error("boom"))).toMatchObject({ retry: true });
  });
});
