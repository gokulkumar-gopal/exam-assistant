import { describe, expect, it } from "vitest";
import { applyMe, initialSession, startLogout, finishLogout, startRefresh } from "../lib/sessionState";

const user = { id: "u1", name: "A", email: "a@x.com", role: "teacher" as const };

describe("session state", () => {
  it("200 sets the user", () => {
    expect(applyMe(initialSession, { ok: true, user })).toEqual({ user, loading: false, error: false, loggedOut: false });
  });
  it("401 means signed out, not error", () => {
    const s = applyMe(initialSession, { ok: false, status: 401 });
    expect(s).toEqual({ user: null, loading: false, error: false, loggedOut: false });
  });
  it.each([500, 503, 0, 404])("status %i is an error and keeps a known user", (status) => {
    const known = applyMe(initialSession, { ok: true, user });
    const s = applyMe(known, { ok: false, status });
    expect(s.error).toBe(true);
    expect(s.user).toEqual(user);
    expect(s.loading).toBe(false);
  });
  it("error with no user stays user=null but error=true", () => {
    const s = applyMe(initialSession, { ok: false, status: 500 });
    expect(s).toMatchObject({ user: null, error: true, loading: false });
  });
  it("logout clears user and marks loggedOut", () => {
    const known = applyMe(initialSession, { ok: true, user });
    const s = finishLogout(startLogout(known));
    expect(s).toMatchObject({ user: null, loggedOut: true });
  });
  it("refresh after logout restores the user and clears loggedOut", () => {
    const out = finishLogout(startLogout(applyMe(initialSession, { ok: true, user })));
    const mid = startRefresh(out);
    expect(mid).toMatchObject({ loading: true, error: false });
    expect(applyMe(mid, { ok: true, user })).toEqual({ user, loading: false, error: false, loggedOut: false });
  });
  it("retry clears error; loading only when no user is known", () => {
    const err = applyMe(initialSession, { ok: false, status: 500 });
    expect(startRefresh(err)).toMatchObject({ error: false, loading: true });
    const known = { ...applyMe(initialSession, { ok: true, user }), error: true };
    expect(startRefresh(known)).toMatchObject({ error: false, loading: false, user });
  });
});
