// Debounced autosave with retry (fs 6.5, 8.1). No network or React here: the caller supplies `put`.
// A rejection with status 409 (exam closed or already submitted) stops everything.

export interface AutosaveState {
  /** idle: nothing saved yet this session; saved: last put succeeded; retrying: last put failed; closed: 409. */
  status: "idle" | "saved" | "retrying" | "closed";
  /** Epoch ms of the last successful save, or null. */
  savedAt: number | null;
}
export type FlushResult = "saved" | "failed" | "closed";

export interface AutosaverOpts {
  put: (answers: Record<string, string>, lastQuestionIndex: number) => Promise<void>;
  debounceMs?: number;
  retryMs?: number;
  onState: (s: AutosaveState) => void;
  /** Latest answers and index, read when a put is sent. `change(s)` may pass it instead. */
  snapshot?: () => { answers: Record<string, string>; lastQuestionIndex: number };
  now?: () => number;
  /** Last successful save time known at load (resume), so the bar can show it before any edit. */
  initialSavedAt?: number | null;
}

export function createAutosaver(o: AutosaverOpts) {
  const debounceMs = o.debounceMs ?? 500;
  const retryMs = o.retryMs ?? 3000;
  const now = o.now ?? Date.now;
  let timer: ReturnType<typeof setTimeout> | null = null;
  let dirty = false;
  let failing = false;
  let closed = false;
  let inflight: Promise<FlushResult> | null = null;
  let savedAt: number | null = o.initialSavedAt ?? null;
  let latest: { answers: Record<string, string>; lastQuestionIndex: number } | undefined;

  const emit = (status: AutosaveState["status"]) => o.onState({ status, savedAt });
  const clear = () => { if (timer) { clearTimeout(timer); timer = null; } };

  function schedule(ms: number) {
    clear();
    timer = setTimeout(fire, ms);
  }

  async function fire() {
    timer = null;
    if (inflight) await inflight.catch(() => undefined);
    if (closed || timer || !dirty) return;
    await attempt();
  }

  function attempt(): Promise<FlushResult> {
    clear();
    dirty = false;
    const snap = o.snapshot ? o.snapshot() : latest;
    const p = (async (): Promise<FlushResult> => {
      try {
        await o.put(snap?.answers as Record<string, string>, snap?.lastQuestionIndex as number);
        savedAt = now();
        failing = false;
        emit("saved");
        if (dirty && !closed) schedule(debounceMs);
        return "saved";
      } catch (e) {
        if ((e as { status?: number } | null)?.status === 409) {
          closed = true;
          dirty = false;
          clear();
          emit("closed");
          return "closed";
        }
        failing = true;
        dirty = true;
        emit("retrying");
        schedule(retryMs);
        return "failed";
      }
    })();
    inflight = p;
    const clearInflight = () => { if (inflight === p) inflight = null; };
    p.then(clearInflight, clearInflight);
    return p;
  }

  return {
    /** Something changed: save the latest snapshot 500 ms after the last change. */
    change(s?: { answers: Record<string, string>; lastQuestionIndex: number }) {
      if (closed) return;
      if (s) latest = s;
      dirty = true;
      schedule(debounceMs);
    },
    /** Save now. "saved" also when nothing was pending; "failed" keeps retrying in the background. */
    async flush(): Promise<FlushResult> {
      if (closed) return "closed";
      clear();
      if (inflight) await inflight;
      if (closed) return "closed";
      if (!dirty && !failing) return "saved";
      return attempt();
    },
    /** Stop timers (unmount). Call flush() first to send pending changes. */
    dispose() { clear(); },
  };
}

const hhmm = (ms: number) => {
  const d = new Date(ms);
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
};

/** Header text: "Saved 10:42", "Not saved. Retrying…", or "" before the first save. */
export function autosaveText(s: AutosaveState, fmt: (ms: number) => string = hhmm): string {
  if (s.status === "retrying") return "Not saved. Retrying…";
  if (s.savedAt !== null && s.status !== "closed") return `Saved ${fmt(s.savedAt)}`;
  return "";
}
