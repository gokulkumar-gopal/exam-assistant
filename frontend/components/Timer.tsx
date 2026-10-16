"use client";
// Header countdown (fs 6.5). Derived from the server deadline and a server offset fixed at load,
// so a reload never resets it. role=timer is not a live region; a separate visually hidden status
// speaks only at 5:00, 1:00 and 0.
import { useEffect, useState } from "react";
import { timerBucket, timerView } from "@/lib/timer";
import { cn } from "@/lib/utils";

export function Timer({ closesAtIso, offsetMs, className }: { closesAtIso: string; offsetMs: number; className?: string }) {
  const [now, setNow] = useState(() => Date.now() + offsetMs);
  useEffect(() => {
    const tick = () => setNow(Date.now() + offsetMs);
    tick();
    const id = setInterval(tick, 1000);
    return () => clearInterval(id);
  }, [offsetMs]);
  const { text, low } = timerView(closesAtIso, now);
  return (
    <>
      <span role="timer" aria-label="Time left" className={cn("tabular whitespace-nowrap text-sm font-bold", low && "text-bad", className)}>
        {text}
      </span>
      <span role="status" className="sr-only">{timerBucket(closesAtIso, now)}</span>
    </>
  );
}
