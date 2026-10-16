"use client";
// Owns an exam attempt for the take and review pages (fs 6.5): loads the exam, holds the answers, runs autosave,
// the deadline submit, Save & exit and the closed handling, and draws the exam bar above whichever page is shown.
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { api, ApiError, serverNow } from "@/lib/api";
import { useAttempt } from "@/lib/attempt";
import { createAutosaver, keepaliveOk, shouldFlushOnEvent, type AutosaveState } from "@/lib/autosave";
import { autoSubmitAtZero, canSaveAndExit, submitExam, withTimeout, type SubmitPhase } from "@/lib/submit";
import { scheduleAt } from "@/lib/timer";
import { answersByIndex, clampIndex, takeDecision } from "@/lib/take";
import { applyReveal, applyRevealError, initReveal, peeksOn, revealControl, type RevealedAnswer } from "@/lib/reveal";
import type { Attempt, ExamResponse, RevealResponse } from "@/lib/types";
import { EmptyState } from "@/components/EmptyState";
import { ExamBar } from "@/components/ExamBar";
import { Notice } from "@/components/Notice";

export const CLOSED_TOAST = "This exam has closed. Your last saved answers were submitted.";
export const TIMES_UP_TOAST = "Time's up. Your answers were submitted.";
export const NOT_OPEN_TOAST = "This exam is closed";
/** A submit request that has not answered by then counts as failed (the loop retries; submit is idempotent). */
const SUBMIT_TIMEOUT_MS = 20_000;
const NETWORK_TOAST = "Couldn't reach the server. Try again.";

/** What a reveal attempt ended as: ok, not allowed right now (blocked), refused by the server (403/409), closed, or a failure. */
export type RevealOutcome = "ok" | "blocked" | "refused" | "closed" | "failed";

export interface PeeksApi {
  /** maxPeeks above 0, peeks not turned off, UI flag on: the counter and controls are shown. */
  on: boolean;
  left: number;
  /** A 409 said no peeks are left (or the key is visible). */
  noneLeft: boolean;
  control: (questionId: string) => "none" | "disabled" | "enabled";
  answer: (questionId: string) => RevealedAnswer | undefined;
  reveal: (questionId: string) => Promise<RevealOutcome>;
  /** A reveal is in flight. */
  busy: boolean;
}

export interface AttemptScreen {
  exam: ExamResponse["exam"];
  attempt: Attempt | null;
  answers: Record<string, string>;
  /** Answers in question order (what the dots and the review line read). */
  byIndex: (string | undefined)[];
  /** Current (last visited) question, clamped. */
  index: number;
  setAnswer: (id: string, value: string, opts?: { typing?: boolean }) => void;
  goTo: (index: number) => void;
  /** Manual submit. Resolves true when the app is leaving for the result page, false on failure (answers kept). */
  submit: () => Promise<boolean>;
  submitting: boolean;
  /** Set by the review page when it hands over to the take page, so the question heading takes focus. */
  markFocusQuestion: () => void;
  /** True once (and clears) when the review page asked the take page to focus its question heading. */
  takeFocusQuestion: () => boolean;
  peeks: PeeksApi;
}

const Ctx = createContext<AttemptScreen | null>(null);
export function useAttemptScreen(): AttemptScreen {
  const c = useContext(Ctx);
  if (!c) throw new Error("useAttemptScreen outside AttemptProvider");
  return c;
}

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

export function AttemptProvider({ children }: { children: React.ReactNode }) {
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

  // Decided once per load (not on re-render): what opening this exam does at this moment.
  const decision = useMemo(() => {
    if (!data) return null;
    const accepting = data.exam.acceptingUntil ?? data.acceptingUntil ?? data.exam.closesAt;
    return takeDecision(data.attempt?.status, serverNow(), Date.parse(data.exam.closesAt), Date.parse(accepting));
  }, [data]);
  const redirect = decision === "result" || decision === "home-closed" ? decision : null;
  useEffect(() => {
    if (redirect === "result") router.replace(`/exams/${encodeURIComponent(id)}/result`);
    // Never started and past closesAt (fs 6.5): not submitted blank, just Home.
    if (redirect === "home-closed") { toast(NOT_OPEN_TOAST); router.replace("/"); }
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
  return <Attempting key={data.exam.id} data={data}>{children}</Attempting>;
}

function Attempting({ data, children }: { data: ExamResponse; children: React.ReactNode }) {
  const router = useRouter();
  const { exam, attempt } = data;
  const total = exam.questions.length;
  const a = useAttempt(attempt ?? { answers: {}, lastQuestionIndex: 0 }, total);
  const index = clampIndex(a.index, total);
  const examId = exam.id;

  // Server offset: fixed once per load (a refresh re-derives the same remaining time from closesAt).
  const [offsetMs] = useState(() => serverNow() - Date.now());
  const closesMs = Date.parse(exam.closesAt);
  const acceptingMs = Date.parse(exam.acceptingUntil ?? data.acceptingUntil ?? exam.closesAt);

  // The newest answers, read by autosave and by the deadline submit.
  const latest = useRef({ answers: a.answers, lastQuestionIndex: index });
  useEffect(() => { latest.current = { answers: a.answers, lastQuestionIndex: index }; });
  const initialSaved = attempt?.savedAt ? Date.parse(attempt.savedAt) : NaN;
  const [save, setSave] = useState<AutosaveState>({ status: "idle", savedAt: Number.isNaN(initialSaved) ? null : initialSaved });
  const saverRef = useRef<ReturnType<typeof createAutosaver> | null>(null);

  // One way out of the attempt: a toast (if any), then the result page with history replaced so Back never
  // returns to the exam. Autosave is stopped first: nothing may be put after the attempt ended.
  const finished = useRef(false);
  const alive = useRef(true); // false once the take/review route was left: a late outcome must not navigate
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  const focusQuestion = useRef(false);
  const markFocusQuestion = useCallback(() => { focusQuestion.current = true; }, []);
  const takeFocusQuestion = useCallback(() => { const v = focusQuestion.current; focusQuestion.current = false; return v; }, []);
  const submitting = useRef(false); // a manual or deadline submit is running; its answer decides the outcome
  const leave = useCallback((message: string | null) => {
    if (finished.current) return;
    finished.current = true;
    saverRef.current?.dispose();
    if (!alive.current) return; // the user left (Home, Back): keep the submit, skip the hijack
    if (message) toast(message);
    router.replace(`/exams/${encodeURIComponent(examId)}/result`);
  }, [router, examId]);
  const goClosed = useCallback(() => {
    if (submitting.current) return; // a submit is in flight: wait for its own answer
    leave(CLOSED_TOAST);
  }, [leave]);

  useEffect(() => {
    const saver = createAutosaver({
      put: async (answers, lastQuestionIndex) => {
        const body = { answers, lastQuestionIndex };
        // Leaving the page: keepalive lets the request finish after unload (only if the body is small enough).
        const keepalive = document.visibilityState === "hidden" && keepaliveOk(JSON.stringify(body));
        await api("PUT", `/exams/${encodeURIComponent(examId)}/attempt`, body, { noRetry: true, keepalive });
      },
      snapshot: () => latest.current,
      initialSavedAt: Number.isNaN(initialSaved) ? null : initialSaved,
      onState: (s) => { setSave(s); if (s.status === "closed") goClosed(); },
    });
    saverRef.current = saver;
    // Closing the tab or switching away: send what is pending (closing loses at most the last keystroke).
    const onLeave = (e: Event) => { if (shouldFlushOnEvent(e.type, document.visibilityState)) void saver.flush(); };
    window.addEventListener("pagehide", onLeave);
    document.addEventListener("visibilitychange", onLeave);
    return () => {
      window.removeEventListener("pagehide", onLeave);
      document.removeEventListener("visibilitychange", onLeave);
      // One best-effort final save, unless a submit has started (it carries the answers itself).
      if (!submitting.current && !finished.current) void saver.flush();
      saver.dispose();
      saverRef.current = null;
    };
    // initialSaved comes from props fixed for this keyed screen.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [examId, goClosed]);

  // Only a real change schedules a save (not mount, and not React's dev double-run of effects).
  // Typing is debounced (500 ms); option clicks and navigation save right away (fs 6.5).
  const typing = useRef(false);
  const seen = useRef({ answers: a.answers, index });
  useEffect(() => {
    if (seen.current.answers === a.answers && seen.current.index === index) return;
    seen.current = { answers: a.answers, index };
    saverRef.current?.change({ immediate: !typing.current });
    typing.current = false;
  }, [a.answers, index]);

  const { setAnswer: rawSetAnswer } = a;
  const setAnswer = useCallback((qid: string, value: string, opts?: { typing?: boolean }) => {
    if (opts?.typing) typing.current = true;
    rawSetAnswer(qid, value);
  }, [rawSetAnswer]);

  // Deadline: at zero submit once with the in-memory answers (no autosave first). The server accepts this until
  // acceptingUntil; only a "closed" answer, or running out of grace while offline, ends in the closed handling.
  const [phase, setPhase] = useState<SubmitPhase>("idle");
  const zeroSkipped = useRef(false); // zero came while a manual submit was running
  const zero = useRef<ReturnType<typeof autoSubmitAtZero> | null>(null);
  const runZero = useCallback(async () => {
    if (finished.current) return;
    zero.current ??= autoSubmitAtZero({
      submit: (answers, lastQuestionIndex) => withTimeout(submitExam(examId, answers, lastQuestionIndex), SUBMIT_TIMEOUT_MS),
      snapshot: () => latest.current,
      stopAutosave: () => saverRef.current?.dispose(),
      canRetry: () => serverNow() < acceptingMs,
    });
    submitting.current = true;
    setPhase("submitting");
    const r = await zero.current();
    submitting.current = false;
    leave(r === "ok" ? TIMES_UP_TOAST : CLOSED_TOAST);
  }, [examId, acceptingMs, leave]);
  useEffect(() => {
    if (Number.isNaN(closesMs)) return;
    // scheduleAt re-arms for far deadlines (a single setTimeout overflows above ~24.8 days).
    return scheduleAt(closesMs, () => Date.now() + offsetMs, () => { if (submitting.current) zeroSkipped.current = true; else void runZero(); });
  }, [closesMs, offsetMs, runZero]);
  // Past the grace window the exam is over whatever else is happening.
  useEffect(() => {
    if (Number.isNaN(acceptingMs)) return;
    return scheduleAt(acceptingMs, () => Date.now() + offsetMs, goClosed);
  }, [acceptingMs, offsetMs, goClosed]);

  const [busy, setBusy] = useState(false);

  // Peeks (fs 6.5). Revealed answers come from attempt.revealed (so a reload keeps them) and from the reveal response.
  const [rv, setRv] = useState(() => initReveal(exam.maxPeeks, attempt, data.keyVisible));
  const rvRef = useRef(rv);
  useEffect(() => { rvRef.current = rv; });
  const revealing = useRef<Promise<unknown> | null>(null);
  const [revealBusy, setRevealBusy] = useState(false);
  const reveal = useCallback(async (qid: string): Promise<RevealOutcome> => {
    // Never during or after a submit, and one reveal at a time.
    if (submitting.current || finished.current || revealing.current) return "blocked";
    if (revealControl(rvRef.current, qid, false) !== "enabled") return "blocked";
    setRevealBusy(true);
    const p = api<RevealResponse>("POST", `/exams/${encodeURIComponent(examId)}/reveal`, { questionId: qid }, { noRetry: true });
    revealing.current = p;
    try {
      const r = await p;
      setRv((s) => applyReveal(s, r));
      return "ok";
    } catch (e) {
      if (e instanceof ApiError && e.status === 409 && e.code === "exam_closed") { goClosed(); return "closed"; }
      if (e instanceof ApiError && (e.status === 403 || e.status === 409)) {
        setRv((s) => applyRevealError(s, e.status));
        toast(e.message);
        return "refused";
      }
      toast.error(NETWORK_TOAST);
      return "failed";
    } finally {
      revealing.current = null;
      setRevealBusy(false);
    }
  }, [examId, goClosed]);

  const submit = useCallback(async (): Promise<boolean> => {
    if (submitting.current || finished.current) return false;
    submitting.current = true;
    setBusy(true);
    setPhase("submitting");
    try {
      // A reveal already sent finishes first, so the server has the peek before it scores the attempt.
      if (revealing.current) await revealing.current.catch(() => undefined);
      const r = await withTimeout(submitExam(examId, latest.current.answers, latest.current.lastQuestionIndex), SUBMIT_TIMEOUT_MS);
      submitting.current = false;
      leave(r === "ok" ? null : CLOSED_TOAST);
      return true; // stay busy: the page is being replaced
    } catch {
      submitting.current = false;
      setBusy(false);
      setPhase("idle");
      toast.error(NETWORK_TOAST);
      // The deadline passed while this was failing: the deadline submit takes over with the same answers.
      if (zeroSkipped.current || serverNow() >= closesMs) void runZero();
      return false;
    }
  }, [examId, leave, closesMs, runZero]);

  const [exiting, setExiting] = useState(false);
  const saveExit = async () => {
    if (submitting.current || finished.current) return; // a submit has started: nothing to save, never go Home
    setExiting(true);
    const r = await (saverRef.current?.flush() ?? Promise.resolve("saved" as const));
    if (r === "saved") {
      toast("Progress saved. Resume any time from Home.");
      router.push("/");
      return;
    }
    // Failed: stay on the exam (the bar shows "Not saved. Retrying…" and keeps retrying), so no answer is lost.
    setExiting(false);
    if (r === "failed") toast.error(NETWORK_TOAST);
  };

  const byIndex = answersByIndex(exam.questions, a.answers);
  const submitBusy = busy || phase !== "idle";
  const peeksOnNow = peeksOn(rv);
  const peeks = useMemo<PeeksApi>(() => ({
    on: peeksOnNow,
    left: rv.peeksLeft,
    noneLeft: rv.noPeeksLeft || rv.peeksLeft <= 0,
    control: (qid) => revealControl(rv, qid, submitBusy || revealBusy),
    answer: (qid) => rv.revealed[qid],
    reveal,
    busy: revealBusy,
  }), [rv, peeksOnNow, submitBusy, revealBusy, reveal]);
  const value = useMemo<AttemptScreen>(
    () => ({ exam, attempt, answers: a.answers, byIndex, index, setAnswer, goTo: a.goTo, submit, submitting: submitBusy, markFocusQuestion, takeFocusQuestion, peeks }),
    // byIndex derives from exam + answers
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [exam, attempt, a.answers, index, setAnswer, a.goTo, submit, submitBusy, markFocusQuestion, takeFocusQuestion, peeks],
  );

  return (
    <Ctx.Provider value={value}>
      <ExamBar
        title={exam.title}
        save={save}
        peeksLeft={peeksOnNow ? rv.peeksLeft : undefined}
        closesAtIso={exam.closesAt}
        offsetMs={offsetMs}
        exiting={exiting}
        exitDisabled={!canSaveAndExit(phase)}
        onSaveExit={saveExit}
      />
      {children}
    </Ctx.Provider>
  );
}
