"use client";
// One generated question (fs 6.4). All question and option text is rendered as React text nodes only.
import { useEffect, useRef, useState } from "react";
import { MarkBadge } from "@/components/MarkBadge";
import { validateQuestionEdit } from "@/lib/question-edit";
import type { ExamType, Question } from "@/lib/types";

const TXT_BTN =
  "inline-flex min-h-10 min-w-10 items-center justify-center rounded-lg px-2.5 text-[13px] font-semibold text-accent hover:bg-tint focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-50";
const FIELD =
  "w-full rounded-lg border border-input bg-card px-2.5 py-2 text-base outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 md:text-sm";
const LETTERS = ["A", "B", "C", "D"];

interface Props {
  index: number; // 1-based position shown to the user
  question: Question;
  type: ExamType;
  regenerating: boolean;
  /** Another regenerate or add is in flight. */
  locked: boolean;
  /** A publish is in flight or done: nothing may change. */
  frozen: boolean;
  onSave: (q: Question) => void;
  onRegenerate: () => void;
  onDelete: () => void;
}

export function QuestionCard({ index, question, type, regenerating, locked, frozen, onSave, onRegenerate, onDelete }: Props) {
  const [editing, setEditing] = useState(false);
  const [text, setText] = useState(question.text);
  const [options, setOptions] = useState<string[]>(question.options ?? []);
  const [correct, setCorrect] = useState<number | undefined>(question.correctIndex);
  const [modelAnswer, setModelAnswer] = useState<string | undefined>(question.modelAnswer);
  const [error, setError] = useState<string | null>(null);
  const editRef = useRef<HTMLButtonElement>(null);
  const refocus = useRef(false);
  const id = `q-${question.id}`;

  // Save and Cancel unmount the focused button; put focus back on Edit once it is rendered again.
  useEffect(() => {
    if (!editing && refocus.current) {
      refocus.current = false;
      editRef.current?.focus();
    }
  }, [editing]);

  const startEdit = () => {
    setModelAnswer(question.modelAnswer);
    setText(question.text);
    setOptions(question.options ?? []);
    setCorrect(question.correctIndex);
    setError(null);
    setEditing(true);
  };
  const save = () => {
    const draft = { text, options: type === "mcq" ? options : undefined, correctIndex: correct, modelAnswer: type === "free" ? modelAnswer : undefined };
    const err = validateQuestionEdit(draft, type);
    if (err) { setError(err); return; }
    const next: Question = { ...question, text: text.trim() };
    if (type === "mcq") {
      next.options = options.map((o) => o.trim());
      next.correctIndex = correct;
    }
    if (type === "free" && modelAnswer !== undefined) next.modelAnswer = modelAnswer.trim();
    onSave(next);
    refocus.current = true;
    setEditing(false);
  };

  const busy = regenerating || frozen;
  return (
    <li className="card p-4" aria-busy={regenerating || undefined}>
      <div className="mb-2 flex flex-wrap items-center gap-x-3 gap-y-1">
        <MarkBadge marks={question.marks} />
        {question.sourceDocName && <span className="text-[13px] text-muted">from {question.sourceDocName}</span>}
        {!editing && (
          <div className="ml-auto flex flex-wrap">
            <button type="button" ref={editRef} data-focus={`${question.id}:edit`} className={TXT_BTN} onClick={startEdit} disabled={busy}>Edit<span className="sr-only"> question {index}</span></button>
            <button type="button" data-focus={`${question.id}:regen`} className={TXT_BTN} onClick={onRegenerate} disabled={busy || locked}>
              {regenerating ? "Regenerating…" : "Regenerate"}<span className="sr-only"> question {index}</span>
            </button>
            <button type="button" data-focus={`${question.id}:del`} className={TXT_BTN} onClick={onDelete} disabled={busy}>Delete<span className="sr-only"> question {index}</span></button>
          </div>
        )}
      </div>
      {editing ? (
        <fieldset disabled={frozen} className="m-0 flex min-w-0 flex-col gap-3 border-0 p-0">
          <div>
            <label htmlFor={`${id}-text`} className="mb-1 block font-semibold">Question {index}</label>
            <textarea id={`${id}-text`} className={FIELD} rows={3} value={text} onChange={(e) => setText(e.target.value)} />
          </div>
          {type === "mcq" && (
            <fieldset className="m-0 min-w-0 border-0 p-0">
              <legend className="mb-1 font-semibold">Options (pick the correct one)</legend>
              <div className="flex flex-col gap-2">
                {options.map((o, i) => (
                  <div key={i} className="flex items-center gap-2">
                    <label className="flex min-h-10 min-w-10 cursor-pointer items-center justify-center">
                      <input
                        type="radio"
                        name={`${id}-correct`}
                        checked={correct === i}
                        onChange={() => setCorrect(i)}
                        className="size-5 accent-[var(--accent)]"
                        aria-label={`Option ${LETTERS[i]} is correct`}
                      />
                    </label>
                    <label htmlFor={`${id}-o${i}`} className="w-5 shrink-0 font-semibold">{LETTERS[i]}</label>
                    <input
                      id={`${id}-o${i}`}
                      className={`${FIELD} h-10 min-w-0 py-0`}
                      value={o}
                      onChange={(e) => setOptions((os) => os.map((x, j) => (j === i ? e.target.value : x)))}
                    />
                  </div>
                ))}
              </div>
            </fieldset>
          )}
          {type === "free" && modelAnswer !== undefined && (
            <div>
              <label htmlFor={`${id}-answer`} className="mb-1 block font-semibold">Model answer</label>
              <textarea id={`${id}-answer`} className={FIELD} rows={3} value={modelAnswer} onChange={(e) => setModelAnswer(e.target.value)} />
            </div>
          )}
          {error && <p role="alert" className="text-[13px] text-bad">{error}</p>}
          <div className="flex gap-2">
            <button type="button" className="btn-primary" onClick={save}>Save</button>
            <button type="button" className="btn-secondary" onClick={() => { refocus.current = true; setEditing(false); setError(null); }}>Cancel</button>
          </div>
        </fieldset>
      ) : (
        <>
          <p className="whitespace-pre-wrap break-words font-semibold">{index}. {question.text}</p>
          {type === "mcq" && question.options && (
            <ul className="mt-2 flex list-none flex-col gap-1 p-0">
              {question.options.map((o, i) => (
                <li key={i} className="flex flex-wrap gap-x-2 break-words">
                  <span className="font-semibold">{LETTERS[i]}.</span>
                  <span className="min-w-0 whitespace-pre-wrap break-words">{o}</span>
                  {question.correctIndex === i && <span className="font-semibold text-ok">✓ correct</span>}
                </li>
              ))}
            </ul>
          )}
        </>
      )}
    </li>
  );
}
