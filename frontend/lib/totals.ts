// Pure helpers for the Create page (fs 6.4).
import type { Doc, Marks } from "./types";

export type Counts = Record<Marks, number>;
export type FooterTone = "accent" | "ok" | "bad";

export const MAX_SOURCES = 10;
export const MAX_BYTES = 20 * 1024 * 1024; // 20971520
export const MAX_PDF_PAGES = 100;

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
  // The spec (6.4) dictates "{count} questions" for every count, including 1.
  const q = `${count} questions`;
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

export interface PageRange { from: number; to: number }
export type PageRanges = Record<string, PageRange>;

/** The range in force for a document: the chosen one, else the whole file. */
export function effectiveRange(pages: number, r?: PageRange): PageRange {
  return r ?? { from: 1, to: pages };
}

/** Number of pages a range covers (0 for a malformed one). */
export function rangeLength(r: PageRange): number {
  return Number.isInteger(r.from) && Number.isInteger(r.to) && r.to >= r.from ? r.to - r.from + 1 : 0;
}

/** Valid means integers with 1 <= from <= to <= pages (backend-spec 8.4). Returns an inline error or null. */
export function validateRange(r: PageRange, pages: number): string | null {
  const ok = Number.isInteger(r.from) && Number.isInteger(r.to) && r.from >= 1 && r.from <= r.to && r.to <= pages;
  return ok ? null : `Enter whole page numbers from 1 to ${pages}, with the first page not after the last.`;
}

export function isWholeFile(r: PageRange, pages: number): boolean {
  return r.from === 1 && r.to === pages;
}

/** PDF pages the selection will use: each PDF counts its range (0 if malformed), other files none. */
export function pdfPageTotal(sel: Pick<Doc, "id" | "type" | "pages">[], ranges: PageRanges = {}): number {
  return sel.reduce(
    (sum, d) => sum + (d.type === "PDF" ? rangeLength(effectiveRange(d.pages, ranges[d.id])) : 0), 0);
}

/** Local mirror of the backend caps (fs 6.4). PDFs count their selected range, other files no pages.
 *  Returns an inline error or null. */
export function sourceLimitError(
  sel: Pick<Doc, "id" | "type" | "sizeBytes" | "pages">[],
  ranges: PageRanges = {},
): string | null {
  if (sel.length > MAX_SOURCES) return "Pick at most 10 files.";
  if (sel.reduce((s, d) => s + d.sizeBytes, 0) > MAX_BYTES) return "The selected files are over 20 MB in total.";
  const pdfPages = pdfPageTotal(sel, ranges);
  if (pdfPages > MAX_PDF_PAGES) {
    return `The selected PDFs contain ${pdfPages} pages. The limit is ${MAX_PDF_PAGES} pages per question set. Select fewer PDFs or narrow a page range.`;
  }
  return null;
}

/** Body field: only PDFs whose range is not the whole file, or undefined when there are none. */
export function pageRangesBody(
  sel: Pick<Doc, "id" | "type" | "pages">[], ranges: PageRanges,
): PageRanges | undefined {
  const out: PageRanges = {};
  for (const d of sel) {
    const r = ranges[d.id];
    if (d.type === "PDF" && r && !isWholeFile(r, d.pages)) out[d.id] = { from: r.from, to: r.to };
  }
  return Object.keys(out).length ? out : undefined;
}
