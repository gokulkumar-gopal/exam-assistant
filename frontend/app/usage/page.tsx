"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "@/lib/api";
import { usePolling } from "@/lib/usePolling";
import {
  clockTime, clockTimeShort, zoneName, formatUsd, kindLabel, mapUsage, nextUsageView, spendTone, statusPill,
  tokensK, usedModels, type UsageView,
} from "@/lib/usage";
import type { Tokens } from "@/lib/types";
import { Meter } from "@/components/Meter";
import { Notice } from "@/components/Notice";
import { Pill } from "@/components/Pill";
import { EmptyState } from "@/components/EmptyState";

const TH = "whitespace-nowrap px-3 py-2 text-left text-xs font-semibold text-muted";
const TD = "whitespace-nowrap px-3 py-2 text-[13px] text-ink";

// A horizontal scroll area must be focusable so keyboard users can scroll it (focus ring from the global :focus-visible).
const SCROLL = { className: "overflow-x-auto focus-visible:outline-offset-[-2px]", tabIndex: 0, role: "region" } as const;

function TokenLine({ t }: { t: Tokens }) {
  return (
    <p className="tabular text-[13px] text-muted">
      Tokens: input {tokensK(t.input)}, output {tokensK(t.output)}, cache read {tokensK(t.cacheRead)}, cache write {tokensK(t.cacheWrite)}
    </p>
  );
}

export default function Page() {
  const [view, setView] = useState<UsageView>({ data: null, failed: false });
  const heading = useRef<HTMLHeadingElement>(null);
  const focused = useRef(false);

  const refresh = useCallback(async () => {
    try {
      // No client retries: the poll itself is the retry.
      const raw = await api<unknown>("GET", "/ai/usage", undefined, { noRetry: true });
      setView((v) => nextUsageView(v, { ok: true, data: mapUsage(raw) }));
    } catch {
      setView((v) => nextUsageView(v, { ok: false }));
    }
  }, []);
  usePolling(refresh, 5000);

  // The h1 takes focus once, when the first content (or the first failure) arrives.
  const ready = view.data !== null || view.failed;
  useEffect(() => {
    if (ready && !focused.current) { focused.current = true; heading.current?.focus(); }
  }, [ready]);

  const d = view.data;
  const names = new Map((d?.byModel ?? []).map((m) => [m.id, m.name]));
  const empty = d !== null && d.month.jobs === 0 && d.recent.length === 0;
  const models = d ? usedModels(d.byModel) : [];

  return (
    <main className="page py-5">
      <h1 ref={heading} tabIndex={-1} className="outline-none">AI usage</h1>
      <p className="text-[13px] text-muted">Live. Updates every 5 seconds.</p>
      {d && <p className="tabular mb-3 text-[13px] text-muted">Updated {clockTime(d.serverTime)} {zoneName(d.serverTime)}</p>}
      {/* Only this region is live: the 5-second updates are silent, a failure is announced politely. */}
      <div aria-live="polite" className="mb-3 text-[13px] text-bad">
        {view.failed && <p>Couldn&apos;t refresh. Retrying…</p>}
      </div>

      {!d && !view.failed && <p className="text-muted">Loading…</p>}

      {d && (
        <div className="flex flex-col gap-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <section className="card p-4" aria-labelledby="u-month">
              <h2 id="u-month" className="h-section mb-2.5">This month ({d.month.key})</h2>
              <Meter
                value={d.month.spendUsd}
                max={d.month.budgetUsd}
                tone={spendTone(d.month.spendUsd, d.month.budgetUsd)}
                label="Spend against the monthly budget"
              />
              <p className="tabular mb-2 mt-1.5 text-[15px] font-semibold text-ink">
                {formatUsd(d.month.spendUsd)} of {formatUsd(d.month.budgetUsd)}
              </p>
              {spendTone(d.month.spendUsd, d.month.budgetUsd) === "bad" && (
                <Notice>The monthly AI budget has been used. New question sets are paused until next month.</Notice>
              )}
              <p className="tabular text-[13px] text-ink">Jobs {d.month.jobs}, calls {d.month.calls}</p>
              <TokenLine t={d.month.tokens} />
            </section>
            <section className="card p-4" aria-labelledby="u-today">
              <h2 id="u-today" className="h-section mb-2.5">Today (UTC)</h2>
              <p className="tabular mb-2 text-[15px] font-semibold text-ink">{formatUsd(d.today.spendUsd)}</p>
              <p className="tabular text-[13px] text-ink">Jobs {d.today.jobs}, calls {d.today.calls}</p>
              <TokenLine t={d.today.tokens} />
            </section>
          </div>

          <p className="tabular font-semibold text-ink">Running now: {d.running}</p>

          {empty ? (
            <EmptyState>No AI jobs yet.</EmptyState>
          ) : (
            <>
              <section className="card overflow-hidden" aria-labelledby="u-models">
                <h2 id="u-models" className="h-section px-4 pb-1 pt-4">By model</h2>
                <div {...SCROLL} aria-labelledby="u-models">
                  <table className="tabular w-full border-collapse">
                    <thead>
                      <tr className="border-b border-line">
                        <th scope="col" className={TH}>Model</th>
                        <th scope="col" className={TH}>Jobs</th>
                        <th scope="col" className={TH}>Calls</th>
                        <th scope="col" className={TH}>Tokens</th>
                        <th scope="col" className={TH}>Spend</th>
                      </tr>
                    </thead>
                    <tbody>
                      {models.map((m) => (
                        <tr key={m.id} className="border-b border-line last:border-0">
                          <th scope="row" className={`${TD} text-left font-normal`}>{m.name || m.id}</th>
                          <td className={TD}>{m.jobs}</td>
                          <td className={TD}>{m.calls}</td>
                          <td className={TD}>
                            {tokensK(m.tokens.input + m.tokens.output + m.tokens.cacheRead + m.tokens.cacheWrite)}
                          </td>
                          <td className={TD}>{formatUsd(m.spendUsd)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </section>

              <section className="card overflow-hidden" aria-labelledby="u-jobs">
                <h2 id="u-jobs" className="h-section px-4 pb-1 pt-4">Latest jobs</h2>
                <div {...SCROLL} aria-labelledby="u-jobs">
                  <table className="tabular w-full border-collapse">
                    <thead>
                      <tr className="border-b border-line">
                        <th scope="col" className={TH}>Time ({zoneName(d.serverTime)})</th>
                        <th scope="col" className={TH}>Kind</th>
                        <th scope="col" className={TH}>Model</th>
                        <th scope="col" className={TH}>Status</th>
                        <th scope="col" className={TH}>Calls</th>
                        <th scope="col" className={TH}>Cost</th>
                      </tr>
                    </thead>
                    <tbody>
                      {d.recent.map((j, i) => {
                        const p = statusPill(j.status);
                        return (
                          <tr key={`${j.createdAt}-${i}`} className="border-b border-line last:border-0">
                            <td className={TD}>{clockTimeShort(j.createdAt)}</td>
                            <td className={TD}>{kindLabel(j.kind)}</td>
                            <td className={TD}>{names.get(j.model) || j.model}</td>
                            <td className={TD}><Pill variant={p.variant}>{p.text}</Pill></td>
                            <td className={TD}>{j.calls}</td>
                            <td className={TD}>{formatUsd(j.costUsd)}</td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>
              </section>
            </>
          )}
        </div>
      )}
    </main>
  );
}
