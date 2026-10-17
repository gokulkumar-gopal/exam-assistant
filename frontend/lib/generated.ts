// Hand-off of a finished generation to the question list (F-2.4): memory first, sessionStorage as backup.
import type { ExamType, Question } from "./types";

export interface GeneratedParams {
  sourceIds: string[]; type: ExamType; counts: Record<string, number>; shuffle: boolean; target: number;
  pageRanges?: Record<string, { from: number; to: number }>;
}
export interface GeneratedResult { questions: Question[]; model: string; params: GeneratedParams }

const KEY = "ea.generated";
let memory: GeneratedResult | null = null;

export function setGenerated(r: GeneratedResult): void {
  memory = r;
  try { sessionStorage.setItem(KEY, JSON.stringify(r)); } catch { /* storage unavailable */ }
}

export function getGenerated(): GeneratedResult | null {
  if (memory) return memory;
  try {
    const raw = sessionStorage.getItem(KEY);
    if (raw) memory = JSON.parse(raw) as GeneratedResult;
  } catch { /* storage unavailable or corrupt */ }
  return memory;
}

export function clearGenerated(): void {
  memory = null;
  try { sessionStorage.removeItem(KEY); } catch { /* storage unavailable */ }
}
