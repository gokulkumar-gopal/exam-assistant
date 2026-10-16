import { afterEach, describe, expect, it, vi } from "vitest";
import {
  MAX_UPLOAD_BYTES, UPLOAD_STALL_MS, UploadError, UploadQueue, uploadFile, validateFile, xhrPut,
  type QueueItem, type UploadDeps,
} from "../lib/upload";
import { ApiError } from "../lib/api";
import type { Doc } from "../lib/types";

const msg = (name: string) => `${name} isn't supported. Use PDF, JPG, PNG, or TXT under 20 MB.`;

describe("validateFile", () => {
  it("rejects image/gif with the exact message", () => {
    expect(validateFile({ name: "a.gif", type: "image/gif", size: 10 })).toBe(msg("a.gif"));
  });
  it("rejects a 20971521-byte PDF", () => {
    expect(validateFile({ name: "big.pdf", type: "application/pdf", size: 20971521 })).toBe(msg("big.pdf"));
  });
  it("accepts exactly 20971520 bytes", () => {
    expect(MAX_UPLOAD_BYTES).toBe(20971520);
    expect(validateFile({ name: "ok.pdf", type: "application/pdf", size: 20971520 })).toBeNull();
  });
  it("rejects an empty file", () => {
    expect(validateFile({ name: "e.txt", type: "text/plain", size: 0 })).toBe(msg("e.txt"));
  });
  it("accepts the four MIME types", () => {
    for (const type of ["application/pdf", "image/jpeg", "image/png", "text/plain"]) {
      expect(validateFile({ name: "f", type, size: 1 })).toBeNull();
    }
  });
  it("keeps the name as plain text", () => {
    expect(validateFile({ name: "<b>x</b>.gif", type: "image/gif", size: 1 })).toBe(msg("<b>x</b>.gif"));
  });
});

const DOC: Doc = { id: "d1", name: "a.pdf", type: "PDF", pages: 1, sizeBytes: 5, createdAt: "2026-10-14T09:00:00Z" };
const file = (name = "a.pdf", type = "application/pdf") => ({ name, type, size: 5 }) as unknown as File;

function fakeDeps(over: Partial<{ presign: () => Promise<unknown>; put: () => Promise<void>; complete: () => Promise<unknown> }> = {}) {
  const calls: string[] = [];
  const putArgs: { url: string; headers: Record<string, string>; body: unknown }[] = [];
  const api = vi.fn(async (...args: [string, string, unknown?]) => {
    const [method, path] = args;
    calls.push(`${method} ${path}`);
    if (path === "/documents/upload-url") {
      if (over.presign) return over.presign();
      return { docId: "d1", uploadUrl: "https://s3.example/obj?sig=1", headers: { "Content-Type": "application/pdf" } };
    }
    if (over.complete) return over.complete();
    return DOC;
  });
  const putFile = vi.fn(async (url: string, headers: Record<string, string>, body: File, onProgress: (f: number) => void) => {
    calls.push(`PUT ${url}`);
    putArgs.push({ url, headers, body });
    onProgress(0.5);
    if (over.put) await over.put();
  });
  return { deps: { api, putFile } as unknown as UploadDeps, calls, putArgs, api, putFile };
}

describe("uploadFile", () => {
  it("presigns with the MIME type, PUTs to the presigned URL with exactly the returned headers, then completes", async () => {
    const f = fakeDeps();
    const progress: number[] = [];
    const file1 = file();
    const doc = await uploadFile(file1, (p) => progress.push(p), f.deps);
    expect(doc).toEqual(DOC);
    expect(f.api).toHaveBeenNthCalledWith(1, "POST", "/documents/upload-url", { name: "a.pdf", type: "application/pdf", sizeBytes: 5 });
    expect(f.putArgs).toEqual([{ url: "https://s3.example/obj?sig=1", headers: { "Content-Type": "application/pdf" }, body: file1 }]);
    expect(f.calls).toEqual(["POST /documents/upload-url", "PUT https://s3.example/obj?sig=1", "POST /documents/d1/complete"]);
    expect(progress).toContain(0.5);
  });

  it("the body is never sent through api (only the S3 transport gets the File)", async () => {
    const f = fakeDeps();
    const file1 = file();
    await uploadFile(file1, () => {}, f.deps);
    for (const c of f.api.mock.calls) expect(c[2]).not.toBe(file1);
  });

  it("presign failure: step 1, no PUT attempted", async () => {
    const f = fakeDeps({ presign: async () => { throw new Error("boom"); } });
    const e = await uploadFile(file(), () => {}, f.deps).catch((x) => x);
    expect(e).toBeInstanceOf(UploadError);
    expect(e.step).toBe(1);
    expect(f.putFile).not.toHaveBeenCalled();
    expect(f.calls).toEqual(["POST /documents/upload-url"]);
  });

  it("PUT network failure: step 2, no /complete", async () => {
    const f = fakeDeps({ put: async () => { throw new UploadError(2, "Couldn't upload the file. Try again."); } });
    const e = await uploadFile(file(), () => {}, f.deps).catch((x) => x);
    expect(e.step).toBe(2);
    expect(f.calls.some((c) => c.endsWith("/complete"))).toBe(false);
  });

  it("any thrown PUT error is tagged step 2", async () => {
    const f = fakeDeps({ put: async () => { throw new TypeError("x"); } });
    const e = await uploadFile(file(), () => {}, f.deps).catch((x) => x);
    expect(e).toBeInstanceOf(UploadError);
    expect(e.step).toBe(2);
    expect(f.calls.some((c) => c.endsWith("/complete"))).toBe(false);
  });

  it("/complete failure: error tagged step 3 with the server message", async () => {
    const f = fakeDeps({ complete: async () => { throw new ApiError(409, "conflict", "The upload didn't finish. Try again."); } });
    const e = await uploadFile(file(), () => {}, f.deps).catch((x) => x);
    expect(e.step).toBe(3);
    expect(e.message).toBe("The upload didn't finish. Try again.");
  });
});

// Fake XHR to check header exactness and non-2xx handling of the real transport.
class FakeXhr {
  static last: FakeXhr;
  headers: Record<string, string> = {};
  method = ""; url = ""; sent: unknown = undefined; status = 200; aborted = false;
  upload: { onprogress: ((e: { lengthComputable: boolean; loaded: number; total: number }) => void) | null } = { onprogress: null };
  onload: (() => void) | null = null; onerror: (() => void) | null = null; onabort: (() => void) | null = null;
  constructor() { FakeXhr.last = this; }
  open(m: string, u: string) { this.method = m; this.url = u; }
  setRequestHeader(k: string, v: string) { this.headers[k] = v; }
  send(b: unknown) { this.sent = b; }
  abort() { this.aborted = true; this.onabort?.(); }
}

describe("xhrPut", () => {
  const mk = () => new FakeXhr() as unknown as XMLHttpRequest;
  it("sets exactly the given headers, sends the file to the URL, reports progress", async () => {
    const body = file();
    const progress: number[] = [];
    const p = xhrPut("https://s3.example/o", { "Content-Type": "application/pdf" }, body, (f) => progress.push(f), undefined, mk);
    const x = FakeXhr.last;
    x.upload.onprogress?.({ lengthComputable: true, loaded: 1, total: 4 });
    x.status = 200; x.onload?.();
    await p;
    expect(x.method).toBe("PUT");
    expect(x.url).toBe("https://s3.example/o");
    expect(x.headers).toEqual({ "Content-Type": "application/pdf" });
    expect(x.sent).toBe(body);
    expect(progress).toEqual([0.25]);
  });
  it("non-2xx rejects as step 2", async () => {
    const p = xhrPut("https://s3.example/o", {}, file(), () => {}, undefined, mk);
    FakeXhr.last.status = 403; FakeXhr.last.onload?.();
    const e = await p.catch((x) => x);
    expect(e).toBeInstanceOf(UploadError);
    expect(e.step).toBe(2);
  });
  it("network error rejects as step 2", async () => {
    const p = xhrPut("https://s3.example/o", {}, file(), () => {}, undefined, mk);
    FakeXhr.last.onerror?.();
    expect((await p.catch((x) => x)).step).toBe(2);
  });
  it("abort signal aborts the XHR", async () => {
    const ac = new AbortController();
    const p = xhrPut("https://s3.example/o", {}, file(), () => {}, ac.signal, mk);
    ac.abort();
    expect(FakeXhr.last.aborted).toBe(true);
    expect((await p.catch((x) => x)).aborted).toBe(true);
  });
});

// Queue
function controllable() {
  const pending: { f: File; resolve: (d: Doc) => void; reject: (e: Error) => void }[] = [];
  const upload = vi.fn((f: File) => new Promise<Doc>((resolve, reject) => { pending.push({ f, resolve, reject }); }));
  return { pending, upload };
}
const tick = () => new Promise((r) => setTimeout(r, 0));
const running = (items: QueueItem[]) => items.filter((i) => i.status === "uploading").length;

describe("UploadQueue", () => {
  it("never runs more than 3 at once, FIFO, and starts the next when one finishes", async () => {
    const c = controllable();
    let snap: QueueItem[] = [];
    let max = 0;
    const q = new UploadQueue({ upload: c.upload, onChange: (i) => { snap = i; max = Math.max(max, running(i)); } });
    q.add(["a", "b", "c", "d", "e"].map((n) => file(n)));
    expect(c.pending.map((p) => p.f.name)).toEqual(["a", "b", "c"]);
    expect(snap.map((i) => i.status)).toEqual(["uploading", "uploading", "uploading", "queued", "queued"]);
    c.pending[0].resolve(DOC);
    await tick();
    expect(c.pending.map((p) => p.f.name)).toEqual(["a", "b", "c", "d"]);
    expect(running(snap)).toBe(3);
    expect(max).toBe(3);
  });

  it("a failure frees its slot, does not block the queue, and stays as a failed item", async () => {
    const c = controllable();
    let snap: QueueItem[] = [];
    const q = new UploadQueue({ upload: c.upload, onChange: (i) => { snap = i; } });
    q.add(["a", "b", "c", "d"].map((n) => file(n)));
    c.pending[1].reject(new UploadError(2, "Upload failed."));
    await tick();
    expect(c.pending.map((p) => p.f.name)).toEqual(["a", "b", "c", "d"]);
    const b = snap.find((i) => i.file.name === "b")!;
    expect(b.status).toBe("failed");
    expect(b.error).toBe("Upload failed.");
    expect(running(snap)).toBe(3);
  });

  it("success calls onDone and removes the item", async () => {
    const c = controllable();
    let snap: QueueItem[] = [];
    const onDone = vi.fn();
    const q = new UploadQueue({ upload: c.upload, onDone, onChange: (i) => { snap = i; } });
    q.add([file("a")]);
    c.pending[0].resolve(DOC);
    await tick();
    expect(onDone).toHaveBeenCalledWith(DOC);
    expect(snap).toEqual([]);
  });

  it("retry runs again from the start (a new upload call), after queued items", async () => {
    const c = controllable();
    let snap: QueueItem[] = [];
    const q = new UploadQueue({ upload: c.upload, limit: 1, onChange: (i) => { snap = i; } });
    q.add([file("a"), file("b")]);
    c.pending[0].reject(new UploadError(3, "x"));
    await tick();
    expect(c.pending.map((p) => p.f.name)).toEqual(["a", "b"]);
    q.retry(snap.find((i) => i.file.name === "a")!.id);
    expect(snap.find((i) => i.file.name === "a")!.status).toBe("queued");
    c.pending[1].resolve(DOC);
    await tick();
    expect(c.pending.map((p) => p.f.name)).toEqual(["a", "b", "a"]);
    expect(c.upload).toHaveBeenCalledTimes(3);
  });

  it("dismiss removes a failed item; retry on a non-failed item is ignored", async () => {
    const c = controllable();
    let snap: QueueItem[] = [];
    const q = new UploadQueue({ upload: c.upload, onChange: (i) => { snap = i; } });
    q.add([file("a")]);
    const id = snap[0].id;
    q.retry(id);
    expect(c.upload).toHaveBeenCalledTimes(1);
    c.pending[0].reject(new UploadError(1, "x"));
    await tick();
    q.dismiss(id);
    expect(snap).toEqual([]);
  });

  it("destroy aborts in-flight work and stops all notifications", async () => {
    const c = controllable();
    const onChange = vi.fn();
    const signals: AbortSignal[] = [];
    const q = new UploadQueue({ upload: (f, _p, s) => { signals.push(s); return c.upload(f); }, onChange });
    q.add([file("a"), file("b"), file("c"), file("d")]);
    onChange.mockClear();
    q.destroy();
    expect(signals.every((s) => s.aborted)).toBe(true);
    c.pending[0].reject(new UploadError(2, "aborted"));
    await tick();
    expect(onChange).not.toHaveBeenCalled();
    expect(c.upload).toHaveBeenCalledTimes(3);
  });

  it("progress updates the item", async () => {
    let snap: QueueItem[] = [];
    const q = new UploadQueue({
      upload: (_f, onProgress) => { onProgress(0.4); return new Promise<Doc>(() => {}); },
      onChange: (i) => { snap = i; },
    });
    q.add([file("a")]);
    expect(snap[0].progress).toBe(0.4);
  });
});

describe("upload stall timeout", () => {
  afterEach(() => vi.useRealTimers());
  const mk = () => new FakeXhr() as unknown as XMLHttpRequest;
  const prog = (x: FakeXhr, loaded: number) => x.upload.onprogress?.({ lengthComputable: true, loaded, total: 100 });

  it("stall limit is a named constant", () => {
    expect(UPLOAD_STALL_MS).toBeGreaterThanOrEqual(10_000);
  });

  it("no progress for the limit: aborts the XHR and fails as step 2", async () => {
    vi.useFakeTimers();
    const p = xhrPut("https://s3.example/o", {}, file(), () => {}, undefined, mk);
    const caught = p.catch((x) => x);
    await vi.advanceTimersByTimeAsync(UPLOAD_STALL_MS);
    const e = await caught;
    expect(e).toBeInstanceOf(UploadError);
    expect(e.step).toBe(2);
    expect(e.aborted).toBe(false);
    expect(FakeXhr.last.aborted).toBe(true);
  });

  it("continuous progress beyond the limit is not killed", async () => {
    vi.useFakeTimers();
    const p = xhrPut("https://s3.example/o", {}, file(), () => {}, undefined, mk);
    const x = FakeXhr.last;
    for (let i = 1; i <= 5; i++) {
      await vi.advanceTimersByTimeAsync(UPLOAD_STALL_MS - 1000);
      prog(x, i * 10);
    }
    expect(x.aborted).toBe(false);
    x.status = 200; x.onload?.();
    await expect(p).resolves.toBeUndefined();
    await vi.advanceTimersByTimeAsync(UPLOAD_STALL_MS * 2);
    expect(x.aborted).toBe(false);
  });

  it("a stall frees the queue slot, the next item starts, and Retry restarts from presign", async () => {
    vi.useFakeTimers();
    const presigns: string[] = [];
    const xhrs: FakeXhr[] = [];
    const upload = (f: File, onProgress: (n: number) => void, signal: AbortSignal) =>
      uploadFile(f, onProgress, {
        signal,
        api: (async (_m: string, path: string) => {
          if (path === "/documents/upload-url") { presigns.push(f.name); return { docId: "d1", uploadUrl: "https://s3.example/o", headers: {} }; }
          return DOC;
        }) as UploadDeps["api"],
        putFile: (url, h, body, p, sig) => xhrPut(url, h, body, p, sig, () => { const x = new FakeXhr(); xhrs.push(x); return x as unknown as XMLHttpRequest; }),
      });
    let snap: QueueItem[] = [];
    const q = new UploadQueue({ upload, limit: 1, onChange: (i) => { snap = i; } });
    q.add([file("a"), file("b")]);
    await vi.advanceTimersByTimeAsync(0);
    expect(presigns).toEqual(["a"]);
    await vi.advanceTimersByTimeAsync(UPLOAD_STALL_MS);
    const a = snap.find((i) => i.file.name === "a")!;
    expect(a.status).toBe("failed");
    expect(a.step).toBe(2);
    expect(presigns).toEqual(["a", "b"]);
    expect(snap.find((i) => i.file.name === "b")!.status).toBe("uploading");
    q.retry(a.id);
    await vi.advanceTimersByTimeAsync(0);
    expect(snap.find((i) => i.file.name === "a")!.status).toBe("queued");
    xhrs[1].status = 200; xhrs[1].onload?.();
    await vi.advanceTimersByTimeAsync(0);
    expect(presigns).toEqual(["a", "b", "a"]);
    q.destroy();
  });
});
