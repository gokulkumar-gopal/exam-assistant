"use client";
import { Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "next/navigation";
import { api } from "@/lib/api";
import { useMe } from "@/lib/session";
import type { Doc, DocsResponse, ExamType, Marks, Role } from "@/lib/types";
import {
  footerState, parseSources, questionCount, sourceLimitError, totalMarks, type Counts,
} from "@/lib/totals";
import { SourcesCard } from "@/components/SourcesCard";
import { CreateFooter } from "@/components/CreateFooter";
import { Stepper } from "@/components/Stepper";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

const MARKS: Marks[] = [1, 2, 3, 4, 5];
const TARGETS = [30, 40, 50] as const;
const MAX_COUNT = 30;
const CLOSES = [
  { value: "3600", label: "1 hour" },
  { value: "86400", label: "1 day" },
  { value: "172800", label: "2 days" },
  { value: "604800", label: "1 week" },
  { value: "custom", label: "Pick date and time" },
];
const SEG =
  "inline-flex min-h-10 flex-1 items-center justify-center rounded-lg border px-4 font-semibold focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
const FIELD = "h-10 w-full rounded-lg border border-input bg-card px-2.5 text-base outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 md:text-sm";

function assignHelper(role: Role | undefined): string {
  // Until the role is known, show the stricter student text.
  return role === "student" || role === undefined ? "Student emails only · up to 10 people" : "Up to 50 people";
}

function Card({ id, title, children }: { id: string; title: string; children: React.ReactNode }) {
  return (
    <section className="card p-4" aria-labelledby={id}>
      <h2 id={id} className="h-section mb-2">{title}</h2>
      {children}
    </section>
  );
}

function nowLocalInput(): string {
  const d = new Date();
  d.setSeconds(0, 0);
  const off = d.getTimezoneOffset() * 60_000;
  return new Date(d.getTime() - off).toISOString().slice(0, 16);
}

function CreateInner() {
  const params = useSearchParams();
  const { user } = useMe();
  const [docs, setDocs] = useState<Doc[] | null>(null);
  const [loadError, setLoadError] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const seeded = useRef(false);
  const [type, setType] = useState<ExamType>("mcq");
  const [target, setTarget] = useState<number>(30);
  const [counts, setCounts] = useState<Counts>({ 1: 0, 2: 0, 3: 0, 4: 0, 5: 0 });
  const [shuffle, setShuffle] = useState(false);
  const [title, setTitle] = useState("");
  const [closes, setCloses] = useState("86400");
  const [closesAt, setClosesAt] = useState("");
  const [assign, setAssign] = useState("");

  const load = useCallback(async () => {
    setLoadError(false);
    setDocs(null);
    try {
      const r = await api<DocsResponse>("GET", "/documents");
      setDocs(r.documents);
      if (!seeded.current) {
        seeded.current = true;
        setSelected(new Set(parseSources(params.get("sources"), r.documents.map((d) => d.id))));
      }
    } catch {
      setLoadError(true);
    }
  }, [params]);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- initial fetch on mount
    void load();
  }, [load]);

  const toggle = useCallback((id: string) => {
    setSelected((s) => {
      const n = new Set(s);
      if (n.has(id)) n.delete(id);
      else n.add(id);
      return n;
    });
  }, []);

  const chosen = useMemo(() => (docs ?? []).filter((d) => selected.has(d.id)), [docs, selected]);
  const limitError = sourceLimitError(chosen);
  const total = totalMarks(counts);
  const fs = footerState(total, target, questionCount(counts), chosen.length > 0);
  const generateEnabled = fs.generateEnabled && !limitError;

  // F-2.3 wires generation (POST /generate and polling). Intentionally a no-op here.
  const onGenerate = useCallback(() => {}, []);

  return (
    <main className="page pt-5">
      <h1>Create exam</h1>
      <p className="mb-4 text-[13px] text-muted">Pick sources, set the marks, generate.</p>
      <div className="grid grid-cols-1 items-start gap-4 min-[720px]:grid-cols-[260px_minmax(0,1fr)]">
        <SourcesCard
          docs={docs}
          selected={selected}
          onToggle={toggle}
          error={limitError}
          loadError={loadError}
          onRetry={() => void load()}
        />
        <div className="flex flex-col gap-4">
          <Card id="type-h" title="Question type">
            <div role="radiogroup" aria-labelledby="type-h" className="flex gap-2">
              {([["mcq", "Multiple choice"], ["free", "Free text"]] as const).map(([v, l]) => (
                <button
                  key={v}
                  type="button"
                  role="radio"
                  aria-checked={type === v}
                  onClick={() => setType(v)}
                  className={cn(SEG, type === v ? "border-transparent bg-grad text-white" : "border-line bg-card text-ink hover:bg-tint")}
                >
                  {l}
                </button>
              ))}
            </div>
          </Card>
          <Card id="total-h" title="Total marks">
            <div role="radiogroup" aria-labelledby="total-h" className="flex gap-2">
              {TARGETS.map((t) => (
                <button
                  key={t}
                  type="button"
                  role="radio"
                  aria-checked={target === t}
                  onClick={() => setTarget(t)}
                  className={cn(SEG, "flex-none min-w-16", target === t ? "border-transparent bg-grad text-white" : "border-line bg-card text-ink hover:bg-tint")}
                >
                  {t}
                </button>
              ))}
            </div>
          </Card>
          <Card id="counts-h" title="Questions per mark value">
            {MARKS.map((m) => (
              <Stepper
                key={m}
                label={`${m} ${m === 1 ? "mark" : "marks"}`}
                marks={m}
                value={counts[m]}
                max={MAX_COUNT}
                onChange={(v) => setCounts((c) => ({ ...c, [m]: Math.min(MAX_COUNT, Math.max(0, v)) }))}
                subtotal={`= ${m * counts[m]} marks`}
              />
            ))}
            <label className="mt-2 flex min-h-10 cursor-pointer items-center gap-2.5">
              <Checkbox checked={shuffle} onCheckedChange={(v) => setShuffle(v === true)} />
              <span>Shuffle order (default: low to high marks)</span>
            </label>
          </Card>
          <Card id="details-h" title="Exam details">
            <div className="flex flex-col gap-3">
              <div>
                <label htmlFor="exam-title" className="mb-1 block font-semibold">Title</label>
                <Input id="exam-title" value={title} onChange={(e) => setTitle(e.target.value)} maxLength={120} />
              </div>
              <div>
                <label htmlFor="exam-closes" className="mb-1 block font-semibold">Closes in</label>
                <select id="exam-closes" className={FIELD} value={closes} onChange={(e) => setCloses(e.target.value)}>
                  {CLOSES.map((c) => <option key={c.value} value={c.value}>{c.label}</option>)}
                </select>
                {closes === "custom" && (
                  <input
                    type="datetime-local"
                    aria-label="Closing date and time"
                    className={cn(FIELD, "mt-2")}
                    min={nowLocalInput()}
                    value={closesAt}
                    onChange={(e) => setClosesAt(e.target.value)}
                  />
                )}
              </div>
              <div>
                <label htmlFor="exam-assign" className="mb-1 block font-semibold">Assign to</label>
                <Input
                  id="exam-assign"
                  value={assign}
                  onChange={(e) => setAssign(e.target.value)}
                  placeholder="name@example.com, another@example.com"
                  aria-describedby="assign-help"
                />
                <p id="assign-help" className="mt-1 text-[13px] text-muted">
                  {assignHelper(user?.role)}. Leave empty for a personal practice exam.
                </p>
              </div>
            </div>
          </Card>
        </div>
      </div>
      <CreateFooter
        total={total}
        target={target}
        tone={fs.tone}
        text={fs.text}
        generateEnabled={generateEnabled}
        onGenerate={onGenerate}
      />
    </main>
  );
}

export default function Page() {
  return (
    <Suspense fallback={<main className="page pt-5"><h1>Create exam</h1></main>}>
      <CreateInner />
    </Suspense>
  );
}
