"use client";
import { useState } from "react";
import Link from "next/link";
import { toast } from "sonner";
import { useMe } from "@/lib/session";
import { useExamList, type Scope } from "@/lib/useExamList";
import type { ExamListItem } from "@/lib/types";
import { ExamRow, ExamRowSkeleton } from "@/components/ExamRow";
import { DeclineDialog } from "@/components/DeclineDialog";
import { EmptyState } from "@/components/EmptyState";
import { Notice } from "@/components/Notice";

const SECTIONS: { scope: Scope; title: string; empty: string | null }[] = [
  { scope: "open", title: "Open", empty: "No open exams. Exams you create or are assigned appear here." },
  { scope: "closed", title: "Closed", empty: "Nothing closed yet." },
  { scope: "created", title: "Created by you", empty: null },
];

type ListState = ReturnType<typeof useExamList>;

function Section({
  scope, title, empty, userId, list, onDecline,
}: {
  scope: Scope; title: string; empty: string | null; userId: string | undefined;
  list: ListState; onDecline?: (e: ExamListItem) => void;
}) {
  const { exams, error, nowMs, reload } = list;
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
  const openList = useExamList("open");
  const closed = useExamList("closed");
  const created = useExamList("created");
  const lists: Record<Scope, ListState> = { open: openList, closed, created };
  // The row stays set while the dialog animates closed; only `declineOpen` flips.
  const [declining, setDeclining] = useState<ExamListItem | null>(null);
  const [declineOpen, setDeclineOpen] = useState(false);

  function startDecline(e: ExamListItem) {
    setDeclining(e);
    setDeclineOpen(true);
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
          list={lists[s.scope]}
          onDecline={s.scope === "open" ? startDecline : undefined}
        />
      ))}
      <DeclineDialog
        exam={declining}
        open={declineOpen}
        onOpenChange={setDeclineOpen}
        onDeclined={(id) => {
          openList.remove(id);
          void openList.refetch();
        }}
        onStale={(message) => {
          toast(message);
          void openList.refetch();
          void closed.refetch();
          void created.refetch();
        }}
      />
    </main>
  );
}
