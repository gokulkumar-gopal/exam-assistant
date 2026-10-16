// Mock "S3 GET" for MOCK_API=true: the mock presign points at small static files in public/mock.
// Loaded only in mock mode. Test triggers, set from the browser console:
//   globalThis.__mockFile403 = 1      -> the next file fetch answers 403 (then clears), so Retry-once is visible
//   globalThis.__mockFile403 = 2      -> two 403s in a row (the viewer must show the error)
//   globalThis.__mockFileFail = true  -> every file fetch answers 500 until cleared
//   globalThis.__mockFileDelay = 800  -> file fetches take this many ms (to see the loading placeholder)
// Log (ids and statuses, never URLs): globalThis.__mockFileLog, entries "presign:<id>" and "fetch:<status>".
import type { Doc } from "../types";

type Flags = { __mockFile403?: number; __mockFileFail?: boolean; __mockFileDelay?: number; __mockFileLog?: string[] };
const g = globalThis as unknown as Flags;

export function mockFileUrl(type: Doc["type"]): string {
  return type === "PDF" ? "/mock/sample.pdf" : type === "TXT" ? "/mock/notes.txt" : "/mock/sample.png";
}

export async function mockFileFetch(url: string): Promise<Response> {
  const log = (g.__mockFileLog ??= []);
  if (g.__mockFileDelay) await new Promise((r) => setTimeout(r, g.__mockFileDelay));
  let res: Response;
  if (g.__mockFile403) {
    g.__mockFile403 -= 1;
    res = new Response("", { status: 403 });
  } else if (g.__mockFileFail) {
    res = new Response("", { status: 500 });
  } else {
    res = await fetch(url);
  }
  log.push(`fetch:${res.status}`);
  return res;
}
