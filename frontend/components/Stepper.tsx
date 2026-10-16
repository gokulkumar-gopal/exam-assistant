import type { ReactNode } from "react";
import { Minus, Plus } from "lucide-react";
import { cn } from "@/lib/utils";
import type { Marks } from "./MarkBadge";

const LABEL_COLOURS: Record<Marks, string> = {
  1: "text-mark-1",
  2: "text-mark-2",
  3: "text-mark-3",
  4: "text-mark-4",
  5: "text-mark-5",
};

const BTN =
  "inline-flex size-10 shrink-0 items-center justify-center rounded-lg border border-line bg-card text-accent hover:bg-tint focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-40 disabled:hover:bg-card";

export function Stepper({
  label,
  marks,
  value,
  onChange,
  subtotal,
  max,
}: {
  label: string;
  marks?: Marks;
  value: number;
  onChange: (value: number) => void;
  subtotal: ReactNode;
  /** Optional upper bound; the + button disables at it. */
  max?: number;
}) {
  const current = Math.max(0, value);
  return (
    <div className="flex items-center gap-2.5 border-t border-line py-2 first:border-t-0">
      <span
        className={cn(
          "w-[70px] text-[15px] font-bold",
          marks ? LABEL_COLOURS[marks] : "text-ink",
        )}
      >
        {label}
      </span>
      <button
        type="button"
        className={BTN}
        aria-label={`Decrease ${label}`}
        disabled={current <= 0}
        onClick={() => onChange(Math.max(0, current - 1))}
      >
        <Minus className="size-4" aria-hidden />
      </button>
      <span
        className="w-[26px] text-center font-semibold tabular-nums"
        aria-live="polite"
      >
        {current}
      </span>
      <button
        type="button"
        className={BTN}
        aria-label={`Increase ${label}`}
        disabled={max !== undefined && current >= max}
        onClick={() => onChange(current + 1)}
      >
        <Plus className="size-4" aria-hidden />
      </button>
      <span className="ml-auto min-w-20 text-right text-[13px] text-muted">
        {subtotal}
      </span>
    </div>
  );
}
