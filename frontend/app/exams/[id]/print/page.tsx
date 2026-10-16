"use client";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { api, ApiError } from "@/lib/api";
import { answerSheetAvailable, defaultOptions, printPlan, type PrintOptions } from "@/lib/print";
import type { ExamResponse } from "@/lib/types";
import { EmptyState } from "@/components/EmptyState";
import { Notice } from "@/components/Notice";
import { PrintSheet } from "@/components/PrintSheet";
import { PrintToolbar } from "@/components/PrintToolbar";
import "@/styles/print.css";

type View =
  | { kind: "loading" }
  | { kind: "ok"; data: ExamResponse }
  | { kind: "denied" }
  | { kind: "error" };

export default function Page() {
  const params = useParams<{ id: string }>();
  const encoded = encodeURIComponent(params.id);
  const [view, setView] = useState<View>({ kind: "loading" });
  const [options, setOptions] = useState<PrintOptions | null>(null);
  const seqRef = useRef({ n: 0 });

  // Read-only: GET /exams/:id never starts an attempt, and this page sends nothing else.
  const load = useCallback(async () => {
    const seq = seqRef.current;
    const mine = ++seq.n;
    setView({ kind: "loading" });
    try {
      const data = await api<ExamResponse>("GET", `/exams/${encoded}`);
      if (mine !== seq.n) return;
      setOptions(defaultOptions(data.exam, data.keyVisible));
      setView({ kind: "ok", data });
    } catch (e) {
      if (mine !== seq.n) return;
      setView(e instanceof ApiError && (e.status === 403 || e.status === 404) ? { kind: "denied" } : { kind: "error" });
    }
  }, [encoded]);

  useEffect(() => {
    const seq = seqRef.current;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- initial fetch on mount
    void load();
    return () => { seq.n++; };
  }, [load]);

  const data = view.kind === "ok" ? view.data : null;
  const title = data?.exam.title;
  useEffect(() => {
    if (!title) return;
    const before = document.title;
    document.title = title; // the browser names the saved PDF after the page title
    return () => { document.title = before; };
  }, [title]);

  // PrintSheet receives only this plan: key material is in it only when keyVisible is true.
  const plan = useMemo(
    () => (data && options ? printPlan(data.exam, options, data.keyVisible) : null),
    [data, options],
  );

  return (
    <main className="page py-5">
      {view.kind === "loading" && (
        <div role="status" aria-label="Loading" data-testid="skeleton" className="card animate-pulse space-y-3 p-4">
          <div className="h-4 w-1/3 rounded bg-line" />
          {[0, 1, 2].map((i) => <div key={i} className="h-4 w-full rounded bg-line" />)}
        </div>
      )}
      {view.kind === "denied" && (
        <EmptyState action={<Link href="/" className="btn-secondary">Home</Link>}>
          That item isn&apos;t available.
        </EmptyState>
      )}
      {view.kind === "error" && (
        <div>
          <Notice tone="bad">Couldn&apos;t load the exam. Try again.</Notice>
          <button type="button" className="btn-secondary" onClick={() => void load()}>Retry</button>
        </div>
      )}
      {data && options && plan && (
        <>
          <PrintToolbar
            title={data.exam.title}
            options={options}
            hasAnswerSheet={answerSheetAvailable(data.exam)}
            keyVisible={data.keyVisible}
            keyAvailableAt={data.exam.acceptingUntil ?? data.acceptingUntil}
            onChange={setOptions}
          />
          <div className="print-preview">
            <PrintSheet plan={plan} />
          </div>
        </>
      )}
    </main>
  );
}
