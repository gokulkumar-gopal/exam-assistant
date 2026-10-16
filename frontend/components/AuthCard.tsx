import type { ReactNode } from "react";

/** Centered card shared by /login and /signup (fs 6.8): logo above, max width 380 px. */
export function AuthCard({
  title, subtitle, children, footer,
}: { title: string; subtitle?: string; children: ReactNode; footer: ReactNode }) {
  return (
    <main className="mx-auto flex w-full max-w-[380px] flex-col items-center px-4 py-7 sm:py-10">
      <div className="mb-5 bg-grad bg-clip-text text-[22px] font-bold text-transparent">Exam Assistant</div>
      <div className="card w-full p-5">
        <h1 className="text-2xl font-bold tracking-[-0.3px]">{title}</h1>
        {subtitle && <p className="mt-1 mb-4 text-[13px] text-muted">{subtitle}</p>}
        {!subtitle && <div className="mb-4" />}
        {children}
      </div>
      <p className="mt-4 text-[15px] text-muted">{footer}</p>
    </main>
  );
}

/** Labelled input with an inline `--bad` error under it (announced politely). */
export function Field({
  id, label, error, helper, children,
}: { id: string; label: string; error?: string; helper?: string; children: ReactNode }) {
  return (
    <div className="mb-3.5">
      <label htmlFor={id} className="mb-1 block text-[13px] font-semibold">{label}</label>
      {children}
      {helper && !error && <p id={`${id}-help`} className="mt-1 text-[13px] text-muted">{helper}</p>}
      <p id={`${id}-err`} aria-live="polite" className="mt-1 text-[13px] text-bad">{error}</p>
    </div>
  );
}
