import type { ReactNode } from "react";

export function EmptyState({
  children,
  action,
}: {
  children: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="flex flex-col items-center gap-3 rounded-[10px] border border-line bg-card px-5 py-7 text-center">
      <p className="text-[15px] text-muted">{children}</p>
      {action}
    </div>
  );
}
