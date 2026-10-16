// Same-origin API client (fs 8, 8.1). Paths are given without the /api prefix.
import type { ApiErrorBody } from "./types";
import { reachability as sharedReach, type Reachability } from "./reachability";

export class ApiError extends Error {
  status: number;
  code: string;
  fields?: Record<string, string>;
  constructor(status: number, code: string, message: string, fields?: Record<string, string>) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.fields = fields;
  }
}

export type Method = "GET" | "POST" | "PUT" | "DELETE";
export interface ApiOpts { idempotencyKey?: string; noRetry?: boolean; /** Let the request outlive the page (body must be under 64 KB). */ keepalive?: boolean }

export interface ApiDeps {
  fetch?: typeof fetch;
  sleep?: (ms: number) => Promise<void>;
  redirect?: (url: string) => void;
  currentPath?: () => string;
  mock?: boolean;
  reach?: Reachability;
}

/** fs 8.1: a write that fails this way toasts this text and keeps the user's input. */
export const NETWORK_TOAST = "Couldn't reach the server. Try again.";
/** fs 8.1 "502, 503, 504, or network error". */
export const isNetworkError = (e: unknown): boolean =>
  e instanceof ApiError && (e.status === 0 || e.status === 502 || e.status === 503 || e.status === 504);

const RETRY_DELAYS = [1000, 2000, 4000];
const RETRYABLE = new Set([502, 503, 504]);
const NO_REDIRECT_PATHS = ["/login", "/signup"];

export function createApi(deps: ApiDeps = {}) {
  const doFetch: typeof fetch = deps.fetch ?? ((...a) => fetch(...a));
  const sleep = deps.sleep ?? ((ms: number) => new Promise<void>((r) => setTimeout(r, ms)));
  const hasWindow = typeof window !== "undefined";
  const redirect = deps.redirect ?? ((url: string) => { if (hasWindow) window.location.assign(url); });
  const currentPath =
    deps.currentPath ?? (() => (hasWindow ? window.location.pathname + window.location.search : "/"));
  const reach = deps.reach ?? sharedReach;
  const useMock = deps.mock ?? process.env.NEXT_PUBLIC_MOCK_API === "true";

  let offsetMs = 0;
  function noteServerTime(body: unknown) {
    const st = (body as { serverTime?: unknown } | null)?.serverTime;
    if (typeof st === "string") {
      const t = Date.parse(st);
      if (!Number.isNaN(t)) offsetMs = t - Date.now();
    }
  }
  /** Device time plus the offset learned from the latest serverTime. */
  function serverNow(): number {
    return Date.now() + offsetMs;
  }

  async function attempt<T>(method: Method, path: string, body: unknown, opts?: ApiOpts): Promise<T> {
    let res: Response;
    if (useMock) {
      const { mockFetch } = await import("./mock");
      try {
        res = await mockFetch(method, path, body, opts?.idempotencyKey);
      } catch {
        throw new ApiError(0, "network", NETWORK_TOAST);
      }
    } else {
      const headers: Record<string, string> = {};
      if (body !== undefined) headers["Content-Type"] = "application/json";
      if (opts?.idempotencyKey) headers["Idempotency-Key"] = opts.idempotencyKey;
      try {
        res = await doFetch(`/api${path}`, {
          method,
          headers,
          credentials: "same-origin",
          keepalive: opts?.keepalive || undefined,
          body: body === undefined ? undefined : JSON.stringify(body),
        });
      } catch {
        throw new ApiError(0, "network", NETWORK_TOAST);
      }
    }
    if (res.status === 204) return undefined as T;
    let data: unknown = null;
    try {
      data = await res.json();
    } catch {
      /* non-JSON body */
    }
    if (!res.ok) {
      const e = (data as Partial<ApiErrorBody> | null)?.error;
      if (res.status === 401) {
        const here = currentPath();
        const p = here.split("?")[0];
        if (!NO_REDIRECT_PATHS.includes(p)) redirect(`/login?next=${encodeURIComponent(here)}`);
      }
      throw new ApiError(
        res.status,
        e?.code ?? "internal",
        e?.message ?? "Something went wrong. Try again.",
        e?.fields,
      );
    }
    noteServerTime(data);
    return data as T;
  }

  async function api<T>(method: Method, path: string, body?: unknown, opts?: ApiOpts): Promise<T> {
    const isRead = method === "GET";
    let marked = false;
    try {
      for (let i = 0; ; i++) {
        try {
          return await attempt<T>(method, path, body, opts);
        } catch (err) {
          const retryable = err instanceof ApiError && (err.status === 0 || RETRYABLE.has(err.status));
          // Reads retry 1/2/4 s; writes never retry here (callers keep form state).
          if (!isRead || opts?.noRetry || !retryable || i >= RETRY_DELAYS.length) throw err;
          if (!marked) { marked = true; reach.begin(); }
          await sleep(RETRY_DELAYS[i]);
        }
      }
    } finally {
      if (marked) reach.end();
    }
  }

  return { api, serverNow, noteServerTime };
}

const shared = createApi();
export const api = shared.api;
export const serverNow = shared.serverNow;
