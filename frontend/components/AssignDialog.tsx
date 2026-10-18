"use client";
import { useId, useMemo, useRef, useState } from "react";
import { api, ApiError, errorMessageWithFields, isNetworkError, NETWORK_TOAST } from "@/lib/api";
import { toast } from "sonner";
import { assignHelper, parseEmails, withSelf } from "@/lib/publish";
import type { AssignResponse, Role } from "@/lib/types";
import { Notice } from "@/components/Notice";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
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
  open, onOpenChange, examId, role, existing, selfEmail, onAssigned, finalFocus,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  examId: string;
  role: Role | undefined;
  existing: string[];
  /** The signed-in creator's email; enables "Assign to me" while they have no row on the exam yet. */
  selfEmail?: string;
  onAssigned: () => void;
  /** Where focus goes when the dialog finishes closing (true = back to the opener). */
  finalFocus?: () => boolean | HTMLElement | null;
}) {
  const uid = useId();
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const inflight = useRef(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<AssignResponse | null>(null);

  const [includeSelf, setIncludeSelf] = useState(false);
  const self = selfEmail?.toLowerCase();
  const offerSelf = !!self && !existing.some((e) => e.toLowerCase() === self);
  const useSelf = offerSelf && includeSelf;
  const have = useMemo(() => (useSelf && self ? [...existing, self] : existing), [existing, useSelf, self]);
  const parsed = useMemo(() => parseEmails(text, role, have), [text, role, have]);
  const emails = withSelf(parsed.emails, self, useSelf);
  const invalid = parsed.error !== null;
  const canSubmit = emails.length > 0 && !invalid && !busy;

  async function submit() {
    if (inflight.current || !canSubmit) return;
    inflight.current = true;
    setBusy(true);
    setError(null);
    setResult(null);
    try {
      const r = await api<AssignResponse>("POST", `/exams/${encodeURIComponent(examId)}/assign`, { emails });
      setResult(r);
      setText("");
      setIncludeSelf(false);
      onAssigned();
    } catch (e) {
      if (isNetworkError(e)) toast.error(NETWORK_TOAST);
      else setError(e instanceof ApiError ? errorMessageWithFields(e) : "Couldn't assign. Try again.");
    } finally {
      inflight.current = false;
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!busy) onOpenChange(o); }}>
      <DialogContent finalFocus={finalFocus}>
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
          {offerSelf && (
            <label className="flex min-h-10 cursor-pointer items-center gap-2.5">
              <Checkbox checked={includeSelf} onCheckedChange={(v) => setIncludeSelf(v === true)} disabled={busy} />
              <span>Assign to me</span>
            </label>
          )}
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
