// Pure mapping of the POST /auth/google outcome to a UI state (fs 6.8, backend-spec 8.2).
export const GOOGLE_COPY = {
  failure: "Something went wrong. Try again.",
  unauthorized: "Google sign-in didn't work. Try again.",
  needRole: "Choose who you are to finish creating your account.",
  unavailable: "Google sign-in isn't available here. Use email and password.",
} as const;

export type GoogleOutcome =
  | { kind: "success" }
  | { kind: "needRole" }
  | { kind: "unauthorized"; message: string }
  | { kind: "conflict"; message: string }
  | { kind: "failure"; message: string };

export function googleOutcome(r: { status: number; code?: string; message?: string }): GoogleOutcome {
  if (r.status === 200 || r.status === 201) return { kind: "success" };
  if (r.status === 422 && r.code === "role_required") return { kind: "needRole" };
  if (r.status === 401) return { kind: "unauthorized", message: GOOGLE_COPY.unauthorized };
  if (r.status === 409) return { kind: "conflict", message: r.message || GOOGLE_COPY.failure };
  // Network (status 0) keeps its own message; anything else gets generic copy.
  return { kind: "failure", message: r.status === 0 && r.message ? r.message : GOOGLE_COPY.failure };
}
