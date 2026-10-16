"use client";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { api, ApiError } from "@/lib/api";
import type { Doc, DocsResponse } from "@/lib/types";
import {
  deleteTitle, filterAndSortDocs, generateHref, pruneSelection, type DocSort,
} from "@/lib/documents";
import { DocRow } from "@/components/DocRow";
import { EmptyState } from "@/components/EmptyState";
import { SelectionBar } from "@/components/SelectionBar";
import { Input } from "@/components/ui/input";
import {
  AlertDialog, AlertDialogCancel, AlertDialogContent, AlertDialogDescription,
  AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";

// F-1.4 SLOT: replace this handler with the upload sheet trigger (Take photo / Choose file /
// Choose from photos). F-1.3 only renders the button; there is no upload code here.
function onUpload() {}

function UploadButton() {
  return (
    <button type="button" className="btn-primary" onClick={onUpload}>
      Upload
    </button>
  );
}

export default function Page() {
  const router = useRouter();
  const [docs, setDocs] = useState<Doc[] | null>(null);
  const [loadError, setLoadError] = useState(false);
  const [query, setQuery] = useState("");
  const [sort, setSort] = useState<DocSort>("newest");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [confirming, setConfirming] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoadError(false);
    setDocs(null);
    try {
      const r = await api<DocsResponse>("GET", "/documents");
      setDocs(r.documents);
      setSelected((s) => pruneSelection(s, r.documents));
    } catch {
      setLoadError(true);
    }
  }, []);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- initial fetch on mount
    void load();
  }, [load]);

  const visible = useMemo(() => filterAndSortDocs(docs ?? [], query, sort), [docs, query, sort]);
  const ids = useMemo(() => (docs ?? []).filter((d) => selected.has(d.id)).map((d) => d.id), [docs, selected]);

  const toggle = useCallback((id: string) => {
    setSelected((s) => {
      const n = new Set(s);
      if (n.has(id)) n.delete(id);
      else n.add(id);
      return n;
    });
  }, []);

  async function confirmDelete() {
    setDeleting(true);
    setDeleteError(null);
    const targets = ids;
    const results = await Promise.allSettled(targets.map((id) => api<void>("DELETE", `/documents/${encodeURIComponent(id)}`)));
    const gone = new Set(targets.filter((_, i) => results[i].status === "fulfilled"));
    // A 404 means it is already gone; treat as deleted.
    results.forEach((r, i) => {
      if (r.status === "rejected" && r.reason instanceof ApiError && r.reason.status === 404) gone.add(targets[i]);
    });
    const failedCount = targets.filter((id) => !gone.has(id)).length;
    setDocs((d) => (d ? d.filter((x) => !gone.has(x.id)) : d));
    setSelected((s) => new Set([...s].filter((id) => !gone.has(id))));
    setDeleting(false);
    if (failedCount === 0) {
      setConfirming(false);
    } else {
      setDeleteError(
        failedCount === 1
          ? "1 document couldn't be deleted. Try again."
          : `${failedCount} documents couldn't be deleted. Try again.`,
      );
    }
  }

  function openConfirm() {
    setDeleteError(null);
    setConfirming(true);
  }

  let body: React.ReactNode;
  if (loadError) {
    body = (
      <div role="alert">
        <p className="mb-3">Couldn&apos;t load your documents. Try again.</p>
        <button type="button" className="btn-secondary" onClick={() => void load()}>Retry</button>
      </div>
    );
  } else if (docs === null) {
    body = <div className="text-muted" role="status">Loading…</div>;
  } else if (docs.length === 0) {
    body = (
      <EmptyState action={<UploadButton />}>
        No documents yet. Upload your first file to get started.
      </EmptyState>
    );
  } else {
    body = (
      <>
        <div className="mb-4 flex flex-col gap-2 sm:flex-row">
          <Input
            type="search"
            placeholder="Search documents"
            aria-label="Search documents"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className="sm:flex-1"
          />
          <select
            aria-label="Sort documents"
            value={sort}
            onChange={(e) => setSort(e.target.value as DocSort)}
            className="h-10 rounded-lg border border-input bg-card px-2.5 text-base sm:w-40 md:text-sm"
          >
            <option value="newest">Newest</option>
            <option value="name">Name</option>
          </select>
        </div>
        {visible.length === 0 ? (
          <EmptyState>No documents match your search.</EmptyState>
        ) : (
          <ul className="flex flex-col gap-2">
            {visible.map((d) => (
              <DocRow key={d.id} doc={d} selected={selected.has(d.id)} onToggle={toggle} />
            ))}
          </ul>
        )}
      </>
    );
  }

  return (
    <main className="page pt-5">
      <div className="mb-4 flex items-start justify-between gap-3">
        <div>
          <h1>Documents</h1>
          <p className="text-[13px] text-muted">Only you can see these.</p>
        </div>
        {docs && docs.length > 0 && <UploadButton />}
      </div>
      {body}
      <SelectionBar count={ids.length} onDelete={openConfirm} onGenerate={() => router.push(generateHref(ids))} />
      <AlertDialog open={confirming} onOpenChange={(o) => { if (!deleting) setConfirming(o); }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{deleteTitle(ids.length)}</AlertDialogTitle>
            <AlertDialogDescription>This can&apos;t be undone.</AlertDialogDescription>
          </AlertDialogHeader>
          {deleteError && <p role="alert" className="text-sm text-bad">{deleteError}</p>}
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleting}>Cancel</AlertDialogCancel>
            <Button variant="destructive" disabled={deleting} onClick={() => void confirmDelete()}>
              {deleting ? "Deleting…" : "Delete"}
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </main>
  );
}
