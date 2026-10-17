// Status page: one student's attempts, marks and times only. The API sends no answers, so none can be shown (fs 6.6).
import { attemptScore, attemptTime } from "@/lib/attempts";
import type { AttemptHistoryItem } from "@/lib/types";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

const TH = "h-10 bg-bg px-3 text-xs font-semibold uppercase tracking-[0.4px] text-muted";
const STATE: Record<AttemptHistoryItem["status"], string> = {
  submitted: "Submitted", in_progress: "In progress", not_started: "Not started", declined: "Declined",
};

export function AttemptHistory({ name, history, totalMarks }: { name: string; history: readonly AttemptHistoryItem[]; totalMarks: number }) {
  return (
    <Table className="min-w-[380px]" aria-label={`Attempts for ${name}`}>
      <TableHeader>
        <TableRow className="hover:bg-bg">
          <TableHead scope="col" className={TH}>Attempt</TableHead>
          <TableHead scope="col" className={TH}>Score</TableHead>
          <TableHead scope="col" className={TH}>Submitted</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {history.map((h) => (
          <TableRow key={h.attemptNo}>
            <TableCell className="px-3 py-2 tabular">{h.attemptNo}</TableCell>
            <TableCell className="px-3 py-2 tabular">{h.status === "submitted" ? attemptScore(h, totalMarks) : STATE[h.status]}</TableCell>
            <TableCell className="px-3 py-2 tabular">{attemptTime(h.submittedAt)}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
