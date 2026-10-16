// Pure helpers for the documents library (fs 6.2). Names are plain text; nothing here builds HTML.
import { shortDate } from "./format";
import type { Doc } from "./types";

export type DocSort = "newest" | "name";

/** Case-insensitive name filter, then sort. Array.sort is stable, so equal keys keep list order. */
export function filterAndSortDocs(docs: Doc[], query: string, sort: DocSort): Doc[] {
  const q = query.trim().toLowerCase();
  const out = q ? docs.filter((d) => d.name.toLowerCase().includes(q)) : docs.slice();
  if (sort === "name") {
    out.sort((a, b) => a.name.toLowerCase().localeCompare(b.name.toLowerCase(), "en"));
  } else {
    out.sort((a, b) => Date.parse(b.createdAt) - Date.parse(a.createdAt));
  }
  return out;
}

/** "Oct 14 · 12 pages" / "Oct 12 · 1 page". */
export function docMeta(doc: Doc): string {
  return `${shortDate(Date.parse(doc.createdAt))} · ${doc.pages} ${doc.pages === 1 ? "page" : "pages"}`;
}

/** Keep only ids still in the list. Returns the same Set when nothing is dropped. */
export function pruneSelection(selected: Set<string>, docs: Doc[]): Set<string> {
  const live = new Set(docs.map((d) => d.id));
  let changed = false;
  const next = new Set<string>();
  for (const id of selected) {
    if (live.has(id)) next.add(id);
    else changed = true;
  }
  return changed ? next : selected;
}

/** Commas stay literal; each id is encoded. */
export function generateHref(ids: string[]): string {
  return `/create?sources=${ids.map(encodeURIComponent).join(",")}`;
}

export function deleteTitle(n: number): string {
  return `Delete ${n} ${n === 1 ? "document" : "documents"}?`;
}
