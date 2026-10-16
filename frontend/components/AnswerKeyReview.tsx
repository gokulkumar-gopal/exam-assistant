"use client";
// Inline editable answer key (fs 6.4): one answer per question, edits the same state as the question list.
import type { ExamType, Question } from "@/lib/types";

const LETTERS = ["A", "B", "C", "D"];
const FIELD =
  "w-full rounded-lg border border-input bg-card px-2.5 py-2 text-base outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 md:text-sm";

export function AnswerKeyReview({
  questions, type, disabled, onChange,
}: {
  questions: Question[];
  type: ExamType;
  disabled: boolean;
  onChange: (q: Question) => void;
}) {
  return (
    <section id="answer-key" aria-labelledby="key-h" className="card mt-4 p-4">
      <h3 id="key-h" className="h-section mb-1">Answer key</h3>
      <p className="mb-3 text-[13px] text-muted">One answer per question. Drafted from the generated answers; change any of them.</p>
      <ol className="m-0 flex list-none flex-col gap-4 p-0">
        {questions.map((q, i) => (
          <li key={q.id} className="border-t border-line pt-3 first:border-t-0 first:pt-0">
            {type === "mcq" ? (
              <fieldset disabled={disabled} className="m-0 min-w-0 border-0 p-0">
                <legend className="mb-1 font-semibold">{i + 1}. {q.text}</legend>
                {(q.options ?? []).map((o, k) => (
                  <label key={k} className="flex min-h-10 cursor-pointer items-center gap-2.5">
                    <input
                      type="radio"
                      name={`key-${q.id}`}
                      className="size-4 accent-[var(--accent)]"
                      checked={q.correctIndex === k}
                      onChange={() => onChange({ ...q, correctIndex: k })}
                    />
                    <span>{LETTERS[k]}. {o}</span>
                  </label>
                ))}
              </fieldset>
            ) : (
              <div>
                <label htmlFor={`key-${q.id}`} className="mb-1 block font-semibold">{i + 1}. {q.text}</label>
                <textarea
                  id={`key-${q.id}`}
                  rows={2}
                  disabled={disabled}
                  className={FIELD}
                  value={q.modelAnswer ?? ""}
                  onChange={(e) => onChange({ ...q, modelAnswer: e.target.value })}
                  aria-label={`Model answer for question ${i + 1}`}
                  aria-invalid={!q.modelAnswer?.trim()}
                />
                {!q.modelAnswer?.trim() && <p className="mt-1 text-[13px] text-bad">Write the model answer.</p>}
              </div>
            )}
          </li>
        ))}
      </ol>
    </section>
  );
}
