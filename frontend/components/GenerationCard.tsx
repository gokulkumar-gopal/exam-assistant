"use client";
// Results-region card while a job runs (fs 6.4 flow 1): progress text, gradient bar, Cancel.
import { Meter } from "@/components/Meter";

export function GenerationCard({
  progress, onCancel,
}: { progress: { done: number; total: number } | null; onCancel: () => void }) {
  const known = progress !== null && progress.total > 0;
  return (
    <section className="card p-4" aria-label="Generating questions">
      <p className="mb-2 font-semibold tabular-nums" aria-live="polite">
        {known ? `Writing question ${progress.done} of ${progress.total}…` : "Getting started…"}
      </p>
      <Meter value={known ? progress.done : 0} max={known ? progress.total : 1} label="Question writing progress" />
      <button type="button" className="btn-secondary mt-3" onClick={onCancel}>Cancel</button>
    </section>
  );
}
