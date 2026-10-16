"use client";
// Numbered question dots (fs 6.5). 30 px circle inside a 40 px hit target so touch stays easy.
// Fill: gradient when answered, white with a border when blank. The current dot also gets a 2 px
// --accent2 outline offset by 2 px, drawn over whichever fill it has.
import { dotFlags } from "@/lib/dots";
import { dotLabel } from "@/lib/take";
import { cn } from "@/lib/utils";

export interface DotNavProps {
  /** Answers in question order (undefined = never answered). */
  answers: readonly (string | undefined)[];
  current: number;
  onSelect: (index: number) => void;
}

export function DotNav({ answers, current, onSelect }: DotNavProps) {
  return (
    <nav aria-label="Questions" className="-mx-1 mb-3 flex flex-wrap">
      {answers.map((_, i) => {
        const { fill, current: isCurrent } = dotFlags(i, answers, current);
        return (
          <button
            key={i}
            type="button"
            aria-label={dotLabel(i, fill === "answered")}
            aria-current={isCurrent ? "step" : undefined}
            data-fill={fill}
            data-current={isCurrent ? "true" : undefined}
            onClick={() => onSelect(i)}
            className="flex h-10 w-10 items-center justify-center rounded-full"
          >
            <span
              aria-hidden="true"
              className={cn(
                "tabular flex size-[30px] items-center justify-center rounded-full text-[13px] font-semibold",
                fill === "answered" ? "bg-grad text-white" : "border border-muted bg-card text-ink",
                isCurrent && "outline-2 outline-offset-2 outline-accent2",
              )}
            >
              {i + 1}
            </span>
          </button>
        );
      })}
    </nav>
  );
}
