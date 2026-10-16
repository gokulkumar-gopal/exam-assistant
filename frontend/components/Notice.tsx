import type { ReactNode } from "react";

export function Notice({ children }: { children: ReactNode }) {
  return (
    <div
      role="note"
      className="mb-3.5 rounded-lg border border-warn-border bg-warn-bg px-3.5 py-2.5 text-[13px] text-warn"
    >
      {children}
    </div>
  );
}
