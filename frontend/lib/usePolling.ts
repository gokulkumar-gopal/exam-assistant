"use client";
// Poll an async function on an interval (fs 6.10). Paused while the tab is hidden and refreshed once when it
// shows again; one request at a time (the next wait starts when the previous request ends).
import { useEffect, useRef } from "react";

export interface VisibilityDoc {
  hidden: boolean;
  addEventListener(type: "visibilitychange", fn: () => void): void;
  removeEventListener(type: "visibilitychange", fn: () => void): void;
}

export function createPoller(fn: () => Promise<unknown>, intervalMs: number, doc: VisibilityDoc) {
  let timer: ReturnType<typeof setTimeout> | null = null;
  let inflight = false;
  let stopped = false;

  const clear = () => { if (timer !== null) { clearTimeout(timer); timer = null; } };
  const schedule = () => {
    clear();
    if (stopped || doc.hidden) return;
    timer = setTimeout(run, intervalMs);
  };
  async function run() {
    clear();
    if (stopped || inflight || doc.hidden) return;
    inflight = true;
    try { await fn(); } catch { /* the caller shows failures */ }
    inflight = false;
    schedule();
  }
  const onVisibility = () => {
    if (stopped) return;
    if (doc.hidden) clear();
    else void run();
  };

  return {
    start() {
      doc.addEventListener("visibilitychange", onVisibility);
      void run();
    },
    stop() {
      stopped = true;
      clear();
      doc.removeEventListener("visibilitychange", onVisibility);
    },
  };
}

export function usePolling(fn: () => Promise<unknown>, intervalMs: number) {
  const latest = useRef(fn);
  useEffect(() => { latest.current = fn; });
  useEffect(() => {
    const p = createPoller(() => latest.current(), intervalMs, document);
    p.start();
    return () => p.stop();
  }, [intervalMs]);
}
