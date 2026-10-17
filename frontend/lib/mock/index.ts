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
  /** Oldest first; empty for a creator who has not taken their own exam. The last one is the latest attempt. */
  attempts: MockAttempt[]; assigned: string[];
  /** Status-page rows, built lazily from `assigned` (see rosterOf). */
  roster?: RosterRow[];
  /** Mock only: closesAt is re-set to 20 s ago on each list call so the "Closing…" row stays visible. */
  closingDemo?: boolean;
  /** Mock only: reachable by URL (take-screen fixtures) but never listed on Home. */
  unlisted?: boolean;
}
/** An attempt plus the submit time the API reports in `attempts[]` / `history[]` (never sent inside `attempt`). */
type MockAttempt = Attempt & { submittedAt?: string; autoSubmitted?: boolean };
interface RosterRow {
  userId: string; name: string; email: string; status: "not_started" | "in_progress" | "submitted" | "declined";
  score?: number; submittedAt?: string; attemptCount: number;
  history: { attemptNo: number; status: "not_started" | "in_progress" | "submitted" | "declined"; score?: number; submittedAt?: string; autoSubmitted: boolean }[];
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
      attempts: [{ attemptNo: 1, status: "not_started", answers: {}, lastQuestionIndex: 0, revealed: [], peeksLeft: 3 }], assigned: [] },
    { id: "e2", title: "Free text: Photosynthesis", type: "free", closesAt: iso(now + 8 * 24 * HOUR), maxPeeks: 0,
      createdBy: rao, questions: mk(2, "free"), mine: false,
      attempts: [{ attemptNo: 1, status: "in_progress", answers: { q21: "Light reaction" }, lastQuestionIndex: 0 }], assigned: [] },
    { id: "e3", title: "Last week's quiz", type: "mcq", closesAt: iso(now - 3 * 24 * HOUR), maxPeeks: 0,
      createdBy: rao, questions: mk(3, "mcq"), mine: false,
      attempts: [{ attemptNo: 1, status: "submitted", answers: { q31: "Option B", q32: "Option A", q33: "Option C", q34: "Option D" }, lastQuestionIndex: 3 }], assigned: [] },
    { id: "e4", title: "My class test", type: "mcq", closesAt: iso(now + 2 * 24 * HOUR), maxPeeks: 0,
      createdBy: { id: demoUser.id, name: demoUser.name }, questions: mk(4, "mcq"), mine: true,
      attempts: [], assigned: ["aisha@x.com", "ben@x.com", "chen@x.com", "dev@x.com"] },
    { id: "e5", title: "Closing now: Cell division", type: "mcq", closesAt: iso(now - 20_000), maxPeeks: 0,
      createdBy: rao, questions: mk(5, "mcq"), mine: false, closingDemo: true,
      attempts: [{ attemptNo: 1, status: "in_progress", answers: {}, lastQuestionIndex: 0 }], assigned: [] },
    { id: "e6", title: "Missed: Chemistry basics", type: "mcq", closesAt: iso(now - 5 * 24 * HOUR), maxPeeks: 0,
      createdBy: rao, questions: mk(6, "mcq"), mine: false,
      attempts: [{ attemptNo: 1, status: "not_started", answers: {}, lastQuestionIndex: 0 }], assigned: [] },
    { id: "e7", title: "Essay: Ecosystems", type: "free", closesAt: iso(now - 24 * HOUR), maxPeeks: 0,
      createdBy: { id: demoUser.id, name: demoUser.name }, questions: mk(7, "free"), mine: true,
      attempts: [{ attemptNo: 1, status: "submitted", answers: {}, lastQuestionIndex: 0 }], assigned: ["aisha@x.com"] },
    { id: "e8", title: "Declined: Geography", type: "mcq", closesAt: iso(now + 3 * HOUR), maxPeeks: 0,
      createdBy: rao, questions: mk(8, "mcq"), mine: false,
      attempts: [{ attemptNo: 1, status: "declined", answers: {}, lastQuestionIndex: 0 }], assigned: [] },
    { id: "e9", title: "Not started, closes in 20 minutes", type: "mcq", closesAt: iso(now + 20 * 60_000), maxPeeks: 0,
      createdBy: { id: demoUser.id, name: demoUser.name }, questions: mk(9, "mcq"), mine: true,
      attempts: [{ attemptNo: 1, status: "not_started", answers: {}, lastQuestionIndex: 0 }], assigned: [] },
    { id: "e10", title: "No assignees yet", type: "mcq", closesAt: iso(now + 4 * 24 * HOUR), maxPeeks: 0,
      createdBy: { id: demoUser.id, name: demoUser.name }, questions: mk(10, "mcq"), mine: true,
      attempts: [], assigned: [] },
  ];
}

/**
 * Take-screen fixtures (unlisted; open by URL).
 *   /exams/e11/take  12 questions, mixed MCQ and free text (the real backend keeps one type per exam; the UI
 *                    decides per question by `options`), resume state (answers + last index 4), a question
 *                    whose text and options are `<b>x</b>` markup, and a deadline tomorrow 6:00 PM local.
 *   /exams/e12/take  30 MCQ questions, never started (dots wrap at 375 px).
 *   /exams/e19/take  peeks demo (MCQ, 2 peeks); /exams/e20/take free text, 1 peek. Mock e1 allows 3 peeks.
 *                    Flag ?mockReveal=403|409|closed|fail|slow (see the reveal route).
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
      attempts: [{ attemptNo: 1, status: "in_progress", answers: { t1: "Answer 1B", t2: "The light reaction makes ATP.", t4: "Because it is.", t5: "Answer 5D" }, lastQuestionIndex: 4, savedAt: iso(Date.now() - 600_000) }],
      assigned: [] },
    { id: "e12", title: "Take demo: 30 questions", type: "mcq", closesAt: iso(Date.now() + 2 * 24 * HOUR), maxPeeks: 0,
      createdBy: rao, questions: Array.from({ length: 30 }, (_, i) => ({ ...makeQuestion(1200 + i, ((i % 5) + 1) as Marks, "mcq"), id: `m${i + 1}` })), mine: false, unlisted: true,
      attempts: [{ attemptNo: 1, status: "not_started", answers: {}, lastQuestionIndex: 0 }], assigned: [] },
    { id: "e13", title: "Take demo: 2 questions", type: "mcq", closesAt: iso(Date.now() + 2 * 24 * HOUR), maxPeeks: 0,
      createdBy: rao, questions: Array.from({ length: 2 }, (_, i) => ({ ...makeQuestion(1300 + i, 1 as Marks, "mcq"), id: `w${i + 1}` })), mine: false, unlisted: true,
      attempts: [{ attemptNo: 1, status: "not_started", answers: {}, lastQuestionIndex: 0 }], assigned: [] },
    // Peeks demos (also the mock e1): 2 peeks allowed, markup must stay literal.
    { id: "e19", title: "Peeks demo: multiple choice", type: "mcq", closesAt: iso(Date.now() + 2 * 24 * HOUR), maxPeeks: 2,
      createdBy: rao, questions: [
        { id: "k1", marks: 2, text: "Which part makes ATP?", options: ["Nucleus", "<b>Mitochondria</b>", "Ribosome", "Golgi"], correctIndex: 1 },
        { id: "k2", marks: 2, text: "Second question", options: ["One", "Two", "Three", "Four"], correctIndex: 2 },
        { id: "k3", marks: 1, text: "Third question", options: ["Red", "Green", "Blue", "Black"], correctIndex: 0 },
      ], mine: false, unlisted: true,
      attempts: [{ attemptNo: 1, status: "not_started", answers: {}, lastQuestionIndex: 0, revealed: [], peeksLeft: 2 }], assigned: [] },
    { id: "e20", title: "Peeks demo: free text", type: "free", closesAt: iso(Date.now() + 2 * 24 * HOUR), maxPeeks: 1,
      createdBy: rao, questions: [
        { id: "k4", marks: 3, text: "Explain photosynthesis.", modelAnswer: "<i>Light</i> energy becomes chemical energy.\nIt happens in chloroplasts." },
        { id: "k5", marks: 2, text: "What is osmosis?", modelAnswer: "Water moves across a membrane." },
      ], mine: false, unlisted: true,
      attempts: [{ attemptNo: 1, status: "not_started", answers: {}, lastQuestionIndex: 0, revealed: [], peeksLeft: 1 }], assigned: [] },
  );
}
/**
 * Result-page fixtures (unlisted; open by URL). Page-URL flags (?mockResult=...) apply to e14:
 *   /exams/e14/result                       8 x 5-mark MCQ, closed, key visible, 6 correct (score 30 / 40)
 *   /exams/e14/result?mockResult=hidden     key hidden (keyAvailableAt set, no correct/correctAnswer fields)
 *   /exams/e14/result?mockResult=leak       key hidden, but the response (wrongly) still carries the key fields
 *   /exams/e14/result?mockResult=fractional score 27.5 / 40
 *   /exams/e14/result?mockResult=unfinished 409 "Submit the exam to see results."
 *   /exams/e15/result                       free text, closed, key visible, score null (self-check)
 *   /exams/e15/result?mockResult=hidden     free text with the key hidden
 *   /exams/e21/result                       MCQ, 3 peeks allowed, 2 revealed (r2, r4): "Score 25 / 40", before the penalty 30 / 40
 *   /exams/e22/result                       MCQ, 3 peeks allowed, none used ("No peeks" badge)
 *   /exams/e23/result                       free text, 1 peek allowed, x2 revealed (self-check scores it at half marks)
 *   (e21 to e23 accept the same ?mockResult flags as e14)
 */
function seedResultFixtures() {
  const mcq = Array.from({ length: 8 }, (_, i) => ({ ...makeQuestion(1400 + i, 5 as Marks, "mcq"), id: `r${i + 1}` }));
  mcq[1] = { ...mcq[1], text: "<b>x</b> stays literal", options: ["<b>x</b>", "<i>y</i>", "Option C", "Option D"], correctIndex: 0 };
  const answers: Record<string, string> = {};
  mcq.forEach((q, i) => { if (i === 7) return; answers[q.id] = q.options![i < 6 ? q.correctIndex! : (q.correctIndex! + 1) % 4]; });
  const free: Question[] = [2, 3, 2, 3].map((m, i) => ({
    ...makeQuestion(1500 + i, m as Marks, "free"), id: `x${i + 1}`,
    ...(i === 1 ? { text: "<b>Explain</b> photosynthesis.", modelAnswer: "<i>Light</i> energy becomes chemical energy." } : {}),
  }));
  const freeAnswers: Record<string, string> = { x1: "Plants make food.", x2: "Sunlight makes sugar.", x3: "" , x4: "Because of osmosis." };
  const peekMcq = (n: number) => mcq.map((q, i) => ({ ...q, id: `${n}r${i + 1}` }));
  const peekAnswers = (qs: Question[]) => {
    const a: Record<string, string> = {};
    qs.forEach((q, i) => { if (i !== 7) a[q.id] = q.options![i < 6 ? q.correctIndex! : (q.correctIndex! + 1) % 4]; });
    return a;
  };
  const used = peekMcq(21), none = peekMcq(22);
  const revealedOf = (q: Question) => ({ questionId: q.id, correctIndex: q.correctIndex, modelAnswer: q.modelAnswer });
  state.exams.push(
    { id: "e21", title: "Result demo: peeks used", type: "mcq", closesAt: iso(Date.now() - 2 * 24 * HOUR), maxPeeks: 3,
      createdBy: rao, questions: used, mine: false, unlisted: true,
      attempts: [{ attemptNo: 1, status: "submitted", answers: peekAnswers(used), lastQuestionIndex: 7, revealed: [revealedOf(used[1]), revealedOf(used[3])], peeksLeft: 1 }], assigned: [] },
    { id: "e22", title: "Result demo: no peeks", type: "mcq", closesAt: iso(Date.now() - 2 * 24 * HOUR), maxPeeks: 3,
      createdBy: rao, questions: none, mine: false, unlisted: true,
      attempts: [{ attemptNo: 1, status: "submitted", answers: peekAnswers(none), lastQuestionIndex: 7, revealed: [], peeksLeft: 3 }], assigned: [] },
    { id: "e23", title: "Result demo: free text with a peek", type: "free", closesAt: iso(Date.now() - 2 * 24 * HOUR), maxPeeks: 1,
      createdBy: rao, questions: free.map((q, i) => ({ ...q, id: `${"y"}${i + 1}` })), mine: false, unlisted: true,
      attempts: [{ attemptNo: 1, status: "submitted", answers: { y1: "Plants make food.", y2: "Sunlight makes sugar.", y3: "", y4: "Because of osmosis." }, lastQuestionIndex: 3, revealed: [{ questionId: "y2", modelAnswer: free[1].modelAnswer }], peeksLeft: 0 }], assigned: [] },
    { id: "e14", title: "Result demo: multiple choice", type: "mcq", closesAt: iso(Date.now() - 2 * 24 * HOUR), maxPeeks: 0,
      createdBy: rao, questions: mcq, mine: false, unlisted: true,
      attempts: [{ attemptNo: 1, status: "submitted", answers, lastQuestionIndex: 7 }], assigned: [] },
    { id: "e15", title: "Result demo: free text", type: "free", closesAt: iso(Date.now() - 2 * 24 * HOUR), maxPeeks: 0,
      createdBy: rao, questions: free, mine: false, unlisted: true,
      attempts: [{ attemptNo: 1, status: "submitted", answers: freeAnswers, lastQuestionIndex: 3 }], assigned: [] },
  );
}
/**
 * Print-view fixtures (unlisted; open by URL, created by the demo user so the key is visible by default).
 * Page-URL flag (?mockKey=...) applies to e16 to e18:
 *   /exams/e16/print                60 MCQ questions of 1 to 5 marks (one is `<b>x</b>` markup, one very long)
 *   /exams/e17/print                free text, 1 to 5 marks, multi-line and very long text, `<b>x</b>` model answer
 *   /exams/e18/print                small MCQ exam
 *   /exams/e16/print?mockKey=hidden key hidden (nothing key-related in the payload, available after tomorrow)
 *   /exams/e16/print?mockKey=leak   key hidden, but the payload (wrongly) still carries the key fields
 */
function seedPrintFixtures() {
  const long = "A very long question that keeps going so that it must wrap across several lines on the printed page and never be clipped at the edge of the sheet: " + "lorem ipsum dolor sit amet ".repeat(12);
  const sixty: Question[] = Array.from({ length: 60 }, (_, i) => ({ ...makeQuestion(1600 + i, ((i % 5) + 1) as Marks, "mcq"), id: `p${i + 1}` }));
  sixty[1] = { ...sixty[1], text: "<b>x</b> stays literal", options: ["<b>x</b>", "<i>y</i>", "Option C", "Option D"], correctIndex: 0 };
  sixty[6] = { ...sixty[6], text: long };
  const free: Question[] = [1, 2, 3, 4, 5, 3, 2, 5].map((m, i) => ({ ...makeQuestion(1700 + i, m as Marks, "free"), id: `f${i + 1}` }));
  free[1] = { ...free[1], text: "<b>Explain</b> osmosis.\nGive one example.", modelAnswer: "<i>Water</i> moves across a membrane." };
  free[2] = { ...free[2], text: long };
  const make = (id: string, title: string, type: ExamType, questions: Question[]): MockExam => ({
    id, title, type, closesAt: iso(Date.now() + 2 * 24 * HOUR), maxPeeks: 0,
    createdBy: { id: demoUser.id, name: demoUser.name }, questions, mine: true, unlisted: true, attempts: [], assigned: [],
  });
  state.exams.push(
    make("e16", "Print demo: 60 questions", "mcq", sixty),
    make("e17", "Print demo: free text", "free", free),
    make("e18", "Print demo: small multiple choice", "mcq", Array.from({ length: 5 }, (_, i) => ({ ...makeQuestion(1800 + i, ((i % 3) + 1) as Marks, "mcq"), id: `s${i + 1}` }))),
  );
}
/**
 * Retake fixture, listed under Closed with Retake (the exam is still open, two submitted attempts, the better one first):
 *   /exams/e24/result                 latest submitted attempt (2) with the Attempts table; View attempt 1 loads ?attempt=1
 *   ?mockRetake=closed|limit|fail     what the Retake button hits (sticky; clear with ?mockRetake=)
 * Also e1 on Home once it has been submitted, and a practice exam you create and submit.
 */
function seedRetakeFixtures() {
  const qs = [1, 1, 2, 3].map((m, k) => makeQuestion(2400 + k, m as Marks, "mcq")).map((q, k) => ({ ...q, id: `r${k + 1}` }));
  const answers = (right: number) => Object.fromEntries(qs.map((q, k) => [q.id, q.options![k < right ? q.correctIndex! : (q.correctIndex! + 1) % 4]]));
  state.exams.push({
    id: "e24", title: "Retake demo: Cell biology", type: "mcq", closesAt: iso(Date.now() + 3 * 24 * HOUR), maxPeeks: 0,
    createdBy: rao, questions: qs, mine: false, assigned: [],
    attempts: [
      { attemptNo: 1, status: "submitted", answers: answers(2), lastQuestionIndex: 3, submittedAt: iso(Date.now() - 5 * HOUR) },
      { attemptNo: 2, status: "submitted", answers: answers(4), lastQuestionIndex: 3, submittedAt: iso(Date.now() - 2 * HOUR) },
    ],
  });
}
seedExams();
seedTakeFixtures();
seedResultFixtures();
seedPrintFixtures();
seedRetakeFixtures();

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
/** The latest attempt (the one take, autosave, submit, reveal and decline act on); null when the caller has none. */
const latest = (e: MockExam): MockAttempt | null => e.attempts[e.attempts.length - 1] ?? null;
const MAX_ATTEMPTS = 50;
/** bs 9.2: only the latest attempt can be unfinished; past the grace window an in_progress one is finalized on read into a submitted one (its answers kept). */
function finalizeOnRead(e: MockExam) {
  const l = latest(e);
  if (l?.status === "in_progress" && over(e)) { l.status = "submitted"; l.submittedAt ??= acceptingUntil(e); l.autoSubmitted = true; }
}
/** bs 9.4: visible once over; else for the creator unless they are mid-way through their own latest attempt (a row that is not submitted). */
const keyVisible = (e: MockExam) => over(e) || (e.mine && (latest(e) === null || latest(e)?.status === "submitted"));
/** Seeds carry no submit time: a submitted attempt reads as submitted an hour per attempt before the deadline. */
const submittedAtOf = (e: MockExam, a: MockAttempt) => a.submittedAt ?? iso(Date.parse(e.closesAt) - (e.attempts.length - a.attemptNo + 1) * HOUR);
/** The attempt object the exam GET sends: no mock-only fields. */
const publicAttempt = (a: MockAttempt | null): Attempt | null => {
  if (!a) return null;
  const rest: MockAttempt = { ...a };
  delete rest.submittedAt;
  delete rest.autoSubmitted;
  return rest;
};
/** MCQ answers are stored as the option text (bs 9.3: exact match with options[correctIndex]). */
const isRight = (q: Question, a: string | undefined) =>
  a !== undefined && q.correctIndex !== undefined && a === q.options?.[q.correctIndex];
const totalMarks = (e: MockExam) => e.questions.reduce((s, q) => s + q.marks, 0);

function grade(e: MockExam, a: MockAttempt | null = latest(e)): { score: number | null; before: number | null } {
  if (e.type !== "mcq" || !a) return { score: null, before: null };
  let before = 0;
  for (const q of e.questions) if (isRight(q, a.answers[q.id])) before += q.marks;
  // A correct answer that was revealed earns half its marks (bs 9.3).
  let score = before;
  for (const r of a.revealed ?? []) {
    const q = e.questions.find((x) => x.id === r.questionId);
    if (q && isRight(q, a.answers[q.id])) score -= q.marks / 2;
  }
  return { before, score };
}
/** Highest-scoring submitted attempt (the earliest on a tie); undefined for free text. */
function best(e: MockExam): { attempt: MockAttempt; score: number } | undefined {
  let out: { attempt: MockAttempt; score: number } | undefined;
  for (const a of e.attempts) {
    if (a.status !== "submitted") continue;
    const sc = grade(e, a).score;
    if (sc !== null && (!out || sc > out.score)) out = { attempt: a, score: sc };
  }
  return out;
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
      const row: RosterRow = { userId: `u_${email}`, name, email, status, attemptCount: 1, history: [{ attemptNo: 1, status, autoSubmitted: false }] };
      if (status === "submitted") {
        // The first submitted student retook twice: best is the second attempt, not the latest. Marks and times only.
        const scores = e.type === "mcq" ? (i === 0 ? [marks - 4, marks - 1.5, marks - 3] : [marks - 1.5]).map((x) => Math.max(0, x)) : [];
        const n = i === 0 ? 3 : 1;
        row.attemptCount = n;
        row.history = Array.from({ length: n }, (_, k) => ({
          attemptNo: k + 1, status: "submitted" as const, score: scores[k], submittedAt: iso(Date.now() - (i + 1 + (n - k - 1) * 2) * HOUR), autoSubmitted: false,
        }));
        if (scores.length > 0) {
          const top = scores.indexOf(Math.max(...scores));
          row.score = scores[top];
          row.submittedAt = row.history[top].submittedAt;
        } else row.submittedAt = row.history[n - 1].submittedAt;
      }
      return row;
    });
    if (rows.length === 0 && latest(e)) return [practiceRow(e)]; // live: follows the creator's own attempts
    e.roster = rows;
  }
  return e.roster;
}
/** A practice exam lists the creator's own attempts: best score, count and history, like any other student. */
function practiceRow(e: MockExam): RosterRow {
  const l = latest(e) as MockAttempt;
  const b = best(e);
  const anySubmitted = e.attempts.some((a) => a.status === "submitted");
  const status = (anySubmitted ? "submitted" : l.status) as RosterRow["status"];
  const history = e.attempts.map((a) => {
    const sc = a.status === "submitted" ? grade(e, a).score : null;
    return { attemptNo: a.attemptNo, status: a.status as RosterRow["status"], ...(sc !== null ? { score: sc } : {}),
      ...(a.status === "submitted" ? { submittedAt: submittedAtOf(e, a) } : {}), autoSubmitted: a.autoSubmitted ?? false };
  });
  const shown = b?.attempt ?? [...e.attempts].reverse().find((a) => a.status === "submitted");
  return { userId: e.createdBy.id, name: e.createdBy.name, email: state.user.email, status, attemptCount: e.attempts.length, history,
    ...(b ? { score: b.score } : {}), ...(shown ? { submittedAt: submittedAtOf(e, shown) } : {}) };
}
function rosterCounts(e: MockExam) {
  const r = rosterOf(e);
  const n = (s: RosterRow["status"]) => r.filter((x) => x.status === s).length;
  return { submitted: n("submitted"), inProgress: n("in_progress"), notStarted: n("not_started"), declined: n("declined") };
}

function listItem(e: MockExam, scope: string): ExamListItem {
  finalizeOnRead(e);
  const base: ExamListItem = {
    id: e.id, title: e.title, createdBy: e.createdBy, closesAt: e.closesAt,
    acceptingUntil: acceptingUntil(e), totalMarks: totalMarks(e), type: e.type,
  };
  if (scope === "created") {
    return { ...base, counts: rosterCounts(e) };
  }
  const l = latest(e);
  const anySubmitted = e.attempts.some((a) => a.status === "submitted");
  // bs 8.5: "submitted" if any attempt is, except an open item mid-retake, which reports its unfinished latest attempt.
  const derived = (over(e) && l?.status === "not_started" ? "missed" : l?.status) as AttemptStatus;
  const status = anySubmitted && !(scope === "open" && l?.status !== "submitted") ? "submitted" : derived;
  const b = best(e);
  return {
    ...base, attemptStatus: status, attemptCount: e.attempts.length,
    canRetake: l?.status === "submitted" && !over(e) && e.attempts.length < MAX_ATTEMPTS,
    ...(b ? { score: b.score, maxScore: totalMarks(e) } : {}),
  };
}

function keyedQuestions(e: MockExam, visible: boolean): Question[] {
  const revealed = new Map((latest(e)?.revealed ?? []).map((r) => [r.questionId, r]));
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
  // Test trigger (mock only, sticky): ?mockDown=1 makes every request except GET /me fail like a dropped network
  // (reads show "Can't reach the server. Retrying…" then Retry; writes toast and keep input). ?mockDown= clears it.
  if (stickyFlag("mockDown") && path !== "/me") throw new TypeError("mock network failure");
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
  if (route === "GET /ai/usage") {
    // Test triggers (mock only), read on every poll so they can change without a reload:
    //   ?mockUsage=empty  no jobs this month     ?mockUsage=warn  spend at 83%     ?mockUsage=full  spend at 100%
    //   ?mockUsage=fail   500                    ?mockUsage=slow  answers after 8 s
    //   ?mockUsage=leak   normal numbers, but the payload (wrongly) carries user, file and question text
    // Every answer is logged in globalThis.__mockUsageLog (request count for the hidden-tab check).
    const flag = stickyFlag("mockUsage");
    const g = globalThis as unknown as { __mockUsageLog?: number[] };
    (g.__mockUsageLog ??= []).push(Date.now());
    if (flag === "fail") return err(500, "internal", "Something went wrong.");
    const body = () => {
      const z = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 };
      if (flag === "empty") {
        return { serverTime: st(), month: { key: "2026-10", spendUsd: 0, budgetUsd: 15, jobs: 0, calls: 0, tokens: z },
          today: { jobs: 0, calls: 0, spendUsd: 0, tokens: z }, byModel: [], running: 0, recent: [] };
      }
      const spend = flag === "full" ? 15 : flag === "warn" ? 12.45 : null;
      const u = spend === null ? usage : { ...usage, month: { ...usage.month, spendUsd: spend } };
      if (flag === "leak") {
        return { serverTime: st(), ...u, ownerName: "Leaky Owner",
          recent: u.recent.map((j) => ({ ...j, userName: "Leaky User", fileName: "Leaky.pdf", questionText: "Leaky question?" })) };
      }
      return { serverTime: st(), ...u };
    };
    if (flag === "slow") return new Promise<Response>((r) => setTimeout(() => r(json(200, body())), 8000));
    return json(200, body());
  }

  // Generation
  // Test triggers (mock only), read from the page URL: ?mockGen=
  //   failed | failedSources | rate | budget | file422 | range422 | model422 | network
  //   (range422: always answers 422 with fields.pageRanges, like an out-of-range request)
  //   html (first generated question is the literal text <b>x</b>)
  //   regenFail (POST /generate/regenerate jobs end in failed)
  // model422 only answers 422 when a non-default model is sent, so the reset-to-default retry succeeds.
  if (route === "POST /generate") {
    const b = as<GenerateBody>();
    const trigger = typeof window === "undefined" ? null : new URLSearchParams(window.location.search).get("mockGen");
    if (trigger === "rate") return err(429, "rate_limited", "You've reached today's limit for generating exams. Try again tomorrow.");
    if (trigger === "budget") return err(429, "rate_limited", "The monthly AI budget has been used. Try again next month.");
    if (trigger === "file422") return err(422, "validation", "Biology.pdf has too many pages to use. Remove it or pick a smaller file.", { sourceIds: "Biology.pdf" });
    if (trigger === "range422") {
      return err(422, "validation", "Textbook.pdf has a page range outside the file. Choose pages from 1 to 300.", { pageRanges: "Textbook.pdf" });
    }
    // Like the real backend (backend-spec 8.4): keys in sourceIds, PDFs only, 1 <= from <= to <= pages.
    for (const [rid, r] of Object.entries(b.pageRanges ?? {})) {
      const doc = state.docs.find((d) => d.id === rid);
      const name = doc?.name ?? "A file";
      const ok = doc !== undefined && doc.type === "PDF" && b.sourceIds.includes(rid) && r !== null && typeof r === "object"
        && Number.isInteger(r.from) && Number.isInteger(r.to) && r.from >= 1 && r.from <= r.to && r.to <= doc.pages;
      if (!ok) return err(422, "validation", `${name} has a page range that can't be used. Choose pages within the file.`, { pageRanges: name });
    }
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
      failedSources: job.trigger === "failedSources" ? [{ docId: "doc-photosynthesis", name: "Photosynthesis.jpg", reason: "unreadable" }] : [], error: null, serverTime: st() });
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
      mine: true, attempts: [], assigned: emails,
    };
    if (emails.length === 0) exam.attempts = [{ attemptNo: 1, status: "not_started", answers: {}, lastQuestionIndex: 0, revealed: [], peeksLeft: exam.maxPeeks }];
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
        if (trigger === "declOnly" && scope === "open") return e.id === "e1" && latest(e)?.status === "not_started";
        if (scope === "created") return e.mine;
        const l = latest(e);
        if (!l || l.status === "declined") return false;
        // A retake in progress is open again (Resume); a submitted latest attempt stays Closed, with Retake.
        const isClosed = over(e) || l.status === "submitted";
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
      // Test trigger (mock only): ?mockExam=403 refuses the exam GET (take page: toast and Home).
      if (stickyFlag("mockExam") === "403") return err(403, "forbidden", "You don't have access to that.");
      // Test trigger (mock only): ?mockClose=8 closes this exam 8 s after the first load that carries the flag
      // (auto-submit at zero; the 60 s grace window follows). Sticky for the session, applied once per exam.
      const closeIn = Number(stickyFlag("mockClose"));
      const w = globalThis as unknown as { __mockCloseSet?: string[] };
      if (Number.isFinite(closeIn) && closeIn !== 0 && !(w.__mockCloseSet ??= []).includes(exam.id)) {
        w.__mockCloseSet.push(exam.id);
        exam.closesAt = iso(Date.now() + closeIn * 1000);
      }
      if (exam.closingDemo) exam.closesAt = iso(Date.now() - 20_000); // stay inside the grace window
      // Mock-only page-URL flag for the print fixtures (see seedPrintFixtures): hidden | leak.
      const keyFlag = ["e16", "e17", "e18"].includes(exam.id) ? stickyFlag("mockKey") : null;
      const vis = keyFlag === "hidden" || keyFlag === "leak" ? false : keyVisible(exam);
      return json(200, {
        serverTime: st(),
        exam: { id: exam.id, title: exam.title, type: exam.type, totalMarks: totalMarks(exam), closesAt: exam.closesAt,
          acceptingUntil: acceptingUntil(exam), maxPeeks: exam.maxPeeks, createdBy: exam.createdBy,
          questions: keyedQuestions(exam, vis || keyFlag === "leak").map((q) => { const c = { ...q }; delete c.sourceDocId; delete c.sourceDocName; return c; }) },
        attempt: publicAttempt(latest(exam)), attemptCount: exam.attempts.length, keyVisible: vis, acceptingUntil: acceptingUntil(exam),
      });
    }
    if (method === "PUT" && sub === "attempt") {
    const b = as<AttemptBody>();
      // Test triggers (mock only), from the page URL: ?mockAutosave=fail (500 every time) | failOnce (first
      // PUT only) | 409 (closed) | 422 (answers over 20 characters) | 422other (always). Request log: globalThis.__mockAutosaveLog = [{ answers, lastQuestionIndex }].
      const t = typeof window === "undefined" ? null : new URLSearchParams(window.location.search).get("mockAutosave");
      const g = globalThis as unknown as { __mockAutosaveLog?: unknown[]; __mockAutosaveFailed?: boolean };
      (g.__mockAutosaveLog ??= []).push({ answers: b.answers, lastQuestionIndex: b.lastQuestionIndex });
      if (t === "fail") return err(500, "internal", "Something went wrong.");
      if (t === "failOnce" && !g.__mockAutosaveFailed) { g.__mockAutosaveFailed = true; return err(500, "internal", "Something went wrong."); }
      if (t === "409") return err(409, "exam_closed", "This exam is closed.");
      // 422: an answer longer than 20 characters is refused (answers[id] "Too long"); 422other: always refused (unknown question).
      if (t === "422") {
        const long = Object.entries(b.answers ?? {}).find(([, v]) => typeof v === "string" && v.length > 20);
        if (long) return err(422, "validation", "An answer is too long.", { [`answers[${long[0]}]`]: "Too long" });
      }
      if (t === "422other") return err(422, "validation", "Unknown question.", { "answers[zz]": "Unknown question" });
      if (over(exam)) return err(409, "exam_closed", "This exam is closed.");
      const cur = latest(exam);
      if (!cur || cur.status === "submitted") return err(409, "conflict", "Already submitted.");
      exam.attempts[exam.attempts.length - 1] = { ...cur, status: "in_progress", answers: b.answers, lastQuestionIndex: b.lastQuestionIndex, savedAt: st() };
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
      const cur = latest(exam);
      if (!cur) return err(404, "not_found", "That item isn't available.");
      if (cur.status !== "submitted") {
        if (over(exam)) return err(409, "exam_closed", "This exam is closed.");
        if (b.answers) cur.answers = b.answers;
        if (b.lastQuestionIndex !== undefined) cur.lastQuestionIndex = b.lastQuestionIndex;
        cur.status = "submitted";
        cur.submittedAt = st();
      }
      const g = grade(exam);
      return json(200, { status: "submitted", score: g.score, scoreBeforePeeks: g.before, maxScore: totalMarks(exam),
        submittedAt: st(), autoSubmitted: false, serverTime: st() });
    }
    if (method === "POST" && sub === "reveal") {
    const b = as<RevealBody>();
      const cur = latest(exam);
      // Test triggers (mock only), sticky from the page URL: ?mockReveal=403 (peeking off) | visible (409, key already visible) | 409 (no peeks left,
      // for a question not yet revealed) | closed (409 exam_closed) | fail (network failure) | slow (2 s).
      // Log: globalThis.__mockRevealLog = [{ questionId }] (one entry per request that reached the mock).
      const t = stickyFlag("mockReveal");
      if (!resumed) ((globalThis as unknown as { __mockRevealLog?: unknown[] }).__mockRevealLog ??= []).push({ questionId: b.questionId });
      if (t === "slow" && !resumed) return new Promise((res) => setTimeout(() => res(mockFetch(method, rawPath, body, idempotencyKey, true)), 2000));
      if (t === "fail") throw new TypeError("mock network failure");
      if (t === "403") return err(403, "forbidden", "Peeking is turned off for this exam.");
      if (t === "closed") return err(409, "exam_closed", "This exam is closed.");
      if (t === "visible") return err(409, "conflict", "The answer is already available.");
      if (t === "409" && !(cur?.revealed ?? []).some((r) => r.questionId === b.questionId)) return err(409, "conflict", "No peeks left.");
      if (exam.maxPeeks === 0) return err(403, "forbidden", "Peeking is off for this exam.");
      if (keyVisible(exam) || !cur) return err(409, "conflict", "The answer is already available.");
      if (over(exam)) return err(409, "exam_closed", "This exam is closed.");
      const left = cur.peeksLeft ?? 0;
      const q = exam.questions.find((x) => x.id === b.questionId);
      if (!q) return err(404, "not_found", "That item isn't available.");
      if (left <= 0) return err(409, "conflict", "No peeks left.");
      const revealed = cur.revealed ?? [];
      if (!revealed.some((r) => r.questionId === q.id)) {
        if (cur.status === "not_started") cur.status = "in_progress"; // a reveal starts the attempt (bs 9.6)
        revealed.push({ questionId: q.id, correctIndex: q.correctIndex, modelAnswer: q.modelAnswer });
        cur.revealed = revealed;
        cur.peeksLeft = left - 1;
      }
      return json(200, { questionId: q.id, correctIndex: q.correctIndex, modelAnswer: q.modelAnswer,
        peeksLeft: cur.peeksLeft, serverTime: st() });
    }
    if (method === "POST" && sub === "retake") {
      // Test trigger (mock only), sticky from the page URL: ?mockRetake=closed (409 exam_closed) | limit (409, the
      // 50-attempt limit) | fail (network failure). ?mockRetake= clears it. Log: globalThis.__mockRetakeLog = [examId].
      const t = stickyFlag("mockRetake");
      ((globalThis as unknown as { __mockRetakeLog?: string[] }).__mockRetakeLog ??= []).push(exam.id);
      if (t === "fail") throw new TypeError("mock network failure");
      const cur = latest(exam);
      if (!cur || cur.status === "declined") return err(404, "not_found", "That item isn't available.");
      // Idempotent: an unfinished latest attempt is returned and nothing is created.
      if (cur.status !== "submitted") return json(200, { attemptNo: cur.attemptNo, serverTime: st() });
      if (t === "closed" || over(exam)) return err(409, "exam_closed", "This exam is closed.");
      if (t === "limit" || exam.attempts.length >= MAX_ATTEMPTS) return err(409, "conflict", `You have reached the limit of ${MAX_ATTEMPTS} attempts for this exam.`);
      const attemptNo = cur.attemptNo + 1;
      exam.attempts.push({ attemptNo, status: "not_started", answers: {}, lastQuestionIndex: 0, revealed: [], peeksLeft: exam.maxPeeks });
      return json(200, { attemptNo, serverTime: st() });
    }
    if (method === "POST" && sub === "decline") {
      // Test triggers (mock only), from the page URL: ?mockDecline=409 (the exam was started elsewhere)
      // | 404 (the exam is gone) | 403 | fail (500).
      const cur = latest(exam);
      const t = typeof window === "undefined" ? null : new URLSearchParams(window.location.search).get("mockDecline");
      if (t === "409") {
        if (cur) cur.status = "in_progress";
        return err(409, "conflict", "You've already started this exam.");
      }
      if (t === "404") { state.exams = state.exams.filter((e) => e.id !== exam.id); return err(404, "not_found", "That item isn't available."); }
      if (t === "403") return err(403, "forbidden", "You don't have access to that.");
      if (t === "fail") return err(500, "internal", "Something went wrong.");
      if (cur && cur.status !== "not_started" && cur.status !== "declined") return err(409, "conflict", "You've already started this exam.");
      if (cur) cur.status = "declined";
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
      const roster = (exam.roster ??= [...rosterOf(exam)]);
      const emails = [...new Set((b.emails ?? []).map((x) => String(x).trim().toLowerCase()))];
      // Unknown: addresses starting "unknown", and (student creators) addresses starting "teacher" (not a student account).
      const unknown = emails.filter((x) => x.startsWith("unknown") || (state.user.role === "student" && x.startsWith("teacher")));
      const known = emails.filter((x) => !unknown.includes(x));
      const already = known.filter((x) => roster.some((r) => r.email === x));
      const assigned = known.filter((x) => !already.includes(x));
      const cap = state.user.role === "student" ? 10 : 50;
      if (roster.length + assigned.length > cap) return err(422, "validation", `You can assign up to ${cap} people in total.`, { emails: "over cap" });
      (globalThis as unknown as { __mockAssigned?: boolean }).__mockAssigned = true;
      for (const x of assigned) roster.push({ userId: `u_${x}`, name: titleCase(x.split("@")[0]), email: x, status: "not_started", attemptCount: 1, history: [{ attemptNo: 1, status: "not_started", autoSubmitted: false }] });
      exam.assigned.push(...assigned);
      return json(200, { assigned, alreadyAssigned: already, unknown });
    }
    if (method === "GET" && sub === "result") {
      const l = latest(exam);
      if (!l || l.status === "declined") return err(404, "not_found", "That item isn't available.");
      finalizeOnRead(exam);
      // Test trigger (mock only): ?mockResult=403 refuses the result GET (toast and Home, fs 8.1); clear with ?mockResult=.
      if (stickyFlag("mockResult") === "403") return err(403, "forbidden", "You don't have access to that.");
      // Mock-only page-URL flag (see seedResultFixtures), honoured for e14 and e15 only: hidden | leak | fractional | unfinished.
      const flag = ["e14", "e15", "e21", "e22", "e23"].includes(exam.id) ? stickyFlag("mockResult") : null;
      const isOver = over(exam);
      const wanted = query.get("attempt");
      // bs 8.5: without ?attempt the latest submitted attempt (so a retake in progress is never a 409); ?attempt=N that attempt.
      const att = wanted !== null
        ? exam.attempts.find((a) => String(a.attemptNo) === wanted)
        : [...exam.attempts].reverse().find((a) => a.status === "submitted") ?? (isOver ? exam.attempts[exam.attempts.length - 1] : undefined);
      if (wanted !== null && !att) return err(404, "not_found", "That item isn't available.");
      if (!att || flag === "unfinished" || (att.status !== "submitted" && !isOver)) return err(409, "conflict", "Submit the exam to see results.");
      const hide = flag === "hidden" || flag === "leak";
      const vis = hide ? false : keyVisible(exam);
      const never = att.status !== "submitted";
      const g = never ? { score: null, before: null } : grade(exam, att);
      if (flag === "fractional" && g.score !== null) { g.score = 27.5; g.before = 27.5; }
      const revealed = new Map((att.revealed ?? []).map((r) => [r.questionId, r]));
      const questions: ResultQuestion[] = exam.questions.map((q) => {
        const yours = never ? "" : att.answers[q.id] ?? "";
        const r: ResultQuestion = { id: q.id, marks: q.marks, text: q.text, options: q.options, yourAnswer: yours, revealed: revealed.has(q.id) };
        const answerText = q.options && q.correctIndex !== undefined ? q.options[q.correctIndex] : undefined;
        if (vis) {
          r.correct = q.options ? isRight(q, yours) : undefined;
          r.correctAnswer = answerText;
          r.modelAnswer = q.modelAnswer ?? null;
        } else if (revealed.has(q.id) || flag === "leak") {
          r.correctAnswer = answerText;
          r.modelAnswer = q.modelAnswer ?? null;
          if (flag === "leak") r.correct = q.options ? isRight(q, yours) : undefined;
        }
        return r;
      });
      const bestNo = best(exam)?.attempt.attemptNo;
      return json(200, {
        serverTime: st(),
        exam: { id: exam.id, title: exam.title, type: exam.type, totalMarks: totalMarks(exam), closesAt: exam.closesAt,
          acceptingUntil: acceptingUntil(exam), maxPeeks: exam.maxPeeks },
        attemptNo: att.attemptNo, ...(bestNo !== undefined ? { bestAttemptNo: bestNo } : {}),
        // Marks for every attempt are always visible; only per-question correctness follows the key rule.
        attempts: exam.attempts.map((a) => {
          const sc = a.status === "submitted" ? grade(exam, a).score : null;
          return { attemptNo: a.attemptNo, status: a.status, ...(sc !== null ? { score: sc } : {}), maxScore: totalMarks(exam),
            ...(a.status === "submitted" ? { submittedAt: submittedAtOf(exam, a) } : {}), autoSubmitted: a.autoSubmitted ?? false };
        }),
        status: never ? "missed" : "submitted",
        score: g.score, scoreBeforePeeks: g.before, maxScore: totalMarks(exam),
        peeksUsed: att.revealed?.length ?? 0,
        keyVisible: vis, keyAvailableAt: vis ? null : acceptingUntil(exam),
        questions,
      });
    }
  }
  return err(404, "not_found", "That item isn't available.");
}
