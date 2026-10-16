"use client";
import Link from "next/link";
import { Checkbox } from "@/components/ui/checkbox";
import { FileTile } from "@/components/FileTile";
import { docMeta } from "@/lib/documents";
import type { Doc } from "@/lib/types";

/** One library row. The name is rendered as a text node only (never HTML). */
export function DocRow({
  doc,
  selected,
  disabled,
  onToggle,
}: {
  doc: Doc;
  selected: boolean;
  disabled?: boolean;
  onToggle: (id: string) => void;
}) {
  return (
    <li className="card flex items-center gap-3 px-3 py-2.5">
      {/* The label is the 40 px hit target around the 16 px box. */}
      <label className="-ml-2 flex size-10 shrink-0 cursor-pointer items-center justify-center">
        <Checkbox
          checked={selected}
          disabled={disabled}
          onCheckedChange={() => onToggle(doc.id)}
          aria-label={`Select ${doc.name}`}
        />
      </label>
      <FileTile type={doc.type.toLowerCase() as "pdf" | "jpg" | "png" | "txt"} />
      <div className="min-w-0 flex-1">
        <Link
          href={`/documents/${encodeURIComponent(doc.id)}`}
          className="inline-flex min-h-10 max-w-full items-center break-words font-semibold hover:underline"
        >
          {doc.name}
        </Link>
        <div className="text-[13px] text-muted">{docMeta(doc)}</div>
      </div>
    </li>
  );
}
