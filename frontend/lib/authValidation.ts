// Pure field validators for login/signup (fs 6.8, backend-spec 8.2).
// Each returns an error message, or null when the value is fine.
import type { Role } from "@/lib/types";

export const PASSWORD_MIN = 8;
export const PASSWORD_MAX_BYTES = 72; // bcrypt limit
export const NAME_MAX = 80;

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

export function validateName(name: string): string | null {
  const n = name.trim();
  if (n.length === 0) return "Enter your name.";
  if (Array.from(n).length > NAME_MAX) return `Name must be ${NAME_MAX} characters or fewer.`;
  return null;
}

export function validateEmail(email: string): string | null {
  const e = email.trim();
  if (e.length === 0) return "Enter your email.";
  if (e.length > 254 || !EMAIL_RE.test(e)) return "Enter a valid email address.";
  return null;
}

export function validatePassword(password: string): string | null {
  if (password.length < PASSWORD_MIN) return `Password must be at least ${PASSWORD_MIN} characters.`;
  if (new TextEncoder().encode(password).length > PASSWORD_MAX_BYTES) {
    return `Password must be at most ${PASSWORD_MAX_BYTES} bytes. Use a shorter one.`;
  }
  return null;
}

export type FieldErrors = Partial<Record<"name" | "email" | "password" | "role", string>>;

function collect(entries: [keyof FieldErrors, string | null][]): FieldErrors {
  const out: FieldErrors = {};
  for (const [k, v] of entries) if (v) out[k] = v;
  return out;
}

export function validateLogin(v: { email: string; password: string }): FieldErrors {
  return collect([
    ["email", validateEmail(v.email)],
    ["password", v.password.length === 0 ? "Enter your password." : null],
  ]);
}

export function validateSignup(v: { name: string; email: string; password: string; role: Role | null }): FieldErrors {
  return collect([
    ["name", validateName(v.name)],
    ["email", validateEmail(v.email)],
    ["password", validatePassword(v.password)],
    ["role", v.role ? null : "Choose who you are."],
  ]);
}
