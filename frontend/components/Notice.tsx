import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

const TONES = {
  warn: "border-warn-border bg-warn-bg text-warn",
  // Ink text on the bad tint keeps the contrast above what bad-on-bad-bg gives.
  bad: "border-bad-border bg-bad-bg text-ink",
};

export function Notice({ children, tone = "warn" }: { children: ReactNode; tone?: keyof typeof TONES }) {
  return (
    <div
      role={tone === "bad" ? "alert" : "note"}
      className={cn("mb-3.5 rounded-lg border px-3.5 py-2.5 text-[13px]", TONES[tone])}
    >
      {children}
    </div>
  );
}
