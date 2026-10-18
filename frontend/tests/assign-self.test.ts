import { describe, expect, it } from "vitest";
import { othersOf, parseEmails, withSelf } from "@/lib/publish";
import { defaultMaxPeeks } from "@/lib/peeks";

describe("Assign to me", () => {
  it("appends the creator once and not when unticked", () => {
    expect(withSelf(["a@x.com"], "Me@X.com", true)).toEqual(["a@x.com", "me@x.com"]);
    expect(withSelf(["a@x.com"], "me@x.com", false)).toEqual(["a@x.com"]);
    expect(withSelf([], undefined, true)).toEqual([]);
  });
  it("dedupes a typed copy of the creator's email", () => {
    expect(withSelf(["me@x.com", "a@x.com"], "me@x.com", true)).toEqual(["me@x.com", "a@x.com"]);
  });
  it("counts toward the cap", () => {
    const ten = Array.from({ length: 10 }, (_, i) => `s${i}@x.com`).join(",");
    expect(parseEmails(ten, "student", ["me@x.com"]).error).toMatch(/up to 10/);
    expect(parseEmails("a@x.com", "student", ["me@x.com"]).error).toBeNull();
  });
  it("keeps the practice peek default when only the creator is assigned", () => {
    expect(defaultMaxPeeks(othersOf(withSelf([], "me@x.com", true), "me@x.com"))).toBe(3);
    expect(defaultMaxPeeks(othersOf(withSelf(["a@x.com"], "me@x.com", true), "me@x.com"))).toBe(0);
  });
});
