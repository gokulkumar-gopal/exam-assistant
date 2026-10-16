import { describe, expect, it, vi } from "vitest";
import { createApi } from "../lib/api";
import {
  buildPublishBody, makePublisher, newKey, parseEmails, type PublishBody,
} from "../lib/publish";
import type { Question } from "../lib/types";

const ok = () =>
  new Response(JSON.stringify({ id: "e1", unknownEmails: [], serverTime: "2026-10-16T10:00:00Z" }), {
    status: 201, headers: { "content-type": "application/json" },
  });

function setup(responses: Array<() => Response | Promise<Response>>) {
  const calls: { key: string | null; body: unknown }[] = [];
  let n = 0;
  const f = vi.fn(async (_u: unknown, init?: RequestInit) => {
    calls.push({
      key: (init?.headers as Record<string, string>)["Idempotency-Key"] ?? null,
      body: JSON.parse(init?.body as string),
    });
    const r = responses[Math.min(n++, responses.length - 1)];
    return r();
  });
  let id = 0;
  const { api } = createApi({ fetch: f as never, mock: false });
  const publisher = makePublisher({ api, newId: () => `key-${++id}-xxxxxxxx` });
  return { publisher, calls, f };
}
const fail = () => { throw new TypeError("offline"); };
const body = (over: Partial<PublishBody> = {}): PublishBody => ({
  title: "T", type: "mcq", closesInSeconds: 86400, assigneeEmails: [], maxPeeks: 3,
  questions: [{ marks: 1, text: "Q", options: ["a", "b", "c", "d"], correctIndex: 1, sourceDocId: "d", sourceDocName: "D" }],
  ...over,
});

describe("makePublisher", () => {
  it("sends the identical Idempotency-Key when retrying after a failure", async () => {
    const { publisher, calls } = setup([fail, ok]);
    await expect(publisher.publish(body())).rejects.toMatchObject({ status: 0 });
    await publisher.publish(body());
    expect(calls).toHaveLength(2);
    expect(calls[0].key).toBeTruthy();
    expect(calls[1].key).toBe(calls[0].key);
  });
  it("uses a different key for a new publish after success", async () => {
    const { publisher, calls } = setup([ok, ok]);
    const r = await publisher.publish(body());
    expect(r).toMatchObject({ id: "e1", unknownEmails: [] });
    await publisher.publish(body());
    expect(calls[1].key).not.toBe(calls[0].key);
  });
  it("creates a new key when the question is edited after a failure", async () => {
    const { publisher, calls } = setup([fail, ok]);
    await publisher.publish(body()).catch(() => {});
    const b = body();
    b.questions[0] = { ...b.questions[0], text: "Edited" };
    await publisher.publish(b);
    expect(calls[1].key).not.toBe(calls[0].key);
  });
  it("creates a new key when any other body field changes after a failure", async () => {
    const { publisher, calls } = setup([fail, fail, ok]);
    await publisher.publish(body()).catch(() => {});
    await publisher.publish(body({ title: "Other" })).catch(() => {});
    await publisher.publish(body({ title: "Other" }));
    expect(calls[1].key).not.toBe(calls[0].key);
    expect(calls[2].key).toBe(calls[1].key);
  });
  it("shares one request between two simultaneous calls with the same body", async () => {
    const { publisher, f } = setup([ok]);
    await Promise.all([publisher.publish(body()), publisher.publish(body())]);
    expect(f).toHaveBeenCalledTimes(1);
  });
  it("keeps the key across a 422 for the same body", async () => {
    const bad = () => new Response(JSON.stringify({ error: { code: "validation", message: "No." } }), { status: 422 });
    const { publisher, calls } = setup([bad, ok]);
    await publisher.publish(body()).catch(() => {});
    await publisher.publish(body());
    expect(calls[1].key).toBe(calls[0].key);
  });
});

describe("newKey", () => {
  it("uses crypto.randomUUID when present and a valid fallback otherwise", () => {
    const orig = globalThis.crypto;
    vi.stubGlobal("crypto", { randomUUID: () => "11111111-2222-4333-8444-555555555555" });
    expect(newKey()).toBe("11111111-2222-4333-8444-555555555555");
    vi.stubGlobal("crypto", undefined);
    const k = newKey();
    expect(k).toMatch(/^[A-Za-z0-9_-]{8,64}$/);
    expect(newKey()).not.toBe(k);
    vi.stubGlobal("crypto", orig);
  });
});

describe("buildPublishBody", () => {
  const qs: Question[] = [
    { id: "q1", marks: 2, text: "A?", options: ["1", "2", "3", "4"], correctIndex: 2, sourceDocId: "d1", sourceDocName: "Bio.pdf" },
  ];
  it("strips ids, sends the deadline as given and no key-visibility field", () => {
    const b = buildPublishBody({
      title: " Quiz ", type: "mcq", deadline: { closesInSeconds: 3600 }, assigneeEmails: ["a@x.com"], maxPeeks: 9, questions: qs,
    });
    expect(b).toEqual({
      title: "Quiz", type: "mcq", closesInSeconds: 3600, assigneeEmails: ["a@x.com"], maxPeeks: 1,
      questions: [{ marks: 2, text: "A?", options: ["1", "2", "3", "4"], correctIndex: 2, sourceDocId: "d1", sourceDocName: "Bio.pdf" }],
    });
    expect(JSON.stringify(b)).not.toMatch(/visib/i);
  });
  it("sends closesAt for a picked time and modelAnswer for free text", () => {
    const b = buildPublishBody({
      title: "x", type: "free", deadline: { closesAt: "2026-10-20T12:00:00.000Z" }, assigneeEmails: [], maxPeeks: 0,
      questions: [{ id: "q", marks: 1, text: "Why?", modelAnswer: "Because." }],
    });
    expect(b.closesAt).toBe("2026-10-20T12:00:00.000Z");
    expect("closesInSeconds" in b).toBe(false);
    expect(b.questions[0]).toEqual({ marks: 1, text: "Why?", modelAnswer: "Because.", sourceDocId: "", sourceDocName: "" });
  });
});

describe("parseEmails", () => {
  it("trims, lowercases and dedupes", () => {
    expect(parseEmails(" A@x.com, b@y.org ,a@X.com,, ", "teacher")).toEqual({ emails: ["a@x.com", "b@y.org"], error: null });
  });
  it("flags an invalid address", () => {
    expect(parseEmails("a@x.com, nope", "teacher").error).toMatch(/nope/);
  });
  it("caps at 10 for students (and unknown role) and 50 otherwise", () => {
    const many = (n: number) => Array.from({ length: n }, (_, i) => `u${i}@x.com`).join(",");
    expect(parseEmails(many(10), "student").error).toBeNull();
    expect(parseEmails(many(11), "student").error).toMatch(/10/);
    expect(parseEmails(many(11), undefined).error).toMatch(/10/);
    expect(parseEmails(many(50), "parent").error).toBeNull();
    expect(parseEmails(many(51), "teacher").error).toMatch(/50/);
  });
  it("treats empty input as no assignees", () => {
    expect(parseEmails("  ", "student")).toEqual({ emails: [], error: null });
  });
});

describe("keyMissing", () => {
  it("counts questions without a usable answer", async () => {
    const { keyMissing } = await import("../lib/publish");
    const m = (x: Partial<Question>): Question => ({ id: "q", marks: 1, text: "t", ...x });
    expect(keyMissing([m({ correctIndex: 0 }), m({}), m({ correctIndex: 4 })], "mcq")).toBe(2);
    expect(keyMissing([m({ modelAnswer: "a" }), m({ modelAnswer: " " })], "free")).toBe(1);
  });
});
