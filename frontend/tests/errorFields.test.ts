import { describe, expect, it } from "vitest";
import { errorMessageWithFields } from "../lib/api";
import { failedSourceNames } from "../lib/generate";

describe("errorMessageWithFields", () => {
  const base = "Some fields need attention.";
  it("uses the first field message in response key order", () => {
    expect(errorMessageWithFields({ message: base, fields: { deadline: "Pick a deadline within 30 days.", title: "x" } })).toBe("Pick a deadline within 30 days.");
  });
  it("falls back when fields is empty or missing", () => {
    expect(errorMessageWithFields({ message: base, fields: {} })).toBe(base);
    expect(errorMessageWithFields({ message: base })).toBe(base);
  });
  it("skips non-string and blank values", () => {
    expect(errorMessageWithFields({ message: base, fields: { a: 3, b: null, c: " ", d: "Real" } })).toBe("Real");
    expect(errorMessageWithFields({ message: base, fields: { a: 3, b: {} } })).toBe(base);
  });
});

describe("failedSourceNames", () => {
  it("extracts names from objects", () => {
    expect(failedSourceNames([{ docId: "d1", name: "a.jpg", reason: "r" }, { docId: "d2", name: "b.pdf", reason: "r" }])).toEqual(["a.jpg", "b.pdf"]);
  });
  it("tolerates legacy strings and junk without [object Object]", () => {
    const out = failedSourceNames(["old.jpg", { docId: "d" }, null, 5, { name: "" }, { name: "ok.png" }]);
    expect(out).toEqual(["old.jpg", "ok.png"]);
    expect(out.join(",")).not.toContain("[object");
  });
  it("handles non-arrays", () => {
    expect(failedSourceNames(undefined)).toEqual([]);
    expect(failedSourceNames(null)).toEqual([]);
  });
});
