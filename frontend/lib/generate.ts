// Generation job (fs 6.4 flow 1-6, 9): start, poll, cancel, give up.
import { api, ApiError } from "./api";
import type { ExamType, JobResponse, Marks, Question, RegenerateBody } from "./types";

export interface GenerateRequest {
  sourceIds: string[]; type: ExamType; counts: Record<string, number>; shuffle: boolean; model?: string;
}
export type JobResult =
  | { status: "done"; questions: Question[]; model: string; failedSources?: string[] }
  | { status: "failed"; error: string }
  | { status: "cancelled" }
  | { status: "gaveUp" };

export type Send = (method: "GET" | "DELETE", path: string) => Promise<unknown>;
export interface PollOpts {
  intervalMs?: number; maxMs?: number; maxFailures?: number;
  now?: () => number; sleep?: (ms: number) => Promise<void>; send?: Send;
  onProgress?: (p: { done: number; total: number }) => void;
  /** Abort sends one DELETE straight away (Cancel button, route change, page unload). */
  signal?: AbortSignal;
}

export const GAVE_UP_MESSAGE = "That took too long. Try again.";
export const MODEL_GONE_MESSAGE = "That model isn't available. Pick another.";
export const SENT_TO_AI_LINE = "Selected files are sent to an AI service to write questions.";

const isMock = () => process.env.NEXT_PUBLIC_MOCK_API === "true";

/** Production transport. Polls never retry here (3 failures in a row is the give-up rule).
 *  DELETE uses fetch keepalive so it survives a page unload (sendBeacon cannot do DELETE). */
export const defaultSend: Send = async (method, path) => {
  if (method === "DELETE" && !isMock()) {
    return fetch(`/api${path}`, { method: "DELETE", keepalive: true, credentials: "same-origin" });
  }
  return api(method, path, undefined, { noRetry: true });
};

export async function startGeneration(
  req: GenerateRequest,
  post: (body: Record<string, unknown>) => Promise<{ jobId: string }> = (b) => api("POST", "/generate", b),
): Promise<{ jobId: string }> {
  const { model, ...rest } = req;
  return post(model === undefined ? { ...rest } : { ...rest, model });
}

const AVOID_MAX = 60;
const AVOID_LEN = 600;

/** Body for POST /generate/regenerate: `avoid` is the text of every question now in the list
 *  (clamped to the backend caps), `model` the one the original job used. */
export function buildRegenerateBody(
  current: Question[], marks: Marks, p: { sourceIds: string[]; type: ExamType }, model?: string,
): RegenerateBody {
  const body: RegenerateBody = {
    marks, sourceIds: p.sourceIds, type: p.type,
    avoid: current.slice(0, AVOID_MAX).map((q) => q.text.slice(0, AVOID_LEN)),
  };
  if (model) body.model = model;
  return body;
}

export async function startRegenerate(
  body: RegenerateBody,
  post: (b: Record<string, unknown>) => Promise<{ jobId: string }> = (b) => api("POST", "/generate/regenerate", b),
): Promise<{ jobId: string }> {
  return post({ ...body });
}

export async function pollJob(jobId: string, opts: PollOpts = {}): Promise<JobResult> {
  const intervalMs = opts.intervalMs ?? 1500;
  const maxMs = opts.maxMs ?? 360000;
  const maxFailures = opts.maxFailures ?? 3;
  const now = opts.now ?? Date.now;
  const sleep = opts.sleep ?? ((ms: number) => new Promise<void>((r) => setTimeout(r, ms)));
  const send = opts.send ?? defaultSend;
  const path = `/generate/${jobId}`;

  let deleted = false;
  const sendDelete = async () => {
    if (deleted) return;
    deleted = true;
    try { await send("DELETE", path); } catch { /* best effort */ }
  };
  const onAbort = () => void sendDelete();
  if (opts.signal?.aborted) {
    await sendDelete();
    return { status: "cancelled" };
  }
  opts.signal?.addEventListener("abort", onAbort);

  try {
    const start = now();
    let failures = 0;
    for (;;) {
      if (opts.signal?.aborted) return { status: "cancelled" };
      if (now() - start > maxMs) {
        await sendDelete();
        return { status: "gaveUp" };
      }
      let job: JobResponse | null = null;
      try {
        job = (await send("GET", path)) as JobResponse;
        failures = 0;
      } catch {
        failures++;
        if (failures >= maxFailures) {
          await sendDelete();
          return { status: "gaveUp" };
        }
      }
      if (opts.signal?.aborted) return { status: "cancelled" };
      if (job) {
        if (job.status === "done") {
          return { status: "done", questions: job.questions ?? [], model: job.model, failedSources: job.failedSources ?? [] };
        }
        if (job.status === "failed") return { status: "failed", error: job.error ?? "Something went wrong. Try again." };
        if (job.status === "cancelled") return { status: "cancelled" };
        if (job.progress) opts.onProgress?.(job.progress);
      }
      await sleep(intervalMs);
    }
  } finally {
    opts.signal?.removeEventListener("abort", onAbort);
  }
}

export interface ErrorNotice { text: string; retry: boolean; resetModel: boolean }

/** What to show for a failed POST /generate (fs 6.4 flow 6 and the Sources card). */
export function generateErrorNotice(err: unknown): ErrorNotice {
  if (err instanceof ApiError) {
    if (err.status === 429) return { text: err.message, retry: false, resetModel: false };
    if (err.status === 422) {
      if (err.fields?.model) return { text: MODEL_GONE_MESSAGE, retry: false, resetModel: true };
      return { text: err.message, retry: false, resetModel: false };
    }
    return { text: err.message, retry: true, resetModel: false };
  }
  return { text: "Something went wrong. Try again.", retry: true, resetModel: false };
}
