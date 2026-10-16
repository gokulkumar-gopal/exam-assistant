"use client";
// One result question (fs 6.7). Receives only the helper-derived ResultCardData (lib/key.ts), never the raw API
// question, so a hidden answer key cannot reach the DOM. All text is rendered as React text nodes.
import { MarkBadge } from "@/components/MarkBadge";
import { Pill } from "@/components/Pill";
import { REVEALED_MARKER, type Check, type ResultCardData } from "@/lib/key";
import { cn } from "@/lib/utils";
import type { Marks } from "@/lib/types";

const CHECK_BTN =
  "inline-flex min-h-10 min-w-10 items-center justify-center rounded-lg border px-3 text-[13px] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";

interface Props {
  card: ResultCardData;
  marks: Marks;
  check?: Check;
  onCheck?: (value: Check) => void;
}

export function ResultCard({ card, marks, check, onCheck }: Props) {
  const label = `Question ${card.number}`;
  return (
    <section className="card p-4" aria-label={label} data-testid="result-card">
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <span
          aria-hidden="true"
          className="inline-flex size-[26px] items-center justify-center rounded-full bg-tint text-xs font-bold text-accent"
        >
          {card.number}
        </span>
        <MarkBadge marks={marks} />
        {card.pill && <Pill variant={card.pill.variant}>{card.pill.text}</Pill>}
        {card.revealed && <Pill variant="neutral">{REVEALED_MARKER}</Pill>}
      </div>
      <p className="mb-2 whitespace-pre-wrap break-words text-base font-semibold">{card.text}</p>
      <p className="break-words text-sm">
        <span className="text-muted">Your answer: </span>
        <span className="whitespace-pre-wrap">{card.yourAnswer}</span>
      </p>
      {card.revealedText !== undefined && (
        <p className="mt-1 break-words text-sm">
          <span className="text-muted">Revealed answer: </span>
          <span className="whitespace-pre-wrap font-semibold">{card.revealedText}</span>
        </p>
      )}
      {card.correctText !== undefined && (
        <p className="mt-1 break-words text-sm">
          <span className="text-muted">Correct answer: </span>
          <span className="whitespace-pre-wrap font-semibold">{card.correctText}</span>
        </p>
      )}
      {card.modelAnswer !== undefined && (
        <div className="mt-2">
          <p className="text-[13px] font-semibold text-muted">Compare with the model answer</p>
          <p className="mt-1 whitespace-pre-wrap break-words rounded-lg border border-line bg-bg px-3 py-2 text-sm">
            {card.modelAnswer}
          </p>
        </div>
      )}
      {card.canSelfCheck && onCheck && (
        <div role="group" aria-label={`Self-check for ${label}`} className="mt-3 flex flex-wrap gap-2">
          {([["got", "I got it"], ["missed", "I missed it"]] as const).map(([value, text]) => (
            <button
              key={value}
              type="button"
              aria-pressed={check === value}
              onClick={() => onCheck(value)}
              className={cn(CHECK_BTN, check === value ? "border-accent bg-tint font-semibold text-accent" : "border-line bg-card text-ink hover:bg-tint")}
            >
              {text}
            </button>
          ))}
        </div>
      )}
    </section>
  );
}
