// AI usage page helpers (fs 6.10). Pure; aggregate numbers only.
import type { PillVariant } from "@/components/Pill";
import type { MeterTone } from "@/components/Meter";
import type { Tokens, UsageResponse } from "./types";

const finite = (n: unknown): number => (typeof n === "number" && Number.isFinite(n) ? n : 0);
const str = (s: unknown): string => (typeof s === "string" ? s : "");

/** Tokens in thousands with one decimal ("12.3K"). Integer tenths, so no float artefacts. */
export function tokensK(n: number): string {
  const v = finite(n);
  const tenths = Math.round(Math.max(0, v) / 100);
  return `${(tenths / 10).toFixed(1)}K`;
}

/** Meter colour for spend against budget: warn from 80%, bad from 100%. Bad input stays accent. */
export function spendTone(spend: number, budget: number): Exclude<MeterTone, "ok"> {
  if (!Number.isFinite(spend) || !Number.isFinite(budget) || budget <= 0 || spend < 0) return "accent";
  if (spend >= budget) return "bad";
  if (spend * 100 >= budget * 80) return "warn";
  return "accent";
}

const USD = new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", minimumFractionDigits: 2, maximumFractionDigits: 2 });

/** "$3.42"; a positive amount under half a cent is "<$0.01" (never a misleading "$0.00"). */
export function formatUsd(n: number): string {
  const v = finite(n);
  if (v <= 0) return USD.format(0);
  if (v < 0.005) return "<$0.01";
  return USD.format(v);
}

// Built per call so the user's timezone is read when used.
const clock = (seconds: boolean) =>
  new Intl.DateTimeFormat("en-US", { hour: "2-digit", minute: "2-digit", ...(seconds ? { second: "2-digit" as const } : {}), hourCycle: "h23" });

/** "10:42:07" in the user's timezone from an ISO time (the server time, never the device clock). */
export function clockTime(iso: string): string {
  const t = Date.parse(iso);
  return Number.isNaN(t) ? "" : clock(true).format(new Date(t));
}
export function clockTimeShort(iso: string): string {
  const t = Date.parse(iso);
  return Number.isNaN(t) ? "" : clock(false).format(new Date(t));
}

const PILLS: Record<string, PillVariant> = {
  queued: "neutral", running: "warn", done: "ok", failed: "bad", cancelled: "neutral",
};
export function statusPill(status: string): { variant: PillVariant; text: string } {
  return status in PILLS ? { variant: PILLS[status], text: status } : { variant: "neutral", text: "unknown" };
}

export function kindLabel(kind: string): string {
  return kind === "regenerate" ? "Regenerate" : "Generate";
}

function tokens(t: unknown): Tokens {
  const o = (t ?? {}) as Record<string, unknown>;
  return { input: finite(o.input), output: finite(o.output), cacheRead: finite(o.cacheRead), cacheWrite: finite(o.cacheWrite) };
}

/** Copies only the whitelisted fields; anything else the API sends (names, files, text) is dropped. */
export function mapUsage(raw: unknown): UsageResponse {
  const r = (raw ?? {}) as Record<string, unknown>;
  const m = (r.month ?? {}) as Record<string, unknown>;
  const t = (r.today ?? {}) as Record<string, unknown>;
  const list = (v: unknown): Record<string, unknown>[] => (Array.isArray(v) ? (v as Record<string, unknown>[]) : []);
  return {
    serverTime: str(r.serverTime),
    month: { key: str(m.key), spendUsd: finite(m.spendUsd), budgetUsd: finite(m.budgetUsd), jobs: finite(m.jobs), calls: finite(m.calls), tokens: tokens(m.tokens) },
    today: { jobs: finite(t.jobs), calls: finite(t.calls), spendUsd: finite(t.spendUsd), tokens: tokens(t.tokens) },
    byModel: list(r.byModel).map((b) => ({
      id: str(b.id), name: str(b.name), jobs: finite(b.jobs), calls: finite(b.calls), spendUsd: finite(b.spendUsd), tokens: tokens(b.tokens),
    })),
    running: finite(r.running),
    recent: list(r.recent).map((j) => ({
      createdAt: str(j.createdAt), kind: str(j.kind), model: str(j.model), status: str(j.status),
      calls: finite(j.calls), tokens: tokens(j.tokens), costUsd: finite(j.costUsd),
    })),
  };
}

/** Only models with usage this month. The server lists them cheapest first; that order is kept. */
export function usedModels(models: UsageResponse["byModel"]): UsageResponse["byModel"] {
  return models.filter((m) => m.jobs > 0 || m.calls > 0 || m.spendUsd > 0);
}

export interface UsageView { data: UsageResponse | null; failed: boolean }
export type UsageResult = { ok: true; data: UsageResponse } | { ok: false };

/** A failed refresh keeps the last numbers and only raises the error flag. */
export function nextUsageView(prev: UsageView, res: UsageResult): UsageView {
  return res.ok ? { data: res.data, failed: false } : { data: prev.data, failed: true };
}
