import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { SourcesCard } from "../components/SourcesCard";
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
