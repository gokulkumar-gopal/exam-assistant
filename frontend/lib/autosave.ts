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
  /** Latest answers and index, read when a put is sent. `change({snapshot})` may pass it instead. */
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
  let disposed = false;
  let wantsNow = false; // an immediate change is waiting (for the timer or an in-flight put)
  let inflight: Promise<FlushResult> | null = null;
  let flushing: Promise<FlushResult> | null = null;
  let savedAt: number | null = o.initialSavedAt ?? null;
  let latest: { answers: Record<string, string>; lastQuestionIndex: number } | undefined;

  const emit = (status: AutosaveState["status"]) => { if (!disposed) o.onState({ status, savedAt }); };
  const clear = () => { if (timer) { clearTimeout(timer); timer = null; } };

  function schedule(ms: number) {
    clear();
    if (disposed) return;
    timer = setTimeout(fire, ms);
  }

  async function fire() {
    timer = null;
    while (inflight) await inflight.catch(() => undefined);
    if (disposed || closed || timer || !dirty) return;
    await attempt();
  }

  function attempt(): Promise<FlushResult> {
    clear();
    dirty = false;
    wantsNow = false;
    const snap = o.snapshot ? o.snapshot() : latest;
    const p = (async (): Promise<FlushResult> => {
      try {
        await o.put(snap?.answers as Record<string, string>, snap?.lastQuestionIndex as number);
        savedAt = now();
        failing = false;
        emit("saved");
        if (dirty && !closed) schedule(wantsNow ? 0 : debounceMs);
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
        schedule(retryMs); // no-op once disposed: a retry never outlives the page
        return "failed";
      }
    })();
    inflight = p;
    const clearInflight = () => { if (inflight === p) inflight = null; };
    p.then(clearInflight, clearInflight);
    return p;
  }

  return {
    /**
     * Something changed. Text is saved 500 ms after the last change; `immediate` (option clicks,
     * navigation) saves right away, still queued behind a put already in flight.
     */
    change(c?: { immediate?: boolean; snapshot?: { answers: Record<string, string>; lastQuestionIndex: number } }) {
      if (closed || disposed) return;
      if (c?.snapshot) latest = c.snapshot;
      dirty = true;
      if (c?.immediate) wantsNow = true;
      schedule(c?.immediate ? 0 : debounceMs);
    },
    /**
     * Save now. "saved" also when nothing was pending; "failed" keeps retrying in the background.
     * Concurrent calls share one run. A flush started before dispose() still sends its one put.
     */
    flush(): Promise<FlushResult> {
      if (closed) return Promise.resolve("closed");
      if (disposed) return Promise.resolve(dirty || failing ? "failed" : "saved");
      if (flushing) return flushing;
      clear();
      const p = (async (): Promise<FlushResult> => {
        while (inflight) await inflight.catch(() => undefined);
        if (closed) return "closed";
        if (!dirty && !failing) return "saved";
        return attempt();
      })();
      flushing = p;
      const done = () => { if (flushing === p) flushing = null; };
      p.then(done, done);
      return p;
    },
    /** Stop for good (unmount): no timers, no new puts, no state reports. Call flush() first to send pending changes. */
    dispose() { disposed = true; clear(); },
  };
}

/** Browsers cap a keepalive request body at 64 KB; stay under it (UTF-8 bytes). */
export function keepaliveOk(body: string): boolean {
  return new TextEncoder().encode(body).length < 60_000;
}

/** Leaving the page: flush on pagehide and when the page becomes hidden. */
export function shouldFlushOnEvent(type: string, visibility: string): boolean {
  return type === "pagehide" || (type === "visibilitychange" && visibility === "hidden");
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
