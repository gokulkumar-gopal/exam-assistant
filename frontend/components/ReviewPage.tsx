"use client";
// Review page (fs 6.5): what is still blank, jump back to a question, or submit.
import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useAttemptScreen } from "@/components/AttemptProvider";
import { DotNav } from "@/components/DotNav";
import { EmptyState } from "@/components/EmptyState";
import { Button } from "@/components/ui/button";
import {
  AlertDialog, AlertDialogCancel, AlertDialogContent, AlertDialogFooter,
  AlertDialogHeader, AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { submitDialogTitle, unansweredIndexes, unansweredLine } from "@/lib/submit";

export function ReviewPage() {
  const router = useRouter();
  const { exam, byIndex, goTo, submit, submitting, markFocusQuestion } = useAttemptScreen();
  const total = exam.questions.length;
  const takeHref = `/exams/${encodeURIComponent(exam.id)}/take`;
  const headingRef = useRef<HTMLHeadingElement>(null);
  const [confirm, setConfirm] = useState(false);
  const missing = unansweredIndexes(byIndex, total).length;

  // Arriving on a new page: focus the heading so a screen reader starts at the top of it.
  useEffect(() => { headingRef.current?.focus(); markFocusQuestion(); }, [markFocusQuestion]);

  if (total === 0) {
    return <main className="page pt-5"><EmptyState>This exam has no questions.</EmptyState></main>;
  }

  const onSubmit = () => {
    if (missing > 0) setConfirm(true);
    else void submit();
  };
  const confirmSubmit = async () => {
    const leaving = await submit();
    if (!leaving) setConfirm(false);
  };

  return (
    <main className="page pt-5">
      <div className="mx-auto max-w-[640px]">
        <h1 ref={headingRef} tabIndex={-1} className="mb-2 text-[20px] leading-snug tracking-normal focus:outline-none">Review before you submit</h1>
        <p className="mb-3 text-[15px]">{unansweredLine(byIndex, total)}</p>
        {/* No dot is outlined: this page is not on a question. A dot jumps to it. */}
        <DotNav
          answers={byIndex}
          current={-1}
          onSelect={(i) => { goTo(i); router.push(takeHref); }}
        />
        <div className="mt-6 flex items-center justify-between gap-3">
          <Link href={takeHref} className="btn-secondary">Back to questions</Link>
          <Button onClick={onSubmit} disabled={submitting} aria-busy={submitting || undefined}>
            {submitting ? "Submitting…" : "Submit exam"}
          </Button>
        </div>
      </div>
      <AlertDialog open={confirm} onOpenChange={(o) => { if (!submitting) setConfirm(o); }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{submitDialogTitle(missing)}</AlertDialogTitle>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={submitting}>Cancel</AlertDialogCancel>
            <Button disabled={submitting} aria-busy={submitting || undefined} onClick={() => void confirmSubmit()}>
              {submitting ? "Submitting…" : "Submit"}
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </main>
  );
}
