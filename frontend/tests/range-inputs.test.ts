import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { SourcesCard } from "../components/SourcesCard";
import { pdfPageTotal } from "../lib/totals";
import type { Doc } from "../lib/types";

const pdf = (id: string, pages: number): Doc => ({
  id, name: `${id}.pdf`, type: "PDF", pages, sizeBytes: 1000, createdAt: "2026-10-17T00:00:00Z",
});

function render(docs: Doc[], selected: string[], ranges: Record<string, { from: number; to: number }> = {}) {
  return renderToStaticMarkup(createElement(SourcesCard, {
    docs, selected: new Set(selected), onToggle: () => {}, onRange: () => {}, ranges, error: null,
  }));
}

describe("SourcesCard page range inputs", () => {
  it("shows labelled first and last page inputs defaulting to the whole file", () => {
    const html = render([pdf("book", 300)], ["book"]);
    expect(html).toContain('aria-label="book.pdf first page"');
    expect(html).toContain('aria-label="book.pdf last page"');
    expect(html).toContain('value="1"');
    expect(html).toContain('value="300"');
    expect(html).toContain("of 300");
    expect(html).not.toContain('role="alert"');
  });
  it("shows nothing for unchecked, single-page and non-PDF sources", () => {
    expect(render([pdf("book", 300)], [])).not.toContain("first page");
    expect(render([pdf("one", 1)], ["one"])).not.toContain("first page");
    const txt: Doc = { ...pdf("n", 5), type: "TXT", name: "n.txt" };
    expect(render([txt], ["n"])).not.toContain("first page");
  });
  it("shows the chosen range and an inline alert for an invalid one", () => {
    const ok = render([pdf("book", 300)], ["book"], { book: { from: 41, to: 80 } });
    expect(ok).toContain('value="41"');
    expect(ok).toContain('value="80"');
    expect(ok).not.toContain('role="alert"');
    const bad = render([pdf("book", 300)], ["book"], { book: { from: 80, to: 41 } });
    expect(bad).toContain('role="alert"');
    expect(bad).toContain("1 to 300");
    expect(bad).toContain('aria-invalid="true"');
  });
  it("treats an emptied field as invalid", () => {
    const html = render([pdf("book", 300)], ["book"], { book: { from: NaN, to: 80 } });
    expect(html).toContain('role="alert"');
  });
});

describe("SourcesCard page slider", () => {
  it("shows a two-handle slider with named handles and a live count", () => {
    const html = render([pdf("book", 300)], ["book"], { book: { from: 41, to: 80 } });
    expect(html.match(/type="range"/g)?.length).toBe(2);
    expect(html).toContain('aria-label="book.pdf first page slider"');
    expect(html).toContain('aria-label="book.pdf last page slider"');
    expect(html).toContain("40 of 300 pages");
  });
  it("offers Use all pages only when the range is not the whole file", () => {
    expect(render([pdf("book", 300)], ["book"])).not.toContain("Use all pages");
    expect(render([pdf("book", 300)], ["book"])).toContain("Whole file");
    expect(render([pdf("book", 300)], ["book"], { book: { from: 2, to: 300 } })).toContain("Use all pages");
  });
  it("keeps the slider sane while a number box is invalid", () => {
    const html = render([pdf("book", 300)], ["book"], { book: { from: NaN, to: 9999 } });
    expect(html).toContain('role="alert"');
    expect(html).toContain('type="range"');
  });
  it("shows the page budget meter for selected PDFs, using the range", () => {
    const html = render([pdf("book", 300), pdf("b", 20)], ["book", "b"], { book: { from: 1, to: 80 } });
    expect(html).toContain('role="meter"');
    expect(html).toContain('aria-valuenow="100"');
    expect(html).toContain("100 / 100 pages");
    expect(render([pdf("book", 300)], [])).not.toContain('role="meter"');
  });
  it("turns the meter red and shows the overage when over the cap", () => {
    const html = render([pdf("a", 80), pdf("b", 40)], ["a", "b"]);
    expect(html).toContain("120 / 100 pages");
    expect(html).toContain("text-bad");
    expect(html).toContain('aria-valuenow="100"');
  });
});

describe("pdfPageTotal", () => {
  it("sums ranges for PDFs and ignores other files", () => {
    const sel = [pdf("a", 50), pdf("b", 30), { ...pdf("n", 9), type: "TXT" as const, name: "n.txt" }];
    expect(pdfPageTotal(sel)).toBe(80);
    expect(pdfPageTotal(sel, { a: { from: 11, to: 20 } })).toBe(40);
    expect(pdfPageTotal(sel, { a: { from: 20, to: 11 } })).toBe(30);
  });
});

