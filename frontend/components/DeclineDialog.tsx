"use client";
import { useRef, useState } from "react";
import { api, ApiError, isNetworkError, NETWORK_TOAST } from "@/lib/api";
import { toast } from "sonner";
import type { ExamListItem } from "@/lib/types";
import { Button } from "@/components/ui/button";
import {
  AlertDialog, AlertDialogCancel, AlertDialogContent, AlertDialogDescription,
  AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from "@/components/ui/alert-dialog";

/** Statuses meaning the exam changed under the user (started, gone, no longer theirs). */
const STALE = new Set([403, 404, 409]);
const GENERIC_STALE = "This exam can't be declined any more.";

/**
 * fs 6.1 Decline confirmation. `exam` stays set while the dialog animates closed (the parent only flips `open`),
 * so the creator name never blanks out. On 403/404/409 it closes and calls `onStale` with the server's message.
 */
export function DeclineDialog({
  exam, open, onOpenChange, onDeclined, onStale, finalFocus,
}: {
  exam: ExamListItem | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onDeclined: (examId: string) => void;
  onStale: (message: string) => void;
  /** Where focus goes when the dialog finishes closing (true = back to the opener). */
  finalFocus?: () => boolean | HTMLElement | null;
}) {
  const [busy, setBusy] = useState(false);
  const inflight = useRef(false);
  const [error, setError] = useState<string | null>(null);

  async function confirm() {
    if (!exam || inflight.current) return;
    inflight.current = true;
    setBusy(true);
    setError(null);
    try {
      await api<void>("POST", `/exams/${encodeURIComponent(exam.id)}/decline`);
      onOpenChange(false);
      onDeclined(exam.id);
    } catch (e) {
      if (e instanceof ApiError && STALE.has(e.status)) {
        onOpenChange(false);
        // A response without a message gets the client's generic text (code "internal"); use ours instead.
        onStale(e.code === "internal" ? GENERIC_STALE : e.message);
      } else if (isNetworkError(e)) {
        toast.error(NETWORK_TOAST);
      } else {
        setError("Couldn't decline this exam. Try again.");
      }
    } finally {
      inflight.current = false;
      setBusy(false);
    }
  }

  return (
    <AlertDialog
      open={open}
      onOpenChange={(o) => {
        if (busy) return;
        if (!o) setError(null);
        onOpenChange(o);
      }}
    >
      <AlertDialogContent finalFocus={finalFocus}>
        <AlertDialogHeader>
          <AlertDialogTitle>Decline this exam?</AlertDialogTitle>
          <AlertDialogDescription>
            It will be removed from your list. {exam?.createdBy.name} will see that you declined.
          </AlertDialogDescription>
        </AlertDialogHeader>
        {error && <p role="alert" className="text-sm text-bad">{error}</p>}
        <AlertDialogFooter>
          <AlertDialogCancel disabled={busy}>Cancel</AlertDialogCancel>
          <Button variant="destructive" disabled={busy} onClick={() => void confirm()}>
            {busy ? "Declining…" : "Decline"}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
