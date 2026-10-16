// Pure viewer logic (fs 6.3). The presigned URL lives only in memory: it is never
// stored, logged, put in the page URL, or included in an error message.

export const ZOOM_MIN = 50;
export const ZOOM_MAX = 300;
export const ZOOM_STEP = 15;
export const ZOOM_DEFAULT = 100;
export const PAGE_MAX_WIDTH = 560;

export function nextZoom(current: number, dir: 1 | -1): number {
  return Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, current + dir * ZOOM_STEP));
}

/** A page is at most 560 px wide at 100% zoom (less when the container is narrower). */
export function pageWidthPx(zoom: number, available = Infinity): number {
  const base = Math.max(1, Math.min(PAGE_MAX_WIDTH, available));
  return Math.round((base * zoom) / 100);
}

/** Fixed, URL-free message. `status` is 0 for a network failure. */
export class FileLoadError extends Error {
  status: number;
  constructor(status: number) {
    super("We couldn't open this file.");
    this.name = "FileLoadError";
    this.status = status;
  }
}

/**
 * Fetches the file bytes from a presigned URL. A 403 (expired URL) triggers exactly one
 * refetch of the presigned URL and one retry; a second 403, or any other failure, throws.
 * Errors from `getUrl` propagate unchanged.
 */
export async function openFile(
  getUrl: () => Promise<string>,
  fetcher: (url: string) => Promise<Response>,
): Promise<Response> {
  async function once(): Promise<Response> {
    const url = await getUrl();
    try {
      return await fetcher(url);
    } catch {
      throw new FileLoadError(0);
    }
  }
  let res = await once();
  if (res.status === 403) res = await once();
  if (!res.ok) throw new FileLoadError(res.status);
  return res;
}
