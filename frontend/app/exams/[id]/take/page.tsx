"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { api, ApiError, serverNow } from "@/lib/api";
import { useAttempt } from "@/lib/attempt";
import { createAutosaver, type AutosaveState } from "@/lib/autosave";
import { answersByIndex, clampIndex, closesText, metaText, takeRedirect } from "@/lib/take";
import type { ExamResponse } from "@/lib/types";
import { DotNav } from "@/components/DotNav";
import { EmptyState } from "@/components/EmptyState";
import { ExamBar } from "@/components/ExamBar";
import { Notice } from "@/components/Notice";
import { OptionGroup } from "@/components/OptionCard";

/** Backend-spec 8.5: autosave answers are strings of up to 5,000 characters. */
const MAX_ANSWER = 5000;
/** F-4.3 builds this page; until then the link target is fixed here in one place. */
const reviewHref = (id: string) => `/exams/${encodeURIComponent(id)}/review`;

function Loading() {
  return (
    <main className="page pt-5">
      <div role="status" aria-label="Loading" className="mx-auto max-w-[640px] animate-pulse space-y-3">
        <div className="h-10 w-2/3 rounded bg-line" />
        <div className="h-6 w-full rounded bg-line" />
        {[0, 1, 2, 3].map((i) => <div key={i} className="h-11 w-full rounded-[10px] bg-line" />)}
      </div>
    </main>
  );
}

export default function Page() {
  const params = useParams<{ id: string }>();
  const id = params.id;
  const router = useRouter();
  const [data, setData] = useState<ExamResponse | null>(null);
  const [error, setError] = useState<"gone" | "failed" | null>(null);
  const [tries, setTries] = useState(0);

  useEffect(() => {
    let alive = true;
    api<ExamResponse>("GET", `/exams/${encodeURIComponent(id)}`).then(
      (r) => { if (alive) { setData(r); setError(null); } },
      (e) => { if (alive) setError(e instanceof ApiError && (e.status === 404 || e.status === 403) ? "gone" : "failed"); },
    );
    return () => { alive = false; };
  }, [id, tries]);

  const redirect = data
    ? takeRedirect(data.attempt?.status, data.exam.acceptingUntil ?? data.acceptingUntil ?? data.exam.closesAt, serverNow())
    : null;
  useEffect(() => {
    if (redirect === "result") router.replace(`/exams/${encodeURIComponent(id)}/result`);
  }, [redirect, id, router]);

  if (error === "gone") {
    return (
      <main className="page pt-5">
        <EmptyState action={<Link href="/" className="btn-secondary">Home</Link>}>This exam isn&apos;t available.</EmptyState>
      </main>
    );
  }
  if (error) {
    return (
      <main className="page pt-5">
        <div className="mx-auto max-w-[640px]">
          <Notice tone="bad">Couldn&apos;t load the exam. Try again.</Notice>
          <button type="button" className="btn-secondary" onClick={() => { setError(null); setTries((n) => n + 1); }}>Retry</button>
        </div>
      </main>
    );
  }
  if (!data || redirect) return <Loading />;
  // Keyed by exam id so state is re-initialised from the loaded attempt (resume) on a new exam.
  return <TakeScreen key={data.exam.id} data={data} />;
}

function TakeScreen({ data }: { data: ExamResponse }) {
  const router = useRouter();
  const { exam, attempt } = data;
  const questions = exam.questions;
  const total = questions.length;
  const a = useAttempt(attempt ?? { answers: {}, lastQuestionIndex: 0 }, total);
  const index = clampIndex(a.index, total);
  const q = questions[index];
  const byIndex = answersByIndex(questions, a.answers);
  const isLast = index === total - 1;
  const nextRef = useRef<HTMLButtonElement | HTMLAnchorElement | null>(null);
  const refocusPrimary = useRef(false);
  const headingId = "take-question";
  const encodedId = encodeURIComponent(exam.id);

  // Server offset: fixed once per load (a refresh re-derives the same remaining time from closesAt).
  const [offsetMs] = useState(() => serverNow() - Date.now());
  const acceptingMs = Date.parse(exam.acceptingUntil ?? data.acceptingUntil ?? exam.closesAt);

  // Autosave (fs 6.5). The saver reads the newest answers through a ref.
  const latest = useRef({ answers: a.answers, lastQuestionIndex: index });
  useEffect(() => { latest.current = { answers: a.answers, lastQuestionIndex: index }; });
  const initialSaved = attempt?.savedAt ? Date.parse(attempt.savedAt) : NaN;
  const [save, setSave] = useState<AutosaveState>({ status: "idle", savedAt: Number.isNaN(initialSaved) ? null : initialSaved });
  const saverRef = useRef<ReturnType<typeof createAutosaver> | null>(null);
  const closedHandled = useRef(false);
  const examId = exam.id;
  const goClosed = useCallback(() => {
    if (closedHandled.current) return;
    closedHandled.current = true;
    // Autosave 409 (closed or already submitted): the last saved answers stand (fs 6.5).
    toast("This exam has closed. Your last saved answers were submitted.");
    router.replace(`/exams/${encodeURIComponent(examId)}/result`);
  }, [router, examId]);
  useEffect(() => {
    const saver = createAutosaver({
      put: async (answers, lastQuestionIndex) => {
        await api("PUT", `/exams/${encodeURIComponent(examId)}/attempt`, { answers, lastQuestionIndex }, { noRetry: true });
      },
      snapshot: () => latest.current,
      initialSavedAt: Number.isNaN(initialSaved) ? null : initialSaved,
      onState: (s) => { setSave(s); if (s.status === "closed") goClosed(); },
    });
    saverRef.current = saver;
    return () => { void saver.flush(); saver.dispose(); saverRef.current = null; };
    // initialSaved comes from props fixed for this keyed screen.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [examId, goClosed]);
  // Only a real change schedules a save (not mount, and not React's dev double-run of effects).
  const seen = useRef({ answers: a.answers, index });
  useEffect(() => {
    if (seen.current.answers === a.answers && seen.current.index === index) return;
    seen.current = { answers: a.answers, index };
    saverRef.current?.change();
  }, [a.answers, index]);

  // Past the grace window the exam is over whatever the autosave says: show the result.
  useEffect(() => {
    if (Number.isNaN(acceptingMs)) return;
    const id = setTimeout(goClosed, Math.max(0, acceptingMs - (Date.now() + offsetMs)));
    return () => clearTimeout(id);
  }, [acceptingMs, offsetMs, goClosed]);

  const [exiting, setExiting] = useState(false);
  const saveExit = async () => {
    setExiting(true);
    const r = await (saverRef.current?.flush() ?? Promise.resolve("saved" as const));
    if (r === "saved") {
      toast("Progress saved. Resume any time from Home.");
      router.push("/");
      return;
    }
    // Failed: stay on the exam (the bar shows "Not saved. Retrying…" and keeps retrying), so no answer is lost.
    setExiting(false);
    if (r === "failed") toast.error("Couldn't reach the server. Try again.");
  };

  // Next turns into the Review link on the last question (a different element): keep keyboard focus on it.
  useEffect(() => {
    if (refocusPrimary.current) { refocusPrimary.current = false; nextRef.current?.focus(); }
  }, [isLast]);

  // Keys 1-4 choose an option, but never while typing in a field.
  const options = q?.options;
  const { setAnswer } = a;
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
    return (
      <>
        <ExamBar title={exam.title} save={save} closesAtIso={exam.closesAt} offsetMs={offsetMs} exiting={exiting} onSaveExit={saveExit} />
        <main className="page pt-5"><EmptyState>This exam has no questions.</EmptyState></main>
      </>
    );
  }

  const goBack = () => {
    // Back disables on the first question: hand focus to Next so it is not dropped to the page.
    // On a 2-question exam Next/Review swap elements, so focus is placed after the swap.
    if (index - 1 === 0) {
      if (isLast) refocusPrimary.current = true;
      else nextRef.current?.focus();
    }
    a.goTo(index - 1);
  };
  const goNext = () => {
    if (index + 1 === total - 1) refocusPrimary.current = true;
    a.goTo(index + 1);
  };
  const encoded = encodedId;
  const closes = closesText(exam.closesAt);

  return (
    <>
      <ExamBar
        title={exam.title}
        save={save}
        peeksLeft={exam.maxPeeks > 0 ? attempt?.peeksLeft : undefined}
        closesAtIso={exam.closesAt}
        offsetMs={offsetMs}
        exiting={exiting}
        onSaveExit={saveExit}
      />
      <main className="page pt-5">
        <div className="mx-auto max-w-[640px]">
          <DotNav answers={byIndex} current={index} onSelect={a.goTo} />
          <p className="mb-1 text-[13px] text-muted" aria-live="polite">{metaText(index, total, q.marks)}</p>
          <h1 id={headingId} className="mb-4 whitespace-pre-wrap break-words text-[20px] leading-snug tracking-normal">{q.text}</h1>
          {q.options && q.options.length > 0 ? (
            <OptionGroup
              key={q.id}
              options={q.options}
              value={a.answers[q.id]}
              labelledBy={headingId}
              onChange={(text) => a.setAnswer(q.id, text)}
            />
          ) : (
            <div key={q.id}>
              <label htmlFor="take-answer" className="sr-only">Your answer</label>
              <textarea
                id="take-answer"
                placeholder="Type your answer"
                maxLength={MAX_ANSWER}
                value={a.answers[q.id] ?? ""}
                onChange={(e) => a.setAnswer(q.id, e.target.value)}
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
