// Fixture data for MOCK_API=true. Shapes follow backend-spec.md section 8.
import type { AiModel, Doc, Marks, Question, User } from "../types";

export const demoUser: User = { id: "u_demo", name: "Demo User", email: "demo@example.com", role: "teacher" };
export const rao = { id: "u_rao", name: "Ms. Rao" };

export const docs: Doc[] = [
  { id: "d1", name: "Biology.pdf", type: "PDF", pages: 12, sizeBytes: 481234, createdAt: "2026-10-14T09:00:00Z" },
  { id: "d2", name: "Photosynthesis.jpg", type: "JPG", pages: 1, sizeBytes: 220311, createdAt: "2026-10-12T09:00:00Z" },
  { id: "d3", name: "Notes.txt", type: "TXT", pages: 2, sizeBytes: 5120, createdAt: "2026-10-10T09:00:00Z" },
];

export const models: AiModel[] = [
  { id: "gemini-3.1-flash-lite", name: "Gemini 3.1 Flash-Lite", provider: "gemini", tier: "cheapest",
    priceInPerMTok: 0.25, priceOutPerMTok: 1.5, approxJobCost: { small: 0.03, heavy: 0.1 }, default: true },
  { id: "claude-haiku-4-5", name: "Claude Haiku 4.5", provider: "anthropic", tier: "cheap",
    priceInPerMTok: 1, priceOutPerMTok: 5, approxJobCost: { small: 0.08, heavy: 0.2 }, default: false },
  { id: "claude-sonnet-5-5", name: "Claude Sonnet 5.5", provider: "anthropic", tier: "balanced",
    priceInPerMTok: 2, priceOutPerMTok: 10, approxJobCost: { small: 0.17, heavy: 0.4 }, default: false },
  { id: "claude-opus-5-5", name: "Claude Opus 5.5", provider: "anthropic", tier: "most capable",
    priceInPerMTok: 4, priceOutPerMTok: 20, approxJobCost: { small: 0.33, heavy: 0.8 }, default: false },
];

const zeroTokens = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 };
export const usage = {
  month: { key: "2026-10", spendUsd: 3.42, budgetUsd: 15, jobs: 21, calls: 88, tokens: { ...zeroTokens, input: 120000, output: 30000 } },
  today: { jobs: 2, calls: 9, spendUsd: 0.31, tokens: { ...zeroTokens, input: 9000, output: 2500 } },
  byModel: [
    { id: "gemini-3.1-flash-lite", name: "Gemini 3.1 Flash-Lite", jobs: 18, calls: 70, spendUsd: 1.9, tokens: zeroTokens },
    { id: "claude-haiku-4-5", name: "Claude Haiku 4.5", jobs: 3, calls: 18, spendUsd: 1.52, tokens: zeroTokens },
  ],
  running: 1,
  recent: [
    { createdAt: "2026-10-16T08:00:00Z", kind: "generate", model: "gemini-3.1-flash-lite", status: "running", calls: 4, tokens: zeroTokens, costUsd: 0.12 },
    { createdAt: "2026-10-16T07:00:00Z", kind: "regenerate", model: "gemini-3.1-flash-lite", status: "done", calls: 1, tokens: zeroTokens, costUsd: 0.02 },
  ],
};

export function makeQuestion(i: number, marks: Marks, type: "mcq" | "free", withKey = true): Question {
  const q: Question = {
    id: `q${i + 1}`, marks, text: `Sample question ${i + 1} worth ${marks} mark${marks > 1 ? "s" : ""}?`,
    sourceDocId: "d1", sourceDocName: "Biology.pdf",
  };
  if (type === "mcq") {
    q.options = ["Option A", "Option B", "Option C", "Option D"];
    if (withKey) q.correctIndex = i % 4;
  } else if (withKey) q.modelAnswer = `Model answer for question ${i + 1}.`;
  return q;
}
