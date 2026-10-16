// F-7.2 lint-style checks (fs 9 Accessibility, fs 1 rule 1): read the sources, no DOM.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import ts from "typescript";
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
      const lines = read(f).split("\n");
      lines.forEach((l, i) => {
        if (!/outline-none|outline-hidden|focus:outline-none/.test(l)) return;
        // Look at the whole JSX tag/expression (up to 6 lines each way) so a split className cannot evade the check.
        const line = lines.slice(Math.max(0, i - 6), i + 7).join(" ");
        // Replacement: a focus-visible ring/outline, a highlighted/focus background, or a non-interactive programmatic-focus target.
        if (/focus-visible:|data-\[highlighted\]|focus:bg-|tabIndex=\{-1\}/.test(line)) return;
        if (/(Popup|Content)|popup|dialog|popover/i.test(line) || /bg-popover/.test(line)) return; // overlay surfaces take focus programmatically
        bad.push(`${f.replace(ROOT, "")}:${i + 1}`);
      });
    }
    expect(bad).toEqual([]);
  });
});

// Icon-only controls found by a real TypeScript AST pass (tolerates `=>` and multi-line attributes).
const MIN_ICON_ONLY = 7; // pinned: the scan must keep finding the known controls (Zoom in/out, Stepper +/-, ...)
function iconOnlyControls(file: string, src: string): { where: string; ok: boolean }[] {
  const sf = ts.createSourceFile(file, src, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const icons = new Set<string>();
  sf.forEachChild((n) => {
    if (ts.isImportDeclaration(n) && ts.isStringLiteral(n.moduleSpecifier) && n.moduleSpecifier.text === "lucide-react") {
      const nb = n.importClause?.namedBindings;
      if (nb && ts.isNamedImports(nb)) nb.elements.forEach((e) => icons.add(e.name.text));
    }
  });
  const out: { where: string; ok: boolean }[] = [];
  const isIcon = (c: ts.JsxChild) =>
    (ts.isJsxSelfClosingElement(c) && icons.has(c.tagName.getText(sf))) ||
    (ts.isJsxElement(c) && icons.has(c.openingElement.tagName.getText(sf)));
  const labelled = (attrs: ts.JsxAttributes) =>
    attrs.properties.some((p) => ts.isJsxAttribute(p) && /^aria-(label|labelledby)$/.test(p.name.getText(sf)));
  const visit = (n: ts.Node) => {
    if (ts.isJsxElement(n)) {
      const kids = n.children.filter((c) => !(ts.isJsxText(c) && c.containsOnlyTriviaWhiteSpaces));
      const tag = n.openingElement.tagName.getText(sf);
      // Controls: <button>, <Button>, anything ending in Trigger/Close/Action/Item (Menu.Trigger, Dialog.Close, ...).
      const control = /^(button|Button)$|(Trigger|Close|Action|Item)$/.test(tag);
      if (control && kids.length === 1 && isIcon(kids[0])) {
        const { line } = sf.getLineAndCharacterOfPosition(n.getStart(sf));
        out.push({ where: `${file.replace(ROOT, "")}:${line + 1} <${tag}>`, ok: labelled(n.openingElement.attributes) });
      }
    }
    ts.forEachChild(n, visit);
  };
  visit(sf);
  return out;
}

describe("icon-only controls", () => {
  const found = tsx.flatMap((f) => iconOnlyControls(f, read(f)));
  it(`finds at least ${MIN_ICON_ONLY} icon-only controls (scan cannot silently go vacuous)`, () => {
    expect(found.length).toBeGreaterThanOrEqual(MIN_ICON_ONLY);
  });
  it("every icon-only control has aria-label or aria-labelledby", () => {
    expect(found.filter((c) => !c.ok).map((c) => c.where)).toEqual([]);
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
