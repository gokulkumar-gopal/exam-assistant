"use client";
// Session: the signed-in user from GET /api/me (fs 4, 8). The auth cookie is
// httpOnly and backend-set; nothing credential-like is read or stored here.
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import { api } from "@/lib/api";
import type { MeResponse, User } from "@/lib/types";

interface SessionValue {
  user: User | null;
  loading: boolean;
  logout: () => Promise<void>;
  /** True once logout started, so guards do not add a `next` to the login URL. */
  loggedOut: boolean;
}

const SessionContext = createContext<SessionValue>({
  user: null, loading: true, logout: async () => {}, loggedOut: false,
});

export function SessionProvider({ children }: { children: React.ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);
  const [loggedOut, setLoggedOut] = useState(false);
  const alive = useRef(true);

  useEffect(() => {
    alive.current = true;
    api<MeResponse>("GET", "/me")
      .then((r) => { if (alive.current) setUser(r.user); })
      .catch(() => { if (alive.current) setUser(null); })
      .finally(() => { if (alive.current) setLoading(false); });
    return () => { alive.current = false; };
  }, []);

  const logout = useCallback(async () => {
    setLoggedOut(true);
    try {
      await api<void>("POST", "/auth/logout");
    } catch {
      /* the session is cleared locally either way */
    }
    setUser(null);
  }, []);

  const value = useMemo(() => ({ user, loading, logout, loggedOut }), [user, loading, logout, loggedOut]);
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

export function useSession(): SessionValue {
  return useContext(SessionContext);
}

export function useMe(): { user: User | null; loading: boolean } {
  const { user, loading } = useContext(SessionContext);
  return { user, loading };
}
