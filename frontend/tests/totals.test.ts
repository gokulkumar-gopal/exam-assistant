import { describe, expect, it } from "vitest";
import {
  effectiveRange, footerState, MAX_PDF_PAGES, pageRangesBody, parseSources, rangeLength, sourceLimitError,
  totalMarks, questionCount, validateRange,
} from "../lib/totals";
import type { Doc } from "../lib/types";

const zero = { 1: 0, 2: 0, 3: 0, 4: 0, 5: 0 };

describe("totalMarks / questionCount", () => {
  it("sums marks times count", () => {
    expect(totalMarks({ ...zero, 1: 2, 3: 2 })).toBe(8);
    expect(totalMarks(zero)).toBe(0);
    expect(questionCount({ ...zero, 1: 2, 3: 2 })).toBe(4);
  });
});

describe("footerState", () => {
  it("under target", () => {
    expect(footerState(28, 30, 12, true)).toEqual({ tone: "accent", text: "2 to go · 12 questions", generateEnabled: false });
  });
  it("at target", () => {
    expect(footerState(30, 30, 12, true)).toEqual({ tone: "ok", text: "Ready to generate · 12 questions", generateEnabled: true });
  });
  it("over target", () => {
    expect(footerState(32, 30, 12, true)).toEqual({ tone: "bad", text: "2 over · 12 questions", generateEnabled: false });
  });
  it("no source forces disabled even at target", () => {
    const s = footerState(30, 30, 12, false);
    expect(s.generateEnabled).toBe(false);
    expect(s.tone).toBe("ok");
  });
  it("spec text keeps the plural for 1", () => {
    expect(footerState(5, 30, 1, true).text).toBe("25 to go · 1 questions");
  });
});

describe("parseSources", () => {
  const ready = ["a", "b", "c"];
  it("keeps known ids in param order, drops unknown and duplicates", () => {
    expect(parseSources("c,x,a,c, ,b", ready)).toEqual(["c", "a", "b"]);
  });
  it("null or empty gives none", () => {
    expect(parseSources(null, ready)).toEqual([]);
    expect(parseSources("", ready)).toEqual([]);
  });
});

describe("sourceLimitError", () => {
  const d = (n: number, bytes = 1000, pages = 1, type: Doc["type"] = "PDF") =>
    Array.from({ length: n }, (_, i) => ({ id: `d${i}`, type, sizeBytes: bytes, pages }));
  it("ok within caps", () => {
    expect(sourceLimitError(d(10, 2097152, 4))).toBeNull();
    expect(MAX_PDF_PAGES).toBe(100);
  });
  it("too many files", () => {
    expect(sourceLimitError(d(11))).toMatch(/10 files/);
  });
  it("too many bytes", () => {
    expect(sourceLimitError(d(2, 10485761))).toMatch(/20 MB/);
  });
  it("too many pages", () => {
    expect(sourceLimitError(d(2, 1000, 51))).toMatch(/102 pages.*100 pages/);
  });
  it("exactly at caps is fine", () => {
    expect(sourceLimitError(d(2, 10485760, 50))).toBeNull();
  });
  it("does not treat estimated TXT pages as PDF pages for four small files", () => {
    expect(sourceLimitError(d(4, 64_000, 22, "TXT"))).toBeNull();
  });
  it("allows 100 PDF pages together with images and TXT", () => {
    expect(sourceLimitError([
      ...d(1, 1000, 100),
      ...d(1, 1000, 1, "JPG"),
      ...d(1, 1000, 1, "PNG"),
      ...d(1, 1000, 20, "TXT"),
    ])).toBeNull();
  });
  it("explains that small PDFs still exceed the page cap and shows the total", () => {
    expect(sourceLimitError(d(4, 1000, 26))).toBe(
      "The selected PDFs contain 104 pages. The limit is 100 pages per question set. Select fewer PDFs or narrow a page range.",
    );
  });
  it("clears the page error when the oversized PDF is no longer selected", () => {
    const large = d(1, 1000, 150);
    const small = d(4, 1000, 2);
    expect(sourceLimitError([...large, ...small])).toContain("100 pages");
    expect(sourceLimitError(small)).toBeNull();
  });
  it("sums the selected range length for a PDF instead of its page count", () => {
    const sel = d(1, 1000, 300);
    expect(sourceLimitError(sel)).toContain("300 pages");
    expect(sourceLimitError(sel, { d0: { from: 41, to: 140 } })).toBeNull();
    expect(sourceLimitError(sel, { d0: { from: 41, to: 141 } })).toContain("101 pages");
  });
  it("ignores ranges on non-PDF files and counts a malformed range as zero", () => {
    expect(sourceLimitError(d(1, 1000, 300, "TXT"), { d0: { from: 1, to: 300 } })).toBeNull();
    expect(sourceLimitError(d(1, 1000, 300), { d0: { from: NaN, to: 5 } })).toBeNull();
  });
});

describe("page range helpers", () => {
  it("defaults to the whole file and measures length", () => {
    expect(effectiveRange(12)).toEqual({ from: 1, to: 12 });
    expect(effectiveRange(12, { from: 3, to: 4 })).toEqual({ from: 3, to: 4 });
    expect(rangeLength({ from: 41, to: 80 })).toBe(40);
    expect(rangeLength({ from: 5, to: 4 })).toBe(0);
  });
  it("validates integers with 1 <= from <= to <= pages", () => {
    expect(validateRange({ from: 1, to: 12 }, 12)).toBeNull();
    expect(validateRange({ from: 12, to: 12 }, 12)).toBeNull();
    for (const r of [{ from: 0, to: 3 }, { from: 5, to: 4 }, { from: 1, to: 13 }, { from: 1.5, to: 3 }, { from: NaN, to: 3 }, { from: 1, to: NaN }]) {
      expect(validateRange(r, 12), JSON.stringify(r)).toMatch(/1 to 12/);
    }
  });
  it("pageRangesBody keeps only PDFs whose range is not the whole file", () => {
    const sel = [
      { id: "a", type: "PDF" as const, pages: 300 },
      { id: "b", type: "PDF" as const, pages: 10 },
      { id: "c", type: "TXT" as const, pages: 5 },
    ];
    expect(pageRangesBody(sel, { a: { from: 41, to: 80 }, b: { from: 1, to: 10 }, c: { from: 1, to: 2 }, z: { from: 1, to: 2 } }))
      .toEqual({ a: { from: 41, to: 80 } });
    expect(pageRangesBody(sel, { b: { from: 1, to: 10 } })).toBeUndefined();
    expect(pageRangesBody(sel, {})).toBeUndefined();
  });
});
