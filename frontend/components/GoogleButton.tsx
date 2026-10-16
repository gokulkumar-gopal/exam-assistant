"use client";
// Google Identity Services ID-token button (fs 6.8). Rendered only on /login and /signup,
// so the GIS script is only ever requested there. No One Tap: prompt() is never called.
// The credential lives in a ref only: never state that renders, storage, URL or console.
import { useCallback, useEffect, useRef, useState } from "react";
import Script from "next/script";
import { useRouter, useSearchParams } from "next/navigation";
import { api, ApiError } from "@/lib/api";
import { useMe } from "@/lib/session";
import { safeNext } from "@/lib/safeNext";
import { GOOGLE_COPY, googleOutcome } from "@/lib/googleAuth";
import type { GoogleBody, Role, UserResponse } from "@/lib/types";

interface GisId {
  initialize: (cfg: { client_id: string; callback: (r: { credential?: string }) => void }) => void;
  renderButton: (el: HTMLElement, opts: Record<string, unknown>) => void;
}
declare global {
  interface Window { google?: { accounts?: { id?: GisId } } }
}

const GSI_SRC = "https://accounts.google.com/gsi/client";
const CLIENT_ID = process.env.NEXT_PUBLIC_GOOGLE_CLIENT_ID ?? "";
const MOCK = process.env.NEXT_PUBLIC_MOCK_API === "true";

interface Props {
  text: "signup_with" | "signin_with";
  /** Role chosen in the page's chips, if any. */
  role?: Role | null;
  /** The backend asked for a role (422 role_required). */
  onNeedRole: () => void;
  /** Message to show, or null to clear it. */
  onError: (message: string | null) => void;
  /** Lets the page disable its form while a Google request is in flight. */
  onBusy?: (busy: boolean) => void;
}

export function GoogleButton({ text, role, onNeedRole, onError, onBusy }: Props) {
  const router = useRouter();
  const next = useSearchParams().get("next");
  const { refresh } = useMe();
  const [busy, setBusyState] = useState(false);
  const [unavailable, setUnavailable] = useState(!CLIENT_ID && !MOCK);
  const container = useRef<HTMLDivElement>(null);
  const credentialRef = useRef<string | null>(null);
  const roleRef = useRef<Role | null>(role ?? null);
  const awaitingRole = useRef(false);
  const rendered = useRef(false);
  const latest = useRef({ onNeedRole, onError, onBusy, next, refresh });
  useEffect(() => { latest.current = { onNeedRole, onError, onBusy, next, refresh }; });

  const setBusy = useCallback((b: boolean) => { setBusyState(b); latest.current.onBusy?.(b); }, []);

  const send = useCallback(async () => {
    const credential = credentialRef.current;
    if (!credential) return;
    const h = latest.current;
    h.onError(null);
    setBusy(true);
    const body: GoogleBody = { credential, ...(roleRef.current ? { role: roleRef.current } : {}) };
    let status = 200;
    let code: string | undefined;
    let message: string | undefined;
    try {
      await api<UserResponse>("POST", "/auth/google", body);
    } catch (e) {
      const a = e instanceof ApiError ? e : null;
      status = a?.status ?? 0;
      code = a?.code;
      message = a?.message;
    }
    const o = googleOutcome({ status, code, message });
    if (o.kind === "success") {
      credentialRef.current = null;
      awaitingRole.current = false;
      await latest.current.refresh();
      router.replace(safeNext(latest.current.next));
      return; // stay busy while navigating
    }
    if (o.kind === "needRole") {
      awaitingRole.current = true; // keep the same credential for the retry
      latest.current.onNeedRole();
    } else {
      credentialRef.current = null; // expired or rejected: press the Google button again
      awaitingRole.current = false;
      latest.current.onError(o.message);
    }
    setBusy(false);
  }, [router, setBusy]);

  // A role picked after role_required resends the same credential.
  useEffect(() => {
    roleRef.current = role ?? null;
    if (role && awaitingRole.current && credentialRef.current) {
      awaitingRole.current = false;
      void send();
    }
  }, [role, send]);

  const init = useCallback(() => {
    if (rendered.current) return;
    const gid = window.google?.accounts?.id;
    const el = container.current;
    if (!gid || !el) { setUnavailable(true); return; }
    rendered.current = true;
    gid.initialize({
      client_id: CLIENT_ID,
      callback: (r) => {
        if (!r.credential) { latest.current.onError(GOOGLE_COPY.unauthorized); return; }
        credentialRef.current = r.credential;
        awaitingRole.current = false;
        void send();
      },
    });
    gid.renderButton(el, {
      type: "standard", theme: "outline", size: "large", text,
      width: Math.min(340, el.offsetWidth || 340),
    });
  }, [send, text]);

  // Script already loaded (client-side move between /login and /signup).
  useEffect(() => {
    if (CLIENT_ID && window.google?.accounts?.id) init();
  }, [init]);

  if (unavailable) {
    return <p role="status" className="mb-4 text-[13px] text-muted">{GOOGLE_COPY.unavailable}</p>;
  }

  const useMockButton = MOCK && !CLIENT_ID;
  return (
    <div className="mb-1">
      {!useMockButton && (
        <Script src={GSI_SRC} strategy="afterInteractive" onReady={init} onError={() => setUnavailable(true)} />
      )}
      {busy && (
        <button type="button" disabled className="btn-secondary w-full disabled:cursor-not-allowed disabled:opacity-40">
          Signing in…
        </button>
      )}
      {useMockButton ? (
        !busy && (
          <button
            type="button"
            className="btn-secondary w-full"
            onClick={() => { credentialRef.current = "mock-credential"; awaitingRole.current = false; void send(); }}
          >
            Continue as demo user
          </button>
        )
      ) : (
        <div ref={container} data-google-button className={busy ? "hidden" : "flex min-h-10 justify-center"} />
      )}
      <div className="my-4 flex items-center gap-3 text-[13px] text-muted" aria-hidden="true">
        <span className="h-px flex-1 bg-line" />or<span className="h-px flex-1 bg-line" />
      </div>
    </div>
  );
}
