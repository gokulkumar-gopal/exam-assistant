"use client";
// Left card of Create (fs 6.4): ready documents with PDF page counts.
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { MAX_BYTES, MAX_PDF_PAGES, MAX_SOURCES, validateRange, type PageRange } from "@/lib/totals";
import type { Doc } from "@/lib/types";

const MAX_MB = MAX_BYTES / (1024 * 1024);

function toNum(raw: string): number {
  return raw.trim() === "" ? NaN : Number(raw);
}

/** "Pages [from] to [to] of N" under a checked PDF (fs 6.4). */
function RangeRow({ doc, range, onChange, disabled }: {
  doc: Doc; range: PageRange; onChange: (r: PageRange) => void; disabled?: boolean;
}) {
  const err = validateRange(range, doc.pages);
  const errId = `range-err-${doc.id}`;
  const show = (n: number) => (Number.isFinite(n) ? String(n) : "");
  const field = (label: string, value: number, set: (n: number) => PageRange) => (
    <Input
      type="number"
      inputMode="numeric"
      min={1}
      max={doc.pages}
      step={1}
      aria-label={`${doc.name} ${label}`}
      aria-invalid={err !== null}
      aria-describedby={err ? errId : undefined}
      className="h-9 w-20"
      value={show(value)}
      disabled={disabled}
      onChange={(e) => onChange(set(toNum(e.target.value)))}
    />
  );
  return (
    <div className="mb-1 ml-[30px]">
      <div className="flex flex-wrap items-center gap-2 text-[13px]">
        <span>Pages</span>
        {field("first page", range.from, (n) => ({ from: n, to: range.to }))}
        <span>to</span>
        {field("last page", range.to, (n) => ({ from: range.from, to: n }))}
        <span>of {doc.pages}</span>
      </div>
      {err && <p id={errId} role="alert" className="mt-1 text-[13px] font-semibold text-bad">{err}</p>}
    </div>
  );
}

export function SourcesCard({
  docs,
  selected,
  onToggle,
  ranges = {},
  onRange,
  error,
  loadError,
  onRetry,
  disabled,
}: {
  docs: Doc[] | null;
  selected: Set<string>;
  onToggle: (id: string) => void;
  /** Chosen page range per PDF id; missing means the whole file. */
  ranges?: Record<string, PageRange>;
  onRange?: (id: string, r: PageRange) => void;
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
                <span className="shrink-0 text-xs text-muted">
                  {d.type === "PDF" ? `${d.pages} PDF ${d.pages === 1 ? "page" : "pages"}` : d.type}
                </span>
              </label>
              {selected.has(d.id) && d.type === "PDF" && d.pages > 1 && onRange && (
                <RangeRow doc={d} range={ranges[d.id] ?? { from: 1, to: d.pages }} onChange={(r) => onRange(d.id, r)} disabled={disabled} />
              )}
            </li>
          ))}
        </ul>
      )}
      <p className="mt-2 text-[13px] text-muted">Up to {MAX_SOURCES} files, {MAX_MB} MB and {MAX_PDF_PAGES} PDF pages in total.</p>
      {n === 0 && docs && docs.length > 0 && (
        <p className="mt-1 text-[13px] text-muted">Pick at least one source.</p>
      )}
      {error && <p role="alert" className="mt-2 text-[13px] font-semibold text-bad">{error}</p>}
    </section>
  );
}
