// Direct-to-S3 upload (fs 6.2, 8.1). The file body goes ONLY to the presigned S3 URL, never to /api.
import { api as sharedApi, ApiError } from "./api";
import type { Doc, UploadUrlBody, UploadUrlResponse } from "./types";

export const MAX_UPLOAD_BYTES = 20_971_520;
export const MAX_PARALLEL = 3;
export const ALLOWED_TYPES = ["application/pdf", "image/jpeg", "image/png", "text/plain"] as const;

/** Returns the user-facing message, or null when the file may be uploaded. The name is plain text. */
export function validateFile(f: { name: string; type: string; size: number }): string | null {
  const ok = (ALLOWED_TYPES as readonly string[]).includes(f.type) && f.size >= 1 && f.size <= MAX_UPLOAD_BYTES;
  return ok ? null : `${f.name} isn't supported. Use PDF, JPG, PNG, or TXT under 20 MB.`;
}

export type UploadStep = 1 | 2 | 3;

/** Says which step failed: 1 presign, 2 PUT to S3, 3 complete. */
export class UploadError extends Error {
  step: UploadStep;
  aborted: boolean;
  constructor(step: UploadStep, message: string, aborted = false) {
    super(message);
    this.name = "UploadError";
    this.step = step;
    this.aborted = aborted;
  }
}

export type PutFile = (
  url: string,
  headers: Record<string, string>,
  file: File,
  onProgress: (fraction: number) => void,
  signal?: AbortSignal,
) => Promise<void>;

const PUT_FAILED = "Couldn't upload the file. Try again.";

/** XHR PUT of the file body to the presigned URL with exactly the given headers (they are signed). */
export const xhrPut = (
  url: string,
  headers: Record<string, string>,
  file: File,
  onProgress: (fraction: number) => void,
  signal?: AbortSignal,
  createXhr: () => XMLHttpRequest = () => new XMLHttpRequest(),
): Promise<void> =>
  new Promise<void>((resolve, reject) => {
    const xhr = createXhr();
    const onAbort = () => xhr.abort();
    const done = () => signal?.removeEventListener("abort", onAbort);
    xhr.open("PUT", url);
    for (const [k, v] of Object.entries(headers)) xhr.setRequestHeader(k, v);
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable && e.total > 0) onProgress(e.loaded / e.total);
    };
    xhr.onload = () => {
      done();
      if (xhr.status >= 200 && xhr.status < 300) resolve();
      else reject(new UploadError(2, PUT_FAILED));
    };
    xhr.onerror = () => { done(); reject(new UploadError(2, PUT_FAILED)); };
    xhr.onabort = () => { done(); reject(new UploadError(2, "Upload cancelled.", true)); };
    if (signal) {
      if (signal.aborted) { reject(new UploadError(2, "Upload cancelled.", true)); return; }
      signal.addEventListener("abort", onAbort);
    }
    xhr.send(file);
  });

export interface UploadDeps {
  api?: <T>(method: "POST", path: string, body?: unknown) => Promise<T>;
  putFile?: PutFile;
  signal?: AbortSignal;
}

// With NEXT_PUBLIC_MOCK_API the presigned URL is mock://..., so the transport is the mock one.
const defaultPut: PutFile = async (url, headers, file, onProgress, signal) => {
  if (process.env.NEXT_PUBLIC_MOCK_API === "true") {
    const { mockPut } = await import("./mock/s3");
    return mockPut(url, headers, file, onProgress, signal);
  }
  return xhrPut(url, headers, file, onProgress, signal);
};

const isAbort = (s?: AbortSignal) => !!s?.aborted;
const apiMessage = (e: unknown, fallback: string) => (e instanceof ApiError ? e.message : fallback);

export async function uploadFile(file: File, onProgress: (fraction: number) => void, deps: UploadDeps = {}): Promise<Doc> {
  const api = deps.api ?? sharedApi;
  const put = deps.putFile ?? defaultPut;

  let presigned: UploadUrlResponse;
  try {
    const body: UploadUrlBody = { name: file.name, type: file.type, sizeBytes: file.size };
    presigned = await api<UploadUrlResponse>("POST", "/documents/upload-url", body);
  } catch (e) {
    throw new UploadError(1, apiMessage(e, "Couldn't start the upload. Try again."), isAbort(deps.signal));
  }

  try {
    await put(presigned.uploadUrl, presigned.headers, file, onProgress, deps.signal);
  } catch (e) {
    if (e instanceof UploadError) throw e;
    throw new UploadError(2, PUT_FAILED, isAbort(deps.signal));
  }

  try {
    return await api<Doc>("POST", `/documents/${encodeURIComponent(presigned.docId)}/complete`);
  } catch (e) {
    throw new UploadError(3, apiMessage(e, "Couldn't finish the upload. Try again."), isAbort(deps.signal));
  }
}

export type QueueStatus = "queued" | "uploading" | "failed";
export interface QueueItem {
  id: string;
  file: File;
  status: QueueStatus;
  /** 0..1 */
  progress: number;
  error?: string;
  step?: UploadStep;
}

export interface QueueOpts {
  upload: (file: File, onProgress: (fraction: number) => void, signal: AbortSignal) => Promise<Doc>;
  limit?: number;
  onChange?: (items: QueueItem[]) => void;
  onDone?: (doc: Doc) => void;
}

/** FIFO, at most `limit` running. Finished items leave the queue; failed ones stay until retried or dismissed. */
export class UploadQueue {
  private items: QueueItem[] = [];
  private seq = 0;
  private dead = false;
  private ac = new AbortController();
  private limit: number;
  constructor(private opts: QueueOpts) {
    this.limit = opts.limit ?? MAX_PARALLEL;
  }

  add(files: File[]): void {
    for (const file of files) this.items.push({ id: `u${++this.seq}`, file, status: "queued", progress: 0 });
    this.pump();
  }

  retry(id: string): void {
    const i = this.items.findIndex((x) => x.id === id);
    if (i < 0 || this.items[i].status !== "failed") return;
    const [it] = this.items.splice(i, 1);
    this.items.push({ id: it.id, file: it.file, status: "queued", progress: 0 });
    this.pump();
  }

  dismiss(id: string): void {
    const i = this.items.findIndex((x) => x.id === id);
    if (i < 0 || this.items[i].status !== "failed") return;
    this.items.splice(i, 1);
    this.emit();
  }

  /** Abort every in-flight upload and silence all callbacks (page left). */
  destroy(): void {
    this.dead = true;
    this.ac.abort();
    this.items = [];
  }

  private emit() {
    if (!this.dead) this.opts.onChange?.(this.items.map((x) => ({ ...x })));
  }

  private patch(id: string, p: Partial<QueueItem>) {
    this.items = this.items.map((x) => (x.id === id ? { ...x, ...p } : x));
  }

  private pump() {
    if (this.dead) return;
    let running = this.items.filter((x) => x.status === "uploading").length;
    for (const it of this.items.filter((x) => x.status === "queued")) {
      if (running >= this.limit) break;
      running++;
      this.patch(it.id, { status: "uploading", progress: 0, error: undefined, step: undefined });
      void this.run(it.id, it.file);
    }
    this.emit();
  }

  private async run(id: string, file: File) {
    try {
      const doc = await this.opts.upload(file, (f) => {
        if (this.dead) return;
        this.patch(id, { progress: f });
        this.emit();
      }, this.ac.signal);
      if (this.dead) return;
      this.items = this.items.filter((x) => x.id !== id);
      this.opts.onDone?.(doc);
    } catch (e) {
      if (this.dead) return;
      const ue = e instanceof UploadError ? e : null;
      this.patch(id, {
        status: "failed",
        error: ue?.message ?? (e instanceof ApiError ? e.message : "Upload failed. Try again."),
        step: ue?.step,
      });
    }
    this.pump();
  }
}
