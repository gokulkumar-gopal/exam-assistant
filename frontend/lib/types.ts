// Types follow backend-spec.md section 8 (it wins over frontend-spec 8).

export type Role = "student" | "parent" | "teacher";
export type ExamType = "mcq" | "free";
export type Marks = 1 | 2 | 3 | 4 | 5;

export interface User { id: string; name: string; email: string; role: Role }
export interface UserRef { id: string; name: string }

export interface Doc {
  id: string; name: string; type: "PDF" | "JPG" | "PNG" | "TXT";
  pages: number; sizeBytes: number; createdAt: string;
}

export interface Question {
  id: string; marks: Marks; text: string;
  sourceDocId?: string; sourceDocName?: string;
  options?: string[];
  correctIndex?: number;
  modelAnswer?: string;
}

/** Question as sent when publishing: server mints the id. */
export interface NewQuestion {
  marks: Marks; text: string; sourceDocId: string; sourceDocName: string;
  options?: string[]; correctIndex?: number; modelAnswer?: string;
}

export type AttemptStatus = "not_started" | "in_progress" | "submitted" | "missed" | "declined";

export interface ApiErrorBody { error: { code: string; message: string; fields?: Record<string, string> } }

// Auth
export interface MeResponse { user: User; serverTime: string }
export interface UserResponse { user: User }
export interface SignupBody { name: string; email: string; password: string; role: Role }
export interface LoginBody { email: string; password: string }
export interface GoogleBody { credential: string; role?: Role }

// Documents
export interface DocsResponse { documents: Doc[]; serverTime: string }
export interface UploadUrlBody { name: string; type: string; sizeBytes: number }
export interface UploadUrlResponse { docId: string; uploadUrl: string; headers: Record<string, string> }
export interface FileUrlResponse { url: string; expiresAt: string }

// AI
export interface AiModel {
  id: string; name: string; provider: string; tier: string;
  priceInPerMTok: number; priceOutPerMTok: number;
  approxJobCost: { small: number; heavy: number }; default: boolean;
}
export interface ModelsResponse { models: AiModel[]; serverTime: string }
export interface Tokens { input: number; output: number; cacheRead: number; cacheWrite: number }
export interface UsageResponse {
  serverTime: string;
  month: { key: string; spendUsd: number; budgetUsd: number; jobs: number; calls: number; tokens: Tokens };
  today: { jobs: number; calls: number; spendUsd: number; tokens: Tokens };
  byModel: { id: string; name: string; jobs: number; calls: number; spendUsd: number; tokens: Tokens }[];
  running: number;
  recent: { createdAt: string; kind: string; model: string; status: string; calls: number; tokens: Tokens; costUsd: number }[];
}

// Generation
export interface GenerateBody {
  sourceIds: string[]; type: ExamType;
  counts: Record<string, number>; // keyed "1".."5"
  shuffle: boolean; model?: string;
  /** PDFs only, keyed by document id; 1-based inclusive. Omitted means whole files. */
  pageRanges?: Record<string, { from: number; to: number }>;
}
export interface RegenerateBody {
  marks: Marks; sourceIds: string[]; type: ExamType; avoid: string[]; model?: string;
  pageRanges?: Record<string, { from: number; to: number }>;
}
export interface JobStartResponse { jobId: string }
export type JobStatus = "queued" | "running" | "done" | "failed" | "cancelled";
export interface FailedSource { docId: string; name: string; reason: string }
export interface JobResponse {
  status: JobStatus; model: string;
  progress: { done: number; total: number };
  questions: Question[] | null;
  failedSources: FailedSource[];
  error: string | null;
  serverTime: string;
}

// Exams
export interface CreateExamBody {
  title: string; type: ExamType;
  closesInSeconds?: number; closesAt?: string;
  maxPeeks?: number; assigneeEmails: string[];
  questions: NewQuestion[];
}
export interface CreateExamResponse { id: string; unknownEmails: string[]; serverTime: string }

export interface ExamListItem {
  id: string; title: string; createdBy: UserRef;
  closesAt: string; acceptingUntil: string; totalMarks: number; type: ExamType;
  attemptStatus?: AttemptStatus;
  /** The best score over submitted attempts. */
  score?: number; maxScore?: number;
  /** How many attempts the caller has on this exam (absent on the created list). */
  attemptCount?: number;
  /** True while a retake may start: latest attempt submitted, before acceptingUntil, under the 50-attempt limit. */
  canRetake?: boolean;
  counts?: { submitted: number; inProgress: number; notStarted: number; declined: number };
}
export interface ExamListResponse { serverTime: string; exams: ExamListItem[] }

export interface ExamDetail {
  id: string; title: string; type: ExamType; totalMarks: number;
  closesAt: string; acceptingUntil: string; maxPeeks: number;
  createdBy: UserRef; questions: Question[];
}
export interface Attempt {
  /** 1-based; a retake adds the next number. */
  attemptNo: number;
  status: AttemptStatus;
  answers: Record<string, string>;
  lastQuestionIndex: number;
  savedAt?: string;
  revealed?: { questionId: string; correctIndex?: number; modelAnswer?: string }[];
  peeksLeft?: number;
}
export interface ExamResponse {
  serverTime: string; exam: ExamDetail;
  /** The caller's latest attempt. */
  attempt: Attempt | null; attemptCount?: number; keyVisible: boolean;
  acceptingUntil?: string;
}
export interface RetakeResponse { attemptNo: number; serverTime: string }

export interface AttemptBody { answers: Record<string, string>; lastQuestionIndex: number }
export interface AttemptSaved { savedAt: string; serverTime: string }
export interface SubmitBody { answers?: Record<string, string>; lastQuestionIndex?: number }
export interface SubmitResponse {
  status: "submitted"; score: number | null; scoreBeforePeeks: number | null; maxScore: number;
  submittedAt: string; autoSubmitted: boolean; serverTime: string;
}
export interface RevealBody { questionId: string }
export interface RevealResponse { questionId: string; correctIndex?: number; modelAnswer?: string; peeksLeft: number; serverTime: string }

export interface StatusResponse {
  serverTime: string;
  exam: { id: string; title: string; closesAt: string; totalMarks: number };
  counts: { submitted: number; inProgress: number; notStarted: number; declined: number };
  attempts: StatusRow[];
}
/** One attempt in a student's history: marks and time only, never answers. */
export interface AttemptHistoryItem {
  attemptNo: number; status: Exclude<AttemptStatus, "missed">; score?: number; submittedAt?: string; autoSubmitted?: boolean;
}
/** One student. `score` and `submittedAt` are those of the best attempt. */
export interface StatusRow {
  userId: string; name: string; email: string; status: Exclude<AttemptStatus, "missed">;
  score?: number; submittedAt?: string;
  attemptCount?: number; history?: AttemptHistoryItem[];
}
export interface AssignBody { emails: string[] }
export interface AssignResponse { assigned: string[]; alreadyAssigned: string[]; unknown: string[] }

export interface ResultQuestion {
  id: string; marks: Marks; text: string; options?: string[];
  yourAnswer: string;
  correct?: boolean; correctAnswer?: string; modelAnswer?: string | null;
  revealed: boolean;
}
/** One entry of the result's `attempts[]`: every attempt of the caller with its marks (oldest first). */
export interface AttemptSummary {
  attemptNo: number; status: AttemptStatus; score?: number | null; maxScore: number; submittedAt?: string; autoSubmitted?: boolean;
}
export interface ResultResponse {
  serverTime: string;
  /** The attempt shown, the best submitted one (absent for free text) and every attempt. */
  attemptNo?: number; bestAttemptNo?: number; attempts?: AttemptSummary[];
  exam: { id: string; title: string; type: ExamType; totalMarks: number; closesAt: string; acceptingUntil: string; maxPeeks: number };
  status: "submitted" | "missed";
  score: number | null; scoreBeforePeeks: number | null; maxScore: number;
  peeksUsed: number;
  keyVisible: boolean;
  keyAvailableAt: string | null;
  questions: ResultQuestion[];
}
