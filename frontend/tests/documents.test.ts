import { describe, expect, it } from "vitest";
import {
  deleteTitle, docMeta, filterAndSortDocs, generateHref, pruneSelection,
} from "../lib/documents";
import type { Doc } from "../lib/types";

process.env.TZ = "UTC";

const doc = (id: string, name: string, createdAt: string, pages = 1): Doc => ({
  id, name, type: "PDF", pages, sizeBytes: 1, createdAt,
});
const A = doc("a", "Biology.pdf", "2026-10-14T09:00:00Z", 12);
const B = doc("b", "algebra.pdf", "2026-10-12T09:00:00Z");
const C = doc("c", "Notes.pdf", "2026-10-10T09:00:00Z", 2);
const ids = (d: Doc[]) => d.map((x) => x.id);

describe("filterAndSortDocs", () => {
  it("returns everything for an empty or blank query", () => {
    expect(ids(filterAndSortDocs([C, A, B], "", "newest"))).toEqual(["a", "b", "c"]);
    expect(ids(filterAndSortDocs([C, A, B], "   ", "newest"))).toEqual(["a", "b", "c"]);
  });
  it("matches by name, case-insensitively", () => {
    expect(ids(filterAndSortDocs([A, B, C], "BIO", "newest"))).toEqual(["a"]);
    expect(ids(filterAndSortDocs([A, B, C], "ALG", "newest"))).toEqual(["b"]);
    expect(ids(filterAndSortDocs([A, B, C], "zzz", "newest"))).toEqual([]);
  });
  it("sorts by name A-Z ignoring case", () => {
    expect(ids(filterAndSortDocs([C, A, B], "", "name"))).toEqual(["b", "a", "c"]);
  });
  it("keeps the original order for equal names (stable)", () => {
    const x1 = doc("x1", "Same.pdf", "2026-10-01T00:00:00Z");
    const x2 = doc("x2", "same.PDF", "2026-10-02T00:00:00Z");
    const x3 = doc("x3", "Same.pdf", "2026-10-03T00:00:00Z");
    expect(ids(filterAndSortDocs([x2, x1, x3], "", "name"))).toEqual(["x2", "x1", "x3"]);
  });
  it("keeps the original order for equal dates when sorting newest", () => {
    const y1 = doc("y1", "a", "2026-10-01T00:00:00Z");
    const y2 = doc("y2", "b", "2026-10-01T00:00:00Z");
    expect(ids(filterAndSortDocs([y1, y2], "", "newest"))).toEqual(["y1", "y2"]);
  });
  it("does not mutate the input", () => {
    const input = [C, A, B];
    filterAndSortDocs(input, "", "name");
    expect(ids(input)).toEqual(["c", "a", "b"]);
  });
  it("treats markup in a name as plain text", () => {
    const h = doc("h", "<b>x</b>.pdf", "2026-10-01T00:00:00Z");
    expect(ids(filterAndSortDocs([A, h], "<b>", "newest"))).toEqual(["h"]);
    expect(h.name).toBe("<b>x</b>.pdf");
  });
});

describe("docMeta", () => {
  it("shows a short date and plural pages", () => {
    expect(docMeta(A)).toBe("Oct 14 · 12 pages");
  });
  it("uses singular for one page", () => {
    expect(docMeta(B)).toBe("Oct 12 · 1 page");
  });
});

describe("selection helpers", () => {
  it("pruneSelection drops ids not in the list", () => {
    expect(pruneSelection(new Set(["a", "zz"]), [A, B])).toEqual(new Set(["a"]));
  });
  it("pruneSelection returns the same set when nothing changes", () => {
    const s = new Set(["a"]);
    expect(pruneSelection(s, [A, B])).toBe(s);
  });
  it("generateHref comma-joins URL-safe ids", () => {
    expect(generateHref(["a", "b"])).toBe("/create?sources=a,b");
    expect(generateHref(["a b", "c&d"])).toBe("/create?sources=a%20b,c%26d");
  });
  it("deleteTitle is singular for one", () => {
    expect(deleteTitle(1)).toBe("Delete 1 document?");
    expect(deleteTitle(2)).toBe("Delete 2 documents?");
  });
});

describe("addDoc", () => {
  it("prepends and never duplicates an id", async () => {
    const { addDoc } = await import("../lib/documents");
    expect(ids(addDoc([A, B], C))).toEqual(["c", "a", "b"]);
    expect(ids(addDoc([A, B], { ...B, name: "new" }))).toEqual(["b", "a"]);
  });
});
