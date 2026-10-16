"use client";
import { useSyncExternalStore } from "react";
import { reachability, RETRYING_TEXT } from "@/lib/reachability";

/** fs 8.1: while a read is retrying after a network or gateway failure, say so. Never takes layout away from print. */
export function ReachabilityNotice() {
  const retrying = useSyncExternalStore(reachability.subscribe, reachability.retrying, () => false);
  if (!retrying) return null;
  return (
    <div className="page pt-3 print:hidden" role="status">
      <div className="rounded-lg border border-warn-border bg-warn-bg px-3.5 py-2.5 text-[13px] text-warn">{RETRYING_TEXT}</div>
    </div>
  );
}
