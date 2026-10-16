import { describe, expect, it } from "vitest";
import { googleOutcome, GOOGLE_COPY } from "../lib/googleAuth";

describe("googleOutcome", () => {
  it("200 and 201 are success", () => {
    expect(googleOutcome({ status: 200 })).toEqual({ kind: "success" });
    expect(googleOutcome({ status: 201 })).toEqual({ kind: "success" });
  });
  it("422 role_required asks for a role", () => {
    expect(googleOutcome({ status: 422, code: "role_required" })).toEqual({ kind: "needRole" });
  });
  it("other 422 is a failure, not a role request", () => {
    expect(googleOutcome({ status: 422, code: "validation", message: "bad" })).toEqual({ kind: "failure", message: GOOGLE_COPY.failure });
  });
  it("401 is unauthorized with the fixed copy (ignores backend text)", () => {
    expect(googleOutcome({ status: 401, message: "Google sign-in failed. Try again." })).toEqual({ kind: "unauthorized", message: "Google sign-in didn't work. Try again." });
  });
  it("409 shows the backend message", () => {
    expect(googleOutcome({ status: 409, message: "This email is already linked to another sign-in. Use the original sign-in method." }))
      .toEqual({ kind: "conflict", message: "This email is already linked to another sign-in. Use the original sign-in method." });
  });
  it("409 without a message falls back to generic copy", () => {
    expect(googleOutcome({ status: 409 })).toEqual({ kind: "conflict", message: GOOGLE_COPY.failure });
  });
  it("network and 5xx are failures", () => {
    expect(googleOutcome({ status: 0, message: "Couldn't reach the server. Try again." })).toEqual({ kind: "failure", message: "Couldn't reach the server. Try again." });
    expect(googleOutcome({ status: 500 })).toEqual({ kind: "failure", message: GOOGLE_COPY.failure });
  });
});
