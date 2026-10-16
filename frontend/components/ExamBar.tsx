"use client";
// Exam header (fs 4, 6.5): title, autosave state, peeks, timer, Save & exit.
import { Button } from "@/components/ui/button";
import { Timer } from "@/components/Timer";
import { autosaveText, type AutosaveState } from "@/lib/autosave";
import { cn } from "@/lib/utils";

export interface ExamBarProps {
  title: string;
  save: AutosaveState;
  peeksLeft?: number;
  closesAtIso: string;
  /** serverTime minus device time, fixed once per load. */
  offsetMs: number;
  exiting?: boolean;
  onSaveExit: () => void;
}

export function ExamBar({ title, save, peeksLeft, closesAtIso, offsetMs, exiting, onSaveExit }: ExamBarProps) {
  const text = autosaveText(save);
  const failing = save.status === "retrying";
  return (
    <header className="sticky top-0 z-30 h-14 border-b border-line bg-white">
      <div className="page flex h-full items-center gap-3">
        <div className="min-w-0 flex-1 truncate font-semibold">{title}</div>
        {text && (
          <span aria-hidden="true" className={cn("shrink-0 whitespace-nowrap text-[13px]", failing ? "text-bad" : "text-muted")}>{text}</span>
        )}
        <span role="status" className="sr-only">{failing ? text : ""}</span>
        {peeksLeft !== undefined && <span className="text-sm">💡 {peeksLeft} left</span>}
        <Timer closesAtIso={closesAtIso} offsetMs={offsetMs} />
        <Button variant="outline" onClick={onSaveExit} disabled={exiting} aria-busy={exiting || undefined}>Save &amp; exit</Button>
      </div>
    </header>
  );
}
