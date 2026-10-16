import { describe, expect, it } from "vitest";
import { ApiError } from "@/lib/api";
import { FORBIDDEN_TOAST, isForbidden } from "@/lib/forbidden";

describe("forbidden (fs 8.1: 403 toasts and goes Home)", () => {
  it("recognises only a 403 ApiError", () => {
    expect(isForbidden(new ApiError(403, "forbidden", "x"))).toBe(true);
    expect(isForbidden(new ApiError(404, "not_found", "x"))).toBe(false);
    expect(isForbidden(new Error("403"))).toBe(false);
  });
  it("uses the spec toast text", () => {
    expect(FORBIDDEN_TOAST).toBe("You don't have access to that.");
  });
});
