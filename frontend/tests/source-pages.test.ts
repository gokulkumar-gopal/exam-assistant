import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { SourcesCard } from "../components/SourcesCard";
import type { Doc } from "../lib/types";

function document(type: Doc["type"], pages: number): Doc {
  return {
    id: type,
    name: `study.${type.toLowerCase()}`,
    type,
    pages,
    sizeBytes: 1000,
    createdAt: "2026-10-17T00:00:00Z",
  };
}

function render(docs: Doc[]) {
  return renderToStaticMarkup(createElement(SourcesCard, {
    docs,
    selected: new Set(docs.map((d) => d.id)),
    onToggle: () => {},
    error: null,
  }));
}

describe("SourcesCard page-limit explanation", () => {
  it("shows each PDF's page count and identifies the PDF-only limit", () => {
    const html = render([document("PDF", 11)]);
    expect(html).toContain("11 PDF pages");
    expect(html).toContain("100 PDF pages");
    expect(html).toContain("20 MB");
  });

  it("does not present estimated TXT pages as physical PDF pages", () => {
    const html = render([document("TXT", 50)]);
    expect(html).not.toContain("50 PDF pages");
    expect(html).toContain("TXT");
  });
});
