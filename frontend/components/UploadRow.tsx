"use client";
import { X } from "lucide-react";
import { FileTile, type FileType } from "@/components/FileTile";
import { Meter } from "@/components/Meter";
import type { QueueItem } from "@/lib/upload";

const TYPES: Record<string, FileType> = {
  "application/pdf": "pdf", "image/jpeg": "jpg", "image/png": "png", "text/plain": "txt",
};

/** In-flight or failed upload. The name is rendered as a text node only. */
export function UploadRow({
  item,
  onRetry,
  onDismiss,
}: {
  item: QueueItem;
  onRetry: (id: string) => void;
  onDismiss: (id: string) => void;
}) {
  const failed = item.status === "failed";
  return (
    <li
      data-status={item.status}
      className={
        failed
          ? "flex items-center gap-3 rounded-[10px] border border-bad-border bg-bad-bg px-3 py-2.5"
          : "card flex items-center gap-3 px-3 py-2.5"
      }
    >
      <FileTile type={TYPES[item.file.type] ?? "txt"} />
      <div className="min-w-0 flex-1">
        <div className="break-words font-semibold">{item.file.name}</div>
        {failed ? (
          <div role="alert" className="text-[13px] text-bad">
            {item.error ?? "Upload failed. Try again."}
          </div>
        ) : (
          <>
            <div className="mb-1 text-[13px] text-muted" role="status">
              {item.status === "queued" ? "Waiting…" : "Uploading…"}
            </div>
            <Meter value={item.progress * 100} max={100} label={`Uploading ${item.file.name}`} />
          </>
        )}
      </div>
      {failed && (
        <>
          <button type="button" className="btn-secondary" onClick={() => onRetry(item.id)}>
            Retry
          </button>
          <button
            type="button"
            className="btn-secondary px-0"
            aria-label={`Dismiss ${item.file.name}`}
            onClick={() => onDismiss(item.id)}
          >
            <X size={16} aria-hidden="true" />
          </button>
        </>
      )}
    </li>
  );
}
