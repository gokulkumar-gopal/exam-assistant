// Pure helpers for the print view (fs 6.9). The page and PrintSheet only ever see a PrintPlan: it copies the
// answer key in only when the key is visible, so key material cannot reach the DOM while it is hidden.
import { submittedCell } from "./status";
import type { ExamType, Marks, UserRef } from "./types";

export interface PrintOptions { paper: true; answerSheet: boolean; key: boolean; nameLine: boolean }

/** The exam as the API sends it. Key fields may be present even when they must not be printed. */
export interface PrintExam {
  title: string;
  type: ExamType;
  totalMarks: number;
  createdBy: UserRef;
  questions: { id: string; marks: Marks; text: string; options?: string[]; correctIndex?: number; modelAnswer?: string }[];
}

const LETTERS = ["A", "B", "C", "D"] as const;

/** Three ruled answer lines per mark (fs 6.9). */
export const ruledLines = (marks: number): number => marks * 3;

export const answerSheetAvailable = (exam: Pick<PrintExam, "type">): boolean => exam.type === "mcq";

export function defaultOptions(exam: Pick<PrintExam, "type">, keyVisible: boolean): PrintOptions {
  return { paper: true, answerSheet: answerSheetAvailable(exam), key: keyVisible, nameLine: true };
}

/** Forces the rules: paper always on, no answer sheet for free text, no key while it is hidden. */
export function normalizeOptions(exam: Pick<PrintExam, "type">, o: PrintOptions, keyVisible: boolean): PrintOptions {
  return { paper: true, answerSheet: answerSheetAvailable(exam) && o.answerSheet, key: keyVisible && o.key, nameLine: o.nameLine };
}

/** Text next to the disabled Answer key checkbox. Never an empty gap. */
export function keyUnavailableText(iso: string | null | undefined): string {
  const when = iso ? submittedCell(iso) : "—";
  return when === "—" ? "Available after the exam closes" : `Available after ${when}`;
}

const marksLabel = (n: number) => `[${n} ${n === 1 ? "mark" : "marks"}]`;
const plural = (n: number, one: string) => `${n} ${n === 1 ? one : `${one}s`}`;

export interface PaperQuestion {
  number: number;
  text: string;
  marksLabel: string;
  options?: { letter: string; text: string }[];
  /** Ruled answer lines (free text only). */
  lines: number;
}
export interface KeyEntry {
  number: number;
  marksLabel: string;
  /** Multiple choice. */
  letter?: string;
  optionText?: string;
  /** Free text. */
  text?: string;
  modelAnswer?: string;
}
export type PrintPart = "paper" | "answerSheet" | "key";
export interface PrintPlan {
  title: string;
  parts: PrintPart[];
  meta: string;
  instruction: string;
  /** Null when the name line is off. */
  nameLine: { total: number } | null;
  paper: { questions: PaperQuestion[] };
  answerSheet: { numbers: number[]; twoColumns: boolean } | null;
  key: { heading: string; note: string; entries: KeyEntry[] } | null;
}

/** Decides which parts render and returns the only data the sheet may use. Key fields are read only when keyVisible. */
export function printPlan(exam: PrintExam, options: PrintOptions, keyVisible: boolean): PrintPlan {
  const o = normalizeOptions(exam, options, keyVisible);
  const mcq = exam.type === "mcq";
  const total = exam.questions.reduce((s, q) => s + q.marks, 0);
  const paperQuestions: PaperQuestion[] = exam.questions.map((q, i) => ({
    number: i + 1,
    text: q.text,
    marksLabel: marksLabel(q.marks),
    ...(mcq ? { options: (q.options ?? []).slice(0, 4).map((t, k) => ({ letter: LETTERS[k], text: t })) } : {}),
    lines: mcq ? 0 : ruledLines(q.marks),
  }));
  const answerSheet = o.answerSheet
    ? { numbers: exam.questions.map((_, i) => i + 1), twoColumns: exam.questions.length > 10 }
    : null;
  let key: PrintPlan["key"] = null;
  if (o.key) {
    key = {
      heading: `Answer key: ${exam.title}`,
      note: "For the teacher, parent or marker. Not for students.",
      entries: exam.questions.map((q, i): KeyEntry => {
        const base = { number: i + 1, marksLabel: marksLabel(q.marks) };
        if (!mcq) return { ...base, text: q.text, modelAnswer: q.modelAnswer ?? "" };
        const k = q.correctIndex;
        return typeof k === "number" && q.options?.[k] !== undefined
          ? { ...base, letter: LETTERS[k], optionText: q.options[k] }
          : base;
      }),
    };
  }
  const parts: PrintPart[] = ["paper"];
  if (answerSheet) parts.push("answerSheet");
  if (key) parts.push("key");
  return {
    title: exam.title,
    parts,
    meta: `Set by ${exam.createdBy.name} · ${plural(exam.questions.length, "question")} · ${plural(total, "mark")}`,
    instruction: mcq
      ? "Choose one answer for each question. Marks are shown in brackets."
      : "Answer all questions in the space provided. Marks are shown in brackets.",
    nameLine: o.nameLine ? { total } : null,
    paper: { questions: paperQuestions },
    answerSheet,
    key,
  };
}
