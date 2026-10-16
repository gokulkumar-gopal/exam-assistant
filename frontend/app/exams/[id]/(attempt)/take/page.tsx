"use client";
import { useEffect, useRef } from "react";
import Link from "next/link";
import { useAttemptScreen } from "@/components/AttemptProvider";
import { closesText, metaText } from "@/lib/take";
import { DotNav } from "@/components/DotNav";
import { EmptyState } from "@/components/EmptyState";
import { OptionGroup } from "@/components/OptionCard";

/** Backend-spec 8.5: autosave answers are strings of up to 5,000 characters. */
const MAX_ANSWER = 5000;
const reviewHref = (id: string) => `/exams/${encodeURIComponent(id)}/review`;

export default function Page() {
  const { exam, answers, byIndex, index, setAnswer, goTo, takeFocusQuestion } = useAttemptScreen();
  const questions = exam.questions;
  const total = questions.length;
  const q = questions[index];
  const isLast = index === total - 1;
  const nextRef = useRef<HTMLButtonElement | HTMLAnchorElement | null>(null);
  const refocusPrimary = useRef(false);
  const headingId = "take-question";
  const encoded = encodeURIComponent(exam.id);

  // Arriving from the review page (Back to questions or a dot): put focus on the question heading, never the body.
  useEffect(() => {
    if (takeFocusQuestion()) document.getElementById(headingId)?.focus();
  }, [takeFocusQuestion]);

  // Next turns into the Review link on the last question (a different element): keep keyboard focus on it.
  useEffect(() => {
    if (refocusPrimary.current) { refocusPrimary.current = false; nextRef.current?.focus(); }
  }, [isLast]);

  // Keys 1-4 choose an option, but never while typing in a field.
  const options = q?.options;
  const qid = q?.id;
  useEffect(() => {
    if (!options || !qid) return;
    function onKey(e: KeyboardEvent) {
      if (e.ctrlKey || e.metaKey || e.altKey) return;
      const t = e.target as HTMLElement | null;
      if (t && t.closest("input, textarea, select, [contenteditable='true']") !== null) return;
      const n = Number(e.key);
      if (Number.isInteger(n) && n >= 1 && n <= (options ?? []).length) {
        setAnswer(qid as string, (options as string[])[n - 1]);
        // A focused option keeps focus with the selection (roving tabindex), like the arrow keys.
        if (t && t.getAttribute("role") === "radio") {
          e.preventDefault();
          t.closest("[role='radiogroup']")?.querySelectorAll<HTMLElement>("[role='radio']")[n - 1]?.focus();
        }
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [options, qid, setAnswer]);

  if (!q) {
    return <main className="page pt-5"><EmptyState>This exam has no questions.</EmptyState></main>;
  }

  const goBack = () => {
    // Back disables on the first question: hand focus to Next so it is not dropped to the page.
    // On a 2-question exam Next/Review swap elements, so focus is placed after the swap.
    if (index - 1 === 0) {
      if (isLast) refocusPrimary.current = true;
      else nextRef.current?.focus();
    }
    goTo(index - 1);
  };
  const goNext = () => {
    if (index + 1 === total - 1) refocusPrimary.current = true;
    goTo(index + 1);
  };
  const closes = closesText(exam.closesAt);

  return (
    <>
      <main className="page pt-5">
        <div className="mx-auto max-w-[640px]">
          <DotNav answers={byIndex} current={index} onSelect={goTo} />
          <p className="mb-1 text-[13px] text-muted" aria-live="polite">{metaText(index, total, q.marks)}</p>
          <h1 id={headingId} tabIndex={-1} className="mb-4 focus:outline-none whitespace-pre-wrap break-words text-[20px] leading-snug tracking-normal">{q.text}</h1>
          {q.options && q.options.length > 0 ? (
            <OptionGroup
              key={q.id}
              options={q.options}
              value={answers[q.id]}
              labelledBy={headingId}
              onChange={(text) => setAnswer(q.id, text)}
            />
          ) : (
            <div key={q.id}>
              <label htmlFor="take-answer" className="sr-only">Your answer</label>
              <textarea
                id="take-answer"
                placeholder="Type your answer"
                maxLength={MAX_ANSWER}
                value={answers[q.id] ?? ""}
                onChange={(e) => setAnswer(q.id, e.target.value, { typing: true })}
                className="min-h-[120px] w-full resize-y rounded-[10px] border border-line bg-card px-3.5 py-2.5 text-[15px]"
              />
            </div>
          )}
          <div className="mt-6 flex items-center justify-between gap-3">
            <button type="button" className="btn-secondary" disabled={index === 0} onClick={goBack}>Back</button>
            {isLast ? (
              <Link ref={(el) => { nextRef.current = el; }} href={reviewHref(exam.id)} className="btn-primary">Review</Link>
            ) : (
              <button ref={(el) => { nextRef.current = el; }} type="button" className="btn-primary" onClick={goNext}>Next</button>
            )}
          </div>
          <p className="mt-4 text-[13px] text-muted">{closes}</p>
          <a
            href={`/exams/${encoded}/print`}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex min-h-10 items-center text-[13px] text-accent underline"
          >
            Print this exam
          </a>
        </div>
      </main>
    </>
  );
}
