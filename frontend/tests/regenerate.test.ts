import { describe, expect, it } from "vitest";
import { buildRegenerateBody, startRegenerate } from "../lib/generate";
import { validateQuestionEdit } from "../lib/question-edit";
import type { Question } from "../lib/types";

const qs: Question[] = [
  { id: "a", marks: 1, text: "First?" },
  { id: "b", marks: 2, text: "<b>x</b>" },
];

describe("buildRegenerateBody", () => {
  it("has exactly marks, sourceIds, type, avoid, model", () => {
    const body = buildRegenerateBody(qs, 3, { sourceIds: ["d1"], type: "mcq" }, "m1");
    expect(Object.keys(body).sort()).toEqual(["avoid", "marks", "model", "sourceIds", "type"]);
    expect(body).toEqual({ marks: 3, sourceIds: ["d1"], type: "mcq", avoid: ["First?", "<b>x</b>"], model: "m1" });
  });
  it("respects the backend caps on avoid", () => {
    const many = Array.from({ length: 70 }, (_, i) => ({ id: `${i}`, marks: 1 as const, text: "x".repeat(700) }));
    const body = buildRegenerateBody(many, 1, { sourceIds: [], type: "free" }, "m");
    expect(body.avoid).toHaveLength(60);
    expect(body.avoid.every((t) => t.length === 600)).toBe(true);
  });
  it("startRegenerate posts the body as given", async () => {
    let sent: unknown;
    const r = await startRegenerate(buildRegenerateBody(qs, 1, { sourceIds: ["d"], type: "free" }, "m"), async (b) => { sent = b; return { jobId: "j" }; });
    expect(r.jobId).toBe("j");
    expect((sent as { marks: number }).marks).toBe(1);
  });
});

describe("validateQuestionEdit", () => {
  const mcq = { text: "Q?", options: ["a", "b", "c", "d"], correctIndex: 1 };
  it("accepts a valid multiple-choice edit", () => expect(validateQuestionEdit(mcq, "mcq")).toBeNull());
  it("rejects empty text", () => expect(validateQuestionEdit({ ...mcq, text: "  " }, "mcq")).toMatch(/question/i));
  it("rejects an empty option", () => expect(validateQuestionEdit({ ...mcq, options: ["a", "", "c", "d"] }, "mcq")).toMatch(/option/i));
  it("rejects duplicate options", () => expect(validateQuestionEdit({ ...mcq, options: ["a", "A ", "c", "d"] }, "mcq")).toMatch(/different/i));
  it("rejects a missing correct answer", () => expect(validateQuestionEdit({ ...mcq, correctIndex: undefined }, "mcq")).toMatch(/correct/i));
  it("needs only text for free text", () => expect(validateQuestionEdit({ text: "Q?" }, "free")).toBeNull());
});
