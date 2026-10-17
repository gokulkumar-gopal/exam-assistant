"use client";
// Result page, "Attempts" (fs 6.7): every attempt with its marks and time, the best one marked, a View button per
// attempt and a Retake button while one is allowed. Marks only; correctness stays behind the result's key rule.
import { attemptScore, attemptTime } from "@/lib/attempts";
import type { AttemptSummary } from "@/lib/types";
import { Pill } from "@/components/Pill";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

const TH = "h-10 bg-bg px-3 text-xs font-semibold uppercase tracking-[0.4px] text-muted";
const TD = "px-3 py-2";

export interface AttemptsTableProps {
  attempts: readonly AttemptSummary[];
  /** The attempt on screen. */
  selected: number | undefined;
  bestAttemptNo: number | undefined;
  onSelect: (attemptNo: number) => void;
  /** Present only while a retake is allowed. */
  onRetake?: () => void;
  retaking?: boolean;
}

export function AttemptsTable({ attempts, selected, bestAttemptNo, onSelect, onRetake, retaking }: AttemptsTableProps) {
  const showTable = attempts.length > 1;
  if (!showTable && !onRetake) return null;
  return (
    <section aria-labelledby="attempts-heading" className="mb-4" data-testid="attempts">
      <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
        {showTable ? <h2 id="attempts-heading" className="h-section">Attempts</h2> : <h2 id="attempts-heading" className="sr-only">Attempts</h2>}
        {onRetake && (
          <button type="button" className="btn-primary" onClick={onRetake} disabled={retaking} aria-busy={retaking || undefined}>
            Retake
          </button>
        )}
      </div>
      {showTable && (
        <div className="card overflow-hidden">
          <Table className="min-w-[420px]">
            <TableHeader>
              <TableRow className="hover:bg-bg">
                <TableHead scope="col" className={TH}>Attempt</TableHead>
                <TableHead scope="col" className={TH}>Score</TableHead>
                <TableHead scope="col" className={TH}>Submitted</TableHead>
                <TableHead scope="col" className={TH}><span className="sr-only">View</span></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {attempts.map((a) => {
                const isSelected = a.attemptNo === selected;
                const viewable = a.status === "submitted";
                return (
                  <TableRow key={a.attemptNo} data-selected={isSelected || undefined}>
                    <TableCell className={`${TD} tabular`}>
                      <span className="mr-2 font-semibold">{a.attemptNo}</span>
                      {a.attemptNo === bestAttemptNo && <Pill variant="ok">Best</Pill>}
                    </TableCell>
                    <TableCell className={`${TD} tabular`}>{viewable ? attemptScore(a, a.maxScore) : a.status === "in_progress" ? "In progress" : "—"}</TableCell>
                    <TableCell className={`${TD} tabular`}>{attemptTime(a.submittedAt)}</TableCell>
                    <TableCell className={`${TD} text-right`}>
                      <button
                        type="button"
                        className="btn-secondary"
                        aria-label={`View attempt ${a.attemptNo}`}
                        aria-current={isSelected ? "true" : undefined}
                        disabled={!viewable}
                        onClick={() => onSelect(a.attemptNo)}
                      >
                        View
                      </button>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </div>
      )}
    </section>
  );
}
