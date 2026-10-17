"use client";
import { Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useParams, useRouter, useSearchParams } from "next/navigation";
import { toast } from "sonner";
import { ArrowLeft } from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { useForbiddenRedirect } from "@/lib/forbidden";
import { canRetakeFrom, checksForAttempt, parseAttemptParam, resultPath, retakeErrorMessage, retakeExam, setScopedCheck, type ScopedChecks } from "@/lib/attempts";
import { keyAvailableLine, peekLines, resultCardData, scoreSubtitle, selfCheckScore, type Check } from "@/lib/key";
import type { ResultResponse } from "@/lib/types";
import { AttemptsTable } from "@/components/AttemptsTable";
import { EmptyState } from "@/components/EmptyState";
import { Notice } from "@/components/Notice";
import { Pill } from "@/components/Pill";
import { ResultCard } from "@/components/ResultCard";

type View =
  | { kind: "loading" }
  | { kind: "ok"; data: ResultResponse }
  | { kind: "unfinished" }
  | { kind: "denied" }
  | { kind: "error" };

function Skeleton() {
  return (
    <div role="status" aria-label="Loading" data-testid="skeleton" className="card animate-pulse space-y-3 p-4">
      <div className="h-4 w-1/3 rounded bg-line" />
      {[0, 1, 2].map((i) => <div key={i} className="h-4 w-full rounded bg-line" />)}
    </div>
  );
}

// useSearchParams needs its own Suspense boundary.
export default function Page() {
  return (
    <Suspense fallback={<main className="page pt-5"><h1>Result</h1></main>}>
      <ResultPage />
    </Suspense>
  );
}

function ResultPage() {
  const params = useParams<{ id: string }>();
  const id = params.id;
  const encoded = encodeURIComponent(id);
  const router = useRouter();
  const attemptParam = parseAttemptParam(useSearchParams().get("attempt"));
  const [view, setView] = useState<View>({ kind: "loading" });
  // Self-check choices are local only (fs 6.7, bs 9.3) and belong to one attempt: another attempt starts clean.
  const [scoped, setScoped] = useState<ScopedChecks>({ attemptNo: undefined, checks: {} });
  const [retaking, setRetaking] = useState(false);
  const onForbidden = useForbiddenRedirect();
  const heading = useRef<HTMLHeadingElement>(null);
  const seqRef = useRef({ n: 0 });

  const load = useCallback(async () => {
    const seq = seqRef.current;
    const mine = ++seq.n;
    setView({ kind: "loading" });
    setScoped({ attemptNo: undefined, checks: {} }); // a different attempt (or a retry) starts with no self-check marks
    try {
      const data = await api<ResultResponse>("GET", resultPath(encoded, attemptParam));
      if (mine === seq.n) setView({ kind: "ok", data });
    } catch (e) {
      if (mine !== seq.n) return;
      if (onForbidden(e)) return;
      if (e instanceof ApiError && e.status === 409) setView({ kind: "unfinished" });
      else if (e instanceof ApiError && e.status === 404) setView({ kind: "denied" });
      else setView({ kind: "error" });
    }
  }, [encoded, attemptParam, onForbidden]);

  useEffect(() => {
    const seq = seqRef.current;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- initial fetch on mount
    void load();
    return () => { seq.n++; };
  }, [load]);

  // The h1 takes focus when the page content arrives (also for the 409 and error states).
  useEffect(() => {
    if (view.kind !== "loading") heading.current?.focus();
  }, [view.kind]);

  const data = view.kind === "ok" ? view.data : null;
  const checks = checksForAttempt(scoped, data?.attemptNo);
  const attempts = data?.attempts ?? [];
  const canRetake = data ? canRetakeFrom(attempts, data.exam.acceptingUntil, Date.parse(data.serverTime)) : false;

  async function retake() {
    if (retaking) return;
    setRetaking(true);
    try {
      await retakeExam(id);
      router.push(`/exams/${encoded}/take`);
    } catch (e) {
      if (onForbidden(e)) return;
      toast(retakeErrorMessage(e));
      setRetaking(false);
    }
  }
  const cards = useMemo(
    () => (data ? data.questions.map((q, i) => {
      const card = resultCardData(q, data.keyVisible, i + 1, data.exam.type);
      // A missed attempt has nothing to self-check.
      return { id: q.id, marks: q.marks, revealed: q.revealed, card: data.status === "missed" ? { ...card, canSelfCheck: false } : card };
    }) : []),
    [data],
  );
  const score = data
    ? selfCheckScore({
        base: data.score,
        maxScore: data.maxScore,
        questions: cards.filter((c) => c.card.canSelfCheck),
        checks,
      })
    : null;
  const needsCheck = cards.some((c) => c.card.canSelfCheck);
  // (P1) Peek lines. The score without the penalty reuses the self-check rule with nothing counted as revealed.
  const peeks = data
    ? peekLines({
        maxPeeks: data.exam.maxPeeks,
        peeksUsed: data.peeksUsed,
        maxScore: data.maxScore,
        scoreBeforePeeks: selfCheckScore({
          base: data.scoreBeforePeeks,
          maxScore: data.maxScore,
          questions: cards.filter((c) => c.card.canSelfCheck).map((c) => ({ id: c.id, marks: c.marks, revealed: false })),
          checks,
        }),
        submitted: data.status === "submitted",
      })
    : null;

  return (
    <main className="page pt-5">
      <div className="mb-3">
        <Link href="/" className="btn-secondary"><ArrowLeft size={16} aria-hidden="true" />Back</Link>
      </div>
      <div className="mb-4 flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h1 ref={heading} tabIndex={-1} className="break-words outline-none">
            {data ? `${data.exam.title}: result${data.attempts && data.attempts.length > 1 && data.attemptNo ? `, attempt ${data.attemptNo}` : ""}` : "Result"}
          </h1>
          {data && (
            <div className="text-[13px] text-muted" aria-live="polite">
              {data.status === "missed" ? (
                <p>You didn&apos;t submit this exam.</p>
              ) : score !== null ? (
                <p>{scoreSubtitle(score, data.maxScore)}</p>
              ) : needsCheck ? (
                <p>Check each answer to see your score.</p>
              ) : null}
              {peeks?.withoutPenalty && <p>{peeks.withoutPenalty}</p>}
              {peeks?.used && <p>{peeks.used}</p>}
              {peeks?.noPeeks && <p className="mt-1"><Pill variant="ok">No peeks</Pill></p>}
            </div>
          )}
          {data && !data.keyVisible && (
            <p className="text-[13px] text-muted">{keyAvailableLine(data.keyAvailableAt)}</p>
          )}
        </div>
        {(view.kind === "ok" || view.kind === "unfinished") && (
          <a
            href={`/exams/${encoded}/print`}
            target="_blank"
            rel="noopener noreferrer"
            className="btn-secondary shrink-0"
          >
            Print
          </a>
        )}
      </div>
      {view.kind === "loading" && <Skeleton />}
      {view.kind === "unfinished" && (
        <EmptyState action={<Link href={`/exams/${encoded}/take`} className="btn-secondary">Back to the exam</Link>}>
          Submit the exam to see results.
        </EmptyState>
      )}
      {view.kind === "denied" && (
        <EmptyState action={<Link href="/" className="btn-secondary">Home</Link>}>
          That item isn&apos;t available.
        </EmptyState>
      )}
      {view.kind === "error" && (
        <div>
          <Notice tone="bad">Couldn&apos;t load the result. Try again.</Notice>
          <button type="button" className="btn-secondary" onClick={() => void load()}>Retry</button>
        </div>
      )}
      {data && (
        <AttemptsTable
          attempts={attempts}
          selected={data.attemptNo}
          bestAttemptNo={data.bestAttemptNo}
          onSelect={(n) => router.push(`/exams/${encoded}/result?attempt=${n}`)}
          onRetake={canRetake ? () => void retake() : undefined}
          retaking={retaking}
        />
      )}
      {data && (
        <div className="space-y-3">
          {cards.map((c) => (
            <ResultCard
              key={c.id}
              card={c.card}
              marks={c.marks}
              check={checks[c.id]}
              onCheck={c.card.canSelfCheck ? (v: Check) => setScoped((prev) => setScopedCheck(prev, data.attemptNo, c.id, v)) : undefined}
            />
          ))}
        </div>
      )}
    </main>
  );
}
