import { cn } from "@/lib/utils";

export type Marks = 1 | 2 | 3 | 4 | 5;

const COLOURS: Record<Marks, string> = {
  1: "bg-mark-1",
  2: "bg-mark-2",
  3: "bg-mark-3",
  4: "bg-mark-4",
  5: "bg-mark-5",
};

export function MarkBadge({ marks }: { marks: Marks }) {
  return (
    <span
      data-marks={marks}
      className={cn(
        "inline-block rounded-[6px] px-[9px] py-0.5 text-xs font-bold text-white",
        COLOURS[marks],
      )}
    >
      {marks} {marks === 1 ? "mark" : "marks"}
    </span>
  );
}
