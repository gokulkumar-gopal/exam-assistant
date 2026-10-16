import { afterEach, describe, expect, it, vi } from "vitest";
import { clearGenerated, getGenerated, setGenerated, type GeneratedResult } from "../lib/generated";

const r: GeneratedResult = {
  questions: [{ id: "q1", marks: 1, text: "Q?" }],
  model: "m1",
  params: { sourceIds: ["d1"], type: "mcq", counts: { "1": 1 }, shuffle: false, target: 30 },
};

afterEach(() => { clearGenerated(); vi.unstubAllGlobals(); });

describe("generated store", () => {
  it("round-trips in memory when sessionStorage is unavailable", () => {
    expect(getGenerated()).toBeNull();
    setGenerated(r);
    expect(getGenerated()).toEqual(r);
    clearGenerated();
    expect(getGenerated()).toBeNull();
  });
  it("also writes sessionStorage and reads it back after a memory reset", () => {
    const store = new Map<string, string>();
    vi.stubGlobal("sessionStorage", {
      getItem: (k: string) => store.get(k) ?? null,
      setItem: (k: string, v: string) => void store.set(k, v),
      removeItem: (k: string) => void store.delete(k),
    });
    setGenerated(r);
    expect(store.size).toBe(1);
    expect(JSON.parse([...store.values()][0])).toEqual(r);
  });
});
