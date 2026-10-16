"use client";
import { use, useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { api } from "@/lib/api";
import type { Doc, DocsResponse } from "@/lib/types";
import { EmptyState } from "@/components/EmptyState";
import { Viewer } from "@/components/Viewer";

type State = { s: "loading" } | { s: "ready"; doc: Doc } | { s: "missing" } | { s: "error" };

export default function Page({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  const [state, setState] = useState<State>({ s: "loading" });

  // There is no GET /documents/:id; name, type and pages come from the (ready-only) list.
  const load = useCallback(async () => {
    setState({ s: "loading" });
    try {
      const r = await api<DocsResponse>("GET", "/documents");
      const doc = r.documents.find((d) => d.id === id);
      setState(doc ? { s: "ready", doc } : { s: "missing" });
    } catch {
      setState({ s: "error" });
    }
  }, [id]);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- initial fetch on mount
    void load();
  }, [load]);

  return (
    <main className="page pt-5">
      {state.s === "ready" && <Viewer doc={state.doc} />}
      {state.s === "loading" && (
        <div role="status" aria-label="Loading document" className="mx-auto h-[420px] max-w-[560px] animate-pulse rounded-[10px] bg-line" />
      )}
      {state.s === "missing" && (
        <EmptyState action={<Link href="/documents" className="btn-secondary">Back</Link>}>
          That document isn&apos;t available.
        </EmptyState>
      )}
      {state.s === "error" && (
        <EmptyState
          action={
            <div className="flex gap-2">
              <button type="button" className="btn-secondary" onClick={() => void load()}>Retry</button>
              <Link href="/documents" className="btn-secondary">Back</Link>
            </div>
          }
        >
          We couldn&apos;t load this document.
        </EmptyState>
      )}
    </main>
  );
}
