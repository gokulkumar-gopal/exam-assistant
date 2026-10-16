"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import { api, serverNow } from "./api";
import type { ExamListItem, ExamListResponse } from "./types";

export type Scope = "open" | "closed" | "created";

/**
 * Fetches GET /exams?scope=. No cache: it loads on mount (so landing on Home after publishing shows the new exam)
 * and reloads silently when the tab regains focus. `nowMs` follows the server clock offset.
 */
export function useExamList(scope: Scope) {
  const [exams, setExams] = useState<ExamListItem[] | null>(null);
  const [error, setError] = useState(false);
  const [nowMs, setNowMs] = useState(0);
  const seqRef = useRef({ n: 0 });

  const load = useCallback(async (silent = false) => {
    const seq = seqRef.current;
    const mine = ++seq.n;
    if (!silent) { setError(false); setExams(null); }
    try {
      const r = await api<ExamListResponse>("GET", `/exams?scope=${scope}`);
      if (mine !== seq.n) return;
      setExams(r.exams);
      setNowMs(serverNow());
      setError(false);
    } catch {
      if (mine !== seq.n) return;
      if (!silent) setError(true);
    }
  }, [scope]);

  useEffect(() => {
    const seq = seqRef.current;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- initial fetch on mount
    void load();
    const refocus = () => { if (document.visibilityState === "visible") void load(true); };
    document.addEventListener("visibilitychange", refocus);
    window.addEventListener("focus", refocus);
    const tick = setInterval(() => setNowMs(serverNow()), 15_000);
    return () => {
      seq.n++;
      document.removeEventListener("visibilitychange", refocus);
      window.removeEventListener("focus", refocus);
      clearInterval(tick);
    };
  }, [load]);

  const remove = useCallback((id: string) => setExams((l) => (l ? l.filter((e) => e.id !== id) : l)), []);
  return { exams, error, nowMs, reload: () => load(), remove };
}
