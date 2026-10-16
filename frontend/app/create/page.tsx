"use client";
import { Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { usePathname, useSearchParams } from "next/navigation";
import { api } from "@/lib/api";
import { generateErrorNotice, GAVE_UP_MESSAGE, pollJob, startGeneration } from "@/lib/generate";
import { clearGenerated, setGenerated, type GeneratedResult } from "@/lib/generated";
import { useMe } from "@/lib/session";
import type { AiModel, Doc, DocsResponse, ExamType, Marks, ModelsResponse, Role } from "@/lib/types";
import {
  footerState, parseSources, questionCount, sourceLimitError, totalMarks, type Counts,
} from "@/lib/totals";
import { defaultModelId } from "@/lib/models";
import { PRESET_SECONDS, parseEmails, type Deadline } from "@/lib/publish";
import type { PublishForm } from "@/components/PublishBar";
import { Minus, Plus } from "lucide-react";
import { ModelSelect } from "@/components/ModelSelect";
import { GenerationCard } from "@/components/GenerationCard";
import { QuestionList } from "@/components/QuestionList";
import { Notice } from "@/components/Notice";
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
const PEEK_BTN =
  "inline-flex size-10 shrink-0 items-center justify-center rounded-lg border border-line bg-card text-accent hover:bg-tint focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-40 disabled:hover:bg-card";
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
  const [peeksEdited, setPeeksEdited] = useState<number | null>(null); // null: follows the default

  // Page state only. undefined means "send no model" (models failed, empty, or not loaded).
  const [models, setModels] = useState<AiModel[]>([]);
  const [modelId, setModelId] = useState<string | undefined>(undefined);

  const applyModels = useCallback((r: ModelsResponse | null) => {
    if (!r || !Array.isArray(r.models) || r.models.length === 0) {
      setModels([]);
      setModelId(undefined);
      return;
    }
    setModels(r.models);
    setModelId(defaultModelId(r.models) ?? r.models[0].id);
  }, []);

  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    api<ModelsResponse>("GET", "/ai/models")
      .then((r) => { if (alive.current) applyModels(r); })
      .catch(() => {});
    return () => { alive.current = false; };
  }, [applyModels]);

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

  // Generation job state. Only one job at a time; the ref guards a double click before state updates.
  const [running, setRunning] = useState(false);
  const [progress, setProgress] = useState<{ done: number; total: number } | null>(null);
  const [notices, setNotices] = useState<{ text: string; bad: boolean }[]>([]);
  const [retry, setRetry] = useState(false);
  const [result, setResult] = useState<GeneratedResult | null>(null);
  const [resultKey, setResultKey] = useState(0); // new list state (edits, shuffle seed) for each generation
  const lastAgain = useRef<GeneratedResult | undefined>(undefined); // so Try again repeats a Regenerate all
  const busy = useRef(false);
  const runId = useRef(0);
  const ctrl = useRef<AbortController | null>(null);
  const resultsRef = useRef<HTMLDivElement>(null);
  const pathname = usePathname();

  // Leaving the page (route change, tab close) stops a running job so it stops spending.
  // pagehide only: beforeunload also fires when the user declines the leave prompt.
  useEffect(() => {
    const stop = () => ctrl.current?.abort();
    window.addEventListener("pagehide", stop);
    return () => {
      window.removeEventListener("pagehide", stop);
      stop();
    };
  }, [pathname]);

  const onGenerate = useCallback(async (again?: GeneratedResult) => {
    if (busy.current) return;
    busy.current = true;
    lastAgain.current = again;
    const me = ++runId.current;
    const c = new AbortController();
    ctrl.current = c;
    setRunning(true);
    setProgress(null);
    setNotices([]);
    setRetry(false);
    setResult(null);
    clearGenerated();
    resultsRef.current?.scrollIntoView({ block: "nearest" });
    // Regenerate all repeats the previous run's settings, whatever the form says now.
    const countsBody: Record<string, number> = {};
    if (again) Object.assign(countsBody, again.params.counts);
    else for (const m of MARKS) if (counts[m] > 0) countsBody[String(m)] = counts[m];
    const gType = again ? again.params.type : type;
    const gTarget = again ? again.params.target : target;
    const req = {
      sourceIds: again ? again.params.sourceIds : chosen.map((d) => d.id),
      type: gType, counts: countsBody, shuffle, model: again ? again.model : modelId,
    };
    const current = () => runId.current === me;
    const finish = (n: { text: string; bad: boolean }[], canRetry: boolean) => {
      setNotices(n);
      setRetry(canRetry);
      setProgress(null);
      setRunning(false);
    };
    try {
      let jobId: string;
      try {
        ({ jobId } = await startGeneration(req));
      } catch (e) {
        if (!current()) return;
        const n = generateErrorNotice(e);
        if (n.resetModel) {
          api<ModelsResponse>("GET", "/ai/models")
            .then((r) => { if (alive.current) applyModels(r); })
            .catch(() => { if (alive.current) applyModels(null); });
        }
        finish([{ text: n.text, bad: true }], n.retry);
        return;
      }
      const r = await pollJob(jobId, { signal: c.signal, onProgress: (p) => { if (current()) setProgress(p); } });
      if (!current()) return;
      if (r.status === "done") {
        const out: GeneratedResult = {
          questions: r.questions,
          model: r.model,
          params: { sourceIds: req.sourceIds, type: gType, counts: countsBody, shuffle, target: gTarget },
        };
        setGenerated(out);
        setResult(out);
        setResultKey((k) => k + 1);
        finish((r.failedSources ?? []).map((name) => ({ text: `We couldn't read ${name}. Try a clearer photo or a different file.`, bad: false })), false);
      } else if (r.status === "failed") {
        finish([{ text: r.error, bad: true }], true);
      } else if (r.status === "gaveUp") {
        finish([{ text: GAVE_UP_MESSAGE, bad: true }], true);
      } else {
        finish([{ text: "Generation was cancelled.", bad: false }], true);
      }
    } finally {
      if (current()) {
        busy.current = false;
        ctrl.current = null;
      }
    }
  }, [applyModels, chosen, counts, modelId, shuffle, target, type]);

  const assignees = useMemo(() => parseEmails(assign, user?.role), [assign, user?.role]);
  // Peeks default to 3 for a personal practice exam and 0 when people are assigned, until the user edits it.
  const maxPeeks = peeksEdited ?? (assignees.emails.length === 0 ? 3 : 0);
  // The deadline is the server's to compute (preset) or the user's chosen time (picker); never the device clock.
  const deadline: Deadline | null = useMemo(() => {
    if (closes !== "custom") return PRESET_SECONDS.includes(Number(closes)) ? { closesInSeconds: Number(closes) } : null;
    const t = closesAt ? new Date(closesAt) : null;
    return t && !Number.isNaN(t.getTime()) ? { closesAt: t.toISOString() } : null;
  }, [closes, closesAt]);
  const publishForm: PublishForm = {
    title, deadline, assigneeEmails: assignees.emails, assignError: assignees.error, maxPeeks,
  };

  const generateEnabled = fs.generateEnabled && !limitError && !running;

  const onCancel = useCallback(() => {
    runId.current++; // ignore anything the in-flight run still reports
    ctrl.current?.abort();
    ctrl.current = null;
    busy.current = false;
    setRunning(false);
    setProgress(null);
    setNotices([]);
    setRetry(false);
  }, []);

  return (
    <main className="page pt-5">
      <h1>Create exam</h1>
      <p className="mb-4 text-[13px] text-muted">Pick sources, set the marks, generate.</p>
      <fieldset disabled={running} className="m-0 min-w-0 border-0 p-0">
      <div className="grid grid-cols-1 items-start gap-4 min-[720px]:grid-cols-[260px_minmax(0,1fr)]">
        <SourcesCard
          docs={docs}
          selected={selected}
          onToggle={toggle}
          error={limitError}
          loadError={loadError}
          onRetry={() => void load()}
          disabled={running}
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
              <Checkbox checked={shuffle} onCheckedChange={(v) => setShuffle(v === true)} disabled={running} />
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
                  aria-describedby={assignees.error ? "assign-error assign-help" : "assign-help"}
                  aria-invalid={assignees.error !== null}
                />
                {assignees.error && <p id="assign-error" role="alert" className="mt-1 text-[13px] text-bad">{assignees.error}</p>}
                <p id="assign-help" className="mt-1 text-[13px] text-muted">
                  {assignHelper(user?.role)}. Leave empty for a personal practice exam.
                </p>
              </div>
              <div>
                <span id="peeks-l" className="mb-1 block font-semibold">Peeks allowed</span>
                <div role="group" aria-labelledby="peeks-l" className="flex items-center gap-2.5">
                  <button type="button" className={PEEK_BTN} aria-label="Decrease peeks allowed" disabled={maxPeeks <= 0} onClick={() => setPeeksEdited(Math.max(0, maxPeeks - 1))}>
                    <Minus className="size-4" aria-hidden />
                  </button>
                  <span className="w-[26px] text-center font-semibold tabular-nums" aria-live="polite">{maxPeeks}</span>
                  <button type="button" className={PEEK_BTN} aria-label="Increase peeks allowed" disabled={maxPeeks >= 10} onClick={() => setPeeksEdited(Math.min(10, maxPeeks + 1))}>
                    <Plus className="size-4" aria-hidden />
                  </button>
                </div>
                <p className="mt-1 text-[13px] text-muted">Takers can reveal an answer mid-exam for half marks. 0 turns it off.</p>
              </div>
            </div>
          </Card>
          {models.length > 0 && (
            <Card id="model-h" title="Model">
              <ModelSelect models={models} value={modelId} onChange={setModelId} />
            </Card>
          )}
        </div>
      </div>
      </fieldset>
      <div ref={resultsRef} className="mt-4 flex flex-col" aria-label="Results">
        {running && <GenerationCard progress={progress} onCancel={onCancel} />}
        {!running && notices.map((n, i) => <Notice key={i} tone={n.bad ? "bad" : "warn"}>{n.text}</Notice>)}
        {!running && retry && (
          <div className="mb-3.5">
            <button type="button" className="btn-secondary" onClick={() => void onGenerate(lastAgain.current)}>Try again</button>
          </div>
        )}
        {result && (
          <QuestionList
            key={resultKey}
            result={result}
            shuffle={shuffle}
            form={publishForm}
            onRegenerateAll={() => void onGenerate(result)}
          />
        )}
      </div>
      <CreateFooter
        total={total}
        target={target}
        tone={fs.tone}
        text={fs.text}
        generateEnabled={generateEnabled}
        onGenerate={() => void onGenerate()}
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
