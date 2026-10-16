"use client";
// Session: the signed-in user from GET /api/me (fs 4, 8). The auth cookie is
// httpOnly and backend-set; nothing credential-like is read or stored here.
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import { api, ApiError } from "@/lib/api";
import type { MeResponse, User } from "@/lib/types";
import {
  applyMe, finishLogout, initialSession, startLogout, startRefresh, type MeOutcome, type SessionState,
} from "@/lib/sessionState";

interface SessionValue extends SessionState {
  logout: () => Promise<void>;
  /** Re-fetch /me. Clears loggedOut and error; sets user or null. */
  refresh: () => Promise<void>;
}

const SessionContext = createContext<SessionValue>({
  ...initialSession, logout: async () => {}, refresh: async () => {},
});

async function fetchMe(): Promise<MeOutcome> {
  try {
    const r = await api<MeResponse>("GET", "/me");
    return { ok: true, user: r.user };
  } catch (e) {
    return { ok: false, status: e instanceof ApiError ? e.status : 0 };
  }
}

export function SessionProvider({ children }: { children: React.ReactNode }) {
  const [state, setState] = useState<SessionState>(initialSession);
  const alive = useRef(true);

  const load = useCallback(async () => {
    const o = await fetchMe();
    if (alive.current) setState((s) => applyMe(s, o));
  }, []);

  const refresh = useCallback(async () => {
    setState(startRefresh);
    await load();
  }, [load]);

  useEffect(() => {
    alive.current = true;
    void load(); // initial state is already loading
    return () => { alive.current = false; };
  }, [load]);

  const logout = useCallback(async () => {
    setState(startLogout);
    try {
      await api<void>("POST", "/auth/logout");
    } catch {
      /* the session is cleared locally either way */
    }
    setState(finishLogout);
  }, []);

  const value = useMemo(() => ({ ...state, logout, refresh }), [state, logout, refresh]);
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

export function useSession(): SessionValue {
  return useContext(SessionContext);
}

export function useMe(): { user: User | null; loading: boolean; error: boolean; refresh: () => Promise<void> } {
  const { user, loading, error, refresh } = useContext(SessionContext);
  return { user, loading, error, refresh };
}
