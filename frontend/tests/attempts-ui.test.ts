import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { AttemptsTable } from "../components/AttemptsTable";
import { AttemptHistory } from "../components/AttemptHistory";
import type { AttemptSummary } from "../lib/types";

const attempts: AttemptSummary[] = [
  { attemptNo: 1, status: "submitted", score: 28, maxScore: 40, submittedAt: "2026-10-16T10:00:00Z" },
  { attemptNo: 2, status: "submitted", score: 34.5, maxScore: 40, submittedAt: "2026-10-16T11:00:00Z" },
  { attemptNo: 3, status: "in_progress", maxScore: 40 },
];
const html = (p: Partial<Parameters<typeof AttemptsTable>[0]> = {}) =>
  renderToStaticMarkup(createElement(AttemptsTable, { attempts, selected: 2, bestAttemptNo: 2, onSelect: () => {}, ...p }));

describe("AttemptsTable", () => {
  it("lists every attempt with score and a labelled View button", () => {
    const h = html();
    expect(h).toContain("28/40");
    expect(h).toContain("34.5/40");
    for (const n of [1, 2, 3]) expect(h).toContain(`aria-label="View attempt ${n}"`);
  });
  it("has column headers with scope=col", () => {
    const h = html();
    expect(h.match(/<th[^>]*scope="col"/g)?.length).toBe(4);
    expect(h).toContain(">Attempt<");
    expect(h).toContain(">Score<");
    expect(h).toContain(">Submitted<");
  });
  it("marks the best attempt and only the selected attempt is aria-current", () => {
    const h = html();
    expect(h.match(/>Best</g)?.length).toBe(1);
    expect(h.match(/aria-current="true"/g)?.length).toBe(1);
    expect(h).toMatch(/aria-label="View attempt 2"[^>]*aria-current="true"|aria-current="true"[^>]*aria-label="View attempt 2"/);
    expect(html({ selected: 1 })).toMatch(/aria-label="View attempt 1"[^>]*aria-current="true"|aria-current="true"[^>]*aria-label="View attempt 1"/);
  });
  it("an unfinished attempt is shown but cannot be opened", () => {
    const h = html();
    expect(h).toContain("In progress");
    expect(h).toMatch(/<button[^>]*aria-label="View attempt 3"[^>]*disabled|<button[^>]*disabled[^>]*aria-label="View attempt 3"/);
  });
  it("Retake shows only when a handler is given", () => {
    expect(html()).not.toContain(">Retake<");
    expect(html({ onRetake: () => {} })).toContain(">Retake<");
  });
  it("one attempt: no table, but still the Retake button when allowed", () => {
    const one = [attempts[0]];
    expect(html({ attempts: one })).toBe("");
    const h = html({ attempts: one, onRetake: () => {} });
    expect(h).toContain(">Retake<");
    expect(h).not.toContain("<table");
  });
});

describe("AttemptHistory (status page)", () => {
  const history = [
    { attemptNo: 1, status: "submitted" as const, score: 28, submittedAt: "2026-10-16T10:00:00Z" },
    { attemptNo: 2, status: "in_progress" as const },
  ];
  it("shows each attempt's number, score and time with labelled headers, and nothing else", () => {
    const h = renderToStaticMarkup(createElement(AttemptHistory, { name: "Aisha", history, totalMarks: 40 }));
    expect(h).toContain('aria-label="Attempts for Aisha"');
    expect(h.match(/scope="col"/g)?.length).toBe(3);
    expect(h).toContain("28/40");
    expect(h).toContain("In progress");
    expect(h.toLowerCase()).not.toContain("answer");
  });
});
