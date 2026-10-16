"use client";
import { useSyncExternalStore } from "react";
import { keyUnavailableText, type PrintOptions } from "@/lib/print";

const noop = () => () => {};
const canPrint = () => typeof window.print === "function";

/** Screen-only controls above the preview (fs 6.9). Hidden when printing. */
export function PrintToolbar({
  title,
  options,
  hasAnswerSheet,
  keyVisible,
  keyAvailableAt,
  onChange,
}: {
  title: string;
  options: PrintOptions;
  hasAnswerSheet: boolean;
  keyVisible: boolean;
  keyAvailableAt: string | null | undefined;
  onChange: (next: PrintOptions) => void;
}) {
  const printable = useSyncExternalStore(noop, canPrint, () => false);
  const box = "flex min-h-10 items-center gap-2";
  return (
    <div className="print-toolbar card mb-4 p-4">
      <h1 className="break-words">{title}</h1>
      <p className="mb-3 text-[13px] text-muted">Choose Save as PDF in the print dialog to keep a copy.</p>
      <div className="flex flex-wrap items-center gap-x-5 gap-y-1">
        <label className={box}>
          <input type="checkbox" checked disabled /> Question paper
        </label>
        {hasAnswerSheet && (
          <label className={box}>
            <input
              type="checkbox"
              checked={options.answerSheet}
              onChange={(e) => onChange({ ...options, answerSheet: e.target.checked })}
            />{" "}
            Answer sheet
          </label>
        )}
        <label className={box}>
          <input
            type="checkbox"
            checked={keyVisible && options.key}
            disabled={!keyVisible}
            onChange={(e) => onChange({ ...options, key: e.target.checked })}
          />{" "}
          Answer key
          {!keyVisible && <span className="text-[13px] text-muted">{keyUnavailableText(keyAvailableAt)}</span>}
        </label>
        <label className={box}>
          <input
            type="checkbox"
            checked={options.nameLine}
            onChange={(e) => onChange({ ...options, nameLine: e.target.checked })}
          />{" "}
          Name line
        </label>
        {printable && (
          <button type="button" className="btn-primary ml-auto" onClick={() => window.print()}>Print</button>
        )}
      </div>
    </div>
  );
}
