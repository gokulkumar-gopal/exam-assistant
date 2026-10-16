import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

export type PillVariant = "neutral" | "ok" | "warn" | "bad";

const VARIANTS: Record<PillVariant, string> = {
  neutral: "border-line bg-bg text-ink font-normal",
  ok: "border-ok-border bg-ok-bg text-ok font-semibold",
  warn: "border-warn-border bg-warn-bg text-warn font-semibold",
  bad: "border-bad-border bg-bad-bg text-bad font-semibold",
};

export function Pill({
  variant = "neutral",
  outline = false,
  children,
}: {
  variant?: PillVariant;
  /** Border and text only (transparent fill). */
  outline?: boolean;
  children: ReactNode;
}) {
  return (
    <span
      data-variant={variant}
      className={cn(
        "inline-block rounded-[99px] border px-[9px] py-0.5 text-xs",
        VARIANTS[variant],
        outline && "bg-transparent",
      )}
    >
      {children}
    </span>
  );
}
