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
}
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
      attempt: null, assigned: ["a@x.com", "b@x.com"] },
  ];
}
seedExams();

const json = (status: number, body: unknown, headers: Record<string, string> = {}) =>
  new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json", ...headers } });
const err = (status: number, code: string, message: string, fields?: Record<string, string>) =>
  json(status, { error: { code, message, fields } });
const nothing = () => new Response(null, { status: 204 });
const st = () => iso(Date.now());

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

function listItem(e: MockExam, scope: string): ExamListItem {
  const base: ExamListItem = {
    id: e.id, title: e.title, createdBy: e.createdBy, closesAt: e.closesAt,
    acceptingUntil: acceptingUntil(e), totalMarks: totalMarks(e), type: e.type,
  };
  if (scope === "created") {
    const n = e.assigned.length;
    return { ...base, counts: { submitted: 1, inProgress: 0, notStarted: Math.max(0, n - 1), declined: 0 } };
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
    const list = state.exams.filter((e) => {
      if (scope === "created") return e.mine;
      if (!e.attempt) return false;
      const isClosed = over(e) || e.attempt.status === "submitted";
      return scope === "closed" ? isClosed : !isClosed;
    });
    return json(200, { serverTime: st(), exams: list.map((e) => listItem(e, scope)) });
  }

  const exam = state.exams.find((e) => e.id === id);
  if (seg[0] === "exams" && seg.length >= 2) {
    if (!exam) return err(404, "not_found", "That item isn't available.");
    const sub = seg[2];
    if (method === "GET" && !sub) {
      const vis = keyVisible(exam);
      return json(200, {
        serverTime: st(),
        exam: { id: exam.id, title: exam.title, type: exam.type, totalMarks: totalMarks(exam), closesAt: exam.closesAt,
          acceptingUntil: acceptingUntil(exam), maxPeeks: exam.maxPeeks, createdBy: exam.createdBy,
          questions: keyedQuestions(exam, vis).map((q) => { const c = { ...q }; delete c.sourceDocId; delete c.sourceDocName; return c; }) },
        attempt: exam.attempt, keyVisible: vis,
      });
    }
    if (method === "PUT" && sub === "attempt") {
    const b = as<AttemptBody>();
      if (over(exam)) return err(409, "exam_closed", "This exam is closed.");
      if (!exam.attempt || exam.attempt.status === "submitted") return err(409, "conflict", "Already submitted.");
      exam.attempt = { ...exam.attempt, status: "in_progress", answers: b.answers, lastQuestionIndex: b.lastQuestionIndex, savedAt: st() };
      return json(200, { savedAt: st(), serverTime: st() });
    }
    if (method === "POST" && sub === "submit") {
    const b = as<SubmitBody>();
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
      if (exam.attempt && exam.attempt.status !== "not_started") return err(409, "conflict", "You've already started this exam.");
      state.exams = state.exams.filter((e) => e.id !== exam.id);
      return nothing();
    }
    if (method === "GET" && sub === "status") {
      if (!exam.mine) return err(403, "forbidden", "You don't have access to that.");
      return json(200, {
        serverTime: st(),
        exam: { id: exam.id, title: exam.title, closesAt: exam.closesAt, totalMarks: totalMarks(exam) },
        counts: { submitted: 1, inProgress: 1, notStarted: 1, declined: 1 },
        attempts: [
          { userId: "u1", name: "Aisha K.", email: "aisha@x.com", status: "submitted", score: 34, submittedAt: st() },
          { userId: "u2", name: "Ben T.", email: "ben@x.com", status: "in_progress" },
          { userId: "u3", name: "Chen L.", email: "chen@x.com", status: "not_started" },
          { userId: "u4", name: "Dev P.", email: "dev@x.com", status: "declined" },
        ],
      });
    }
    if (method === "POST" && sub === "assign") {
    const b = as<AssignBody>();
      if (!exam.mine) return err(403, "forbidden", "You don't have access to that.");
      if (over(exam)) return err(409, "exam_closed", "This exam is closed.");
      const emails = b.emails ?? [];
      const unknown = emails.filter((e) => e.startsWith("unknown"));
      const known = emails.filter((e) => !unknown.includes(e));
      const already = known.filter((e) => exam.assigned.includes(e));
      const assigned = known.filter((e) => !already.includes(e));
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
