"use client";
// Generated question list (fs 6.4): edit, regenerate, delete, add, shuffle. Questions render as text only.
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { ApiError } from "@/lib/api";
import { firstUsable, gapNotice, orderQuestions } from "@/lib/exam-order";
import { buildRegenerateBody, GAVE_UP_MESSAGE, pollJob, startRegenerate } from "@/lib/generate";
import { setGenerated, type GeneratedResult } from "@/lib/generated";
import { footerState } from "@/lib/totals";
import type { Marks, Question } from "@/lib/types";
import { Notice } from "@/components/Notice";
import { QuestionCard } from "@/components/QuestionCard";
import { PublishBar, type PublishForm } from "@/components/PublishBar";
import { cn } from "@/lib/utils";

const TONE = { accent: "text-accent", ok: "text-ok", bad: "text-bad" } as const;

interface Props {
  result: GeneratedResult;
  shuffle: boolean;
  form: PublishForm;
  /** A publish is in flight or done (owned by the page, which also locks the form). */
  locked: boolean;
  onLock: (locked: boolean) => void;
  onCount: (n: number) => void;
  onRegenerateAll: () => void;
}

export function QuestionList({ result, shuffle, form, locked, onLock, onCount, onRegenerateAll }: Props) {
  const { params, model } = result;
  const [questions, setQuestions] = useState<Question[]>(result.questions);
  // Held in state so the shuffled order is stable across renders.
  const [seed] = useState(() => Math.floor(Math.random() * 2 ** 31));
  const [pending, setPending] = useState<string | null>(null); // question id, or "add"
  const [error, setError] = useState<string | null>(null);
  const questionsRef = useRef(questions);
  const ctrls = useRef<Set<AbortController>>(new Set());
  const alive = useRef(true);
  const headingRef = useRef<HTMLHeadingElement>(null);
  const focusAfter = useRef<string[] | null>(null); // data-focus candidates, best first; the heading is the final fallback
  const [say, setSay] = useState({ n: 0, text: "" });
  /** Focus may move after an async action only if the user has not moved it elsewhere meanwhile. */
  const focusIsFree = (started: Element | null) => {
    const a = document.activeElement;
    return !a || a === document.body || a === started || !a.isConnected;
  };
  const announce = (text: string) => setSay((s) => ({ n: s.n + 1, text }));

  const update = useCallback((next: Question[]) => {
    questionsRef.current = next;
    setQuestions(next);
    setGenerated({ ...result, questions: next });
  }, [result]);

  // Cards unmount when deleted or replaced; move focus to a named control after the commit.
  useLayoutEffect(() => {
    const targets = focusAfter.current;
    if (!targets) return;
    focusAfter.current = null;
    const find = (k: string) => document.querySelector<HTMLButtonElement>(`[data-focus="${CSS.escape(k)}"]`);
    const el = firstUsable(targets.map(find), (e) => e !== null && !e.disabled);
    el?.focus();
    // Still not focused (or nothing usable): the list heading.
    if (!el || document.activeElement !== el) headingRef.current?.focus();
  }, [questions]);

  useEffect(() => { onCount(questions.length); }, [questions.length, onCount]);

  // Unsaved changes: warn while generated and unpublished. This must not cancel jobs.
  const published = useRef(false);
  useEffect(() => {
    const warn = (e: BeforeUnloadEvent) => { if (published.current) return; e.preventDefault(); e.returnValue = ""; };
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
    const started = document.activeElement;
    const nq = await request(q.marks);
    if (!alive.current) return;
    setPending(null);
    if (!nq) return;
    const cur = questionsRef.current;
    // The card may have been deleted while the job ran.
    if (cur.some((x) => x.id === q.id)) {
      if (focusIsFree(started)) focusAfter.current = [`${nq.id}:regen`, `${nq.id}:edit`];
      update(cur.map((x) => (x.id === q.id ? nq : x)));
      announce("Question replaced.");
    }
  };

  const add = async (k: number) => {
    if (pending) return;
    setPending("add");
    setError(null);
    const started = document.activeElement;
    const nq = await request(k as Marks);
    if (!alive.current) return;
    setPending(null);
    if (nq) {
      if (focusIsFree(started)) focusAfter.current = [`${nq.id}:edit`];
      update([...questionsRef.current, nq]);
      announce("Question added.");
    }
  };

  const total = questions.reduce((s, q) => s + q.marks, 0);
  const gap = gapNotice(params.target, total);
  const fs = footerState(total, params.target, questions.length, true);
  const shown = orderQuestions(questions, shuffle, seed);

  return (
    <section aria-labelledby="gen-h" className="flex flex-col">
      <h2 id="gen-h" ref={headingRef} tabIndex={-1} className="h-section mb-2 outline-none focus-visible:outline-2 focus-visible:outline-accent">Generated questions · {total} marks</h2>
      <p className={cn("mb-3 font-semibold tabular-nums", TONE[fs.tone])}>
        {total} / {params.target} marks · {questions.length} questions
      </p>
      <div role="status" aria-live="polite">
        <span key={say.n} className="sr-only">{say.text}</span>
        {gap && (
          <Notice>
            <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <span>You deleted {params.target - total} marks. Add a {gap.k}-mark question?</span>
              <button type="button" className="btn-secondary" onClick={() => void add(gap.k)} disabled={pending !== null || locked}>
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
            frozen={locked}
            onSave={(nq) => { update(questionsRef.current.map((x) => (x.id === nq.id ? nq : x))); announce("Question saved."); }}
            onRegenerate={() => void regenerate(q)}
            onDelete={() => {
              const near = [shown[i + 1], shown[i - 1]].filter(Boolean).flatMap((n) => [`${n.id}:edit`, `${n.id}:regen`, `${n.id}:del`]);
              focusAfter.current = near;
              update(questionsRef.current.filter((x) => x.id !== q.id));
              announce("Question deleted.");
            }}
          />
        ))}
      </ol>
      <PublishBar
        questions={shown}
        type={params.type}
        form={form}
        marksOk={fs.generateEnabled}
        target={params.target}
        pending={pending !== null}
        locked={locked}
        onLock={onLock}
        onChangeQuestion={(nq) => update(questionsRef.current.map((x) => (x.id === nq.id ? nq : x)))}
        onPublished={() => { published.current = true; }}
        extra={<button type="button" className="btn-secondary" onClick={onRegenerateAll} disabled={pending !== null || locked}>Regenerate all</button>}
      />
    </section>
  );
}
