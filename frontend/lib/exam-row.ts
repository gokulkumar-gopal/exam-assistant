// Home row view model (fs 6.1 table). Pure; time comes from the caller (server-clock now).
import { deadlineText, scoreText } from "./format";
import type { PillVariant } from "../components/Pill";
import type { ExamListItem } from "./types";

export type RowAction = "Start" | "Resume" | "View result";
export interface RowView {
  pill: { variant: PillVariant; text: string } | null;
  /** Null only for a declined exam, which Home never lists. */
  action: RowAction | null;
}

/**
 * fs 6.1: "Not started" is a plain (neutral) label, not a coloured pill.
 * Past acceptingUntil an unfinished attempt is Missed; between closesAt and acceptingUntil it is Closing….
 */
export function rowView(exam: ExamListItem, nowMs: number): RowView {
  const status = exam.attemptStatus ?? "not_started";
  if (status === "declined") return { pill: { variant: "neutral", text: "Declined" }, action: null };
  if (status === "submitted") {
    const hasScore = typeof exam.score === "number" && typeof exam.maxScore === "number";
    const text = hasScore ? `Submitted · ${scoreText(exam.score as number)}/${exam.maxScore}` : "Submitted";
    return { pill: { variant: "ok", text }, action: "View result" };
  }
  const missed = { pill: { variant: "bad" as const, text: "Missed" }, action: "View result" as const };
  if (status === "missed") return missed;
  if (nowMs >= Date.parse(exam.acceptingUntil)) return missed;
  if (nowMs >= Date.parse(exam.closesAt)) return { pill: { variant: "warn", text: "Closing…" }, action: "Resume" };
  if (status === "in_progress") return { pill: { variant: "warn", text: "In progress" }, action: "Resume" };
  return { pill: { variant: "neutral", text: "Not started" }, action: "Start" };
}

/** "Assigned by Ms. Rao · closes in 2d"; "You" when the viewer created it. */
export function examMeta(exam: ExamListItem, userId: string | undefined, nowMs: number): string {
  const who = userId !== undefined && exam.createdBy.id === userId ? "You" : exam.createdBy.name;
  return `Assigned by ${who} · ${deadlineText(exam.closesAt, nowMs)}`;
}

/** "12 submitted, 3 in progress, 5 not started, 1 declined" (declined only above 0). */
export function countsLine(c: NonNullable<ExamListItem["counts"]> | undefined): string {
  if (!c) return "";
  const parts = [`${c.submitted} submitted`, `${c.inProgress} in progress`, `${c.notStarted} not started`];
  if (c.declined > 0) parts.push(`${c.declined} declined`);
  return parts.join(", ");
}

/**
 * fs 6.1: Decline shows only on an open, not-started exam someone else assigned.
 * Self-created, started, finished, declined, closing or missed rows never offer it.
 */
export function canDecline(exam: ExamListItem, userId: string | undefined, nowMs: number): boolean {
  return (
    userId !== undefined &&
    exam.attemptStatus === "not_started" &&
    exam.createdBy.id !== userId &&
    nowMs < Date.parse(exam.closesAt)
  );
}

/** Focus candidates after a row leaves the list: the next row, then the previous one (ids in display order). */
export function focusCandidates(ids: string[], removedId: string): string[] {
  const i = ids.indexOf(removedId);
  if (i < 0) return [];
  return [ids[i + 1], ids[i - 1]].filter((x): x is string => x !== undefined);
}
