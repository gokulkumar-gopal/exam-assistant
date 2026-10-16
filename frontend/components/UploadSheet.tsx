"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import { Camera, FileUp, Image as ImageIcon } from "lucide-react";
import { toast } from "sonner";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { UploadQueue, uploadFile, validateFile, type QueueItem } from "@/lib/upload";
import type { Doc } from "@/lib/types";

/** Owns the upload queue for a page. Leaving the page aborts in-flight uploads; nothing is persisted. */
export function useUploads(onDone: (doc: Doc) => void) {
  const [items, setItems] = useState<QueueItem[]>([]);
  const queue = useRef<UploadQueue | null>(null);
  const done = useRef(onDone);
  useEffect(() => { done.current = onDone; });

  useEffect(() => {
    const q = new UploadQueue({
      upload: (file, onProgress, signal) => uploadFile(file, onProgress, { signal }),
      onChange: setItems,
      onDone: (doc) => done.current(doc),
    });
    queue.current = q;
    return () => {
      q.destroy();
      queue.current = null;
    };
  }, []);

  const add = useCallback((files: File[]) => {
    const ok: File[] = [];
    for (const f of files) {
      const message = validateFile(f);
      if (message) toast(message);
      else ok.push(f);
    }
    if (ok.length) queue.current?.add(ok);
  }, []);
  const retry = useCallback((id: string) => queue.current?.retry(id), []);
  const dismiss = useCallback((id: string) => queue.current?.dismiss(id), []);
  return { items, add, retry, dismiss };
}

const ITEM = "flex min-h-10 w-full items-center gap-2.5 rounded-lg px-3 text-left font-semibold hover:bg-tint";

export function UploadSheet({ onFiles }: { onFiles: (files: File[]) => void }) {
  const [open, setOpen] = useState(false);
  const camera = useRef<HTMLInputElement>(null);
  const file = useRef<HTMLInputElement>(null);
  const photos = useRef<HTMLInputElement>(null);

  function pick(ref: React.RefObject<HTMLInputElement | null>) {
    setOpen(false);
    ref.current?.click();
  }
  function changed(e: React.ChangeEvent<HTMLInputElement>) {
    const files = Array.from(e.target.files ?? []);
    e.target.value = "";
    if (files.length) onFiles(files);
  }

  return (
    <>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger className="btn-primary">Upload</PopoverTrigger>
        <PopoverContent>
          <button type="button" className={ITEM} onClick={() => pick(camera)}>
            <Camera size={18} aria-hidden="true" /> Take photo
          </button>
          <button type="button" className={ITEM} onClick={() => pick(file)}>
            <FileUp size={18} aria-hidden="true" /> Choose file
          </button>
          <button type="button" className={ITEM} onClick={() => pick(photos)}>
            <ImageIcon size={18} aria-hidden="true" /> Choose from photos
          </button>
        </PopoverContent>
      </Popover>
      {/* Always mounted so the change event still fires after the popover closes. */}
      <input ref={camera} data-testid="input-camera" type="file" accept="image/*" capture="environment" className="hidden" onChange={changed} />
      <input ref={file} data-testid="input-file" type="file" accept="application/pdf,image/jpeg,image/png,text/plain" multiple className="hidden" onChange={changed} />
      <input ref={photos} data-testid="input-photos" type="file" accept="image/*" multiple className="hidden" onChange={changed} />
    </>
  );
}
