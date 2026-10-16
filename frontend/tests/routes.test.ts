import { describe, expect, it } from "vitest";
import { isActiveLink, isExamTakePath, isPublicPath, loginUrl, returnPath } from "../lib/routes";

describe("routes", () => {
  it("public paths", () => {
    expect(isPublicPath("/login")).toBe(true);
    expect(isPublicPath("/signup")).toBe(true);
    expect(isPublicPath("/documents")).toBe(false);
    expect(isPublicPath("/login/x")).toBe(false);
  });
  it("exam take path", () => {
    expect(isExamTakePath("/exams/e1/take")).toBe(true);
    expect(isExamTakePath("/exams/e1/result")).toBe(false);
    expect(isExamTakePath("/exams/take")).toBe(false);
  });
  it("builds next", () => {
    expect(loginUrl("/documents")).toBe("/login?next=%2Fdocuments");
    expect(loginUrl("/exams/e1/take?x=1")).toBe("/login?next=%2Fexams%2Fe1%2Ftake%3Fx%3D1");
  });
  it("active link", () => {
    expect(isActiveLink("/", "/")).toBe(true);
    expect(isActiveLink("/", "/documents")).toBe(false);
    expect(isActiveLink("/documents", "/documents/d1")).toBe(true);
    expect(isActiveLink("/create", "/documents")).toBe(false);
  });
  it("returnPath joins pathname and query", () => {
    expect(returnPath("/create", "")).toBe("/create");
    expect(returnPath("/create", "?a=1&b=2")).toBe("/create?a=1&b=2");
    expect(returnPath("/create", "a=1")).toBe("/create?a=1");
    expect(returnPath("/create", "?")).toBe("/create");
  });
});
