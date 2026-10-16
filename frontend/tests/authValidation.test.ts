import { describe, expect, it } from "vitest";
import { validateEmail, validateName, validatePassword, validateLogin, validateSignup } from "../lib/authValidation";

describe("validateName", () => {
  it("requires 1..80 characters", () => {
    expect(validateName("")).toBeTruthy();
    expect(validateName("   ")).toBeTruthy();
    expect(validateName("A")).toBeNull();
    expect(validateName("a".repeat(80))).toBeNull();
    expect(validateName("a".repeat(81))).toBeTruthy();
  });
});
describe("validateEmail", () => {
  it("accepts valid and rejects invalid", () => {
    expect(validateEmail("a@b.co")).toBeNull();
    expect(validateEmail("  a@b.co ")).toBeNull();
    expect(validateEmail("")).toBeTruthy();
    expect(validateEmail("a@b")).toBeTruthy();
    expect(validateEmail("a b@c.com")).toBeTruthy();
    expect(validateEmail("@c.com")).toBeTruthy();
  });
});
describe("validatePassword (signup)", () => {
  it("needs 8 chars and at most 72 bytes", () => {
    expect(validatePassword("1234567")).toBeTruthy();
    expect(validatePassword("12345678")).toBeNull();
    expect(validatePassword("a".repeat(72))).toBeNull();
    expect(validatePassword("a".repeat(73))).toBeTruthy();
    expect(validatePassword("é".repeat(37))).toBeTruthy(); // 74 bytes
    expect(validatePassword("é".repeat(36))).toBeNull(); // 72 bytes
  });
});
describe("form validators", () => {
  it("login only requires email and a non-empty password", () => {
    expect(validateLogin({ email: "a@b.co", password: "x" })).toEqual({});
    expect(Object.keys(validateLogin({ email: "", password: "" })).sort()).toEqual(["email", "password"]);
  });
  it("signup checks all fields and role", () => {
    const ok = { name: "A", email: "a@b.co", password: "12345678", role: "student" as const };
    expect(validateSignup(ok)).toEqual({});
    expect(Object.keys(validateSignup({ name: "", email: "x", password: "1", role: null })).sort()).toEqual(
      ["email", "name", "password", "role"],
    );
  });
});
