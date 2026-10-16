"use client";
// Generated question list (fs 6.4): edit, regenerate, delete, add, shuffle. Questions render as text only.
import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError } from "@/lib/api";
import { gapNotice, orderQuestions } from "@/lib/exam-order";
import { buildRegenerateBody, GAVE_UP_MESSAGE, pollJob, startRegenerate } from "@/lib/generate";
import { setGenerated, type GeneratedResult } from "@/lib/generated";
import { footerState } from "@/lib/totals";
import type { Marks, Question } from "@/lib/types";
import { Notice } from "@/components/Notice";
import { QuestionCard } from "@/components/QuestionCard";
import { cn } from "@/lib/utils";

const TONE = { accent: "text-accent", ok: "text-ok", bad: "text-bad" } as const;

interface Props {
  result: GeneratedResult;
  shuffle: boolean;
  /** Publish is wired in F-2.5; until then the button only enables and disables. */
  title: string;
  onRegenerateAll: () => void;
  onPublish?: () => void;
}

export function QuestionList({ result, shuffle, title, onRegenerateAll, onPublish }: Props) {
  const { params, model } = result;
  const [questions, setQuestions] = useState<Question[]>(result.questions);
  // Held in state so the shuffled order is stable across renders.
  const [seed] = useState(() => Math.floor(Math.random() * 2 ** 31));
  const [pending, setPending] = useState<string | null>(null); // question id, or "add"
  const [error, setError] = useState<string | null>(null);
  const questionsRef = useRef(questions);
  const ctrls = useRef<Set<AbortController>>(new Set());
  const alive = useRef(true);

  const update = useCallback((next: Question[]) => {
    questionsRef.current = next;
    setQuestions(next);
    setGenerated({ ...result, questions: next });
  }, [result]);

  // Unsaved changes: warn while generated and unpublished. This must not cancel jobs.
  useEffect(() => {
    const warn = (e: BeforeUnloadEvent) => { e.preventDefault(); e.returnValue = ""; };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, []);

  // Regenerate jobs stop when the page goes away (pagehide only) or the list unmounts.
  useEffect(() => {
    alive.current = true;
    const set = ctrls.current;
    const stop = () => set.forEach((c) => c.abort());
    window.addEventListener("pagehide", stop);
    return () => {
      alive.current = false;
      window.removeEventListener("pagehide", stop);
      stop();
    };
  }, []);

  /** One new question of `marks` via POST /generate/regenerate; resolves to it or null after setting the error. */
  const request = useCallback(async (marks: Marks): Promise<Question | null> => {
    const c = new AbortController();
    ctrls.current.add(c);
    try {
      const body = buildRegenerateBody(questionsRef.current, marks, params, model);
      const { jobId } = await startRegenerate(body);
      const r = await pollJob(jobId, { signal: c.signal });
      if (!alive.current) return null;
      if (r.status === "done" && r.questions[0]) return r.questions[0];
      if (r.status === "failed") setError(r.error);
      else if (r.status === "gaveUp") setError(GAVE_UP_MESSAGE);
      else if (r.status === "done") setError("Something went wrong. Try again.");
      return null;
    } catch (e) {
      if (alive.current) setError(e instanceof ApiError ? e.message : "Something went wrong. Try again.");
      return null;
    } finally {
      ctrls.current.delete(c);
    }
  }, [params, model]);

  const regenerate = async (q: Question) => {
    if (pending) return;
    setPending(q.id);
    setError(null);
    const nq = await request(q.marks);
    if (!alive.current) return;
    setPending(null);
    if (!nq) return;
    const cur = questionsRef.current;
    // The card may have been deleted while the job ran.
    if (cur.some((x) => x.id === q.id)) update(cur.map((x) => (x.id === q.id ? nq : x)));
  };

  const add = async (k: number) => {
    if (pending) return;
    setPending("add");
    setError(null);
    const nq = await request(k as Marks);
    if (!alive.current) return;
    setPending(null);
    if (nq) update([...questionsRef.current, nq]);
  };

  const total = questions.reduce((s, q) => s + q.marks, 0);
  const gap = gapNotice(params.target, total);
  const fs = footerState(total, params.target, questions.length, true);
  const shown = orderQuestions(questions, shuffle, seed);
  const publishDisabled = !fs.generateEnabled || !title.trim() || pending !== null;

  return (
    <section aria-labelledby="gen-h" className="flex flex-col">
      <h2 id="gen-h" className="h-section mb-2">Generated questions · {total} marks</h2>
      <p className={cn("mb-3 font-semibold tabular-nums", TONE[fs.tone])}>
        {total} / {params.target} marks · {questions.length} questions
      </p>
      <div role="status" aria-live="polite">
        {gap && (
          <Notice>
            <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <span>You deleted {params.target - total} marks. Add a {gap.k}-mark question?</span>
              <button type="button" className="btn-secondary" onClick={() => void add(gap.k)} disabled={pending !== null}>
                {pending === "add" ? "Adding…" : "Add"}
              </button>
            </span>
          </Notice>
        )}
      </div>
      {error && <Notice tone="bad">{error}</Notice>}
      <ol className="m-0 flex list-none flex-col gap-3 p-0">
        {shown.map((q, i) => (
          <QuestionCard
            key={q.id}
            index={i + 1}
            question={q}
            type={params.type}
            regenerating={pending === q.id}
            locked={pending !== null}
            onSave={(nq) => update(questionsRef.current.map((x) => (x.id === nq.id ? nq : x)))}
            onRegenerate={() => void regenerate(q)}
            onDelete={() => update(questionsRef.current.filter((x) => x.id !== q.id))}
          />
        ))}
      </ol>
      <div className="mt-4 flex flex-wrap gap-2">
        <button type="button" className="btn-secondary" onClick={onRegenerateAll} disabled={pending !== null}>Regenerate all</button>
        <button type="button" className="btn-primary disabled:cursor-not-allowed disabled:opacity-50" disabled={publishDisabled} onClick={onPublish}>
          Publish exam
        </button>
      </div>
    </section>
  );
}
