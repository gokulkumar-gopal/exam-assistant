// Mock "S3 PUT" for MOCK_API=true (the mock presign returns mock://s3/<id>). Loaded only in mock mode.
// Failure triggers, set from the browser console: globalThis.__mockUploadFail = { presign: 1, put: 1, complete: 1 }
// "stall: 1" makes the next PUT hang with no progress until the stall limit
// (globalThis.__mockStallMs overrides UPLOAD_STALL_MS for quick checks). Each count fails that many upcoming calls of that step, then clears (so Retry succeeds).
import { UPLOAD_STALL_MS, UploadError, type PutFile } from "../upload";

type Step = "presign" | "put" | "complete" | "stall";

export function consumeUploadFailure(step: Step): boolean {
  const f = (globalThis as { __mockUploadFail?: Partial<Record<Step, number>> }).__mockUploadFail;
  if (!f || !f[step]) return false;
  f[step] = (f[step] as number) - 1;
  return true;
}

const wait = (ms: number, signal?: AbortSignal) =>
  new Promise<void>((resolve, reject) => {
    if (signal?.aborted) return reject(new UploadError(2, "Upload cancelled.", true));
    const t = setTimeout(() => { signal?.removeEventListener("abort", onAbort); resolve(); }, ms);
    const onAbort = () => { clearTimeout(t); reject(new UploadError(2, "Upload cancelled.", true)); };
    signal?.addEventListener("abort", onAbort, { once: true });
  });

export const mockPut: PutFile = async (url, _headers, _file, onProgress, signal) => {
  if (!url.startsWith("mock://s3/")) throw new UploadError(2, "Couldn't upload the file. Try again.");
  if (consumeUploadFailure("stall")) {
    await wait((globalThis as { __mockStallMs?: number }).__mockStallMs ?? UPLOAD_STALL_MS, signal);
    throw new UploadError(2, "The upload stalled. Try again.");
  }
  const steps = 5;
  for (let i = 1; i <= steps; i++) {
    await wait(300, signal);
    if (i === 3 && consumeUploadFailure("put")) throw new UploadError(2, "Couldn't upload the file. Try again.");
    onProgress(i / steps);
  }
};
