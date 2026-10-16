// Debounced autosave with retry (fs 6.5, 8.1). No network or React here: the caller supplies `put`.
// A rejection with status 409 (exam closed or already submitted) stops everything. A 422 (the server refuses this
// snapshot: too long, unknown question) is permanent for that snapshot: no retry, status "rejected"; the next change
// saves a fresh snapshot. Other failures (network, 5xx) retry every 3 s.

export interface AutosaveState {
  /** idle: nothing saved yet this session; saved: last put succeeded; retrying: last put failed; closed: 409; rejected: 422. */
  status: "idle" | "saved" | "retrying" | "closed" | "rejected";
  /** With status "rejected": why (answers too long, or any other 422). */
  reason?: RejectReason;
  /** Epoch ms of the last successful save, or null. */
  savedAt: number | null;
}
export type RejectReason = "too-long" | "other";
export type FlushResult = "saved" | "failed" | "closed" | "rejected";

/** Backend 422 body fields look like `answers: "Too long overall"` or `answers[id]: "Too long"`. */
export function rejectionReason(e: unknown): RejectReason {
  const fields = (e as { fields?: Record<string, unknown> } | null)?.fields;
  if (fields && Object.values(fields).some((v) => typeof v === "string" && /too long/i.test(v))) return "too-long";
  return "other";
}

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
  let rejected: RejectReason | null = null; // the last put was refused (422) and nothing has changed since
  let disposed = false;
  let wantsNow = false; // an immediate change is waiting (for the timer or an in-flight put)
  let inflight: Promise<FlushResult> | null = null;
  let flushing: Promise<FlushResult> | null = null;
  let savedAt: number | null = o.initialSavedAt ?? null;
  let latest: { answers: Record<string, string>; lastQuestionIndex: number } | undefined;

  const emit = (status: AutosaveState["status"], reason?: RejectReason) => {
    if (!disposed) o.onState(reason ? { status, savedAt, reason } : { status, savedAt });
  };
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
        rejected = null;
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
        if ((e as { status?: number } | null)?.status === 422) {
          rejected = rejectionReason(e);
          failing = false;
          dirty = false; // a retry of the same snapshot would be refused again; a change makes a new one
          clear();
          emit("rejected", rejected);
          return "rejected";
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
      if (disposed) return Promise.resolve(dirty || failing ? "failed" : rejected ? "rejected" : "saved");
      if (flushing) return flushing;
      clear();
      const p = (async (): Promise<FlushResult> => {
        while (inflight) await inflight.catch(() => undefined);
        if (closed) return "closed";
        if (!dirty && !failing) return rejected ? "rejected" : "saved";
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
  if (s.status === "rejected") return s.reason === "too-long" ? "Your answers are too long to save." : "Couldn't save your answers.";
  if (s.savedAt !== null && s.status !== "closed") return `Saved ${fmt(s.savedAt)}`;
  return "";
}
