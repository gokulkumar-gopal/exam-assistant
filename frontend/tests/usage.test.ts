import { describe, expect, it } from "vitest";
import {
  clockTime, formatUsd, zoneName, kindLabel, mapUsage, nextUsageView, spendTone, statusPill, tokensK, usedModels,
} from "../lib/usage";

process.env.TZ = "UTC";

describe("tokensK", () => {
  it("shows thousands with one decimal", () => {
    expect(tokensK(12345)).toBe("12.3K");
    expect(tokensK(0)).toBe("0.0K");
    expect(tokensK(1000)).toBe("1.0K");
    expect(tokensK(1_250_000)).toBe("1250.0K");
  });
  it("rounds to the nearest tenth with no float artefacts", () => {
    expect(tokensK(950)).toBe("1.0K");
    expect(tokensK(949)).toBe("0.9K");
    expect(tokensK(50)).toBe("0.1K");
    expect(tokensK(49)).toBe("0.0K");
  });
  it("is safe on bad input", () => {
    expect(tokensK(NaN)).toBe("0.0K");
    expect(tokensK(-5)).toBe("0.0K");
    expect(tokensK(Infinity)).toBe("0.0K");
  });
});

describe("spendTone", () => {
  it("is accent under 80%, warn from 80%, bad from 100%", () => {
    expect(spendTone(11.9, 15)).toBe("accent");
    expect(spendTone(12, 15)).toBe("warn");
    expect(spendTone(14.99, 15)).toBe("warn");
    expect(spendTone(15, 15)).toBe("bad");
    expect(spendTone(20, 15)).toBe("bad");
  });
  // Rule: spend and budget are compared in whole cents (rounded like the displayed money), so the tone
  // always agrees with the "$X of $Y" text: 11.999 shows "$12.00" and is warn, 14.996 shows "$15.00" and is bad.
  it("compares in cents so the tone matches the displayed money", () => {
    expect(spendTone(11.999, 15)).toBe("warn");
    expect(spendTone(11.994, 15)).toBe("accent");
    expect(spendTone(12.000000001, 15)).toBe("warn");
    expect(spendTone(14.996, 15)).toBe("bad");
    expect(spendTone(14.999, 15)).toBe("bad");
    expect(spendTone(14.994, 15)).toBe("warn");
    expect(spendTone(15.004, 15)).toBe("bad");
    expect(spendTone(1.005, 1.01)).toBe("bad");
  });
  it("budget 0 or missing never changes the tone (accent, no crash)", () => {
    expect(spendTone(0, 0)).toBe("accent");
    expect(spendTone(3, 0)).toBe("accent");
  });
  it("is safe on a zero or invalid budget and invalid spend", () => {
    expect(spendTone(5, 0)).toBe("accent");
    expect(spendTone(5, -1)).toBe("accent");
    expect(spendTone(NaN, 15)).toBe("accent");
    expect(spendTone(5, NaN)).toBe("accent");
    expect(spendTone(-1, 15)).toBe("accent");
  });
});

describe("formatUsd", () => {
  it("formats two decimals", () => {
    expect(formatUsd(3.42)).toBe("$3.42");
    expect(formatUsd(15)).toBe("$15.00");
    expect(formatUsd(0)).toBe("$0.00");
    expect(formatUsd(0.1 + 0.2)).toBe("$0.30");
    expect(formatUsd(1.005)).toBe("$1.01");
    expect(formatUsd(1234.5)).toBe("$1,234.50");
  });
  it("shows a tiny positive amount as under one cent, never $0.00", () => {
    expect(formatUsd(0.004)).toBe("<$0.01");
    expect(formatUsd(0.01)).toBe("$0.01");
  });
  it("is safe on bad input", () => {
    expect(formatUsd(NaN)).toBe("$0.00");
    expect(formatUsd(-2)).toBe("$0.00");
    expect(formatUsd(Infinity)).toBe("$0.00");
  });
});

describe("clockTime", () => {
  it("is HH:MM:SS from the given ISO time", () => {
    expect(clockTime("2026-10-17T10:42:07Z")).toBe("10:42:07");
    expect(clockTime("2026-10-17T00:05:09Z")).toBe("00:05:09");
  });
  it("is empty for an invalid time", () => {
    expect(clockTime("nope")).toBe("");
  });
});

describe("zoneName", () => {
  it("is the short zone name of the viewer", () => {
    expect(zoneName("2026-10-17T10:42:07Z")).toBe("UTC");
  });
  it("is empty for an invalid time", () => {
    expect(zoneName("nope")).toBe("");
  });
});

describe("statusPill and kindLabel", () => {
  it("maps statuses to pill variants", () => {
    expect(statusPill("queued")).toEqual({ variant: "neutral", text: "queued" });
    expect(statusPill("running")).toEqual({ variant: "warn", text: "running" });
    expect(statusPill("done")).toEqual({ variant: "ok", text: "done" });
    expect(statusPill("failed")).toEqual({ variant: "bad", text: "failed" });
    expect(statusPill("cancelled")).toEqual({ variant: "neutral", text: "cancelled" });
    expect(statusPill("???")).toEqual({ variant: "neutral", text: "unknown" });
  });
  it("labels kinds", () => {
    expect(kindLabel("generate")).toBe("Generate");
    expect(kindLabel("regenerate")).toBe("Regenerate");
    expect(kindLabel("other")).toBe("Generate");
  });
});

const T = { input: 1, output: 2, cacheRead: 3, cacheWrite: 4 };
const raw = {
  serverTime: "2026-10-17T10:42:07Z",
  month: { key: "2026-10", spendUsd: 3.42, budgetUsd: 15, jobs: 21, calls: 88, tokens: T, owner: "Ann" },
  today: { jobs: 2, calls: 9, spendUsd: 0.31, tokens: T, userEmail: "a@b.c" },
  byModel: [
    { id: "m1", name: "Model One", jobs: 3, calls: 4, spendUsd: 1, tokens: T, secret: "x" },
    { id: "m2", name: "Idle", jobs: 0, calls: 0, spendUsd: 0, tokens: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } },
  ],
  running: 1,
  recent: [
    { createdAt: "2026-10-17T10:00:00Z", kind: "generate", model: "m1", status: "running", calls: 4, tokens: T, costUsd: 0.12,
      userName: "Bob", fileName: "Biology.pdf", questionText: "What is DNA?" },
  ],
  userName: "Carol",
};

describe("mapUsage", () => {
  it("keeps only whitelisted fields (no user, file or question text can reach the page)", () => {
    const m = mapUsage(raw);
    const s = JSON.stringify(m);
    for (const leak of ["Ann", "a@b.c", "Bob", "Biology.pdf", "What is DNA?", "Carol", "secret", "owner"]) {
      expect(s).not.toContain(leak);
    }
    expect(m.month).toEqual({ key: "2026-10", spendUsd: 3.42, budgetUsd: 15, jobs: 21, calls: 88, tokens: T });
    expect(Object.keys(m.recent[0]).sort()).toEqual(["calls", "costUsd", "createdAt", "kind", "model", "status", "tokens"]);
    expect(Object.keys(m.byModel[0]).sort()).toEqual(["calls", "id", "jobs", "name", "spendUsd", "tokens"]);
    expect(m.running).toBe(1);
    expect(m.serverTime).toBe("2026-10-17T10:42:07Z");
  });
  it("defaults missing or wrong-typed numbers to 0 and lists to empty", () => {
    const m = mapUsage({ serverTime: "2026-10-17T10:42:07Z", month: { key: "2026-10", spendUsd: "x" } });
    expect(m.month.spendUsd).toBe(0);
    expect(m.month.tokens).toEqual({ input: 0, output: 0, cacheRead: 0, cacheWrite: 0 });
    expect(m.byModel).toEqual([]);
    expect(m.recent).toEqual([]);
    expect(m.running).toBe(0);
    expect(mapUsage(null).month.jobs).toBe(0);
  });
});

describe("usedModels", () => {
  it("drops models with no usage and keeps the server (cheapest first) order", () => {
    const m = mapUsage(raw);
    expect(usedModels(m.byModel).map((x) => x.id)).toEqual(["m1"]);
  });
});

describe("nextUsageView", () => {
  const data = mapUsage(raw);
  it("a success replaces the numbers and clears the error", () => {
    expect(nextUsageView({ data: null, failed: true }, { ok: true, data })).toEqual({ data, failed: false });
  });
  it("a failure keeps the previous numbers and flags the error", () => {
    expect(nextUsageView({ data, failed: false }, { ok: false })).toEqual({ data, failed: true });
  });
  it("a failure with no numbers yet stays empty", () => {
    expect(nextUsageView({ data: null, failed: false }, { ok: false })).toEqual({ data: null, failed: true });
  });
});
