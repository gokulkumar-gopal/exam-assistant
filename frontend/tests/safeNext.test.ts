import { describe, expect, it } from "vitest";
import { safeNext } from "../lib/safeNext";

describe("safeNext", () => {
  it("keeps same-origin paths", () => {
    expect(safeNext("/documents")).toBe("/documents");
    expect(safeNext("/documents?x=1")).toBe("/documents?x=1");
    expect(safeNext("/exams/e1/take")).toBe("/exams/e1/take");
    expect(safeNext("/")).toBe("/");
  });
  it("rejects protocol-relative and backslash forms", () => {
    expect(safeNext("//evil.com")).toBe("/");
    expect(safeNext("/\\evil.com")).toBe("/");
    expect(safeNext("/a\\b")).toBe("/");
    expect(safeNext("/%5Cevil.com")).toBe("/");
  });
  it("rejects absolute and scripted URLs", () => {
    expect(safeNext("https://evil.com")).toBe("/");
    expect(safeNext("javascript:alert(1)")).toBe("/");
    expect(safeNext("evil.com")).toBe("/");
  });
  it("rejects empty and null", () => {
    expect(safeNext("")).toBe("/");
    expect(safeNext(null)).toBe("/");
  });
  it("rejects a value that decodes to //", () => {
    expect(safeNext("/%2F%2Fevil.com")).toBe("/");
    expect(safeNext("/%2fevil.com")).toBe("/");
  });
  it("rejects control characters (browsers strip tabs and newlines)", () => {
    expect(safeNext("/\t/evil.com")).toBe("/");
    expect(safeNext("/%09/evil.com")).toBe("/");
    expect(safeNext("/a\nb")).toBe("/");
  });
  it("rejects malformed percent-encoding", () => {
    expect(safeNext("/%E0%A4%A")).toBe("/");
  });
  it("rejects a very long value", () => {
    expect(safeNext("/" + "a".repeat(5000))).toBe("/");
  });
});
