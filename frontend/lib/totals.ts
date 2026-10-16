// Pure helpers for the Create page (fs 6.4).
import type { Marks } from "./types";

export type Counts = Record<Marks, number>;
export type FooterTone = "accent" | "ok" | "bad";

export const MAX_SOURCES = 10;
export const MAX_BYTES = 20 * 1024 * 1024; // 20971520
export const MAX_PAGES = 40;

const KEYS: Marks[] = [1, 2, 3, 4, 5];

export function totalMarks(counts: Counts): number {
  return KEYS.reduce((sum, k) => sum + k * (counts[k] ?? 0), 0);
}

export function questionCount(counts: Counts): number {
  return KEYS.reduce((sum, k) => sum + (counts[k] ?? 0), 0);
}

export function footerState(
  total: number,
  target: number,
  count: number,
  hasSource: boolean,
): { tone: FooterTone; text: string; generateEnabled: boolean } {
  const q = `${count} ${count === 1 ? "question" : "questions"}`;
  if (total < target) return { tone: "accent", text: `${target - total} to go · ${q}`, generateEnabled: false };
  if (total > target) return { tone: "bad", text: `${total - target} over · ${q}`, generateEnabled: false };
  return { tone: "ok", text: `Ready to generate · ${q}`, generateEnabled: hasSource };
}

/** Ids from `?sources=a,b`: only ids in `readyIds`, in param order, no duplicates. */
export function parseSources(param: string | null, readyIds: string[]): string[] {
  if (!param) return [];
  const ok = new Set(readyIds);
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of param.split(",")) {
    const id = raw.trim();
    if (id && ok.has(id) && !seen.has(id)) {
      seen.add(id);
      out.push(id);
    }
  }
  return out;
}

/** Local mirror of the backend caps (fs 6.4). Returns an inline error or null. */
export function sourceLimitError(sel: { sizeBytes: number; pages: number }[]): string | null {
  if (sel.length > MAX_SOURCES) return "Pick at most 10 files.";
  if (sel.reduce((s, d) => s + d.sizeBytes, 0) > MAX_BYTES) return "The selected files are over 20 MB in total.";
  if (sel.reduce((s, d) => s + d.pages, 0) > MAX_PAGES) return "The selected files are over 40 pages in total.";
  return null;
}
