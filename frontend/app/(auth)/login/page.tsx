"use client";
import { Suspense, useRef, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { api, ApiError } from "@/lib/api";
import { useMe } from "@/lib/session";
import { safeNext } from "@/lib/safeNext";
import { validateLogin, type FieldErrors } from "@/lib/authValidation";
import { GOOGLE_COPY } from "@/lib/googleAuth";
import type { LoginBody, Role, UserResponse } from "@/lib/types";
import { AuthCard, Field } from "@/components/AuthCard";
import { GoogleButton } from "@/components/GoogleButton";
import { GOOGLE_AUTH } from "@/lib/features";
import { RoleChips } from "@/components/RoleChips";
import { Input } from "@/components/ui/input";

// useSearchParams needs its own Suspense boundary (the layout no longer provides one).
export default function LoginPage() {
  return (
    <Suspense fallback={null}>
      <LoginForm />
    </Suspense>
  );
}

function LoginForm() {
  const router = useRouter();
  const next = useSearchParams().get("next");
  const { refresh } = useMe();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [errors, setErrors] = useState<FieldErrors>({});
  const [failed, setFailed] = useState(false);
  const [formError, setFormError] = useState("");
  const [busy, setBusy] = useState(false);
  const [googleBusy, setGoogleBusy] = useState(false);
  const [googleError, setGoogleError] = useState<string | null>(null);
  const [needRole, setNeedRole] = useState(false);
  const [role, setRole] = useState<Role | null>(null);
  const form = useRef<HTMLFormElement>(null);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (busy) return;
    const v = validateLogin({ email, password });
    setErrors(v);
    setFailed(false);
    setFormError("");
    const first = (["email", "password"] as const).find((k) => v[k]);
    if (first) { form.current?.querySelector<HTMLInputElement>(`[name=${first}]`)?.focus(); return; }
    setBusy(true);
    const body: LoginBody = { email: email.trim(), password };
    try {
      await api<UserResponse>("POST", "/auth/login", body);
      await refresh();
      router.replace(safeNext(next));
    } catch (err) {
      // Every failed password login shows the same message (never reveal whether the email exists).
      if (err instanceof ApiError && err.status === 401) setFailed(true);
      else setFormError(err instanceof ApiError ? err.message : "Something went wrong. Try again.");
      setBusy(false);
    }
  }

  const fe = (k: keyof FieldErrors, extra = "") => ({
    "aria-invalid": errors[k] ? true : undefined,
    "aria-describedby": `${k}-err${extra}`,
  });

  return (
    <AuthCard
      title="Welcome back"
      footer={<>New here? <Link href={next ? `/signup?next=${encodeURIComponent(next)}` : "/signup"} className="font-semibold text-accent underline">Create an account</Link></>}
    >
      <form ref={form} method="post" noValidate onSubmit={onSubmit} aria-busy={busy}>
        <fieldset disabled={busy || googleBusy} className="min-w-0">
          {GOOGLE_AUTH && (
            <>
              <GoogleButton
                text="signin_with"
                role={role}
                onNeedRole={() => { setNeedRole(true); setGoogleError(null); }}
                onError={setGoogleError}
                onBusy={setGoogleBusy}
              />
              <div aria-live="polite">
                {googleError && <p className="mb-3 text-[13px] text-bad">{googleError}</p>}
              </div>
              {needRole && (
                <div className="mb-3.5">
                  <div id="role-label" className="mb-1 text-[13px] font-semibold">I am a</div>
                  <RoleChips value={role} onChange={setRole} highlight labelledBy="role-label" describedBy="role-msg" disabled={googleBusy} />
                  <p id="role-msg" role="status" className="mt-1 text-[13px] text-bad">{GOOGLE_COPY.needRole}</p>
                </div>
              )}
            </>
          )}
          <Field id="email" label="Email" error={errors.email}>
            <Input id="email" name="email" type="email" autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} {...fe("email")} />
          </Field>
          <Field id="password" label="Password" error={errors.password}>
            <Input id="password" name="password" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} {...fe("password")} />
          </Field>
          <div aria-live="polite" className="mb-3">
            {failed && (
              <>
                <p className="text-[13px] text-bad">Email or password is incorrect.</p>
                <p className="mt-0.5 text-[13px] text-muted">Signed up with Google? Use the Google button above.</p>
              </>
            )}
            {formError && <p className="text-[13px] text-bad">{formError}</p>}
          </div>
          <button type="submit" className="btn-primary w-full disabled:cursor-not-allowed disabled:opacity-40">
            {busy ? "Signing in…" : "Log in"}
          </button>
        </fieldset>
      </form>
    </AuthCard>
  );
}
