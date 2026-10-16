"use client";
import { useState } from "react";
import Link from "next/link";
import { api } from "@/lib/api";
import { useMe } from "@/lib/session";
import { useExamList, type Scope } from "@/lib/useExamList";
import type { ExamListItem } from "@/lib/types";
import { ExamRow, ExamRowSkeleton } from "@/components/ExamRow";
import { EmptyState } from "@/components/EmptyState";
import { Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import {
  AlertDialog, AlertDialogCancel, AlertDialogContent, AlertDialogDescription,
  AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from "@/components/ui/alert-dialog";

const SECTIONS: { scope: Scope; title: string; empty: string | null }[] = [
  { scope: "open", title: "Open", empty: "No open exams. Exams you create or are assigned appear here." },
  { scope: "closed", title: "Closed", empty: "Nothing closed yet." },
  { scope: "created", title: "Created by you", empty: null },
];

function Section({
  scope, title, empty, userId, onDecline, refreshKey,
}: {
  scope: Scope; title: string; empty: string | null; userId: string | undefined;
  onDecline?: (e: ExamListItem) => void; refreshKey?: { id: string; n: number };
}) {
  const { exams, error, nowMs, reload, remove } = useExamList(scope);
  // A decline confirmed elsewhere on the page removes the row here.
  const [seen, setSeen] = useState(0);
  if (refreshKey && refreshKey.n !== seen) {
    setSeen(refreshKey.n);
    remove(refreshKey.id);
  }
  const mode = scope === "created" ? "created" : "attempt";
  const rows = exams?.filter((e) => e.attemptStatus !== "declined");

  let body: React.ReactNode;
  if (error) {
    body = (
      <div>
        <Notice tone="bad">Couldn&apos;t load this list. Try again.</Notice>
        <button type="button" className="btn-secondary" onClick={() => void reload()}>Retry</button>
      </div>
    );
  } else if (!rows) {
    body = <ExamRowSkeleton />;
  } else if (rows.length === 0) {
    body = empty ? <EmptyState>{empty}</EmptyState> : (
      <EmptyState action={<Link href="/create" className="btn-secondary">Create exam</Link>}>
        You haven&apos;t created an exam yet.
      </EmptyState>
    );
  } else {
    body = (
      <ul className="flex flex-col gap-2">
        {rows.map((e) => (
          <ExamRow key={e.id} exam={e} mode={mode} userId={userId} nowMs={nowMs} onDecline={onDecline} />
        ))}
      </ul>
    );
  }
  return (
    <section aria-labelledby={`h-${scope}`} className="mb-6">
      <h2 id={`h-${scope}`} className="h-section mb-2">{title}</h2>
      {body}
    </section>
  );
}

export default function Page() {
  const { user } = useMe();
  const [declining, setDeclining] = useState<ExamListItem | null>(null);
  const [busy, setBusy] = useState(false);
  const [declineError, setDeclineError] = useState<string | null>(null);
  const [declined, setDeclined] = useState({ id: "", n: 0 });

  async function confirmDecline() {
    if (!declining) return;
    setBusy(true);
    setDeclineError(null);
    try {
      await api<void>("POST", `/exams/${encodeURIComponent(declining.id)}/decline`);
      setDeclined((d) => ({ id: declining.id, n: d.n + 1 }));
      setDeclining(null);
    } catch {
      setDeclineError("Couldn't decline this exam. Try again.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="page pt-5">
      <div className="mb-5 flex items-start justify-between gap-3">
        <div>
          <h1>Home</h1>
          <p className="text-[13px] text-muted">Your exams, nothing else.</p>
        </div>
        <Link href="/create" className="btn-primary shrink-0">Create exam</Link>
      </div>
      {SECTIONS.map((s) => (
        <Section
          key={s.scope}
          {...s}
          userId={user?.id}
          onDecline={s.scope === "open" ? setDeclining : undefined}
          refreshKey={s.scope === "open" ? declined : undefined}
        />
      ))}
      <AlertDialog open={declining !== null} onOpenChange={(o) => { if (!busy && !o) setDeclining(null); }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Decline this exam?</AlertDialogTitle>
            <AlertDialogDescription>
              It will be removed from your list. {declining?.createdBy.name} will see that you declined.
            </AlertDialogDescription>
          </AlertDialogHeader>
          {declineError && <p role="alert" className="text-sm text-bad">{declineError}</p>}
          <AlertDialogFooter>
            <AlertDialogCancel disabled={busy}>Cancel</AlertDialogCancel>
            <Button variant="destructive" disabled={busy} onClick={() => void confirmDecline()}>
              {busy ? "Declining…" : "Decline"}
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </main>
  );
}
