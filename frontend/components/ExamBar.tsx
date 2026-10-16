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
  /** A submit has started: Save & exit is off. */
  exitDisabled?: boolean;
  onSaveExit: () => void;
}

export function ExamBar({ title, save, peeksLeft, closesAtIso, offsetMs, exiting, exitDisabled, onSaveExit }: ExamBarProps) {
  const text = autosaveText(save);
  const failing = save.status === "retrying";
  return (
    <header className="sticky top-0 z-30 min-h-14 border-b border-line bg-white">
      {/* Up to 480 px the bar wraps: row 1 title + timer, row 2 save state + Save & exit (title never squeezed). */}
      <div className="page flex min-h-14 flex-wrap items-center gap-x-3 gap-y-1 py-1 max-[480px]:py-1.5">
        <div className="order-1 min-w-[140px] flex-1 truncate font-semibold min-[481px]:order-none min-[481px]:min-w-0">{title}</div>
        <div aria-hidden="true" className="order-3 h-0 basis-full min-[481px]:hidden" />
        {text && (
          <span aria-hidden="true" className={cn("order-4 shrink-0 whitespace-nowrap text-[13px] max-[480px]:mr-auto min-[481px]:order-none", failing ? "text-bad" : "text-muted")}>{text}</span>
        )}
        <span role="status" className="sr-only">{failing ? text : ""}</span>
        {peeksLeft !== undefined && <span className="order-4 text-sm min-[481px]:order-none">💡 {peeksLeft} left</span>}
        <Timer closesAtIso={closesAtIso} offsetMs={offsetMs} className="order-2 min-[481px]:order-none" />
        <Button variant="outline" className="order-4 max-[480px]:ml-auto min-[481px]:order-none" onClick={onSaveExit} disabled={exiting || exitDisabled} aria-busy={exiting || undefined}>Save &amp; exit</Button>
      </div>
    </header>
  );
}
