"use client";
// Publish exam + Review answer key (fs 6.4). One Idempotency-Key per attempt: a retry reuses it.
import { useRef, useState, type ReactNode } from "react";
import { useRouter } from "next/navigation";
import { toast } from "sonner";
import { ApiError, errorMessageWithFields, isNetworkError, NETWORK_TOAST } from "@/lib/api";
import { clearGenerated } from "@/lib/generated";
import { buildPublishBody, keyMissing, makePublisher, type Deadline } from "@/lib/publish";
import type { ExamType, Question } from "@/lib/types";
import { AnswerKeyReview } from "@/components/AnswerKeyReview";
import { Notice } from "@/components/Notice";

export interface PublishForm {
  title: string;
  deadline: Deadline | null; // null: the date picker is chosen but empty
  assigneeEmails: string[];
  assignError: string | null;
  maxPeeks: number;
}

export function PublishBar({
  questions, type, form, marksOk, target, pending, locked, onLock, extra, onChangeQuestion, onPublished,
}: {
  questions: Question[]; // in the order shown, which is the order published
  type: ExamType;
  form: PublishForm;
  marksOk: boolean;
  target: number;
  pending: boolean;
  /** Publish in flight or done; the page locks every control while true. */
  locked: boolean;
  onLock: (locked: boolean) => void;
  extra?: ReactNode;
  onChangeQuestion: (q: Question) => void;
  onPublished: () => void;
}) {
  const router = useRouter();
  const [publisher] = useState(() => makePublisher());
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState<string[] | null>(null); // set once published: unknownEmails, shown in a Notice until Done
  const [error, setError] = useState<string | null>(null);
  const [showKey, setShowKey] = useState(false);
  const inflight = useRef(false);
  const btn = useRef<HTMLButtonElement>(null);
  const doneBtn = useRef<HTMLButtonElement>(null);

  const missing = keyMissing(questions, type);
  const hint = !marksOk ? `The marks must add up to ${target}.`
    : !form.title.trim() ? "Add a title to publish."
    : !form.deadline ? "Pick a closing date and time."
    : form.assignError ? "Fix the Assign to list to publish."
    : missing > 0 ? "Every question needs an answer in the answer key."
    : null;
  const disabled = hint !== null || pending || locked;

  const publish = async () => {
    if (inflight.current || disabled || !form.deadline) return;
    inflight.current = true;
    setBusy(true);
    onLock(true);
    setError(null);
    try {
      const body = buildPublishBody({
        title: form.title, type, deadline: form.deadline, assigneeEmails: form.assigneeEmails,
        maxPeeks: form.maxPeeks, questions,
      });
      const r = await publisher.publish(body);
      clearGenerated();
      onPublished();
      toast.success("Exam published");
      // `done` also locks the bar against a second publish while the route changes.
      setDone(r.unknownEmails ?? []);
      if (r.unknownEmails?.length) setTimeout(() => doneBtn.current?.focus(), 0);
      else router.push("/");
    } catch (e) {
      if (e instanceof ApiError && !isNetworkError(e)) setError(errorMessageWithFields(e));
      else toast.error(NETWORK_TOAST);
      onLock(false);
      setTimeout(() => btn.current?.focus(), 0);
    } finally {
      inflight.current = false;
      setBusy(false);
    }
  };

  return (
    <>
      {busy && <p role="status" className="mt-4 text-[13px] text-muted">Publishing…</p>}
      {showKey && <AnswerKeyReview questions={questions} type={type} disabled={locked} onChange={onChangeQuestion} />}
      <div aria-live="polite">
        {error && <div className="mt-4"><Notice tone="bad">{error}</Notice></div>}
        {done && done.length > 0 && (
          <div className="mt-4">
            <Notice>
              <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
                <span>We couldn&apos;t find an account for: {done.join(", ")}. They didn&apos;t get the exam.</span>
                <button ref={doneBtn} type="button" className="btn-secondary" onClick={() => router.push("/")}>Done</button>
              </span>
            </Notice>
          </div>
        )}
      </div>
      <div className="mt-4 flex flex-wrap gap-2">
        {extra}
        <button
          type="button"
          className="btn-secondary"
          aria-expanded={showKey}
          aria-controls="answer-key"
          onClick={() => setShowKey((v) => !v)}
        >
          {showKey ? "Hide answer key" : "Review answer key"}
        </button>
        <button
          ref={btn}
          type="button"
          className="btn-primary disabled:cursor-not-allowed disabled:opacity-50"
          disabled={disabled}
          aria-describedby={hint ? "publish-hint" : undefined}
          onClick={() => void publish()}
        >
          {busy ? "Publishing…" : "Publish exam"}
        </button>
      </div>
      {hint && !locked && <p id="publish-hint" className="mt-2 text-[13px] text-muted">{hint}</p>}
    </>
  );
}
