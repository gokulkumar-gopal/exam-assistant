import { describe, expect, it } from "vitest";
import {
  answerSheetAvailable, defaultOptions, keyUnavailableText, normalizeOptions, printPlan, ruledLines,
  type PrintExam,
} from "../lib/print";

const mcq: PrintExam = {
  title: "Biology: Cells quiz", type: "mcq", totalMarks: 3, createdBy: { id: "u1", name: "Ms. Rao" },
  questions: [
    { id: "q1", marks: 1, text: "<b>x</b>\nsecond line", options: ["<b>x</b>", "B1", "C1", "D1"], correctIndex: 0 },
    { id: "q2", marks: 2, text: "Pick", options: ["A2", "B2", "C2", "D2"], correctIndex: 2 },
  ],
};
const free: PrintExam = {
  title: "Essay", type: "free", totalMarks: 6, createdBy: { id: "u1", name: "Ms. Rao" },
  questions: [
    { id: "f1", marks: 1, text: "Why?", modelAnswer: "SECRET-MODEL-1" },
    { id: "f2", marks: 5, text: "Explain.", modelAnswer: "SECRET-MODEL-2" },
  ],
};
const all = { paper: true as const, answerSheet: true, key: true, nameLine: true };

describe("ruledLines", () => {
  it("is three lines per mark", () => {
    expect(ruledLines(1)).toBe(3);
    expect(ruledLines(5)).toBe(15);
    expect(ruledLines(2)).toBe(6);
  });
});

describe("defaultOptions", () => {
  it("answer sheet on for multiple choice, name line on, paper always true", () => {
    expect(defaultOptions(mcq, true)).toEqual({ paper: true, answerSheet: true, key: true, nameLine: true });
  });
  it("key is on by default only when visible", () => {
    expect(defaultOptions(mcq, false).key).toBe(false);
    expect(defaultOptions(free, true).key).toBe(true);
  });
  it("free text has no answer sheet", () => {
    expect(defaultOptions(free, true).answerSheet).toBe(false);
    expect(answerSheetAvailable(free)).toBe(false);
    expect(answerSheetAvailable(mcq)).toBe(true);
  });
});

describe("normalizeOptions", () => {
  it("the paper part cannot be turned off", () => {
    expect(normalizeOptions(mcq, { ...all, paper: false } as never, true).paper).toBe(true);
  });
  it("drops the answer sheet for free text and the key while hidden", () => {
    expect(normalizeOptions(free, all, false)).toEqual({ paper: true, answerSheet: false, key: false, nameLine: true });
  });
});

describe("keyUnavailableText", () => {
  it("says when the key becomes available", () => {
    expect(keyUnavailableText("2026-10-18T18:01:00Z")).toMatch(/^Available after \w{3} \d{1,2}, /);
  });
  it("never leaves an empty gap", () => {
    expect(keyUnavailableText(undefined)).toBe("Available after the exam closes");
    expect(keyUnavailableText("nonsense")).toBe("Available after the exam closes");
  });
});

describe("printPlan", () => {
  it("lists parts in order: paper, answer sheet, key", () => {
    expect(printPlan(mcq, all, true).parts).toEqual(["paper", "answerSheet", "key"]);
    expect(printPlan(mcq, { ...all, answerSheet: false, key: false }, true).parts).toEqual(["paper"]);
  });
  it("header, meta, instruction and totals", () => {
    const p = printPlan(mcq, all, true);
    expect(p.title).toBe("Biology: Cells quiz");
    expect(p.meta).toBe("Set by Ms. Rao · 2 questions · 3 marks");
    expect(p.instruction).toBe("Choose one answer for each question. Marks are shown in brackets.");
    expect(printPlan(free, all, true).instruction).toBe("Answer all questions in the space provided. Marks are shown in brackets.");
    expect(p.nameLine).toEqual({ total: 3 });
    expect(printPlan(mcq, { ...all, nameLine: false }, true).nameLine).toBeNull();
  });
  it("marks label is singular for one mark; ruled lines only for free text", () => {
    const m = printPlan(mcq, all, true).paper.questions;
    expect(m.map((q) => q.marksLabel)).toEqual(["[1 mark]", "[2 marks]"]);
    expect(m.map((q) => q.lines)).toEqual([0, 0]);
    expect(m[0].options?.map((o) => o.letter)).toEqual(["A", "B", "C", "D"]);
    const f = printPlan(free, all, true).paper.questions;
    expect(f.map((q) => q.lines)).toEqual([3, 15]);
    expect(f[0].options).toBeUndefined();
  });
  it("answer sheet only for multiple choice, two columns above 10 questions", () => {
    expect(printPlan(free, all, true).answerSheet).toBeNull();
    expect(printPlan(mcq, all, true).answerSheet).toEqual({ numbers: [1, 2], twoColumns: false });
    const many: PrintExam = { ...mcq, questions: Array.from({ length: 11 }, (_, i) => ({ ...mcq.questions[0], id: `x${i}` })) };
    expect(printPlan(many, all, true).answerSheet?.twoColumns).toBe(true);
    const ten: PrintExam = { ...mcq, questions: many.questions.slice(0, 10) };
    expect(printPlan(ten, all, true).answerSheet?.twoColumns).toBe(false);
  });
  it("key (visible): multiple choice shows letter and option text, free text the model answer", () => {
    const k = printPlan(mcq, all, true).key;
    expect(k?.heading).toBe("Answer key: Biology: Cells quiz");
    expect(k?.note).toBe("For the teacher, parent or marker. Not for students.");
    expect(k?.entries[1]).toMatchObject({ number: 2, letter: "C", optionText: "C2", marksLabel: "[2 marks]" });
    const fk = printPlan(free, all, true).key;
    expect(fk?.entries[0]).toMatchObject({ number: 1, text: "Why?", modelAnswer: "SECRET-MODEL-1" });
  });
  it("with the key hidden nothing of the key is in the plan, even when the option is passed true", () => {
    for (const exam of [mcq, free]) {
      const p = printPlan(exam, all, false);
      expect(p.key).toBeNull();
      expect(p.parts).not.toContain("key");
      const json = JSON.stringify(p);
      expect(json).not.toContain("Answer key");
      expect(json).not.toContain("Not for students");
      expect(json).not.toContain("SECRET-MODEL");
      expect(json).not.toMatch(/correctIndex|modelAnswer|optionText/);
    }
  });
  it("question markup stays literal text and line breaks are kept", () => {
    const q = printPlan(mcq, all, true).paper.questions[0];
    expect(q.text).toBe("<b>x</b>\nsecond line");
    expect(q.options?.[0].text).toBe("<b>x</b>");
  });
  it("no deadline is printed", () => {
    const json = JSON.stringify(printPlan({ ...mcq, closesAt: "2026-10-20T10:00:00Z" } as PrintExam, all, true));
    expect(json).not.toMatch(/closes|2026-10-20/);
  });
});
