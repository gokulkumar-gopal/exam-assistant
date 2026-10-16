"use client";
import Link from "next/link";
import type { AiModel } from "@/lib/types";
import { COSTLIER_NOTICE, isCostlier, modelOptionLabel } from "@/lib/models";
import { Notice } from "@/components/Notice";

const FIELD = "h-10 w-full rounded-lg border border-input bg-card px-2.5 text-base outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 md:text-sm";

/** Body of the Model card: a labelled select, the See AI usage link and the cost notice. */
export function ModelSelect({
  models, value, onChange,
}: { models: AiModel[]; value: string | undefined; onChange: (id: string) => void }) {
  return (
    <div>
      <label htmlFor="model-select" className="sr-only">Model</label>
      <select id="model-select" className={FIELD} value={value ?? ""} onChange={(e) => onChange(e.target.value)}>
        {models.map((m) => <option key={m.id} value={m.id}>{modelOptionLabel(m)}</option>)}
      </select>
      <Link
        href="/usage"
        className="mt-2 inline-flex min-h-10 items-center text-[13px] text-muted underline underline-offset-2 hover:text-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        See AI usage
      </Link>
      {isCostlier(models, value) && <div className="mt-1"><Notice>{COSTLIER_NOTICE}</Notice></div>}
    </div>
  );
}
