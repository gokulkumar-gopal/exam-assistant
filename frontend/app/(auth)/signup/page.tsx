"use client";
import { Suspense, useRef, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { api, ApiError } from "@/lib/api";
import { useMe } from "@/lib/session";
import { safeNext } from "@/lib/safeNext";
import { validateSignup, type FieldErrors } from "@/lib/authValidation";
import type { Role, SignupBody, UserResponse } from "@/lib/types";
import { AuthCard, Field } from "@/components/AuthCard";
import { GoogleButton } from "@/components/GoogleButton";
import { GOOGLE_COPY } from "@/lib/googleAuth";
import { RoleChips } from "@/components/RoleChips";
import { Input } from "@/components/ui/input";

const ORDER = ["name", "email", "password"] as const;

// useSearchParams needs its own Suspense boundary (the layout no longer provides one).
export default function SignupPage() {
  return (
    <Suspense fallback={null}>
      <SignupForm />
    </Suspense>
  );
}

function SignupForm() {
  const router = useRouter();
  const next = useSearchParams().get("next");
  const { refresh } = useMe();
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState<Role | null>(null);
  const [errors, setErrors] = useState<FieldErrors>({});
  const [formError, setFormError] = useState("");
  const [busy, setBusy] = useState(false);
  const [googleBusy, setGoogleBusy] = useState(false);
  const [googleError, setGoogleError] = useState<string | null>(null);
  const [needRole, setNeedRole] = useState(false);
  const form = useRef<HTMLFormElement>(null);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (busy) return;
    const v = validateSignup({ name, email, password, role });
    setErrors(v);
    setFormError("");
    const first = ORDER.find((k) => v[k]);
    if (first) { form.current?.querySelector<HTMLInputElement>(`[name=${first}]`)?.focus(); return; }
    if (!role) return;
    setBusy(true);
    const body: SignupBody = { name: name.trim(), email: email.trim(), password, role };
    try {
      await api<UserResponse>("POST", "/auth/signup", body);
      await refresh();
      router.replace(safeNext(next));
    } catch (err) {
      const e2 = err instanceof ApiError ? err : null;
      if (e2?.status === 409) setFormError("An account with that email already exists. Log in, or sign in with Google.");
      else if (e2?.status === 422 && e2.fields) {
        const f: FieldErrors = {};
        for (const k of ["name", "email", "password", "role"] as const) if (e2.fields[k]) f[k] = e2.fields[k];
        setErrors(f);
        if (Object.keys(f).length === 0) setFormError(e2.message);
      } else setFormError(e2?.message ?? "Something went wrong. Try again.");
      setBusy(false);
    }
  }

  const fe = (k: keyof FieldErrors) => ({
    "aria-invalid": errors[k] ? true : undefined,
    "aria-describedby": `${k}-err${k === "password" ? " password-help" : ""}`,
  });

  return (
    <AuthCard
      title="Create your account"
      subtitle="Choose who you are, then pick how to sign up."
      footer={<>Already have an account? <Link href={next ? `/login?next=${encodeURIComponent(next)}` : "/login"} className="font-semibold text-accent underline">Log in</Link></>}
    >
      <form ref={form} method="post" noValidate onSubmit={onSubmit} aria-busy={busy}>
        <fieldset disabled={busy || googleBusy} className="min-w-0">
          <div className="mb-3.5">
            <div id="role-label" className="mb-1 text-[13px] font-semibold">I am a</div>
            <RoleChips
              value={role}
              onChange={(r) => { setRole(r); setNeedRole(false); setErrors((x) => ({ ...x, role: undefined })); }}
              labelledBy="role-label"
              describedBy="role-err"
              invalid={!!errors.role}
              highlight={needRole}
              disabled={busy || googleBusy}
            />
            <p id="role-err" aria-live="polite" className="mt-1 text-[13px] text-bad">{needRole ? GOOGLE_COPY.needRole : errors.role}</p>
          </div>
          <GoogleButton
            text="signup_with"
            role={role}
            onNeedRole={() => { setNeedRole(true); setGoogleError(null); }}
            onError={setGoogleError}
            onBusy={setGoogleBusy}
          />
          <div aria-live="polite">
            {googleError && <p className="mb-3 text-[13px] text-bad">{googleError}</p>}
          </div>
          <Field id="name" label="Name" error={errors.name}>
            <Input id="name" name="name" autoComplete="name" value={name} onChange={(e) => setName(e.target.value)} {...fe("name")} />
          </Field>
          <Field id="email" label="Email" error={errors.email}>
            <Input id="email" name="email" type="email" autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} {...fe("email")} />
          </Field>
          <Field id="password" label="Password" helper="At least 8 characters" error={errors.password}>
            <Input id="password" name="password" type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} {...fe("password")} />
          </Field>
          <p aria-live="polite" className="mb-3 text-[13px] text-bad">{formError}</p>
          <button type="submit" className="btn-primary w-full disabled:cursor-not-allowed disabled:opacity-40">
            {busy ? "Signing in…" : "Create account"}
          </button>
        </fieldset>
      </form>
    </AuthCard>
  );
}
