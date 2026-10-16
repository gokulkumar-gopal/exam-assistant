"use client";
// Sticky footer of Create (fs 6.4): marks total, meter, status, Generate.
import { Meter } from "@/components/Meter";
import type { FooterTone } from "@/lib/totals";
import { SENT_TO_AI_LINE } from "@/lib/generate";

const TEXT: Record<FooterTone, string> = { accent: "text-accent", ok: "text-ok", bad: "text-bad" };

export function CreateFooter({
  total,
  target,
  tone,
  text,
  generateEnabled,
  onGenerate,
}: {
  total: number;
  target: number;
  tone: FooterTone;
  text: string;
  generateEnabled: boolean;
  onGenerate: () => void;
}) {
  return (
    <div
      role="region"
      aria-label="Generate"
      className="fixed inset-x-0 bottom-0 z-40 border-t border-line bg-white"
    >
      <p className="page pt-2 text-[13px] text-muted">{SENT_TO_AI_LINE}</p>
      <div className="page flex flex-wrap items-center gap-x-4 gap-y-2 pb-3 pt-1">
        <span className="font-bold tabular-nums">{total} / {target} marks</span>
        <div className="hidden min-w-24 max-w-[260px] flex-1 sm:block">
          <Meter value={total} max={target} tone={tone} label="Marks selected" />
        </div>
        <span className={`font-semibold ${TEXT[tone]}`} role="status">{text}</span>
        <button type="button" className="btn-primary ml-auto" disabled={!generateEnabled} onClick={onGenerate}>
          Generate
        </button>
      </div>
    </div>
  );
}
