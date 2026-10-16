"use client";
import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useAttemptScreen } from "@/components/AttemptProvider";
import { closesText, metaText } from "@/lib/take";
import { DotNav } from "@/components/DotNav";
import { EmptyState } from "@/components/EmptyState";
import { OptionGroup } from "@/components/OptionCard";
import { Notice } from "@/components/Notice";
import { RevealDialog } from "@/components/RevealDialog";
import { ANSWER_LABEL, answerNoticeText, revealedAnswerText } from "@/lib/reveal";

/** Backend-spec 8.5: autosave answers are strings of up to 5,000 characters. */
const MAX_ANSWER = 5000;
const reviewHref = (id: string) => `/exams/${encodeURIComponent(id)}/review`;

export default function Page() {
  const { exam, answers, byIndex, index, setAnswer, goTo, takeFocusQuestion, peeks } = useAttemptScreen();
  const [revealFor, setRevealFor] = useState<string | null>(null); // question id the open dialog is for
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

  // Reveal (fs 6.5). Everything shown comes from the revealed answer the provider holds, never from the question.
  const control = peeks.control(q.id);
  const revealed = peeks.on ? peeks.answer(q.id) : undefined;
  const revealedText = revealed ? revealedAnswerText(q.options && q.options.length > 0 ? "mcq" : "free", q.options, revealed) : null;
  const revealedIndex = revealed && revealedText !== null && q.options && q.options.length > 0 ? revealed.correctIndex : undefined;
  const confirmReveal = async () => {
    if (revealFor === null) return;
    await peeks.reveal(revealFor);
    setRevealFor(null);
  };
  // When the dialog closes: the answer if there is one, else the button, else (it is gone or disabled) the question heading.
  const afterReveal = () => {
    const el = document.getElementById("reveal-notice") ?? document.getElementById("reveal-btn");
    return el instanceof HTMLButtonElement && el.disabled ? document.getElementById(headingId) : el ?? document.getElementById(headingId);
  };

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
              revealedIndex={revealedIndex}
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
          {control !== "none" && (
            <div className="mt-3 flex flex-wrap items-center gap-x-3">
              <button
                id="reveal-btn"
                type="button"
                className="inline-flex min-h-10 min-w-10 items-center justify-center rounded-lg px-2.5 text-[13px] font-semibold text-accent hover:bg-tint focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-50"
                disabled={control === "disabled"}
                onClick={() => setRevealFor(q.id)}
              >
                Reveal answer
              </button>
              {control === "disabled" && peeks.hint && <span className="text-[13px] text-muted">{peeks.hint}</span>}
            </div>
          )}
          {revealed && (
            <div id="reveal-notice" tabIndex={-1} className="mt-3 outline-none">
              <Notice>
                <p className="font-semibold">{ANSWER_LABEL}</p>
                {revealedText !== null && <p className="whitespace-pre-wrap break-words">{answerNoticeText(revealedText)}</p>}
              </Notice>
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
      {peeks.on && (
        <RevealDialog
          open={revealFor !== null}
          onOpenChange={(o) => { if (!o) setRevealFor(null); }}
          peeksLeft={peeks.left}
          busy={peeks.busy}
          onConfirm={() => void confirmReveal()}
          finalFocus={afterReveal}
        />
      )}
    </>
  );
}
