"use client";
import { Button } from "@/components/ui/button";
import {
  AlertDialog, AlertDialogCancel, AlertDialogContent, AlertDialogDescription,
  AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { revealDialogBody } from "@/lib/reveal";

/**
 * fs 6.5 "Reveal this answer?" confirmation. The page owns the request: `onConfirm` resolves when it has finished
 * (success or not) and the page then closes the dialog. While `busy` the dialog cannot be dismissed.
 */
export function RevealDialog({
  open, onOpenChange, peeksLeft, busy, onConfirm, finalFocus,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  peeksLeft: number;
  busy: boolean;
  onConfirm: () => void;
  /** Where focus goes when the dialog finishes closing. */
  finalFocus?: () => boolean | HTMLElement | null;
}) {
  return (
    <AlertDialog open={open} onOpenChange={(o) => { if (!busy) onOpenChange(o); }}>
      <AlertDialogContent finalFocus={finalFocus}>
        <AlertDialogHeader>
          <AlertDialogTitle>Reveal this answer?</AlertDialogTitle>
          <AlertDialogDescription>{revealDialogBody(peeksLeft)}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={busy}>Cancel</AlertDialogCancel>
          <Button disabled={busy} aria-busy={busy || undefined} onClick={onConfirm}>
            {busy ? "Revealing…" : "Reveal"}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
