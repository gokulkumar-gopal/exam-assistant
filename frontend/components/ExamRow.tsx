"use client";
import Link from "next/link";
import { Pill } from "@/components/Pill";
import { canDecline, countsLine, examMeta, rowView } from "@/lib/exam-row";
import type { ExamListItem } from "@/lib/types";

const QUIET = "inline-flex min-h-10 items-center rounded-lg px-2 text-[13px] text-muted underline-offset-2 hover:underline";

/** One Home row: title, meta, optional pill, exactly one action button, quiet Print (and Decline when allowed). */
export function ExamRow({
  exam,
  mode,
  userId,
  nowMs,
  onDecline,
}: {
  exam: ExamListItem;
  mode: "attempt" | "created";
  userId: string | undefined;
  nowMs: number;
  onDecline?: (exam: ExamListItem) => void;
}) {
  const id = encodeURIComponent(exam.id);
  const view = mode === "attempt" ? rowView(exam, nowMs) : null;
  const href = view?.action === "View result" ? `/exams/${id}/result` : `/exams/${id}/take`;
  const showDecline = mode === "attempt" && !!onDecline && canDecline(exam, userId, nowMs);

  return (
    <li className="card flex flex-col gap-2 px-3.5 py-3 sm:flex-row sm:items-center sm:gap-3">
      <div className="min-w-0 flex-1">
        <div className="break-words font-semibold">{exam.title}</div>
        <div className="break-words text-[13px] text-muted">{examMeta(exam, userId, nowMs)}</div>
        {mode === "created" && <div className="mt-0.5 text-[13px]">{countsLine(exam.counts)}</div>}
        {view?.pill && (
          <div className="mt-1.5">
            <Pill variant={view.pill.variant}>{view.pill.text}</Pill>
          </div>
        )}
      </div>
      <div className="flex flex-wrap items-center gap-1">
        {showDecline && (
          <button type="button" className={QUIET} onClick={() => onDecline?.(exam)}>Decline</button>
        )}
        <a
          href={`/exams/${id}/print`}
          target="_blank"
          rel="noopener noreferrer"
          className={QUIET}
          aria-label={`Print ${exam.title}`}
        >
          Print
        </a>
        {mode === "created" ? (
          <Link href={`/exams/${id}/status`} className="btn-secondary">View status</Link>
        ) : (
          view?.action && (
            <Link href={href} className={view.action === "View result" ? "btn-secondary" : "btn-primary"}>
              {view.action}
            </Link>
          )
        )}
      </div>
    </li>
  );
}

export function ExamRowSkeleton() {
  return (
    <ul className="flex flex-col gap-2" role="status" aria-label="Loading" data-testid="skeleton">
      {[0, 1, 2].map((i) => (
        <li key={i} className="card flex animate-pulse items-center gap-3 px-3.5 py-3">
          <div className="flex-1 space-y-2">
            <div className="h-4 w-2/3 rounded bg-line" />
            <div className="h-3 w-1/2 rounded bg-line" />
          </div>
          <div className="h-10 w-20 rounded-lg bg-line" />
        </li>
      ))}
    </ul>
  );
}
