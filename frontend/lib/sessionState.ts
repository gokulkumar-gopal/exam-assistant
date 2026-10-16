// Pure session state transitions. Only a 401 from GET /me means signed out;
// any other failure is `error` and never clears a known user.
import type { User } from "@/lib/types";

export interface SessionState { user: User | null; loading: boolean; error: boolean; loggedOut: boolean }
export type MeOutcome = { ok: true; user: User } | { ok: false; status: number };

export const initialSession: SessionState = { user: null, loading: true, error: false, loggedOut: false };

export function applyMe(prev: SessionState, o: MeOutcome): SessionState {
  if (o.ok) return { user: o.user, loading: false, error: false, loggedOut: false };
  if (o.status === 401) return { user: null, loading: false, error: false, loggedOut: false };
  return { ...prev, loading: false, error: true };
}
export const startRefresh = (prev: SessionState): SessionState =>
  ({ ...prev, error: false, loading: prev.user === null });
export const startLogout = (prev: SessionState): SessionState => ({ ...prev, loggedOut: true });
export const finishLogout = (prev: SessionState): SessionState => ({ ...prev, user: null, loading: false, error: false });
