// Publishing an exam (fs 6.4, bs 8.5): the request body, assignee parsing, and an idempotent publisher.
import { api as sharedApi } from "./api";
import { PEEKS_UI } from "./features";
import type { CreateExamBody, CreateExamResponse, ExamType, NewQuestion, Question, Role } from "./types";

export type PublishBody = CreateExamBody;
export type PublishResult = CreateExamResponse;
export type Deadline = { closesInSeconds: number } | { closesAt: string };

/** Preset deadlines, in seconds, sent as `closesInSeconds` (the server computes the time). */
export const PRESET_SECONDS = [3600, 86400, 172800, 604800];

/** A fresh Idempotency-Key: crypto.randomUUID, else getRandomValues, else Math.random (8-64 chars of A-Za-z0-9_-). */
export function newKey(): string {
  const c = (globalThis as { crypto?: Crypto }).crypto;
  if (c && typeof c.randomUUID === "function") return c.randomUUID();
  const b = new Uint8Array(16);
  if (c && typeof c.getRandomValues === "function") c.getRandomValues(b);
  else for (let i = 0; i < 16; i++) b[i] = Math.floor(Math.random() * 256);
  b[6] = (b[6] & 0x0f) | 0x40;
  b[8] = (b[8] & 0x3f) | 0x80;
  const h = Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}

const clip = (s: string | undefined) => (s ?? "").slice(0, 120);

export function buildPublishBody(f: {
  title: string; type: ExamType; deadline: Deadline; assigneeEmails: string[]; maxPeeks: number; questions: Question[];
}, peeksUi: boolean = PEEKS_UI): PublishBody {
  const questions: NewQuestion[] = f.questions.map((q) => {
    const out: NewQuestion = { marks: q.marks, text: q.text, sourceDocId: clip(q.sourceDocId), sourceDocName: clip(q.sourceDocName) };
    if (f.type === "mcq") {
      out.options = q.options;
      out.correctIndex = q.correctIndex;
    } else {
      out.modelAnswer = q.modelAnswer;
    }
    return out;
  });
  // With peeks cut, maxPeeks is omitted and the server applies its default.
  return {
    title: f.title.trim(), type: f.type, ...f.deadline, assigneeEmails: f.assigneeEmails,
    ...(peeksUi ? { maxPeeks: f.maxPeeks } : {}), questions,
  };
}

const EMAIL = /^[^\s@,;]+@[^\s@,;]+\.[^\s@,;]+$/;
/** The same check parseEmails uses for each address. */
export const isEmail = (s: string): boolean => EMAIL.test(s);

/** fs 6.4 helper under "Assign to" (and in the Assign dialog). The stricter student text shows until the role is known. */
export function assignHelper(role: Role | undefined): string {
  return role === "student" || role === undefined ? "Student emails only · up to 10 people" : "Up to 50 people";
}

/**
 * Emails split on commas, newlines and spaces: trimmed, lowercased, deduped, checked, capped by role
 * (student or unknown: 10, else 50). `existing` are people already on the exam: they count toward the cap
 * (bs 8.5) but listing one again is not an extra person.
 */
export function parseEmails(
  raw: string, role: Role | undefined, existing: readonly string[] = [],
): { emails: string[]; error: string | null } {
  const emails: string[] = [];
  const seen = new Set<string>();
  for (const part of raw.split(/[\s,]+/)) {
    const e = part.trim().toLowerCase();
    if (!e || seen.has(e)) continue;
    seen.add(e);
    emails.push(e);
  }
  const bad = emails.find((e) => !EMAIL.test(e));
  if (bad) return { emails, error: `${bad} doesn't look like an email address.` };
  const max = role === undefined || role === "student" ? 10 : 50;
  const have = new Set(existing.map((e) => e.toLowerCase()));
  const total = have.size + emails.filter((e) => !have.has(e)).length;
  if (total > max) {
    return { emails, error: have.size > 0 ? `You can assign up to ${max} people in total (${have.size} already assigned).` : `You can assign up to ${max} people.` };
  }
  return { emails, error: null };
}

export interface PublisherDeps { api?: typeof sharedApi; newId?: () => string }

/**
 * One Idempotency-Key per attempt. The key is kept while the body stays the same, so a retry after a
 * failure replays the same request; a changed body or a success starts a new key.
 */
export function makePublisher(deps: PublisherDeps = {}) {
  const call = deps.api ?? sharedApi;
  const newId = deps.newId ?? newKey;
  let key: string | null = null;
  let sig = "";
  let inflight: { sig: string; p: Promise<PublishResult> } | null = null;

  function publish(body: PublishBody): Promise<PublishResult> {
    const s = JSON.stringify(body);
    if (inflight && inflight.sig === s) return inflight.p;
    if (key === null || s !== sig) {
      key = newId();
      sig = s;
    }
    const k = key;
    const p = call<PublishResult>("POST", "/exams", body, { idempotencyKey: k, noRetry: true }).then(
      (r) => { key = null; sig = ""; return r; },
    ).finally(() => { if (inflight?.p === p) inflight = null; });
    inflight = { sig: s, p };
    return p;
  }
  return { publish };
}

/** How many questions have no usable answer in the key (MCQ: no valid correct option; free text: empty model answer). */
export function keyMissing(questions: Question[], type: ExamType): number {
  return questions.filter((q) =>
    type === "mcq"
      ? !Number.isInteger(q.correctIndex) || (q.correctIndex as number) < 0 || (q.correctIndex as number) > 3
      : !q.modelAnswer?.trim(),
  ).length;
}

/** "Assign to me" (fs 6.4): the creator's own email goes in the same list as everyone else's, once. */
export function withSelf(emails: readonly string[], selfEmail: string | undefined, includeSelf: boolean): string[] {
  const self = selfEmail?.trim().toLowerCase();
  if (!includeSelf || !self || emails.includes(self)) return [...emails];
  return [...emails, self];
}

/** The assignees other than the creator: the "practice exam" peek default only looks at these. */
export function othersOf(emails: readonly string[], selfEmail: string | undefined): string[] {
  const self = selfEmail?.trim().toLowerCase();
  return emails.filter((e) => e !== self);
}
