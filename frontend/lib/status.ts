// Teacher status page helpers (fs 6.6). Pure.
import { attemptsLabel } from "./attempts";
import { countsLine } from "./exam-row";
import { scoreText } from "./format";
import type { PillVariant } from "../components/Pill";
import type { StatusResponse } from "./types";

type Row = StatusResponse["attempts"][number];

/** A student with more than one attempt gets the Attempts disclosure (fs 6.6). */
export const hasAttemptHistory = (row: Pick<Row, "attemptCount" | "history">): boolean =>
  (row.attemptCount ?? 0) > 1 && (row.history?.length ?? 0) > 0;

export const attemptsToggleLabel = (name: string, open: boolean): string => `${open ? "Hide" : "Show"} attempts for ${name}`;

/** "2 submitted, 1 in progress, 1 not started" plus ", 1 declined" only above 0 (wording is literal, never pluralised). */
export function statusSubtitle(counts: StatusResponse["counts"]): string {
  return countsLine(counts);
}

/**
 * The best attempt's score: "34/40", plus " · 3 attempts" when there is more than one; an em dash until graded
 * (not submitted, or a submitted free-text exam awaiting a score).
 */
export function scoreCell(row: Pick<Row, "status" | "score"> & { attemptCount?: number }, totalMarks: number): string {
  if (row.status !== "submitted" || typeof row.score !== "number") return "—";
  const n = attemptsLabel(row.attemptCount);
  return `${scoreText(row.score)}/${totalMarks}${n ? ` · ${n}` : ""}`;
}

export function statusPill(status: Row["status"]): { variant: PillVariant; text: string; outline: boolean } {
  switch (status) {
    case "submitted": return { variant: "ok", text: "Submitted", outline: false };
    case "in_progress": return { variant: "warn", text: "In progress", outline: false };
    case "declined": return { variant: "bad", text: "Declined", outline: true };
    default: return { variant: "neutral", text: "Not started", outline: false };
  }
}

/** "Oct 16, 2:30 PM" in the viewer's timezone; an em dash when there is no usable timestamp. */
export function submittedCell(iso: string | undefined): string {
  const t = iso ? Date.parse(iso) : NaN;
  if (Number.isNaN(t)) return "—";
  return new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" }).format(new Date(t));
}
