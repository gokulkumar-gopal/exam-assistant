// Tracks reads that are waiting to retry after a network or gateway failure (fs 8.1 errors table).
// api.ts calls begin() before each backoff wait and end() when that read finishes; the shell shows
// RETRYING_TEXT while any read is retrying.
export const RETRYING_TEXT = "Can't reach the server. Retrying…";

export interface Reachability {
  begin(): void;
  end(): void;
  retrying(): boolean;
  subscribe(fn: () => void): () => void;
}

export function createReachability(): Reachability {
  let n = 0;
  const subs = new Set<() => void>();
  const emit = (was: boolean) => { if (was !== (n > 0)) subs.forEach((f) => f()); };
  return {
    begin() { const was = n > 0; n++; emit(was); },
    end() { const was = n > 0; n = Math.max(0, n - 1); emit(was); },
    retrying: () => n > 0,
    subscribe(fn) { subs.add(fn); return () => { subs.delete(fn); }; },
  };
}

export const reachability = createReachability();
