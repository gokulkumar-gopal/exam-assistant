"use client";
// Placeholder exam header (fs 4). F-4.x replaces the timer and saved text.
import { Button } from "@/components/ui/button";

export interface ExamBarProps {
  title: string;
  savedAt: string | null;
  peeksLeft?: number;
  deadlineIso: string;
  onSaveExit: () => void;
}

const hhmm = (iso: string) => {
  const d = new Date(iso);
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
};

export function ExamBar({ title, savedAt, peeksLeft, deadlineIso, onSaveExit }: ExamBarProps) {
  return (
    <header className="sticky top-0 z-30 h-14 border-b border-line bg-white">
      <div className="page flex h-full items-center gap-3">
        <div className="min-w-0 flex-1 truncate font-semibold">{title}</div>
        {savedAt && <span className="hidden text-sm text-muted sm:inline">Saved {hhmm(savedAt)}</span>}
        {peeksLeft !== undefined && <span className="text-sm">💡 {peeksLeft} left</span>}
        <span className="tabular text-sm" data-deadline={deadlineIso}>--:--</span>
        <Button variant="outline" onClick={onSaveExit}>Save &amp; exit</Button>
      </div>
    </header>
  );
}
