"use client";
// Fixed bottom bar for the library (fs 6.2, 7). Hidden when nothing is selected.
import { Trash2 } from "lucide-react";

export function SelectionBar({
  count,
  onDelete,
  onGenerate,
}: {
  count: number;
  onDelete: () => void;
  onGenerate: () => void;
}) {
  if (count < 1) return null;
  return (
    <div
      role="region"
      aria-label="Selection actions"
      className="fixed inset-x-0 bottom-0 z-40 text-white shadow-[0_-4px_14px_rgba(42,15,107,0.28)]"
      style={{
        background: "linear-gradient(135deg, #2a0f6b 0%, #6d28d9 100%)",
        animation: "bar-slide-up 200ms ease-out",
      }}
    >
      <div className="page flex flex-wrap items-center gap-x-3 gap-y-2 py-3">
        <span className="mr-auto font-semibold" aria-live="polite">{count} selected</span>
        <button type="button" className="btn-secondary !text-bad" onClick={onDelete}>
          <Trash2 size={16} aria-hidden="true" />
          Delete
        </button>
        <button type="button" className="btn-primary" onClick={onGenerate}>
          Generate questions
        </button>
      </div>
    </div>
  );
}
