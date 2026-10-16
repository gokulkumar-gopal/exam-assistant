"use client";
// Left card of Create (fs 6.4): checkable list of ready documents, names only.
import { Checkbox } from "@/components/ui/checkbox";
import type { Doc } from "@/lib/types";

export function SourcesCard({
  docs,
  selected,
  onToggle,
  error,
  loadError,
  onRetry,
  disabled,
}: {
  docs: Doc[] | null;
  selected: Set<string>;
  onToggle: (id: string) => void;
  /** Inline cap error from sourceLimitError. */
  error: string | null;
  loadError?: boolean;
  onRetry?: () => void;
  /** True while a generation job runs. */
  disabled?: boolean;
}) {
  const n = selected.size;
  return (
    <section className="card p-4" aria-labelledby="sources-h">
      <h2 id="sources-h" className="h-section mb-2">Sources ({n})</h2>
      {loadError ? (
        <div role="alert" className="mb-2">
          <p className="mb-2">Couldn&apos;t load your documents.</p>
          <button type="button" className="btn-secondary" onClick={onRetry}>Retry</button>
        </div>
      ) : docs === null ? (
        <p className="text-muted" role="status">Loading…</p>
      ) : docs.length === 0 ? (
        <p className="text-[13px] text-muted">No documents yet. Upload one from Documents.</p>
      ) : (
        <ul className="flex flex-col">
          {docs.map((d) => (
            <li key={d.id}>
              <label className="flex min-h-10 cursor-pointer items-center gap-2.5 break-words py-1">
                <Checkbox checked={selected.has(d.id)} onCheckedChange={() => onToggle(d.id)} disabled={disabled} />
                <span className="min-w-0 flex-1 break-words">{d.name}</span>
              </label>
            </li>
          ))}
        </ul>
      )}
      <p className="mt-2 text-[13px] text-muted">Up to 10 files, 20 MB and 40 pages in total.</p>
      {n === 0 && docs && docs.length > 0 && (
        <p className="mt-1 text-[13px] text-muted">Pick at least one source.</p>
      )}
      {error && <p role="alert" className="mt-2 text-[13px] font-semibold text-bad">{error}</p>}
    </section>
  );
}
