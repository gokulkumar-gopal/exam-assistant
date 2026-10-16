// Question order and the gap notice (fs 6.4 generated list).
import type { Question } from "./types";

/** Small seeded PRNG (mulberry32): same seed, same sequence. */
function rng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** Ascending by marks (stable), or a Fisher-Yates shuffle fixed by `seed`. Never mutates `qs`. */
export function orderQuestions(qs: Question[], shuffle: boolean, seed: number): Question[] {
  const out = [...qs];
  if (!shuffle) return out.sort((a, b) => a.marks - b.marks);
  const next = rng(seed);
  for (let i = out.length - 1; i > 0; i--) {
    const j = Math.floor(next() * (i + 1));
    [out[i], out[j]] = [out[j], out[i]];
  }
  return out;
}

/** Marks to add back when deletions leave the list under target: k = min(gap, 5); null when not under. */
export function gapNotice(target: number, total: number): { k: number } | null {
  const gap = target - total;
  return gap > 0 ? { k: Math.min(gap, 5) } : null;
}
