"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import dynamic from "next/dynamic";
import Link from "next/link";
import { ArrowLeft, Minus, Plus } from "lucide-react";
import { api, ApiError } from "@/lib/api";
import type { Doc, FileUrlResponse } from "@/lib/types";
import { generateHref } from "@/lib/documents";
import {
  ZOOM_DEFAULT, ZOOM_MAX, ZOOM_MIN, nextZoom, openFile, pageWidthPx,
} from "@/lib/viewer";
import { EmptyState } from "@/components/EmptyState";

const PdfPages = dynamic(() => import("./PdfPages"), { ssr: false });

type Loaded =
  | { kind: "pdf"; file: { data: Uint8Array } }
  | { kind: "img"; src: string }
  | { kind: "txt"; text: string };

type State =
  | { s: "loading" }
  | { s: "ready"; v: Loaded }
  | { s: "error"; notFound: boolean };

const PAGE = "bg-white shadow-[0_2px_12px_rgba(27,19,64,0.15)]";
const zoomBtn = "btn-secondary px-0 disabled:cursor-not-allowed disabled:opacity-40";

async function fileFetcher(): Promise<(url: string) => Promise<Response>> {
  if (process.env.NEXT_PUBLIC_MOCK_API === "true") return (await import("@/lib/mock/files")).mockFileFetch;
  return (url) => fetch(url, { credentials: "omit", cache: "no-store" });
}

export function Viewer({ doc }: { doc: Doc }) {
  const [state, setState] = useState<State>({ s: "loading" });
  const [zoom, setZoom] = useState(ZOOM_DEFAULT);
  const [attempt, setAttempt] = useState(0);
  const [root, setRoot] = useState<HTMLDivElement | null>(null);
  const [avail, setAvail] = useState(Infinity);
  const blobUrl = useRef<string | null>(null);

  // The presigned URL exists only as a local inside this effect run.
  useEffect(() => {
    let cancelled = false;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- reset to loading on (re)load
    setState({ s: "loading" });
    (async () => {
      try {
        const fetcher = await fileFetcher();
        const res = await openFile(
          async () => (await api<FileUrlResponse>("GET", `/documents/${encodeURIComponent(doc.id)}/file`)).url,
          fetcher,
        );
        let v: Loaded;
        if (doc.type === "PDF") v = { kind: "pdf", file: { data: new Uint8Array(await res.arrayBuffer()) } };
        else if (doc.type === "TXT") v = { kind: "txt", text: await res.text() };
        else {
          const url = URL.createObjectURL(await res.blob());
          if (cancelled) { URL.revokeObjectURL(url); return; }
          blobUrl.current = url;
          v = { kind: "img", src: url };
        }
        if (!cancelled) setState({ s: "ready", v });
      } catch (e) {
        if (cancelled) return;
        const notFound = e instanceof ApiError && e.status === 404;
        // Only fixed copy is ever shown; no URL or server text reaches the page.
        setState({ s: "error", notFound });
      }
    })();
    return () => {
      cancelled = true;
      if (blobUrl.current) { URL.revokeObjectURL(blobUrl.current); blobUrl.current = null; }
    };
  }, [doc.id, doc.type, attempt]);

  useEffect(() => {
    if (!root) return;
    const measure = () => setAvail(Math.max(1, root.clientWidth - 40));
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(root);
    return () => ro.disconnect();
  }, [root]);

  const retry = useCallback(() => setAttempt((a) => a + 1), []);
  const onPdfError = useCallback(() => setState({ s: "error", notFound: false }), []);
  const width = pageWidthPx(zoom, avail);
  const ready = state.s === "ready";

  return (
    <>
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <Link href="/documents" className="btn-secondary"><ArrowLeft size={16} aria-hidden="true" />Back</Link>
        <h1 className="min-w-0 flex-1 basis-40 break-words text-[18px] font-bold">{doc.name}</h1>
        <div className="flex items-center gap-2">
          <button type="button" className={zoomBtn} aria-label="Zoom out" disabled={zoom <= ZOOM_MIN}
            onClick={() => setZoom((z) => nextZoom(z, -1))}><Minus size={16} aria-hidden="true" /></button>
          <span className="min-w-[3.25rem] text-center text-[13px] tabular-nums text-muted" aria-live="polite">{zoom}%</span>
          <button type="button" className={zoomBtn} aria-label="Zoom in" disabled={zoom >= ZOOM_MAX}
            onClick={() => setZoom((z) => nextZoom(z, 1))}><Plus size={16} aria-hidden="true" /></button>
        </div>
        <Link href={generateHref([doc.id])} className="btn-primary">Generate questions</Link>
      </div>

      {state.s === "error" ? (
        <div role="alert">
          <EmptyState
            action={
              <div className="flex gap-2">
                {!state.notFound && <button type="button" className="btn-secondary" onClick={retry}>Retry</button>}
                <Link href="/documents" className="btn-secondary">Back</Link>
              </div>
            }
          >
            {state.notFound ? "That document isn't available." : "We couldn't open this file."}
          </EmptyState>
        </div>
      ) : (
        <div
          ref={setRoot}
          tabIndex={0}
          role="region"
          aria-label="Document pages"
          className="overflow-auto rounded-[10px] bg-[#e6dcfa] p-5"
          style={{ maxHeight: "70vh", touchAction: "pan-x pan-y pinch-zoom" }}
        >
          <div className="mx-auto flex w-max min-w-full flex-col items-center gap-4">
            {!ready && (
              <div role="status" aria-label="Loading document" data-testid="viewer-loading"
                className={`${PAGE} animate-pulse bg-line`} style={{ width, height: Math.round(width * 1.3) }} />
            )}
            {state.s === "ready" && state.v.kind === "pdf" && (
              <PdfPages file={state.v.file} width={width} root={root} onError={onPdfError} />
            )}
            {state.s === "ready" && state.v.kind === "img" && (
              <div className={PAGE} style={{ width }}>
                {/* eslint-disable-next-line @next/next/no-img-element -- blob URL of the downloaded bytes */}
                <img src={state.v.src} alt={doc.name} style={{ width: "100%", display: "block" }} />
              </div>
            )}
            {state.s === "ready" && state.v.kind === "txt" && (
              <div className={`${PAGE} p-6`} style={{ width, boxSizing: "border-box" }}>
                <pre className="m-0 whitespace-pre-wrap break-words text-[15px] leading-[1.45]"
                  style={{ fontFamily: "inherit", fontSize: `${(15 * zoom) / 100}px` }}>{state.v.text}</pre>
              </div>
            )}
          </div>
        </div>
      )}
    </>
  );
}
