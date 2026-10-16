// In-memory mock backend for MOCK_API=true. Covers every call in fs 8.
// Returns a Response so the client treats it exactly like a network reply.
import type {
  Attempt, AttemptStatus, ExamListItem, ExamType, Marks, Question, ResultQuestion, User,
  AssignBody, AttemptBody, CreateExamBody, GenerateBody, GoogleBody, RegenerateBody, RevealBody, SignupBody, SubmitBody, UploadUrlBody,
} from "../types";
import { consumeUploadFailure } from "./s3";
import { mockFileUrl } from "./files";
import { demoUser, docs as seedDocs, makeQuestion, models, rao, usage } from "./fixtures";

interface MockExam {
  id: string; title: string; type: ExamType; closesAt: string; maxPeeks: number;
  createdBy: { id: string; name: string }; questions: Question[]; mine: boolean;
  attempt: Attempt | null; assigned: string[];
  /** Status-page rows, built lazily from `assigned` (see rosterOf). */
  roster?: RosterRow[];
  /** Mock only: closesAt is re-set to 20 s ago on each list call so the "Closing…" row stays visible. */
  closingDemo?: boolean;
  /** Mock only: reachable by URL (take-screen fixtures) but never listed on Home. */
  unlisted?: boolean;
}
interface RosterRow { userId: string; name: string; email: string; status: "not_started" | "in_progress" | "submitted" | "declined"; score?: number; submittedAt?: string }
interface MockJob { polls: number; marks?: Marks; type: ExamType; counts: Record<string, number>; model: string; trigger: string | null }

const HOUR = 3600_000;
const iso = (ms: number) => new Date(ms).toISOString();
const GRACE = 60_000;

const state = {
  // Start signed out with NEXT_PUBLIC_MOCK_SIGNED_OUT=true (to check route guards).
  loggedIn: process.env.NEXT_PUBLIC_MOCK_SIGNED_OUT !== "true",
  user: { ...demoUser } as User,
  docs: [...seedDocs],
  pendingUploads: {} as Record<string, { name: string; type: string; sizeBytes: number }>,
  jobs: {} as Record<string, MockJob>,
  exams: [] as MockExam[],
  seq: 100,
  published: {} as Record<string, { id: string; unknownEmails: string[] }>,
  publishCalls: 0,
};

function seedExams() {
  const now = Date.now();
  const mk = (i: number, type: ExamType) => [1, 1, 2, 3].map((m, k) => makeQuestion(i * 10 + k, m as Marks, type));
  state.exams = [
    { id: "e1", title: "Biology: Cells quiz", type: "mcq", closesAt: iso(now + 5 * HOUR), maxPeeks: 3,
      createdBy: rao, questions: mk(1, "mcq"), mine: false,
      attempt: { status: "not_started", answers: {}, lastQuestionIndex: 0, revealed: [], peeksLeft: 3 }, assigned: [] },
    { id: "e2", title: "Free text: Photosynthesis", type: "free", closesAt: iso(now + 8 * 24 * HOUR), maxPeeks: 0,
      createdBy: rao, questions: mk(2, "free"), mine: false,
      attempt: { status: "in_progress", answers: { q21: "Light reaction" }, lastQuestionIndex: 0 }, assigned: [] },
    { id: "e3", title: "Last week's quiz", type: "mcq", closesAt: iso(now - 3 * 24 * HOUR), maxPeeks: 0,
      createdBy: rao, questions: mk(3, "mcq"), mine: false,
      attempt: { status: "submitted", answers: { q31: "1", q32: "0", q33: "2", q34: "3" }, lastQuestionIndex: 3 }, assigned: [] },
    { id: "e4", title: "My class test", type: "mcq", closesAt: iso(now + 2 * 24 * HOUR), maxPeeks: 0,
      createdBy: { id: demoUser.id, name: demoUser.name }, questions: mk(4, "mcq"), mine: true,
      attempt: null, assigned: ["aisha@x.com", "ben@x.com", "chen@x.com", "dev@x.com"] },
    { id: "e5", title: "Closing now: Cell division", type: "mcq", closesAt: iso(now - 20_000), maxPeeks: 0,
      createdBy: rao, questions: mk(5, "mcq"), mine: false, closingDemo: true,
      attempt: { status: "in_progress", answers: {}, lastQuestionIndex: 0 }, assigned: [] },
    { id: "e6", title: "Missed: Chemistry basics", type: "mcq", closesAt: iso(now - 5 * 24 * HOUR), maxPeeks: 0,
      createdBy: rao, questions: mk(6, "mcq"), mine: false,
      attempt: { status: "not_started", answers: {}, lastQuestionIndex: 0 }, assigned: [] },
    { id: "e7", title: "Essay: Ecosystems", type: "free", closesAt: iso(now - 24 * HOUR), maxPeeks: 0,
      createdBy: { id: demoUser.id, name: demoUser.name }, questions: mk(7, "free"), mine: true,
      attempt: { status: "submitted", answers: {}, lastQuestionIndex: 0 }, assigned: ["aisha@x.com"] },
    { id: "e8", title: "Declined: Geography", type: "mcq", closesAt: iso(now + 3 * HOUR), maxPeeks: 0,
      createdBy: rao, questions: mk(8, "mcq"), mine: false,
      attempt: { status: "declined", answers: {}, lastQuestionIndex: 0 }, assigned: [] },
    { id: "e9", title: "Not started, closes in 20 minutes", type: "mcq", closesAt: iso(now + 20 * 60_000), maxPeeks: 0,
      createdBy: { id: demoUser.id, name: demoUser.name }, questions: mk(9, "mcq"), mine: true,
      attempt: { status: "not_started", answers: {}, lastQuestionIndex: 0 }, assigned: [] },
    { id: "e10", title: "No assignees yet", type: "mcq", closesAt: iso(now + 4 * 24 * HOUR), maxPeeks: 0,
      createdBy: { id: demoUser.id, name: demoUser.name }, questions: mk(10, "mcq"), mine: true,
      attempt: null, assigned: [] },
  ];
}

/**
 * Take-screen fixtures (unlisted; open by URL).
 *   /exams/e11/take  12 questions, mixed MCQ and free text (the real backend keeps one type per exam; the UI
 *                    decides per question by `options`), resume state (answers + last index 4), a question
 *                    whose text and options are `<b>x</b>` markup, and a deadline tomorrow 6:00 PM local.
 *   /exams/e12/take  30 MCQ questions, never started (dots wrap at 375 px).
 * Other cases reuse seeded exams: e6 is closed and never started (redirect to result), e5 is inside the
 * 60 s grace window, any unknown id is a 404.
 */
function seedTakeFixtures() {
  const tomorrow = new Date();
  tomorrow.setDate(tomorrow.getDate() + 1);
  tomorrow.setHours(18, 0, 0, 0);
  const mixed: Question[] = Array.from({ length: 12 }, (_, i) => {
    const free = i % 2 === 1;
    const q: Question = { id: `t${i + 1}`, marks: ([1, 2, 3, 1, 2, 3] as Marks[])[i % 6], text: `Fixture question ${i + 1}: ${free ? "explain in your own words." : "pick the best answer."}` };
    if (free) q.modelAnswer = `Model answer ${i + 1}`;
    else { q.options = [`Answer ${i + 1}A`, `Answer ${i + 1}B`, `Answer ${i + 1}C`, `Answer ${i + 1}D`]; q.correctIndex = 1; }
    return q;
  });
  // Markup must render literally (text nodes only).
  mixed[2] = { id: "t3", marks: 3, text: "<b>x</b>", options: ["<b>x</b>", "<i>y</i>", "&amp; <script>alert(1)</script>", "plain"], correctIndex: 0 };
  state.exams.push(
    { id: "e11", title: "Take demo: mixed questions", type: "mcq", closesAt: iso(tomorrow.getTime()), maxPeeks: 0,
      createdBy: rao, questions: mixed, mine: false, unlisted: true,
      attempt: { status: "in_progress", answers: { t1: "Answer 1B", t2: "The light reaction makes ATP.", t4: "Because it is.", t5: "Answer 5D" }, lastQuestionIndex: 4, savedAt: iso(Date.now() - 600_000) },
      assigned: [] },
    { id: "e12", title: "Take demo: 30 questions", type: "mcq", closesAt: iso(Date.now() + 2 * 24 * HOUR), maxPeeks: 0,
      createdBy: rao, questions: Array.from({ length: 30 }, (_, i) => ({ ...makeQuestion(1200 + i, ((i % 5) + 1) as Marks, "mcq"), id: `m${i + 1}` })), mine: false, unlisted: true,
      attempt: { status: "not_started", answers: {}, lastQuestionIndex: 0 }, assigned: [] },
    { id: "e13", title: "Take demo: 2 questions", type: "mcq", closesAt: iso(Date.now() + 2 * 24 * HOUR), maxPeeks: 0,
      createdBy: rao, questions: Array.from({ length: 2 }, (_, i) => ({ ...makeQuestion(1300 + i, 1 as Marks, "mcq"), id: `w${i + 1}` })), mine: false, unlisted: true,
      attempt: { status: "not_started", answers: {}, lastQuestionIndex: 0 }, assigned: [] },
  );
}
seedExams();
seedTakeFixtures();

const json = (status: number, body: unknown, headers: Record<string, string> = {}) =>
  new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json", ...headers } });
const err = (status: number, code: string, message: string, fields?: Record<string, string>) =>
  json(status, { error: { code, message, fields } });
const nothing = () => new Response(null, { status: 204 });
const st = () => iso(Date.now());

/**
 * Mock-only page-URL flag that survives client-side navigation (take -> review drops the query string):
 * the value last seen on a page URL is remembered for the session. `?name=` (empty) clears it.
 */
function stickyFlag(name: string): string | null {
  const g = globalThis as unknown as { __mockFlags?: Record<string, string> };
  const flags = (g.__mockFlags ??= {});
  if (typeof window !== "undefined") {
    const v = new URLSearchParams(window.location.search).get(name);
    if (v !== null) { if (v === "") delete flags[name]; else flags[name] = v; }
  }
  return flags[name] ?? null;
}

const acceptingUntil = (e: MockExam) => iso(Date.parse(e.closesAt) + GRACE);
const over = (e: MockExam) => Date.now() >= Date.parse(e.closesAt) + GRACE;
const keyVisible = (e: MockExam) => e.mine || over(e);
const totalMarks = (e: MockExam) => e.questions.reduce((s, q) => s + q.marks, 0);

function grade(e: MockExam): { score: number | null; before: number | null } {
  if (e.type !== "mcq" || !e.attempt) return { score: null, before: null };
  let before = 0;
  for (const q of e.questions) if (e.attempt.answers[q.id] === String(q.correctIndex)) before += q.marks;
  const peeks = e.attempt.revealed?.length ?? 0;
  return { before, score: Math.max(0, before - peeks * 0.5) };
}

const CYCLE: RosterRow["status"][] = ["submitted", "in_progress", "not_started", "declined"];
const titleCase = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

/**
 * Status-page rows. Built once from `assigned`, cycling submitted / in progress / not started / declined so a
 * created exam shows every state; later assignments join as not started. A practice exam (no assignees) lists
 * the creator's own attempt. Free-text submissions have no score yet ("—").
 */
function rosterOf(e: MockExam): RosterRow[] {
  if (!e.roster) {
    const marks = totalMarks(e);
    const rows: RosterRow[] = e.assigned.filter((x) => !x.startsWith("unknown")).map((email, i) => {
      const status = CYCLE[i % CYCLE.length];
      const name = titleCase(email.split("@")[0]);
      const row: RosterRow = { userId: `u_${email}`, name, email, status };
      if (status === "submitted") {
        row.submittedAt = iso(Date.now() - (i + 1) * HOUR);
        if (e.type === "mcq") row.score = Math.max(0, marks - 1.5);
      }
      return row;
    });
    if (rows.length === 0 && e.attempt) {
      rows.push({ userId: e.createdBy.id, name: e.createdBy.name, email: state.user.email, status: e.attempt.status as RosterRow["status"] });
    }
    e.roster = rows;
  }
  return e.roster;
}
function rosterCounts(e: MockExam) {
  const r = rosterOf(e);
  const n = (s: RosterRow["status"]) => r.filter((x) => x.status === s).length;
  return { submitted: n("submitted"), inProgress: n("in_progress"), notStarted: n("not_started"), declined: n("declined") };
}

function listItem(e: MockExam, scope: string): ExamListItem {
  const base: ExamListItem = {
    id: e.id, title: e.title, createdBy: e.createdBy, closesAt: e.closesAt,
    acceptingUntil: acceptingUntil(e), totalMarks: totalMarks(e), type: e.type,
  };
  if (scope === "created") {
    return { ...base, counts: rosterCounts(e) };
  }
  const status = (over(e) && e.attempt?.status !== "submitted" ? "missed" : e.attempt?.status) as AttemptStatus;
  const g = e.attempt?.status === "submitted" ? grade(e) : null;
  return { ...base, attemptStatus: status, ...(g?.score != null ? { score: g.score, maxScore: totalMarks(e) } : {}) };
}

function keyedQuestions(e: MockExam, visible: boolean): Question[] {
  const revealed = new Map((e.attempt?.revealed ?? []).map((r) => [r.questionId, r]));
  return e.questions.map((q) => {
    if (visible) return q;
    const rest: Question = { ...q };
    delete rest.correctIndex;
    delete rest.modelAnswer;
    const r = revealed.get(q.id);
    return r ? { ...rest, correctIndex: r.correctIndex, modelAnswer: r.modelAnswer } : rest;
  });
}

export function mockFetch(method: string, rawPath: string, body: unknown, idempotencyKey?: string, resumed = false): Response | Promise<Response> {
  // Remember the sticky page-URL flags on every call (the first call of a page carries the query string).
  stickyFlag("mockSubmit"); stickyFlag("mockClose");
  const [path, qs = ""] = rawPath.split("?");
  const query = new URLSearchParams(qs);
  const as = <T>() => (body ?? {}) as T;
  const seg = path.split("/").filter(Boolean);
  const route = `${method} /${seg.map((s, i) => (i === 1 && ["documents", "exams", "generate"].includes(seg[0]) && s !== "upload-url" && s !== "regenerate" ? ":id" : s)).join("/")}`;
  const id = seg[1];

  // Auth
  if (route === "POST /auth/signup") {
    const b = as<SignupBody>();
    // Test trigger: this address is already registered.
    if (String(b.email).toLowerCase() === "taken@example.com") return err(409, "conflict", "An account with that email already exists.");
    state.user = { id: "u_new", name: b.name, email: String(b.email).toLowerCase(), role: b.role };
    state.loggedIn = true;
    return json(201, { user: state.user });
  }
  if (route === "POST /auth/login") {
    // Test trigger: the password "wrong-password" always fails.
    if ((body as { password?: string } | undefined)?.password === "wrong-password") return err(401, "unauthenticated", "Email or password is incorrect.");
    state.loggedIn = true;
    return json(200, { user: state.user });
  }
  if (route === "POST /auth/google") {
    const b = as<GoogleBody>();
    // Request log for verification (mock only; this module is never loaded without NEXT_PUBLIC_MOCK_API).
    const w = globalThis as unknown as { __mockGoogleLog?: { credential: unknown; role: unknown }[] };
    (w.__mockGoogleLog ??= []).push({ credential: b.credential, role: b.role });
    // Test triggers: "mock-conflict" -> 409, "mock-new-user" -> new user (422 until a role is sent),
    // "mock-credential" -> existing user, anything else -> 401.
    if (b.credential === "mock-conflict") return err(409, "conflict", "This email is already linked to another sign-in. Use the original sign-in method.");
    if (b.credential === "mock-new-user") {
      if (!b.role || !["student", "parent", "teacher"].includes(b.role)) return err(422, "role_required", "Choose a role to finish creating your account.");
      state.user = { id: "u_google_new", name: "Gina Google", email: "gina@example.com", role: b.role };
      state.loggedIn = true;
      return json(201, { user: state.user });
    }
    if (b.credential !== "mock-credential") return err(401, "unauthenticated", "Google sign-in failed. Try again.");
    state.loggedIn = true;
    return json(200, { user: state.user });
  }
  if (route === "POST /auth/logout") {
    state.loggedIn = false;
    return nothing();
  }
  if (!state.loggedIn) return err(401, "unauthenticated", "Sign in to continue.");
  if (route === "GET /me") return json(200, { user: state.user, serverTime: st() });

  // Documents
  // Test triggers (set from the browser console; this module only loads with MOCK_API):
  //   globalThis.__mockDocsFail = true         -> GET /documents answers 500 until cleared
  //   globalThis.__mockDeleteFail = ["d1"]     -> DELETE of those ids answers 500
  if (route === "GET /documents") {
    if ((globalThis as { __mockDocsFail?: boolean }).__mockDocsFail) return err(500, "internal", "Something went wrong. Try again.");
    return json(200, { documents: state.docs, serverTime: st() });
  }
  if (route === "POST /documents/upload-url") {
    if (consumeUploadFailure("presign")) return err(500, "internal", "Something went wrong. Try again.");
    const b = as<UploadUrlBody>();
    const docId = `d${++state.seq}`;
    state.pendingUploads[docId] = { name: b.name, type: b.type, sizeBytes: b.sizeBytes };
    return json(201, { docId, uploadUrl: `mock://s3/${docId}`, headers: { "Content-Type": b.type } });
  }
  if (route === "POST /documents/:id/complete") {
    if (consumeUploadFailure("complete")) return err(500, "internal", "Something went wrong. Try again.");
    const p = state.pendingUploads[id];
    if (!p) return err(409, "conflict", "The upload didn't finish. Try again.");
    const typeMap: Record<string, "PDF" | "JPG" | "PNG" | "TXT"> = {
      "application/pdf": "PDF", "image/jpeg": "JPG", "image/png": "PNG", "text/plain": "TXT",
    };
    const doc = { id, name: p.name, type: typeMap[p.type] ?? "TXT", pages: 1, sizeBytes: p.sizeBytes, createdAt: st() };
    state.docs = [doc, ...state.docs.filter((d) => d.id !== id)];
    delete state.pendingUploads[id];
    return json(200, doc);
  }
  if (route === "GET /documents/:id/file") {
    if (!state.docs.some((d) => d.id === id)) return err(404, "not_found", "That item isn't available.");
    // Request log for verification (ids only, never URLs). Fetch triggers live in ./files.
    const w = globalThis as unknown as { __mockFileLog?: string[] };
    (w.__mockFileLog ??= []).push(`presign:${id}`);
    const doc = state.docs.find((d) => d.id === id)!;
    return json(200, { url: `${mockFileUrl(doc.type)}?sig=${Date.now()}`, expiresAt: iso(Date.now() + 10 * 60_000) });
  }
  if (route === "DELETE /documents/:id") {
    if ((globalThis as { __mockDeleteFail?: string[] }).__mockDeleteFail?.includes(id)) return err(500, "internal", "Something went wrong. Try again.");
    state.docs = state.docs.filter((d) => d.id !== id);
    return nothing();
  }

  // AI
  if (route === "GET /ai/models") {
    // Test triggers (mock only): ?mockModels=fail or ?mockModels=empty on the page URL.
    const trigger = typeof window === "undefined" ? null : new URLSearchParams(window.location.search).get("mockModels");
    if (trigger === "fail") return err(500, "internal", "Something went wrong.");
    if (trigger === "empty") return json(200, { models: [], serverTime: st() });
    return json(200, { models, serverTime: st() });
  }
  if (route === "GET /ai/usage") return json(200, { serverTime: st(), ...usage });

  // Generation
  // Test triggers (mock only), read from the page URL: ?mockGen=
  //   failed | failedSources | rate | budget | file422 | model422 | network
  //   html (first generated question is the literal text <b>x</b>)
  //   regenFail (POST /generate/regenerate jobs end in failed)
  // model422 only answers 422 when a non-default model is sent, so the reset-to-default retry succeeds.
  if (route === "POST /generate") {
    const b = as<GenerateBody>();
    const trigger = typeof window === "undefined" ? null : new URLSearchParams(window.location.search).get("mockGen");
    if (trigger === "rate") return err(429, "rate_limited", "You've reached today's limit for generating exams. Try again tomorrow.");
    if (trigger === "budget") return err(429, "rate_limited", "The monthly AI budget has been used. Try again next month.");
    if (trigger === "file422") return err(422, "validation", "Biology.pdf has too many pages to use. Remove it or pick a smaller file.", { sourceIds: "Biology.pdf" });
    if (trigger === "model422" && b.model !== undefined && b.model !== models[0].id) {
      return err(422, "validation", "That model isn't available.", { model: "unknown or not enabled" });
    }
    const jobId = `j${++state.seq}`;
    state.jobs[jobId] = { polls: 0, type: b.type, counts: b.counts, model: b.model ?? models[0].id, trigger };
    const log = globalThis as unknown as { __mockGenLog?: string[] };
    (log.__mockGenLog ??= []).push(`POST:${jobId}:${b.model ?? "none"}`);
    return json(202, { jobId }, { Location: `/generate/${jobId}` });
  }
  if (route === "POST /generate/regenerate") {
    const b = as<RegenerateBody>();
    const trigger = typeof window === "undefined" ? null : new URLSearchParams(window.location.search).get("mockGen");
    const jobId = `j${++state.seq}`;
    state.jobs[jobId] = {
      polls: 0, type: b.type, counts: { [String(b.marks)]: 1 }, marks: b.marks, model: b.model ?? models[0].id,
      trigger: trigger === "regenFail" ? "failed" : null,
    };
    (globalThis as unknown as { __mockRegenLog?: unknown[] }).__mockRegenLog ??= [];
    (globalThis as unknown as { __mockRegenLog: unknown[] }).__mockRegenLog.push(b);
    return json(202, { jobId }, { Location: `/generate/${jobId}` });
  }
  if (route === "GET /generate/:id") {
    const job = state.jobs[id];
    if (!job) return err(404, "not_found", "That item isn't available.");
    job.polls++;
    if (job.trigger === "network") throw new TypeError("mock network failure");
    const entries = Object.entries(job.counts).flatMap(([m, n]) => Array(Number(n) || 0).fill(Number(m) as Marks) as Marks[]);
    const total = entries.length || 1;
    if (job.trigger === "failed" && job.polls >= 2) {
      return json(200, { status: "failed", model: job.model, progress: { done: 1, total }, questions: null,
        failedSources: [], error: "We couldn't write questions from those files. Try different files.", serverTime: st() });
    }
    // Progress advances on every poll, so the progress text visibly changes.
    const written = Math.max(0, job.polls - 1) * Math.max(1, Math.ceil(total / 4));
    if (written < total) {
      return json(200, { status: job.polls === 1 ? "queued" : "running", model: job.model,
        progress: { done: Math.min(total - 1, written), total }, questions: null, failedSources: [], error: null, serverTime: st() });
    }
    const questions = entries.map((m, i) => ({ ...makeQuestion(i + state.seq + (job.marks ? 1000 : 0), m, job.type), id: `g${id}-${i}` }));
    if (job.trigger === "html" && !job.marks && questions.length > 0) {
      questions[0].text = "<b>x</b>";
      if (questions[0].options) questions[0].options[0] = "<i>y</i>";
    }
    return json(200, { status: "done", model: job.model, progress: { done: total, total }, questions,
      failedSources: job.trigger === "failedSources" ? ["Photosynthesis.jpg"] : [], error: null, serverTime: st() });
  }
  if (route === "DELETE /generate/:id") {
    // Request log for verification (job ids only).
    const log = globalThis as unknown as { __mockGenLog?: string[] };
    (log.__mockGenLog ??= []).push(`DELETE:${id}`);
    delete state.jobs[id];
    return nothing();
  }

  // Exams
  if (route === "POST /exams") {
    const b = as<CreateExamBody>();
    // Test triggers (mock only), from the page URL: ?mockPublish=failOnce|lostReply|422|403, ?mockUnknown=a@x.com,b@y.com
    // Request log for verification: globalThis.__mockPublishLog = [{ key, body }].
    const q = typeof window === "undefined" ? new URLSearchParams() : new URLSearchParams(window.location.search);
    const trigger = q.get("mockPublish");
    const g = globalThis as unknown as { __mockPublishLog?: { key: string | null; body: unknown; id?: string }[] };
    const entry: { key: string | null; body: unknown; id?: string } =
      (resumed && g.__mockPublishLog?.[g.__mockPublishLog.length - 1]) || { key: idempotencyKey ?? null, body };
    if (!resumed) (g.__mockPublishLog ??= []).push(entry);
    const call = resumed ? state.publishCalls : ++state.publishCalls;
    // ?mockPublish=slow (3 s then succeed) or slowFail (3 s, then a network failure on the first call only).
    if (trigger?.startsWith("slow") && !resumed) {
      return new Promise((res, rej) => setTimeout(() => {
        try {
          if (trigger === "slowFail" && call === 1) throw new TypeError("mock network failure");
          res(mockFetch(method, rawPath, body, idempotencyKey, true));
        } catch (e) { rej(e); }
      }, 3000));
    }
    if (trigger === "failOnce" && call === 1) throw new TypeError("mock network failure");
    if (trigger === "422") return err(422, "validation", "The closing time must be at least 1 minute from now.", { closesAt: "too soon" });
    if (trigger === "403") return err(403, "forbidden", "You can't assign exams to that account.");
    const replay = idempotencyKey ? state.published[idempotencyKey] : undefined;
    if (replay) { entry.id = replay.id; return json(200, { ...replay, serverTime: st() }); }
    const closes = b.closesAt ? b.closesAt : iso(Date.now() + (b.closesInSeconds ?? 0) * 1000);
    const emails = b.assigneeEmails ?? [];
    const exam: MockExam = {
      id: `e${++state.seq}`, title: b.title, type: b.type, closesAt: closes,
      maxPeeks: b.maxPeeks ?? (emails.length === 0 ? 3 : 0),
      createdBy: { id: state.user.id, name: state.user.name },
      questions: b.questions.map((qq, i) => ({ ...qq, id: `q${state.seq}-${i}` })),
      mine: true, attempt: null, assigned: emails,
    };
    if (emails.length === 0) exam.attempt = { status: "not_started", answers: {}, lastQuestionIndex: 0, revealed: [], peeksLeft: exam.maxPeeks };
    state.exams.unshift(exam);
    const flagged = (q.get("mockUnknown") ?? "").split(",").map((e) => e.trim().toLowerCase()).filter(Boolean);
    const unknownEmails = emails.filter((e) => e.startsWith("unknown") || flagged.includes(e));
    entry.id = exam.id;
    if (idempotencyKey) state.published[idempotencyKey] = { id: exam.id, unknownEmails };
    if (trigger === "lostReply" && call === 1) throw new TypeError("mock network failure");
    return json(201, { id: exam.id, unknownEmails, serverTime: st() });
  }
  if (route === "GET /exams") {
    const scope = query.get("scope") ?? "open";
    // Test triggers (mock only), from the page URL: ?mockHome=empty | slow (2.5 s per list)
    // | failOpen | failClosed | failCreated (that list answers 500).
    const trigger = typeof window === "undefined" ? null : new URLSearchParams(window.location.search).get("mockHome");
    const reply = (): Response => {
      if (trigger === `fail${scope[0].toUpperCase()}${scope.slice(1)}`) return err(500, "internal", "Something went wrong.");
      if (trigger === "empty") return json(200, { serverTime: st(), exams: [] });
      for (const e of state.exams) if (e.closingDemo) e.closesAt = iso(Date.now() - 20_000);
      // ?mockHome=declLast (Biology sorts last in Open) | declOnly (it is the only Open row): focus checks.
      if (trigger === "declLast") for (const e of state.exams) if (e.id === "e1") e.closesAt = iso(Date.now() + 30 * 24 * HOUR);
      const list = state.exams.filter((e) => {
        if (e.unlisted) return false;
        if (trigger === "declOnly" && scope === "open") return e.id === "e1" && e.attempt?.status === "not_started";
        if (scope === "created") return e.mine;
        if (!e.attempt || e.attempt.status === "declined") return false;
        const isClosed = over(e) || e.attempt.status === "submitted";
        return scope === "closed" ? isClosed : !isClosed;
      });
      // created: newest first (the store prepends new exams); others: soonest deadline first.
      if (scope === "open") list.sort((a, b) => Date.parse(a.closesAt) - Date.parse(b.closesAt));
      if (scope === "closed") list.sort((a, b) => Date.parse(b.closesAt) - Date.parse(a.closesAt));
      return json(200, { serverTime: st(), exams: list.slice(0, scope === "created" ? 50 : undefined).map((e) => listItem(e, scope)) });
    };
    if (trigger === "slow") return new Promise((res) => setTimeout(() => res(reply()), 2500));
    return reply();
  }
  const exam = state.exams.find((e) => e.id === id);
  if (seg[0] === "exams" && seg.length >= 2) {
    if (!exam) return err(404, "not_found", "That item isn't available.");
    const sub = seg[2];
    if (method === "GET" && !sub) {
      // Test trigger (mock only): ?mockClose=8 closes this exam 8 s after the first load that carries the flag
      // (auto-submit at zero; the 60 s grace window follows). Sticky for the session, applied once per exam.
      const closeIn = Number(stickyFlag("mockClose"));
      const w = globalThis as unknown as { __mockCloseSet?: string[] };
      if (Number.isFinite(closeIn) && closeIn !== 0 && !(w.__mockCloseSet ??= []).includes(exam.id)) {
        w.__mockCloseSet.push(exam.id);
        exam.closesAt = iso(Date.now() + closeIn * 1000);
      }
      if (exam.closingDemo) exam.closesAt = iso(Date.now() - 20_000); // stay inside the grace window
      const vis = keyVisible(exam);
      return json(200, {
        serverTime: st(),
        exam: { id: exam.id, title: exam.title, type: exam.type, totalMarks: totalMarks(exam), closesAt: exam.closesAt,
          acceptingUntil: acceptingUntil(exam), maxPeeks: exam.maxPeeks, createdBy: exam.createdBy,
          questions: keyedQuestions(exam, vis).map((q) => { const c = { ...q }; delete c.sourceDocId; delete c.sourceDocName; return c; }) },
        attempt: exam.attempt, keyVisible: vis, acceptingUntil: acceptingUntil(exam),
      });
    }
    if (method === "PUT" && sub === "attempt") {
    const b = as<AttemptBody>();
      // Test triggers (mock only), from the page URL: ?mockAutosave=fail (500 every time) | failOnce (first
      // PUT only) | 409 (closed). Request log: globalThis.__mockAutosaveLog = [{ answers, lastQuestionIndex }].
      const t = typeof window === "undefined" ? null : new URLSearchParams(window.location.search).get("mockAutosave");
      const g = globalThis as unknown as { __mockAutosaveLog?: unknown[]; __mockAutosaveFailed?: boolean };
      (g.__mockAutosaveLog ??= []).push({ answers: b.answers, lastQuestionIndex: b.lastQuestionIndex });
      if (t === "fail") return err(500, "internal", "Something went wrong.");
      if (t === "failOnce" && !g.__mockAutosaveFailed) { g.__mockAutosaveFailed = true; return err(500, "internal", "Something went wrong."); }
      if (t === "409") return err(409, "exam_closed", "This exam is closed.");
      if (over(exam)) return err(409, "exam_closed", "This exam is closed.");
      if (!exam.attempt || exam.attempt.status === "submitted") return err(409, "conflict", "Already submitted.");
      exam.attempt = { ...exam.attempt, status: "in_progress", answers: b.answers, lastQuestionIndex: b.lastQuestionIndex, savedAt: st() };
      return json(200, { savedAt: st(), serverTime: st() });
    }
    if (method === "POST" && sub === "submit") {
    const b = as<SubmitBody>();
      // Test triggers (mock only), sticky from the page URL: ?mockSubmit=closed (409 exam_closed) | fail (network
      // failure every time) | failOnce (first call only) | slow (2 s). Log: globalThis.__mockSubmitLog = [body].
      const t = stickyFlag("mockSubmit");
      const sg = globalThis as unknown as { __mockSubmitLog?: unknown[]; __mockSubmitFailed?: boolean };
      if (!resumed) (sg.__mockSubmitLog ??= []).push(b);
      if (t === "fail") throw new TypeError("mock network failure");
      if (t === "failOnce" && !sg.__mockSubmitFailed) { sg.__mockSubmitFailed = true; throw new TypeError("mock network failure"); }
      if (t === "closed") return err(409, "exam_closed", "This exam is closed.");
      if (t === "slow" && !resumed) return new Promise((res) => setTimeout(() => res(mockFetch(method, rawPath, body, idempotencyKey, true)), 2000));
      if (!exam.attempt) return err(404, "not_found", "That item isn't available.");
      if (exam.attempt.status !== "submitted") {
        if (over(exam)) return err(409, "exam_closed", "This exam is closed.");
        if (b.answers) exam.attempt.answers = b.answers;
        if (b.lastQuestionIndex !== undefined) exam.attempt.lastQuestionIndex = b.lastQuestionIndex;
        exam.attempt.status = "submitted";
      }
      const g = grade(exam);
      return json(200, { status: "submitted", score: g.score, scoreBeforePeeks: g.before, maxScore: totalMarks(exam),
        submittedAt: st(), autoSubmitted: false, serverTime: st() });
    }
    if (method === "POST" && sub === "reveal") {
    const b = as<RevealBody>();
      if (exam.maxPeeks === 0) return err(403, "forbidden", "Peeking is off for this exam.");
      if (keyVisible(exam) || !exam.attempt) return err(409, "conflict", "The answer is already available.");
      if (over(exam)) return err(409, "exam_closed", "This exam is closed.");
      const left = exam.attempt.peeksLeft ?? 0;
      const q = exam.questions.find((x) => x.id === b.questionId);
      if (!q) return err(404, "not_found", "That item isn't available.");
      if (left <= 0) return err(409, "conflict", "No peeks left.");
      const revealed = exam.attempt.revealed ?? [];
      if (!revealed.some((r) => r.questionId === q.id)) {
        revealed.push({ questionId: q.id, correctIndex: q.correctIndex, modelAnswer: q.modelAnswer });
        exam.attempt.revealed = revealed;
        exam.attempt.peeksLeft = left - 1;
      }
      return json(200, { questionId: q.id, correctIndex: q.correctIndex, modelAnswer: q.modelAnswer,
        peeksLeft: exam.attempt.peeksLeft, serverTime: st() });
    }
    if (method === "POST" && sub === "decline") {
      // Test triggers (mock only), from the page URL: ?mockDecline=409 (the exam was started elsewhere)
      // | 404 (the exam is gone) | 403 | fail (500).
      const t = typeof window === "undefined" ? null : new URLSearchParams(window.location.search).get("mockDecline");
      if (t === "409") {
        if (exam.attempt) exam.attempt.status = "in_progress";
        return err(409, "conflict", "You've already started this exam.");
      }
      if (t === "404") { state.exams = state.exams.filter((e) => e.id !== exam.id); return err(404, "not_found", "That item isn't available."); }
      if (t === "403") return err(403, "forbidden", "You don't have access to that.");
      if (t === "fail") return err(500, "internal", "Something went wrong.");
      if (exam.attempt && exam.attempt.status !== "not_started" && exam.attempt.status !== "declined") return err(409, "conflict", "You've already started this exam.");
      if (exam.attempt) exam.attempt.status = "declined";
      return nothing();
    }
    if (method === "GET" && sub === "status") {
      // Test triggers (mock only): ?mockStatus=403|404|500|slow.
      const t = typeof window === "undefined" ? null : new URLSearchParams(window.location.search).get("mockStatus");
      const reply = (): Response => {
        // failAfterAssign: the table refetch that follows an assign fails (500), so the stale-table notice shows.
        const g = globalThis as unknown as { __mockAssigned?: boolean };
        if (t === "500" || (t === "failAfterAssign" && g.__mockAssigned)) return err(500, "internal", "Something went wrong.");
        if (t === "404") return err(404, "not_found", "That item isn't available.");
        if (!exam.mine || t === "403") return err(403, "forbidden", "You don't have access to that.");
        const rows = rosterOf(exam);
        return json(200, {
          serverTime: st(),
          exam: { id: exam.id, title: exam.title, closesAt: exam.closesAt, totalMarks: totalMarks(exam) },
          counts: rosterCounts(exam),
          attempts: rows.map((r) => ({ ...r })),
        });
      };
      if (t === "slow") return new Promise((res) => setTimeout(() => res(reply()), 2500));
      return reply();
    }
    if (method === "POST" && sub === "assign") {
      const b = as<AssignBody>();
      // Test trigger (mock only): ?mockAssign=403 | 422.
      const t = typeof window === "undefined" ? null : new URLSearchParams(window.location.search).get("mockAssign");
      if (!exam.mine || t === "403") return err(403, "forbidden", "You can't assign exams to that account.");
      if (over(exam)) return err(409, "exam_closed", "This exam is closed.");
      if (t === "422") return err(422, "validation", "That email address isn't valid.", { emails: "invalid" });
      const roster = rosterOf(exam);
      const emails = [...new Set((b.emails ?? []).map((x) => String(x).trim().toLowerCase()))];
      // Unknown: addresses starting "unknown", and (student creators) addresses starting "teacher" (not a student account).
      const unknown = emails.filter((x) => x.startsWith("unknown") || (state.user.role === "student" && x.startsWith("teacher")));
      const known = emails.filter((x) => !unknown.includes(x));
      const already = known.filter((x) => roster.some((r) => r.email === x));
      const assigned = known.filter((x) => !already.includes(x));
      const cap = state.user.role === "student" ? 10 : 50;
      if (roster.length + assigned.length > cap) return err(422, "validation", `You can assign up to ${cap} people in total.`, { emails: "over cap" });
      (globalThis as unknown as { __mockAssigned?: boolean }).__mockAssigned = true;
      for (const x of assigned) roster.push({ userId: `u_${x}`, name: titleCase(x.split("@")[0]), email: x, status: "not_started" });
      exam.assigned.push(...assigned);
      return json(200, { assigned, alreadyAssigned: already, unknown });
    }
    if (method === "GET" && sub === "result") {
      if (!exam.attempt) return err(404, "not_found", "That item isn't available.");
      const isOver = over(exam);
      if (exam.attempt.status !== "submitted" && !isOver) return err(409, "conflict", "Submit the exam to see results.");
      const vis = keyVisible(exam);
      const never = exam.attempt.status !== "submitted";
      const g = never ? { score: null, before: null } : grade(exam);
      const revealed = new Map((exam.attempt.revealed ?? []).map((r) => [r.questionId, r]));
      const questions: ResultQuestion[] = exam.questions.map((q) => {
        const yours = never ? "" : exam.attempt!.answers[q.id] ?? "";
        const r: ResultQuestion = { id: q.id, marks: q.marks, text: q.text, options: q.options, yourAnswer: yours, revealed: revealed.has(q.id) };
        if (vis) {
          r.correct = q.options ? yours === String(q.correctIndex) : undefined;
          r.correctAnswer = q.options ? String(q.correctIndex) : undefined;
          r.modelAnswer = q.modelAnswer ?? null;
        } else if (revealed.has(q.id)) {
          r.correctAnswer = q.options ? String(q.correctIndex) : undefined;
          r.modelAnswer = q.modelAnswer ?? null;
        }
        return r;
      });
      return json(200, {
        serverTime: st(),
        exam: { id: exam.id, title: exam.title, type: exam.type, totalMarks: totalMarks(exam), closesAt: exam.closesAt,
          acceptingUntil: acceptingUntil(exam), maxPeeks: exam.maxPeeks },
        status: never ? "missed" : "submitted",
        score: g.score, scoreBeforePeeks: g.before, maxScore: totalMarks(exam),
        peeksUsed: exam.attempt.revealed?.length ?? 0,
        keyVisible: vis, keyAvailableAt: vis ? null : acceptingUntil(exam),
        questions,
      });
    }
  }
  return err(404, "not_found", "That item isn't available.");
}
