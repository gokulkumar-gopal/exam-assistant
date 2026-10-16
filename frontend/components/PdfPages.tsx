"use client";
// PDF.js pages in one continuous scroll; a page is rendered only while it is near the
// scroll container's viewport (fs 6.3). Loaded client-side only (see Viewer).
import { memo, useEffect, useRef, useState } from "react";
import { Document, Page, pdfjs } from "react-pdf";

// Bundled locally (no CDN fetch at runtime).
pdfjs.GlobalWorkerOptions.workerSrc = new URL("pdfjs-dist/build/pdf.worker.min.mjs", import.meta.url).toString();

const DEFAULT_RATIO = 1.3;

const PageSlot = memo(function PageSlot({
  n, width, ratio, root, onSize,
}: {
  n: number; width: number; ratio: number; root: HTMLElement | null; onSize: (r: number) => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [near, setNear] = useState(n === 1);

  useEffect(() => {
    const el = ref.current;
    if (!el || !root) return;
    const io = new IntersectionObserver(([e]) => setNear(e.isIntersecting), { root, rootMargin: "100% 0px" });
    io.observe(el);
    return () => io.disconnect();
  }, [root]);

  return (
    <div
      ref={ref}
      data-page={n}
      data-rendered={near ? "true" : "false"}
      className="shrink-0 bg-white shadow-[0_2px_12px_rgba(27,19,64,0.15)]"
      style={{ width, height: Math.round(width * ratio) }}
    >
      {near && (
        <Page
          pageNumber={n}
          width={width}
          renderTextLayer={false}
          renderAnnotationLayer={false}
          loading={null}
          onLoadSuccess={(p) => onSize(p.originalHeight / p.originalWidth)}
        />
      )}
    </div>
  );
});

export default function PdfPages({
  file, width, root, onError,
}: {
  file: { data: Uint8Array }; width: number; root: HTMLElement | null; onError: () => void;
}) {
  const [count, setCount] = useState(0);
  const [ratio, setRatio] = useState(DEFAULT_RATIO);

  return (
    <Document
      file={file}
      // Suspense would hide every page while a newly mounted page loads (scroll jumps to 0).
      suspense={false}
      loading={null}
      error={null}
      noData={null}
      onLoadSuccess={async (pdf) => {
        try {
          const p = await pdf.getPage(1);
          const v = p.getViewport({ scale: 1 });
          setRatio(v.height / v.width);
        } catch {
          /* keep the default ratio */
        }
        setCount(pdf.numPages);
      }}
      onLoadError={onError}
      onSourceError={onError}
    >
      <div className="flex flex-col items-center gap-4">
        {Array.from({ length: count }, (_, i) => (
          <PageSlot key={i + 1} n={i + 1} width={width} ratio={ratio} root={root} onSize={setRatio} />
        ))}
      </div>
    </Document>
  );
}
