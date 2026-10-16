// F-7.2 lint-style checks (fs 9 Accessibility, fs 1 rule 1): read the sources, no DOM.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

const ROOT = join(__dirname, "..");
function walk(dir: string, out: string[] = []): string[] {
  for (const n of readdirSync(dir)) {
    const p = join(dir, n);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(tsx|css)$/.test(n)) out.push(p);
  }
  return out;
}
const read = (p: string) => readFileSync(p, "utf8");
const tsx = ["app", "components"].flatMap((d) => walk(join(ROOT, d))).filter((p) => p.endsWith(".tsx"));
const css = read(join(ROOT, "app/globals.css"));

describe("focus ring", () => {
  it("global :focus-visible draws a 2px accent outline offset 2px", () => {
    expect(css).toMatch(/:focus-visible\s*{[^}]*outline:\s*2px solid var\(--accent\);[^}]*outline-offset:\s*2px/);
  });
  it("no outline removal without a replacement on the same line", () => {
    const bad: string[] = [];
    for (const f of tsx) {
      read(f).split("\n").forEach((line, i) => {
        if (!/outline-none|outline-hidden|focus:outline-none/.test(line)) return;
        // Replacement: a focus-visible ring/outline, a highlighted/focus background, or a non-interactive programmatic-focus target.
        if (/focus-visible:|data-\[highlighted\]|focus:bg-|tabIndex=\{-1\}/.test(line)) return;
        if (/(Popup|Content)|popup|dialog|popover/i.test(line) || /bg-popover/.test(line)) return; // overlay surfaces take focus programmatically
        bad.push(`${f.replace(ROOT, "")}:${i + 1}`);
      });
    }
    expect(bad).toEqual([]);
  });
});

describe("icon-only buttons", () => {
  it("every <button> whose only child is an icon has an aria-label", () => {
    const bad: string[] = [];
    for (const f of tsx) {
      const src = read(f);
      for (const m of src.matchAll(/<button\b([^>]*)>\s*<([A-Z][A-Za-z0-9]*)\b[^>]*\/>\s*<\/button>/g)) {
        if (!/aria-label=/.test(m[1])) bad.push(`${f.replace(ROOT, "")}: ${m[2]}`);
      }
    }
    expect(bad).toEqual([]);
  });
});

describe("reduced motion", () => {
  const block = css.slice(css.indexOf("@media (prefers-reduced-motion: reduce)"));
  it("turns off transitions and animations (bar slide, progress)", () => {
    expect(block).toMatch(/transition:\s*none\s*!important/);
    expect(block).toMatch(/animation-duration:\s*0\.01ms\s*!important/);
  });
});

describe("Helvetica", () => {
  it("form controls and tables inherit the font", () => {
    expect(css).toMatch(/button,\s*input,\s*select,\s*textarea,\s*table\s*{[^}]*font-family:\s*inherit/);
  });
  it("the stack is exactly Helvetica Neue, Helvetica, Arial, sans-serif", () => {
    expect(css).toContain('--font-stack: "Helvetica Neue", Helvetica, Arial, sans-serif;');
  });
  it("no other font family is declared in sources", () => {
    const files = ["app", "components", "styles"].flatMap((d) => walk(join(ROOT, d))).concat(join(ROOT, "app/globals.css"));
    const bad: string[] = [];
    for (const f of files) {
      const s = read(f);
      const decls = [...s.matchAll(/font-family:\s*([^;]+);/g)].map((m) => m[1].trim());
      const okDecl = (v: string) => v === "inherit" || v === "var(--font-stack)" || v === '"Helvetica Neue", Helvetica, Arial, sans-serif';
      const utility = f.endsWith(".tsx") && /\bfont-(mono|serif)\b|next\/font/.test(s);
      if (utility || decls.some((v) => !okDecl(v))) bad.push(f.replace(ROOT, ""));
    }
    expect(bad).toEqual([]);
  });
});
