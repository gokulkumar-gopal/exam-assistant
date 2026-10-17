"use client";
// Left card of Create (fs 6.4): ready documents with PDF page counts.
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { RangeSlider } from "@/components/ui/range-slider";
import {
  MAX_BYTES, MAX_PDF_PAGES, MAX_SOURCES, pdfPageTotal, rangeLength, validateRange, type PageRange,
} from "@/lib/totals";
import type { Doc } from "@/lib/types";

const MAX_MB = MAX_BYTES / (1024 * 1024);

function toNum(raw: string): number {
  return raw.trim() === "" ? NaN : Number(raw);
}

const clamp = (n: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, n));

/** Page range under a checked PDF (fs 6.4): a two-handle slider with exact number boxes and a live count. */
function RangeRow({ doc, range, onChange, disabled }: {
  doc: Doc; range: PageRange; onChange: (r: PageRange) => void; disabled?: boolean;
}) {
  const err = validateRange(range, doc.pages);
  const errId = `range-err-${doc.id}`;
  const count = rangeLength(range);
  const whole = range.from === 1 && range.to === doc.pages;
  const show = (n: number) => (Number.isFinite(n) ? String(n) : "");
  // The slider always shows a sane position, even while a number box holds something invalid.
  const lo = Number.isFinite(range.from) ? clamp(range.from, 1, doc.pages) : 1;
  const hi = Number.isFinite(range.to) ? clamp(range.to, 1, doc.pages) : doc.pages;
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
      className="h-8 w-12 px-1 text-center text-[13px]"
      value={show(value)}
      disabled={disabled}
      onChange={(e) => onChange(set(toNum(e.target.value)))}
    />
  );
  return (
    <div className="mb-2 ml-[30px] rounded-xl border border-line bg-tint/60 px-3 pb-1 pt-2.5">
      <div className="flex items-center gap-1.5 text-[13px]">
        <span className="font-semibold">Pages</span>
        {field("first page", range.from, (n) => ({ from: n, to: range.to }))}
        <span aria-hidden="true">–</span>
        {field("last page", range.to, (n) => ({ from: range.from, to: n }))}
      </div>
      <RangeSlider
        value={[Math.min(lo, hi), Math.max(lo, hi)]}
        min={1}
        max={doc.pages}
        invalid={err !== null}
        disabled={disabled}
        labels={[`${doc.name} first page slider`, `${doc.name} last page slider`]}
        onChange={([from, to]) => onChange({ from, to })}
      />
      <div className="flex items-center justify-between pb-1.5 text-xs text-muted">
        <span
          aria-live="polite"
          className="rounded-full bg-accent px-2 py-0.5 text-xs font-semibold text-white"
        >
          {count} of {doc.pages} pages
        </span>
        {whole ? (
          <span>Whole file</span>
        ) : (
          <button
            type="button"
            className="font-semibold text-accent underline-offset-2 hover:underline focus-visible:underline"
            disabled={disabled}
            onClick={() => onChange({ from: 1, to: doc.pages })}
          >
            Use all pages
          </button>
        )}
      </div>
      {err && <p id={errId} role="alert" className="pb-1.5 text-[13px] font-semibold text-bad">{err}</p>}
    </div>
  );
}

/** Pages used so far against the per-job cap, as a bar that turns red when over. */
function PageBudget({ used }: { used: number }) {
  const over = used > MAX_PDF_PAGES;
  return (
    <div className="mt-3">
      <div className="flex items-center justify-between text-[13px]">
        <span className="font-semibold">Page budget</span>
        <span className={over ? "font-semibold text-bad" : "text-muted"}>{used} / {MAX_PDF_PAGES} pages</span>
      </div>
      <div
        role="meter"
        aria-label="PDF pages used"
        aria-valuemin={0}
        aria-valuemax={MAX_PDF_PAGES}
        aria-valuenow={Math.min(used, MAX_PDF_PAGES)}
        aria-valuetext={`${used} of ${MAX_PDF_PAGES} pages`}
        className="mt-1 h-2 w-full overflow-hidden rounded-full border border-line bg-tint"
      >
        <div
          className={`h-full rounded-full transition-[width] ${over ? "bg-bad" : "bg-gradient-to-r from-accent to-accent2"}`}
          style={{ width: `${Math.min(100, (used / MAX_PDF_PAGES) * 100)}%` }}
        />
      </div>
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
  const chosenPdfs = (docs ?? []).filter((d) => selected.has(d.id) && d.type === "PDF");
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
      {chosenPdfs.length > 0 && <PageBudget used={pdfPageTotal(chosenPdfs, ranges)} />}
      <p className="mt-2 text-[13px] text-muted">Up to {MAX_SOURCES} files, {MAX_MB} MB and {MAX_PDF_PAGES} PDF pages in total.</p>
      {n === 0 && docs && docs.length > 0 && (
        <p className="mt-1 text-[13px] text-muted">Pick at least one source.</p>
      )}
      {error && <p role="alert" className="mt-2 text-[13px] font-semibold text-bad">{error}</p>}
    </section>
  );
}
