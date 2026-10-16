"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { ArrowLeft } from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { useMe } from "@/lib/session";
import { scoreCell, statusPill, statusSubtitle, submittedCell } from "@/lib/status";
import type { StatusResponse } from "@/lib/types";
import { AssignDialog } from "@/components/AssignDialog";
import { EmptyState } from "@/components/EmptyState";
import { Notice } from "@/components/Notice";
import { Pill } from "@/components/Pill";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

const TH = "h-10 bg-bg px-3 text-xs font-semibold uppercase tracking-[0.4px] text-muted";
const TD = "px-3 py-2";

function Skeleton() {
  return (
    <div role="status" aria-label="Loading" data-testid="skeleton" className="card animate-pulse space-y-3 p-4">
      <div className="h-4 w-1/3 rounded bg-line" />
      {[0, 1, 2].map((i) => <div key={i} className="h-4 w-full rounded bg-line" />)}
    </div>
  );
}

export default function Page() {
  const params = useParams<{ id: string }>();
  const id = params.id;
  const { user } = useMe();
  const [data, setData] = useState<StatusResponse | null>(null);
  const [error, setError] = useState<{ denied: boolean } | null>(null);
  const [assignOpen, setAssignOpen] = useState(false);
  const [assignKey, setAssignKey] = useState(0);
  const seqRef = useRef({ n: 0 });

  const load = useCallback(async (silent = false) => {
    const seq = seqRef.current;
    const mine = ++seq.n;
    if (!silent) { setError(null); setData(null); }
    try {
      const r = await api<StatusResponse>("GET", `/exams/${encodeURIComponent(id)}/status`);
      if (mine !== seq.n) return;
      setData(r);
      setError(null);
    } catch (e) {
      if (mine !== seq.n) return;
      // Only the creator may open this page: 403/404 look the same and say nothing more.
      if (!silent) setError({ denied: e instanceof ApiError && (e.status === 403 || e.status === 404) });
    }
  }, [id]);

  useEffect(() => {
    const seq = seqRef.current;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- initial fetch on mount
    void load();
    return () => { seq.n++; };
  }, [load]);

  function openAssign() {
    setAssignKey((k) => k + 1);
    setAssignOpen(true);
  }

  if (error?.denied) {
    return (
      <main className="page pt-5">
        <EmptyState action={<Link href="/" className="btn-secondary">Home</Link>}>
          You can&apos;t view this page.
        </EmptyState>
      </main>
    );
  }

  const encoded = encodeURIComponent(id);
  const rows = data?.attempts ?? [];
  const assignButton = (label: string) => (
    <button type="button" className="btn-secondary" onClick={openAssign}>{label}</button>
  );

  return (
    <main className="page pt-5">
      <div className="mb-3">
        <Link href="/" className="btn-secondary"><ArrowLeft size={16} aria-hidden="true" />Back</Link>
      </div>
      {error ? (
        <div>
          <Notice tone="bad">Couldn&apos;t load the status. Try again.</Notice>
          <button type="button" className="btn-secondary" onClick={() => void load()}>Retry</button>
        </div>
      ) : !data ? (
        <Skeleton />
      ) : (
        <>
          <div className="mb-4 flex flex-wrap items-start justify-between gap-3">
            <div className="min-w-0">
              <h1 className="break-words">{data.exam.title}</h1>
              <p className="text-[13px] text-muted" aria-live="polite">{statusSubtitle(data.counts)}</p>
            </div>
            <a
              href={`/exams/${encoded}/print`}
              target="_blank"
              rel="noopener noreferrer"
              className="btn-secondary shrink-0"
            >
              Print
            </a>
          </div>
          {rows.length === 0 ? (
            <EmptyState action={assignButton("Assign")}>Not assigned.</EmptyState>
          ) : (
            <>
              <div className="card overflow-hidden" data-testid="status-card">
                <Table className="min-w-[540px]">
                  <TableHeader>
                    <TableRow className="hover:bg-bg">
                      <TableHead className={TH}>Student</TableHead>
                      <TableHead className={TH}>Status</TableHead>
                      <TableHead className={TH}>Score</TableHead>
                      <TableHead className={TH}>Submitted</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {rows.map((r) => {
                      const pill = statusPill(r.status);
                      return (
                        <TableRow key={r.userId}>
                          <TableCell className={TD}>
                            <div className="font-semibold">{r.name}</div>
                            <div className="text-[13px] text-muted">{r.email}</div>
                          </TableCell>
                          <TableCell className={TD}>
                            <Pill variant={pill.variant} outline={pill.outline}>{pill.text}</Pill>
                          </TableCell>
                          <TableCell className={`${TD} tabular`}>{scoreCell(r, data.exam.totalMarks)}</TableCell>
                          <TableCell className={`${TD} tabular`}>{submittedCell(r.submittedAt)}</TableCell>
                        </TableRow>
                      );
                    })}
                  </TableBody>
                </Table>
              </div>
              <div className="mt-3">{assignButton("Assign to more people")}</div>
            </>
          )}
          <AssignDialog
            key={assignKey}
            open={assignOpen}
            onOpenChange={setAssignOpen}
            examId={id}
            role={user?.role}
            existing={rows.map((r) => r.email)}
            onAssigned={() => void load(true)}
          />
        </>
      )}
    </main>
  );
}
