"use client";
import { useId, useMemo, useRef, useState } from "react";
import { api, ApiError } from "@/lib/api";
import { assignHelper, parseEmails } from "@/lib/publish";
import type { AssignResponse, Role } from "@/lib/types";
import { Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";

const FIELD =
  "min-h-24 w-full rounded-lg border border-input bg-card px-2.5 py-2 text-base outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 md:text-sm";

/** "2 people assigned" etc. for the result line. */
function resultText(r: AssignResponse): string {
  const parts: string[] = [];
  parts.push(r.assigned.length > 0 ? `${r.assigned.length} assigned` : "Nobody new was assigned");
  if (r.alreadyAssigned.length > 0) parts.push(`${r.alreadyAssigned.length} already assigned`);
  return `${parts.join(", ")}.`;
}

/**
 * fs 6.6 "Assign to more people". Shows the role's cap text, validates inline, sends once per click, then shows the
 * result (assigned / already assigned / a Notice for unknown emails) and asks the page to refetch the table.
 * `existing` is who is already on the exam (counts toward the cap). Remount (key) to reset.
 */
export function AssignDialog({
  open, onOpenChange, examId, role, existing, onAssigned,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  examId: string;
  role: Role | undefined;
  existing: string[];
  onAssigned: () => void;
}) {
  const uid = useId();
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const inflight = useRef(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<AssignResponse | null>(null);

  const parsed = useMemo(() => parseEmails(text, role, existing), [text, role, existing]);
  const invalid = parsed.error !== null;
  const canSubmit = parsed.emails.length > 0 && !invalid && !busy;

  async function submit() {
    if (inflight.current || !canSubmit) return;
    inflight.current = true;
    setBusy(true);
    setError(null);
    setResult(null);
    try {
      const r = await api<AssignResponse>("POST", `/exams/${encodeURIComponent(examId)}/assign`, { emails: parsed.emails });
      setResult(r);
      setText("");
      onAssigned();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Couldn't assign. Try again.");
    } finally {
      inflight.current = false;
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!busy) onOpenChange(o); }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Assign to more people</DialogTitle>
          <DialogDescription>{assignHelper(role)}</DialogDescription>
        </DialogHeader>
        <form
          className="flex flex-col gap-2"
          onSubmit={(e) => { e.preventDefault(); void submit(); }}
        >
          <label htmlFor={`${uid}-emails`} className="font-semibold">Email addresses</label>
          <textarea
            id={`${uid}-emails`}
            className={FIELD}
            value={text}
            disabled={busy}
            onChange={(e) => setText(e.target.value)}
            placeholder="a@school.org, b@school.org"
            aria-invalid={text.trim() !== "" && invalid}
            aria-describedby={`${uid}-hint`}
          />
          <p id={`${uid}-hint`} className="text-[13px] text-muted">Separate with commas, spaces or new lines.</p>
          {text.trim() !== "" && parsed.error && <p className="text-[13px] text-bad">{parsed.error}</p>}
          <div aria-live="polite" className="empty:hidden">
            {error && <p role="alert" className="text-sm text-bad">{error}</p>}
            {result && (
              <div>
                <p className="mb-2 text-sm font-semibold">{resultText(result)}</p>
                {result.unknown.length > 0 && (
                  <Notice>
                    We couldn&apos;t find an account for: {result.unknown.join(", ")}. They didn&apos;t get the exam.
                  </Notice>
                )}
              </div>
            )}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
              {result ? "Done" : "Cancel"}
            </Button>
            <Button type="submit" disabled={!canSubmit}>{busy ? "Assigning…" : "Assign"}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
