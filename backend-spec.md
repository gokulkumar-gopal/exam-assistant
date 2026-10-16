# Exam Assistant: Backend Spec

Audience: a coding agent building the backend. Read this whole file before writing code.

**Related files**
- `goals.md`: product goals, the 3-day constraint, architecture rules. Binding.
- `frontend-spec.md`: the frontend this backend serves. **Section 8 of this file is the API contract**; the frontend spec lists the calls it makes and points here. Its section 8.1 covers the Vercel rewrite.
- `exam-assistant-requirements.md`: user stories and acceptance criteria.
- `deployment-spec.md`: how the backend is provisioned and deployed (written separately). This file says **what the backend needs**; that file says **how it is set up**. Do not put deployment steps in this file.

---

## 1. Non-negotiable rules

1. **Go.** The whole backend is written in Go (1.24 or newer). No other language, no scripts in other languages in the runtime path.
2. **3-day, 1-person constraint.** Prefer the standard library. Add a dependency only if it saves real time (see section 3). No frameworks beyond what is listed.
3. **Minimal AWS footprint.** One compute service, one database, one S3 bucket. No VPC, no NAT gateway, no queues, no caches, no Cognito. Section 12 lists what the backend requires; `deployment-spec.md` covers how it is provisioned.
4. **Section 8 is the contract.** Endpoint names, shapes, and error behaviour are defined here and nowhere else. `frontend-spec.md` points to this section and lists the calls the frontend makes. Change section 8 first, then the frontend spec.
5. **Never leak answers.** A client must never receive `correctIndex` or `modelAnswer` unless section 9.4 says it may. The one exception is a question the caller has deliberately revealed (section 9.6).
6. **Private by default.** Every document and exam operation is scoped to the authenticated user. S3 is never public.
7. **Server time is the only time.** Deadlines are decided on the server, on read and on write. Never trust a client timestamp.
8. **REST only.** The public API is resource-oriented REST over HTTPS with JSON bodies (section 8.0). No GraphQL, gRPC, WebSocket, server-sent events, or long-polling anywhere. Long-running work is a job resource that the client polls with `GET`.
9. **The database is DynamoDB.** No other database, and no `Scan` in any request path (section 4).

## 2. What the backend does

- Signs users in with email and password or with Google, and records the role chosen at first sign-up.
- Issues presigned S3 URLs so the browser uploads and views files directly.
- Generates questions from uploaded files with an LLM, as a background **job** the frontend polls.
- Stores exams, answer keys, assignments, and attempts.
- Autosaves attempts, enforces deadlines, auto-submits expired attempts, auto-grades multiple choice.
- Hides or shows answer keys by one fixed rule (section 9.4), and lets a taker peek at single answers for a score penalty (section 9.6, a later-priority feature).

## 3. Stack

| Concern | Choice |
|---|---|
| Language | Go 1.24+ |
| HTTP | standard library `net/http` with `ServeMux` method and path patterns (`"POST /exams/{id}/submit"`) |
| Database | DynamoDB through the AWS SDK for Go v2 (`dynamodb`, `attributevalue`) |
| Other AWS | AWS SDK for Go v2 (`config`, `s3`, and `lambda` only for the Lambda job runner) |
| Lambda entry | `aws-lambda-go` plus `aws-lambda-go-api-proxy` `httpadapter` (handles the v2 event payload; used only by `cmd/lambda`) |
| IDs | `github.com/oklog/ulid/v2` (time-ordered, so sorting by ID sorts by creation time) |
| Passwords | `golang.org/x/crypto/bcrypt`, cost 10 |
| Google sign-in | `google.golang.org/api/idtoken` to validate Google ID tokens, behind an `IDTokenVerifier` interface (a fake in tests) |
| Session | `github.com/golang-jwt/jwt/v5`, HS256, in an `HttpOnly` cookie |
| PDF page count | `github.com/pdfcpu/pdfcpu` (page count only) |
| LLM | The official Go SDK of each provider (`anthropic-sdk-go` for Claude, `google.golang.org/genai` for Gemini), behind one interface (section 7). The model catalog, selection, prompts, and cost are defined in `AI-spec.md` |
| Logging | `log/slog` JSON to stdout (goes to CloudWatch) |
| Tests | `testing` with table-driven tests and an in-memory fake store. No mocking library |
| Deployment | Not covered here. See `deployment-spec.md` |

Do not add: a web framework, an ORM, a DI container, a config library, a validation library, or a job library.

## 4. Database: DynamoDB

DynamoDB is the database. This is decided, so this file does not compare alternatives.

Rules for the data layer:
- **No `Scan`** in any request path. Every read is a `GetItem`, `Query`, or `BatchGetItem` on a key or index from section 6.3.
- **Billing mode.** Tables are on-demand (`deployment-spec.md`), but the code must still tolerate throttling: use the SDK's retryer with exponential backoff (at most 5 attempts), and if a call still fails return `503 upstream_unavailable`.
- **DynamoDB bills by the whole item, not the attributes you project.** Keep hot-path items small: generated questions live in their own item (section 6.2), and index projections never use `ALL`.
- **Consistency.** Use strongly consistent reads (`ConsistentRead: true`) for `GetItem` and `Query` on base tables where a read follows a write: resuming an attempt, polling a job, reading a document after `complete`. Global secondary indexes are always eventually consistent, so a new exam can take a moment to appear in the creator's list (`creator-index`) or an assignee's list (`user-index`). The frontend refetches Home after publishing; this lag is accepted.
- **Item size.** Items are capped at 400 KB. Exams embed up to 60 questions (about 70 KB at the very most) and the generated questions of a job sit in a separate item of similar size.
- **Writes that must not race** use conditional expressions (section 6.4).

## 5. Architecture

```
Browser ──REST/JSON (Vercel /api/* rewrite, "/api" stripped)──► Backend HTTP handler (Go, net/http)
Browser ──presigned PUT / GET (S3 REST API)──────────────────► S3 (private bucket)
Backend ──► DynamoDB
Backend ──► JobRunner.Start(jobID) ──► worker runs the generation job, writes progress to the jobs table
Worker  ──► S3 (read files) ──► LLM API (HTTPS)
```

- **Routes have no `/api` prefix.** The Vercel rewrite strips it: the browser's `/api/me` arrives here as `/me`.
- **Plain `net/http` handlers.** The handler tree is hosting-neutral and has two entry points: `cmd/lambda` (adapts Lambda events to the handler tree) and `cmd/server` (a normal HTTP server for local development and for container hosts).
- **Jobs go through a `JobRunner` interface.** `Start(ctx, jobID) error` begins the job and returns immediately. Two implementations:
  - `InProcessRunner`: runs the job on a goroutine. Used by `cmd/server` and in tests.
  - `LambdaRunner`: asynchronously invokes the worker. Used when deployed on Lambda. The worker entry point in `cmd/lambda` (`MODE=worker`) receives `{"jobId": "..."}` and runs the same job code.
  The job code itself (section 7.2) is identical in both cases. How the worker is provisioned is in `deployment-spec.md`.
- **Two modes, one binary.** `MODE=api` serves HTTP; `MODE=worker` runs one job per invocation. `cmd/server` ignores `MODE`.

### 5.1 Project layout

```
backend/
  cmd/
    lambda/main.go        # MODE=api (HTTP) or MODE=worker (job event)
    server/main.go        # local dev and container hosts, in-process jobs
  internal/
    config/               # env loading, one struct
    api/                  # routes, middleware, handlers, error helpers
    auth/                 # signup, login, session cookie, middleware
    docs/                 # documents service (S3 presign, complete, delete)
    exams/                # exams, attempts, grading, visibility, lazy finalize
    gen/                  # Generator interface, Anthropic impl, JobRunner impls
    store/                # repository interfaces, dynamo impl, memory fake
    clock/                # Clock interface (real and fake)
  scripts/smoke.sh        # end-to-end curl smoke test against a base URL
  Makefile                # build, test, run, smoke
  README.md               # how to run and test locally
```

Layering: **handler** (HTTP only) calls **service** (rules) calls **store** (data). Handlers never touch the store or AWS directly. Services take a `Clock` and never call `time.Now()`.

## 6. Data model

### 6.1 Entities (logical)

```go
type User struct {
    ID, Name, Email string   // Email stored lowercase, unique
    PasswordHash    string   // empty for Google-only users
    GoogleSub       string   // Google's stable user ID; empty for password-only users
    Role            string   // "student" | "parent" | "teacher"
    SessionsValidAfter int64 // unix seconds; sessions issued before this are rejected (section 8.2)
    CreatedAt       time.Time
}

type Document struct {
    ID, OwnerID, Name string
    Type              string // "PDF" | "JPG" | "PNG" | "TXT"
    ContentType       string // MIME type
    SizeBytes         int64
    Pages             int
    S3Key             string
    Status            string // "pending" | "ready"
    CreatedAt         time.Time
    TTL               int64  // set while pending, removed on complete
}

type Question struct {
    ID            string
    Marks         int      // 1..5
    Text          string
    Options       []string // MCQ only, exactly 4
    CorrectIndex  *int     // MCQ only
    ModelAnswer   string   // free text only
    SourceDocID   string
    SourceDocName string   // snapshot, so deleting a document does not break exams
}

type Exam struct {
    ID, CreatorID, Title string
    Type                 string // "mcq" | "free"
    TotalMarks           int
    ClosesAt             time.Time  // acceptance actually ends at ClosesAt + 60 s (section 9.2)
    MaxPeeks             int        // 0..10; 0 turns peeking off (section 9.6)
    Questions            []Question // embedded in the item
    CreatedAt            time.Time
}

type Attempt struct {            // one row per (exam, user): assignment AND attempt in one
    ExamID, UserID   string
    Status           string // "not_started" | "in_progress" | "submitted" | "declined"
    Answers          map[string]string // questionID -> option text or free text
    LastQuestionIndex int
    Revealed         []string // question IDs the taker peeked at (section 9.6)
    ScoreHalves      *int   // MCQ only; nil for free text. Score in half-marks (2 x the score)
    ScoreBeforePeeksHalves *int // the same score without the peek penalty
    StartedAt, SavedAt, SubmittedAt *time.Time
    AutoSubmitted    bool
    CreatedAt        time.Time
}

type Job struct {
    ID, OwnerID   string
    Kind          string // "generate" | "regenerate"
    Model         string // catalog id chosen for this job (AI-spec.md 3.2)
    Day           string // yyyymmdd (UTC) of creation; the day-index key
    Status        string // "queued" | "running" | "done" | "failed" | "cancelled"
    Request       GenerateRequest
    Done, Total   int
    // The generated questions are NOT on this item: they are written to a second item
    // (id = <jobId>#questions) just before Status becomes "done" (section 6.2).
    FailedSources []FailedSource // {docId, name, reason}
    Error         string
    CancelRequested bool
    Usage         JobUsage // prompt version, model, effort, token counts, estimated cost, calls, kept/dropped; updated after every call (AI-spec.md section 11)
    CreatedAt, UpdatedAt time.Time
    TTL           int64    // 24 hours
}
```

**Derived status "missed"** is never stored. An attempt is `missed` when the exam is over (`now >= ClosesAt + 60 s`, section 9.2) and its status is `not_started`. An `in_progress` attempt past that point is **finalized on read** (section 9.2) into `submitted` with `AutoSubmitted = true`. A `declined` attempt is never finalized and never shown to the assignee again.

**Practice exams:** if the create request has no assignees, the creator gets an attempt row (a personal practice exam). If it has assignees, only they get attempt rows. The creator can add themself by email. Any role may create exams, students included.

### 6.2 DynamoDB tables (physical)

Table names are `<STAGE>-<name>`, for example `prod-users`. All keys are strings. Questions are embedded in the exam item (60 questions is far below 400 KB).

| Table | Partition key | Sort key | Indexes | Notes |
|---|---|---|---|---|
| `users` | `id` | none | none | holds user items (`id` = user ID) email lock items (`id` = `email#<lowercased email>`, attribute `userId`), and Google lock items (`id` = `google#<sub>`, attribute `userId`) |
| `documents` | `ownerId` | `id` | none | every document access is owner-scoped, which also enforces privacy; `ttl` attribute enabled |
| `exams` | `id` | none | GSI `creator-index` on `creatorId` (sort `id`), projecting `INCLUDE` title, type, totalMarks, closesAt, createdAt (not the questions) | |
| `attempts` | `examId` | `userId` | GSI `user-index` on `userId` (sort `examId`), projecting `INCLUDE` status, scoreHalves, scoreBeforePeeksHalves, submittedAt. Autosave changes none of these, so autosaves never write to the index | |
| `jobs` | `id` | none | GSI `owner-index` on `ownerId` (sort `id`), projecting `INCLUDE` status, createdAt, updatedAt, kind; and GSI `day-index` on `day` (sort `createdAt`), projecting `INCLUDE` status, model, kind, costMicros, calls, inTok, outTok, cacheReadTok, cacheWriteTok. Only job items have `day` | `ttl` attribute enabled. Holds three kinds of item: the job (`id` = job ID, 24 h), its questions (`id` = `<jobId>#questions`, 24 h), quota counters (`id` = `quota#<userId>#<yyyymmdd>` and `quota#global#<yyyymmdd>`, 3 days; `quota#global#<yyyymm>`, 40 days), and usage counters per model (`id` = `usage#<yyyymm>#<modelId>`, 40 days, and `usage#<yyyymmdd>#<modelId>`, 3 days) |

Enable the DynamoDB TTL feature on `documents` and `jobs`. Never project `ALL` into an index: every write to the base item would be paid for again in the index.

### 6.3 Access patterns

| Need | Query |
|---|---|
| Login | Password: `users` GetItem `id = email#<email>` to get `userId`, then GetItem on the user. Google: GetItem `id = google#<sub>`, falling back to `email#<email>` |
| My documents | `documents` Query `ownerId = me` |
| One document | `documents` GetItem `(ownerId=me, id)` |
| Exams I created | `exams` GSI `creator-index` (at most 50, newest first) |
| My open/closed exams | `attempts` GSI `user-index` Query `userId = me`, then BatchGet the `exams`, then filter by `closesAt` and status in memory. Declined attempts are dropped |
| Status table and `counts` for an exam | `attempts` Query `examId`, then BatchGet `users` for names. The `created` list does this once per exam, 10 at a time |
| Jobs I have running (soft limit) | `jobs` GSI `owner-index` Query, keep `queued` and `running` jobs that are not stale (section 7.2) |
| Daily generation limits (hard limit) | the quota items in `jobs` (section 7.5), updated atomically when a job is created |
| Poll a job | `jobs` GetItem on the job; when `done`, GetItem `<jobId>#questions` |
| AI usage page | `jobs` BatchGet of the month and day counter items for each catalog model, GetItem `quota#global#<yyyymm>`, and Query `day-index` for today (newest first, limit 50). No `Scan` |

### 6.4 Repository interfaces (`internal/store`)

Define one interface per entity (`UserStore`, `DocStore`, `ExamStore`, `AttemptStore`, `JobStore`) with only the methods the access patterns need. Provide the DynamoDB implementation and an in-memory fake for tests. Services depend only on the interfaces.

Use **conditional writes** for: creating a user (email uniqueness: write the user item, the `email#<email>` lock item, and (for Google users) the `google#<sub>` lock item in one `TransactWriteItems`, each with `attribute_not_exists(id)`; if the transaction is cancelled for that reason, return `409 conflict`), the attempt `submit` and autosave writes (only if status is not `submitted`), job status changes (section 7.2), quota counters (section 7.5), creating an exam (`attribute_not_exists(id)`, section 8.5) and each attempt row (`attribute_not_exists(userId)`), recording a peek (section 9.6), and linking Google to an existing account (add the `google#<sub>` lock, set `GoogleSub`, remove `PasswordHash`, set `SessionsValidAfter`, in one transaction conditioned on the user item still having no `GoogleSub`).

## 7. Question generation

> **`AI-spec.md` is authoritative for everything involving the LLM**: model and settings, how files are prepared and sent, prompts, output schema, validation, retries, failures, cost, and testing. This section keeps the job lifecycle and limits that belong to the backend. Where the two differ, `AI-spec.md` wins.

### 7.1 Interface

```go
type Generator interface {
    // Generate returns exactly count questions of the given marks, drawn from the sources.
    Generate(ctx context.Context, in GenInput) (GenOutput, error)
}
```

`GenInput` and `GenOutput` are defined once, in `AI-spec.md` section 4 (sources travel as opaque labels, never file names). One implementation calls the LLM; tests use a fake. The model comes from `LLM_MODEL` (default and options in `AI-spec.md` section 3). `GenInput` also carries an `Avoid []string` list and `GenOutput` carries `Usage`, `StopReason`, and `RequestID`; see `AI-spec.md` section 4 for the full shapes.

### 7.2 Job lifecycle

1. `POST /generate` validates the request (section 8.4, including the optional `model`, which defaults as in `AI-spec.md` 3.1) and the caller's limits (section 7.5). In **one `TransactWriteItems`** it increments the quota counters (conditionally) and puts the `Job` with status `queued`. It then starts the job through the `JobRunner` interface (section 5) and returns `202 {jobId}` immediately. If `Start` returns an error (for example the worker invoke is refused), the job is set to `failed` ("We couldn't start that. Try again.") and the response is `503 upstream_unavailable`.
2. **Claim.** The worker's first write is a conditional update `queued -> running` (condition: status is `queued`). If the condition fails the job is already running, failed, or cancelled (a duplicate delivery, or a stale or cancelled job), and the worker exits without doing anything. Lambda asynchronous delivery is at-least-once, so this claim is what keeps a job from running twice.
3. **Deadline and heartbeat.** The worker runs under a context deadline of **8 minutes** from its start. While running it writes `UpdatedAt` about every 20 seconds (a heartbeat), also during a long LLM call.
4. **Calls.** The worker checks `CancelRequested`, loads the source files from S3 (owner-checked), then calls `Generate` once per mark value that has a non-zero count; a count above 15 is split into chunks of 15, so a job makes at most 10 first-attempt calls, and each call may be retried once (so at most 20). **The first call runs alone** (it writes the prompt cache); the remaining calls then run **concurrently, at most 3 at a time**. After each call the worker validates the output (`AI-spec.md` section 7), keeps the questions in memory, and updates `Done` and `UpdatedAt`. Before each call it reads the monthly spend counter and, after each call, adds the call's estimated cost to it (section 7.5); if the budget is used up it stops and fails the job. Between calls it re-reads the job; if `CancelRequested`, it stops and sets `cancelled`.
5. **Finish.** After the last call the worker sorts the questions (ascending by marks, or randomly if `shuffle`), assigns each a new ID, writes the `<jobId>#questions` item, and only then sets `Status = done`. Every status change after the claim is conditional: `running -> done` only if the status is still `running` and `CancelRequested` is false, and a `failed` or `cancelled` job is never overwritten.
6. **Failure.** On an unrecoverable error, or when the 8-minute deadline is hit, the worker sets `Status = failed` and a short human-readable `Error` (never a stack trace or raw provider error). The deadline message is "That took too long. Try again."
7. **Stale jobs.** If a job is `queued` or `running` and `UpdatedAt` is more than **6 minutes** old, the read endpoint reports it as `failed` with "That took too long. Try again." A stale job does not count as running for the limit in 7.5. (This covers a worker that crashed or was throttled before it could write `failed`.)
8. **Polling reads** the small job item. Only when the status is `done` does it also read the `<jobId>#questions` item.

### 7.3 Sending files to the model

Defined in `AI-spec.md` section 5: how each file type is prepared (including image downscaling and EXIF orientation), labelling, size and page caps, and the message layout for prompt caching. The worker loads files from S3 (owner-checked); unreadable sources go into `FailedSources`; if none remain the job fails with "We couldn't read any of the selected files."

### 7.4 Prompt requirements and mark mapping

Defined in `AI-spec.md` sections 5.5, 6.1, 6.3 and 6.4 (system prompt, output schema, marks-to-difficulty table, multiple-choice and free-text rules).

### 7.5 Validation, retries, and limits

- After each call, validate and clean the output exactly as in `AI-spec.md` section 7 (including the server-side shuffle of multiple-choice options). If the batch is short, make one follow-up call for the missing questions; if it is still short, fail the job: "We couldn't generate enough questions from these files." Failure messages for refusals, timeouts and provider errors are in `AI-spec.md` section 8.
- Request limits: `counts` keys 1 to 5, each count 0 to 30, at least one non-zero, at most 60 questions in total, 1 to 10 sources, total marks (sum of marks times count) at most 200. Source limits (bytes, pages) are in section 8.4. Otherwise `422`.
- **Daily limits are hard limits**, kept in quota items in the `jobs` table (section 6.2) and updated in the same transaction that creates the job. A day is a UTC day. Each item has a 3-day TTL.

| Counter | Item | Default limit | Env |
|---|---|---|---|
| Generate jobs per user per day | `quota#<userId>#<yyyymmdd>`, attribute `generate` | 3 | `GEN_MAX_PER_DAY` |
| Regenerate jobs per user per day | the same item, attribute `regen` | 10 | `GEN_MAX_REGEN_PER_DAY` |
| All jobs, all users, per day (burst limiter) | `quota#global#<yyyymmdd>`, attribute `jobs` | 10 | `GEN_MAX_GLOBAL_PER_DAY` |
| Estimated LLM spend, all users, per month | `quota#global#<yyyymm>`, attribute `spendMicros` (micro-dollars) | $15 | `LLM_MONTHLY_BUDGET_USD` |

  When any condition fails, return `429 rate_limited` and write nothing. The transaction also covers the job put, so a refused job leaves no counter behind. A job that later fails still counts (the model may already have been paid for).
- **Running limit (soft):** at most `GEN_MAX_RUNNING` (default 2) non-stale `queued` or `running` jobs per user, checked on `owner-index`. The index is eventually consistent, so two simultaneous requests can both pass; the daily counters are the real bound.
- **Monthly budget (the main cost guard).** The worker adds each call's estimated cost, at the job's model's prices, to `spendMicros` and to that model's usage counters (`AI-spec.md` 10.4 and 11). The budget is shared by all users and all models. Creating a job is refused with `429 rate_limited` "The monthly AI budget has been used. Try again next month." when `spendMicros` is at or above the budget (a condition in the same create transaction), and the worker stops a running job with the same message when it finds the budget used up before a call. A month is a UTC month; the item has a 40-day TTL.
- LLM usage is the real running cost of this app and is not covered by the AWS free tier. The monthly budget, the daily counters, the optional Lambda concurrency reservations (`deployment-spec.md`), and the provider-side monthly spend limit on the API key are the cost guards. The provider limit is a prerequisite for the production deploy.

### 7.6 Regenerate

`POST /generate/regenerate` creates a job (`Kind = regenerate`, `Total = 1`) for the given marks, sources and type, and uses the same polling endpoints. The result has one question. `avoid` carries the texts of the questions already in the exam so the replacement does not repeat one. It counts against the regenerate limit, not the generate limit.

## 8. API

All requests and responses are JSON (`Content-Type: application/json`) except where noted. Times are ISO 8601 UTC strings. **Every response that carries a deadline or status also carries `serverTime`.** Routes have no `/api` prefix (the Vercel rewrite strips it).

### 8.0 REST conventions

- **Style.** Resource-oriented REST over HTTPS. Resources are nouns: `documents`, `exams`, an exam's `attempt`, generation `jobs`. Bodies are JSON (`Content-Type: application/json`), field names are camelCase.
- **Methods.** `GET` reads. `POST` creates a resource or triggers an action. `PUT` fully replaces a resource and is idempotent (the attempt). `DELETE` removes and is idempotent. No other methods are used.
- **Action endpoints.** Actions that are not plain create, read, update, or delete are modelled as `POST` to a sub-resource (`/documents/{id}/complete`, `/exams/{id}/submit`, `/exams/{id}/assign`, `/exams/{id}/decline`, `/exams/{id}/reveal`, `/documents/upload-url`, `/generate/regenerate`). There is no RPC envelope and no method name in the body.
- **Status codes.** `200` OK, `201` created, `202` accepted (work continues in the background), `204` no content, and the error codes in 8.1.
- **`Location` header.** `201` and `202` responses set `Location` to the new resource path where one exists (for example `202` from `POST /generate` sets `Location: /generate/{jobId}`).
- **Collections** are objects with a named array (`{ "documents": [...] }`), never bare arrays. There is no pagination in the MVP; lists are capped (documents 500, exams 200), newest first.
- **Asynchronous work** is a job resource. `POST` returns `202` and a job ID. The client polls `GET /generate/{jobId}`. Nothing is streamed or pushed.
- **Caching.** Every response carries `Cache-Control: no-store`.
- **`GET` and state.** A `GET` normally changes nothing. The one accepted exception is lazy finalize (section 9.2): reading an expired attempt persists the auto-submit. The response is exactly what a pure computation of the same state would return, and the write is idempotent.
- **Versioning.** None. A breaking change is made in this section first, then in `frontend-spec.md` (rule 4).

### 8.1 Errors

```json
{ "error": { "code": "validation", "message": "Title is required.", "fields": { "title": "Required" } } }
```

| HTTP | `code` | When |
|---|---|---|
| 401 | `unauthenticated` | no or invalid session |
| 403 | `forbidden` | authenticated but not allowed |
| 404 | `not_found` | missing, or not visible to this user (use 404, not 403, for other people's documents and exams) |
| 409 | `conflict` | duplicate email, an email already linked to another Google account, already submitted, no peeks left, idempotency-key clash |
| 409 | `exam_closed` | write after the deadline |
| 415 | `unsupported_media_type` | a request body is not `application/json` |
| 422 | `validation` | bad input; `fields` is optional |
| 422 | `role_required` | first Google sign-in for a new account with no role; the client asks for a role and retries |
| 429 | `rate_limited` | generation limits (section 7.5) |
| 502, 503 | `upstream_unavailable` | LLM or AWS dependency failed |
| 500 | `internal` | anything else; never include internals in `message` |

Messages are written for end users. Log the underlying error with a request ID instead.

### 8.2 Auth

| Method and path | Body | Response |
|---|---|---|
| `POST /auth/signup` | `{ name, email, password, role }` | `201 { user }` and sets the session cookie |
| `POST /auth/login` | `{ email, password }` | `200 { user }` and sets the cookie |
| `POST /auth/google` | `{ credential, role? }` | `200 { user }` for an existing user, `201 { user }` for a new one; sets the cookie |
| `POST /auth/logout` | none | `204` and clears the cookie |
| `GET /me` | none | `200 { user, serverTime }` or `401` |

`user` is `{ id, name, email, role }`.

Rules:
- Email is trimmed and lowercased. Password 8 to 72 bytes (bcrypt ignores or rejects anything longer). Name 1 to 80 characters. Role must be `student`, `parent`, or `teacher`.
- Login failure always returns the same `401 unauthenticated` message "Email or password is incorrect." (never reveal which was wrong). Use a constant-time comparison and run bcrypt even for unknown emails and for Google-only accounts (compare against a dummy hash), so a password login against a Google-only account is indistinguishable from a wrong password.
- Signup with an email that already exists returns `409 conflict` "An account with that email already exists."
- The role is self-selected and unverified. That is acceptable for the MVP.

**Google sign-in (`POST /auth/google`).** `credential` is the ID token (a JWT) that the browser received from Google Identity Services. `role` is optional.

1. **Verify the token** through the `IDTokenVerifier` interface (production implementation: `idtoken.Validate(ctx, credential, GOOGLE_CLIENT_ID)`). It must check the signature against Google's published keys, that the audience equals the configured client ID, that the issuer is `accounts.google.com` or `https://accounts.google.com`, and that the token has not expired. Any failure returns `401 unauthenticated` "Google sign-in failed. Try again." and never says why.
2. Require `email_verified` to be true and `email` to be non-empty; otherwise return the same `401`.
3. Take `sub`, `email` (lowercased), and `name` (fall back to the part of the email before the `@`) **from the verified token only**. Ignore anything else the client sends except `role`.
4. Find or create the user:
   - **Known Google user:** `google#<sub>` exists. Sign them in (`200`). `role` is ignored.
   - **Existing password account with the same email:** link it. Set `GoogleSub`, write the `google#<sub>` lock, **remove `PasswordHash`**, and set **`SessionsValidAfter` to now**, in one transaction. Sign them in (`200`) with a fresh cookie, so this browser stays signed in. The password is removed because a password signup never proved control of the email; keeping it would let someone who pre-registered a victim's email keep access after the victim signs in with Google. Setting `SessionsValidAfter` signs out every session issued before the link (including the pre-registrant's). The account keeps its role and any content created before linking; that is accepted.
   - **Email belongs to an account already linked to a different Google account:** `409 conflict` "This email is already linked to another sign-in. Use the original sign-in method." Nothing is changed.
   - **No account:** if `role` is missing or not one of `student`, `parent`, `teacher`, return `422 role_required` and create nothing. Otherwise create the user (no password) with both lock items in one transaction and return `201`.
5. Set the session cookie exactly as for password sign-in.

The frontend retries a `role_required` response by sending the same credential again with a role (Google ID tokens last about an hour). The `user` object is the same shape as for password sign-in. The backend does not store the Google profile picture.

**Cookie:** name `session`, value a JWT with `sub` (user ID), `role`, `iat`, `exp` (7 days). The middleware loads the user on every request and rejects (`401`) a session whose `iat` is earlier than the user's `SessionsValidAfter`. Attributes: `HttpOnly`, `Secure` (off only when `ENV=local`), `SameSite=Lax`, `Path=/`, `Max-Age=604800`, **no `Domain`**. Sign with `JWT_SECRET` (HS256).

**CSRF:** for `POST`, `PUT`, `PATCH`, and `DELETE`, reject with `403` if an `Origin` header is present and is not in `ALLOWED_ORIGINS`. Requests without `Origin` are allowed (server-to-server and curl).

### 8.3 Documents

```json
// Doc
{ "id": "01J...", "name": "Biology.pdf", "type": "PDF", "pages": 12, "sizeBytes": 481234, "createdAt": "2026-10-14T09:00:00Z" }
```

| Method and path | Purpose |
|---|---|
| `GET /documents` | `200 { documents: Doc[], serverTime }`, only `ready` documents of the caller, newest first |
| `POST /documents/upload-url` | body `{ name, type, sizeBytes }`, where `type` is the MIME type. Response `201 { docId, uploadUrl, headers }` |
| `POST /documents/{id}/complete` | verifies the upload, returns `200 Doc` |
| `GET /documents/{id}/file` | `200 { url, expiresAt }` (presigned GET) |
| `DELETE /documents/{id}` | `204` |

**Upload URL rules**
- Allowed MIME types: `application/pdf` (PDF), `image/jpeg` (JPG), `image/png` (PNG), `text/plain` (TXT). Anything else is `422`. `sizeBytes` is 1 to 20971520 (20 MB), else `422`.
- Create a `pending` document row (with `ttl` of 24 hours), generate the key `users/{ownerId}/{docId}/{sanitizedName}`, and return a presigned **PUT** URL valid for 10 minutes. Sign `Content-Type` and `Content-Length` (the exact `sizeBytes`) into the URL so a different type or size is rejected by S3. `headers` is `{ "Content-Type": "<mime>" }` (the browser sets `Content-Length` itself).
- Sanitize the name: keep letters, digits, dot, dash, underscore, space; replace anything else; limit to 120 characters.

**Complete rules**
- `HeadObject` the key (the API role needs `s3:ListBucket` on the bucket, otherwise S3 answers 403 instead of 404 for a missing key). If missing, `409 conflict` "The upload didn't finish. Try again." If the size differs from the declared size, delete the object and the row and return `422`.
- Sniff the first bytes (`%PDF-`, JPEG `FF D8 FF`, PNG signature; TXT must be valid UTF-8 for the first 4 KB). On mismatch, delete the object and row and return `422` "That file doesn't match its type."
- Compute `pages`: PDF via `pdfcpu` page count (download the object, at most 20 MB), images 1, TXT `ceil(characters / 3000)` with a minimum of 1.
- Set `status = ready` and remove `ttl`.
- Idempotent: calling `complete` on an already-ready document returns it.

**File URL:** presigned GET valid for 10 minutes, with `ResponseContentType` set to the stored MIME type and `ResponseContentDisposition: inline`.

**Delete:** delete the S3 object then the row. Exams made from the document are unaffected (questions snapshot the document name). Orphaned objects from uploads that never complete are accepted in the MVP.

**Not found vs forbidden:** because the table key includes `ownerId`, another user's document is simply not found (`404`).

### 8.4 Generation

| Method and path | Body and response |
|---|---|
| `POST /generate` | body `{ sourceIds: string[], type: "mcq"\|"free", counts: {"1":4,"2":3,...}, shuffle: boolean, model?: string }`. `model` is a catalog id from `GET /ai/models`; omitted means the default. Response `202 { jobId }` |
| `GET /generate/{jobId}` | response below |
| `DELETE /generate/{jobId}` | sets `CancelRequested`, returns `204` (idempotent). The client also calls it when it gives up polling or the user leaves |
| `POST /generate/regenerate` | body `{ marks, sourceIds, type, avoid: string[], model?: string }` (`avoid` at most 60 strings of at most 600 characters), response `202 { jobId }` |

Poll response:

```json
{
  "status": "running",
  "model": "gemini-3.1-flash-lite",
  "progress": { "done": 3, "total": 12 },
  "questions": null,
  "failedSources": [],
  "error": null,
  "serverTime": "..."
}
```

When `status` is `done`, `questions` is the final ordered list. Each question is:

```json
{ "id": "01J...", "marks": 2, "text": "…", "sourceDocId": "01J...", "sourceDocName": "Biology.pdf",
  "options": ["A","B","C","D"], "correctIndex": 1 }
```
(free text has `modelAnswer` instead of `options` and `correctIndex`). The caller is a creator, so the key is included; the frontend needs it for editing and "Review answer key".

**Validation before the job is created** (all from the `Document` rows, no S3 access needed):
- `model`, if present, must be a catalog model that is enabled (`LLM_PROVIDERS`); otherwise `422` with `fields.model`.
- Only the job's owner can read or cancel it (`404` otherwise). All `sourceIds` must belong to the caller and be `ready` (`422` otherwise).
- Total raw bytes of the sources at most `GEN_MAX_TOTAL_BYTES` (20 MB), total PDF pages at most `GEN_MAX_PDF_PAGES` (40), at most 10 sources.
- A single file that alone breaks the pages cap gets `422` with `fields.sourceIds` naming that file. A set that is only too large in total gets `422` "These files are too large to use together. Select fewer or smaller files."

### 8.5 Exams

**Create:** `POST /exams`, with an optional `Idempotency-Key` header

```json
{
  "title": "Biology: Cells quiz",
  "type": "mcq",
  "closesInSeconds": 172800,
  "maxPeeks": 3,
  "assigneeEmails": ["a@x.com"],
  "questions": [ { "marks": 1, "text": "…", "options": ["…","…","…","…"], "correctIndex": 2,
                   "sourceDocId": "…", "sourceDocName": "Biology.pdf" } ]
}
```
- Exactly one of `closesAt` (ISO) or `closesInSeconds` is required. The server computes the deadline from its own clock when `closesInSeconds` is used. The deadline must be at least 1 minute and at most 30 days in the future.
- Title 1 to 120 characters. 1 to 60 questions. Each question: marks 1 to 5, non-empty text; MCQ needs exactly 4 non-empty options and `correctIndex` 0 to 3; free text needs a non-empty `modelAnswer`. `type` must match every question. The server **mints new question IDs** and ignores any ID the client sends. `sourceDocId` and `sourceDocName` are display-only strings (at most 120 characters) and are never used for authorization or lookups.
- `totalMarks` is computed by the server as the sum of marks. (The frontend enforces that the sum equals the chosen target; the server does not know the target.)
- `maxPeeks` is optional, 0 to 10 and at most the number of questions (else `422`). When omitted it is **3 if `assigneeEmails` is empty** (a self-practice exam) and **0 otherwise**.
- **Any role may create** exams. **Assignee rules by the creator's role:**
  - Teacher and parent: any registered user, at most **50** assignees.
  - Student: only registered users whose role is `student`, at most **10** assignees. A known account that is not a student is reported exactly like an unknown email, so the response does not reveal who exists or what role they hold.
  - More than the cap is `422`.
- Look up each assignee email. Create an attempt row (`not_started`) for each allowed known user. Unknown or not-allowed emails are returned, not an error. If `assigneeEmails` is empty, create the attempt row for the creator.
- **Idempotency and partial failure.** The `Idempotency-Key` header (8 to 64 characters from `A-Za-z0-9_-`) becomes the exam ID; without it the server mints a ULID. Write the exam item first with `attribute_not_exists(id)`, then the attempt rows with one `PutItem` each (`attribute_not_exists(userId)`; a failed condition means the row exists and is ignored), 10 at a time. A replay by the same creator completes any missing rows and returns `200` with the same body shape. A key that already belongs to another creator is `409 conflict`.
- Response: `201 { id, unknownEmails: string[], serverTime }`.

**List:** `GET /exams?scope=open|closed|created`

```json
{ "serverTime": "...", "exams": [ {
  "id": "…", "title": "…", "createdBy": { "id": "…", "name": "Ms. Rao" },
  "closesAt": "…", "acceptingUntil": "…", "totalMarks": 30, "type": "mcq",
  "attemptStatus": "not_started|in_progress|submitted|missed",
  "score": 27.5, "maxScore": 30,
  "counts": { "submitted": 12, "inProgress": 3, "notStarted": 5, "declined": 1 }
} ] }
```
- `open`: caller has an attempt row, `acceptingUntil > now`, status `not_started` or `in_progress`. An attempt inside the 60-second grace window is still open (the UI shows "closing…").
- `closed`: caller has an attempt row and (`acceptingUntil <= now` or status `submitted`). Status is `submitted` or `missed`.
- Declined attempts appear in neither list.
- `created`: exams where the caller is the creator, at most 50, newest first; `counts` is included only here; `attemptStatus` and `score` are omitted.
- Each list endpoint runs lazy finalize (section 9.2) on any expired in-progress attempt it touches.
- `score` is present only for submitted MCQ attempts. It is a number that can end in `.5` (section 9.3); `maxScore` is an integer.

**Take / view:** `GET /exams/{id}`

```json
{
  "serverTime": "...",
  "exam": { "id": "…", "title": "…", "type": "mcq", "totalMarks": 30, "closesAt": "…", "acceptingUntil": "…",
            "maxPeeks": 3, "createdBy": { "id": "…", "name": "…" },
            "questions": [ { "id": "…", "marks": 1, "text": "…", "options": ["…"] } ] },
  "attempt": { "status": "in_progress", "answers": { "q1": "…" }, "lastQuestionIndex": 4, "savedAt": "…",
               "revealed": [ { "questionId": "q2", "correctIndex": 1 } ], "peeksLeft": 2 },
  "keyVisible": false
}
```
- Visible to the creator and to users with a non-declined attempt row; everyone else (including a user who declined) gets `404`.
- `attempt` is `null` for a creator who has no attempt row.
- Questions are in stored order (ascending by marks, or as the creator arranged them).
- Keys (`correctIndex`, `modelAnswer`) are included only when section 9.4 allows, or for the questions listed in `attempt.revealed` (section 9.6). Otherwise they are omitted entirely.

**Autosave:** `PUT /exams/{id}/attempt` with `{ answers, lastQuestionIndex }`
- Replaces the whole answers map (idempotent). Keys must be existing question IDs; values are strings up to 5,000 characters; total body up to 200 KB.
- The write is conditional on `status <> submitted`; if it fails, `409 conflict`. At or after `acceptingUntil`: `409 exam_closed` (and lazy finalize runs).
- First save moves `not_started` to `in_progress` and sets `StartedAt`.
- Response: `200 { savedAt, serverTime }`.
- Known accepted gap: the map has no sequence number, so a delayed retry of an older `PUT` can overwrite newer answers.

**Submit:** `POST /exams/{id}/submit` with optional `{ answers, lastQuestionIndex }`
- If `answers` is present, store them first. Then finalize (grade, set `submitted`). `SubmittedAt` is the earlier of now and `closesAt`.
- Accepted until `acceptingUntil` (`closesAt` + 60 seconds). After that: `409 exam_closed`; an expired in-progress attempt is auto-submitted by lazy finalize instead.
- **The client's auto-submit at zero sends its final answers in this call** (it does not autosave first), so the grace window cannot lose them.
- Idempotent: an already-submitted attempt returns its existing result.
- Response: `200 { status: "submitted", score, scoreBeforePeeks, maxScore, submittedAt, autoSubmitted, serverTime }`. `score` and `scoreBeforePeeks` are `null` for free-text exams.

**Reveal an answer (later priority, section 9.6):** `POST /exams/{id}/reveal` with `{ questionId }`
- Response `200 { questionId, correctIndex | modelAnswer, peeksLeft, serverTime }`.
- Rules in section 9.6. Errors: `403 forbidden` (peeking is off), `409 conflict` (no peeks left, or the key is already visible), `409 exam_closed`, `404`.

**Decline:** `POST /exams/{id}/decline`
- Assignee only, and only while their attempt is `not_started`; otherwise `409 conflict`. Sets the attempt status to `declined`. Idempotent. Returns `204`. There is no way to undo it in the MVP.

**Status (creator only):** `GET /exams/{id}/status`

```json
{
  "serverTime": "...",
  "exam": { "id": "…", "title": "…", "closesAt": "…", "totalMarks": 30 },
  "counts": { "submitted": 2, "inProgress": 1, "notStarted": 1, "declined": 1 },
  "attempts": [ { "userId": "…", "name": "Aisha K.", "email": "…", "status": "submitted", "score": 34, "submittedAt": "…" } ]
}
```
- `status` per row is one of `not_started`, `in_progress`, `submitted`, `declined`. (After the exam is over a never-started attempt is reported as `not_started`; the frontend may label it "Missed".)
- `403` if the caller is not the creator.

**Assign more:** `POST /exams/{id}/assign` with `{ emails }`
- Creator only. `409 exam_closed` after `acceptingUntil`.
- The creator's role rules and cap from the create endpoint apply, and the cap counts all the exam's attempt rows (an exam that is already at its cap rejects more with `422`).
- Response: `200 { assigned: [...], alreadyAssigned: [...], unknown: [...] }` (emails in each list; "unknown" includes not-allowed accounts for a student creator).

**Result:** `GET /exams/{id}/result`

```json
{
  "serverTime": "...",
  "exam": { "id": "…", "title": "…", "type": "mcq", "totalMarks": 40, "closesAt": "…", "acceptingUntil": "…", "maxPeeks": 3 },
  "status": "submitted",
  "score": 34.5, "scoreBeforePeeks": 35, "maxScore": 40,
  "peeksUsed": 1,
  "keyVisible": true,
  "keyAvailableAt": null,
  "questions": [ { "id": "…", "marks": 1, "text": "…", "options": ["…"],
                   "yourAnswer": "…", "correct": true, "correctAnswer": "…", "modelAnswer": null, "revealed": false } ]
}
```
- Caller must have a non-declined attempt row. The caller must have submitted or the exam must be over (an expired attempt is finalized first); otherwise `409 conflict` "Submit the exam to see results."
- When `keyVisible` is false, omit `correct`, `correctAnswer`, and `modelAnswer` from every question **except those the caller revealed** (they have already seen the answer), set `keyAvailableAt` to `acceptingUntil`, and still return the scores. The frontend must not render correct or incorrect marks when the key is hidden.
- A never-started attempt on an exam that is over returns `status: "missed"`, no score, and all `yourAnswer` empty.

### 8.6 AI models and usage

Both endpoints need a signed-in user of any role. There is no admin role and no admin page. Responses carry `serverTime` and `Cache-Control: no-store`.

**Models:** `GET /ai/models`

```json
{ "models": [ { "id": "gemini-3.1-flash-lite", "name": "Gemini 3.1 Flash-Lite", "provider": "gemini",
                "tier": "cheapest", "priceInPerMTok": 0.25, "priceOutPerMTok": 1.5,
                "approxJobCost": { "small": 0.03, "heavy": 0.10 }, "default": true } ],
  "serverTime": "..." }
```
- Only enabled models, cheapest first. Exactly one has `default: true` (`LLM_MODEL` if set and enabled, otherwise the cheapest enabled model). The catalog and prices live in code (`AI-spec.md` 3.2).

**Usage:** `GET /ai/usage` (aggregate only: never user names, document names, files, or question text)

```json
{ "serverTime": "...",
  "month": { "key": "2026-10", "spendUsd": 3.42, "budgetUsd": 15, "jobs": 21, "calls": 88,
             "tokens": { "input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0 } },
  "today": { "jobs": 2, "calls": 9, "spendUsd": 0.31, "tokens": { "input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0 } },
  "byModel": [ { "id": "...", "name": "...", "jobs": 0, "calls": 0, "spendUsd": 0, "tokens": { "input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0 } } ],
  "running": 1,
  "recent": [ { "createdAt": "...", "kind": "generate", "model": "...", "status": "running",
                "calls": 4, "tokens": { "input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0 }, "costUsd": 0.12 } ] }
```
- `month` and `byModel` come from the usage counter items; `budgetUsd` is `LLM_MONTHLY_BUDGET_USD`; `spendUsd` is the estimate (`AI-spec.md` 10.4). `today` sums today's per-model counters (UTC day).
- `recent` is the newest 50 jobs of today from `day-index`; `running` counts those whose status is `queued` or `running` and not stale. A running job's numbers move as its calls finish, because the worker updates the job item and the counters after every call.
- The frontend polls this every 5 seconds while the usage page is open (REST only, no streaming). It is cheap: a handful of `GetItem`s and one small `Query`.
- Job items expire after 24 hours, so `recent` never goes back further than yesterday; month totals come from the counters.

## 9. Business rules

### 9.1 Roles and access

| Action | Student | Parent | Teacher |
|---|---|---|---|
| Documents (own) | yes | yes | yes |
| Generate questions | yes (own documents) | yes | yes |
| Create exams | yes | yes | yes |
| Choose the AI model for a generation, see AI usage | yes | yes | yes |
| Assign exams | students only, max 10 | anyone, max 50 | anyone, max 50 |
| Take exams assigned to them, decline them | yes | yes | yes |
| View status of exams they created | yes | yes | yes |

Documents are never shared in the MVP. Teachers cannot see students' documents.

### 9.2 Deadline enforcement and lazy finalize

An exam accepts work until **`acceptingUntil` = `closesAt` + 60 seconds** (the grace window for the client's auto-submit at zero). The exam is **over** from `acceptingUntil`. Autosave, submit, lazy finalize, the list classification, and answer-key visibility all use that one cutoff, so nothing can finalize an attempt inside the grace window.

There are no background jobs. Instead, `finalizeIfExpired(attempt, exam, now)` runs whenever an attempt is read through any endpoint (list, get, status, result) and on writes:
- If `status = in_progress` and `now >= acceptingUntil`, grade the saved answers, set `submitted`, `SubmittedAt = closesAt`, `AutoSubmitted = true`. Use a conditional write (`status <> submitted`) so concurrent calls are safe.
- `not_started` attempts are left alone and reported as `missed` by derivation once the exam is over. `declined` attempts are never touched.
- The status endpoint may finalize many attempts; do the conditional writes concurrently with a small limit (for example 10).

### 9.3 Grading

- MCQ: a question is correct if the stored answer string equals `options[correctIndex]` exactly. A correct question that was **not** revealed scores its marks. A correct question that **was** revealed scores **half its marks** (9.5). Wrong or unanswered scores 0. `maxScore = totalMarks`.
- Scores are computed in **half-marks** (integers) and stored as `ScoreHalves` and `ScoreBeforePeeksHalves` (the same score as if no question had been revealed). The API returns them divided by two, as JSON numbers that may end in `.5`.
- Free text: no auto-grading. `score` and `scoreBeforePeeks` are null. The frontend lets the student self-check and applies the same half-marks cap to revealed questions locally.

### 9.4 Answer key visibility

There is one fixed rule, with no per-exam setting. `keyVisible(exam, attempt, user, now)`:
1. **True once the exam is over** (`now >= acceptingUntil`), for the creator and for everyone with access.
2. Otherwise **true for the creator**, unless the creator is also taking the exam and has not finished: the creator has no attempt row, or their attempt is `submitted`.
3. Otherwise false. This includes a creator who is in the middle of their own practice exam, who goes through the peek path in 9.6 like any other taker.

The rule is evaluated on the server at read time for `GET /exams/{id}` and `GET /exams/{id}/result`. Never include key fields in a response when it is false, except the questions the caller revealed (9.5).

### 9.5 Printing

Printing needs no endpoint of its own. The frontend's print view (`frontend-spec.md` 6.9) reads `GET /exams/{id}` and prints from that response, so everything it can print is already governed by the rules above: questions are returned to the creator and to non-declined assignees, and `correctIndex` and `modelAnswer` are present only when `keyVisible` is true (9.4). The backend does not track printing, does not start an attempt, and does not use a peek when an exam is read for printing. A `GET /exams/{id}` after the exam is over still returns the questions to the creator and to everyone who has an attempt row, which is what lets them print a closed exam.

### 9.6 Peeking at an answer (later priority)

Priority: the visibility rule above is part of the core build. Peeking is built after the core flow works and is the first thing to cut; the fields it needs (`Exam.MaxPeeks`, `Attempt.Revealed`, the half-mark scores) exist from the start so adding it later needs no migration.

Rules for `POST /exams/{id}/reveal`:
- The caller needs an attempt row with status `not_started` or `in_progress` and the exam must not be over. A reveal on a `not_started` attempt starts it, exactly like the first autosave.
- `MaxPeeks = 0`: `403 forbidden` "Peeking is turned off for this exam."
- The question is already in `Revealed`: return the answer again, and charge nothing more (idempotent).
- `len(Revealed) >= MaxPeeks` for a new question: `409 conflict` "No peeks left." Record the reveal with a conditional update (`size(Revealed) < MaxPeeks` and not already containing the question).
- `keyVisible` is already true for the caller: `409 conflict` "The answer key is already visible."
- Return the one answer (`correctIndex` for multiple choice, `modelAnswer` for free text) and `peeksLeft`.
- `GET /exams/{id}` returns `revealed` and `peeksLeft` in the attempt so a reload or resume keeps them and never charges twice.
- Scoring is in 9.3: revealed and correct earns half marks.
- The result screen shows the score with and without the penalty, `peeksUsed`, and which questions were revealed. A "no peeks" badge is shown by the frontend when `maxPeeks > 0` and `peeksUsed = 0`. Nothing else is gamified in the MVP (no points, streaks, levels, or leaderboards).

## 10. Security

- **Authorization on every handler.** Middleware loads the user from the cookie; each service method takes the user and checks ownership or role. Missing or foreign resources return `404`.
- **Input limits:** JSON bodies capped at 1 MB (200 KB for autosave) using `http.MaxBytesReader`. Reject unknown fields on create endpoints. Validate everything in section 8 before touching the store.
- **Presigned URLs** are short-lived (10 minutes) and scoped to one key; the bucket blocks all public access.
- **Secrets:** `JWT_SECRET` and `LLM_API_KEY` come from environment variables (how they are stored and injected is defined in `deployment-spec.md`). The API function needs `JWT_SECRET` but not `LLM_API_KEY`; the worker needs `LLM_API_KEY` but not `JWT_SECRET`. Never log them. Rotating is out of scope for the MVP.
- **Open model choice:** any signed-in user may choose any enabled model for their own generation, so a user can use more of the shared budget by picking a pricier one. The monthly budget guard, the per-user and global daily limits, and the Create screen's cost notice are the controls. `GET /ai/usage` is aggregate only.
- **Sessions:** a Google link or any future password change sets `SessionsValidAfter`, and the middleware rejects older sessions (section 8.2).
- **Logging:** one structured line per request with request ID, user ID, method, path, status, duration. Never log passwords, tokens, cookies, answers, or document contents.
- **Passwords:** bcrypt cost 10. No password reset in the MVP.
- **Google tokens:** always verify audience, issuer, expiry, and signature server-side, and require `email_verified`. Never accept an email, name, or user ID from the client. Do not log or store the credential. `GOOGLE_CLIENT_ID` is not secret, but a token minted for a different client ID must be rejected, so use a separate client per stage.
- **Known MVP gaps (accepted):** no login throttling beyond Lambda concurrency, unverified roles (so "students only" assignment is a courtesy, not security), no email verification, no password reset, no `nonce` on Google sign-in (a captured ID token can be replayed for about an hour), the assignment responses reveal whether an email has an account, and the autosave map has no sequence number. Minors' data needs a privacy review before real users (see `goals.md`).

## 11. Configuration

Each variable is listed once. **Required** variables must be set (the backend fails fast at startup if one is missing). **Optional** variables have the default shown; deployment sets them only to override.

| Variable | Required | Default | Purpose |
|---|---|---|---|
| `ENV` | yes | none | `local` or `production`; `local` turns off the `Secure` cookie flag |
| `MODE` | Lambda only | `api` | `api` or `worker` |
| `STAGE` | yes | none | table name prefix, for example `prod` |
| `JWT_SECRET` | api | none | cookie signing key (32+ random bytes) |
| `ALLOWED_ORIGINS` | api | none | comma-separated origins for the CSRF check (the Vercel domain and `http://localhost:3000`) |
| `S3_BUCKET` | yes | none | the uploads bucket |
| `WORKER_FUNCTION_NAME` | api on Lambda | none | the worker Lambda to invoke |
| `LLM_PROVIDERS` | api and worker | `anthropic` | comma list of enabled providers (`gemini`, `anthropic`); not secret; decides which catalog models are enabled |
| `LLM_API_KEY` | worker, when `anthropic` is enabled | none | the Anthropic key |
| `GEMINI_API_KEY` | worker, when `gemini` is enabled | none | the Gemini key (paid tier) |
| `LLM_MODEL` | no | empty | default model id; empty means the cheapest enabled model (`AI-spec.md` 3.1) |
| `GOOGLE_CLIENT_ID` | api | none | the Google OAuth client ID that sign-in tokens must be issued for |
| `GEN_MAX_RUNNING` | no | `2` | soft limit on running jobs per user |
| `GEN_MAX_PER_DAY` | no | `3` | generate jobs per user per UTC day |
| `GEN_MAX_REGEN_PER_DAY` | no | `10` | regenerate jobs per user per UTC day |
| `GEN_MAX_GLOBAL_PER_DAY` | no | `10` | all jobs, all users, per UTC day |
| `LLM_MONTHLY_BUDGET_USD` | no | `15` | estimated spend after which new jobs are refused for the rest of the UTC month (api and worker) |
| `LLM_EFFORT`, `LLM_TIMEOUT_SECONDS`, other `GEN_MAX_*` caps | no | see `AI-spec.md` section 12 | AI settings |

Load once at startup into a single struct.

## 12. What the backend requires from deployment

This section lists requirements only. How to provision them (tools, templates, accounts, regions, domains, pipelines) belongs in `deployment-spec.md`.

**Runtime**
- Two entry points exist: HTTP (`MODE=api`, or `cmd/server`) and job (`MODE=worker`, Lambda only). The HTTP path must answer every request within 29 seconds. A worker invocation may run up to 10 minutes.
- Must be reachable over HTTPS at the URL set as `API_URL` on the frontend (`frontend-spec.md` section 8.1), and must pass `Set-Cookie` and `Cookie` through unchanged.
- Must have outbound internet access to the LLM API and to Google (the backend fetches Google's public signing keys to verify sign-in tokens).
- Logs go to stdout as JSON lines.
- `GET /health` (no auth) returns `200 {"status":"ok"}`.

**Configuration:** every variable marked required in section 11 must be set.

**DynamoDB:** the five tables, keys, indexes, and TTL attributes in section 6.2, exactly as written. Table names are `<STAGE>-<name>` (for example `prod-users`, `prod-documents`).

**S3**
- One private bucket with all public access blocked and server-side encryption on.
- Key layout `users/{ownerId}/{docId}/{sanitizedName}`.
- CORS allowing the frontend origin(s) and `http://localhost:3000` for the methods `GET`, `PUT`, and `HEAD`, all request headers, and exposing `ETag`. The browser uploads and views files directly, so uploads fail without this.
- Presigned URLs last 10 minutes.

**Permissions the code uses** (grant these and nothing broader, scoped to the project's own resources):
- DynamoDB (API): `GetItem`, `PutItem`, `UpdateItem`, `DeleteItem`, `Query`, `BatchGetItem`, `ConditionCheckItem` on the five tables and their indexes. (`TransactWriteItems` has no permission of its own; it is authorised through the underlying write actions.) There is no `BatchWriteItem` and no `Scan`.
- DynamoDB (worker): `GetItem` on `documents`; `GetItem`, `PutItem`, `UpdateItem`, `Query` on `jobs`. Nothing else.
- S3 (API): `s3:PutObject` (for presigning uploads), `s3:GetObject` (presigned views), `s3:DeleteObject`, and **`s3:ListBucket` on the bucket** (so `HeadObject` on a missing key returns 404, not 403). S3 (worker): `s3:GetObject`.
- Lambda, only for `LambdaRunner`: invoke permission on the worker function.
- The jobs table holds quota counters and question items as well as jobs (section 6.2); the permissions above cover them.

**Things the deployment should avoid on purpose:** a VPC without internet egress, NAT gateways, and any service not needed above. Each adds setup time or cost, and goals rule 7 forbids extras.

## 13. Testing

Write tests as you build each slice. They must pass with `go test ./...` and need no AWS access.

| Area | Must test |
|---|---|
| Auth | password signup and login; Google sign-in with a fake verifier: new user with a role (`201`), new user without a role (`role_required`, nothing created), existing Google user (`200`, role ignored), linking by verified email (password removed), unverified email rejected, wrong audience or expired token rejected; a Google-only account cannot log in with a password and gets the same error as a wrong password |
| Authorization | other users' documents and exams are `404`; students can create exams but a student can assign only to students (a non-student email is reported as unknown) and at most 10; only the creator reads status; a declined exam is `404` for the decliner |
| Grading | MCQ scoring in half-marks, unanswered, free text gives a nil score, a revealed correct answer earns half marks |
| Visibility | all three rules of 9.4, including the creator who is taking their own exam (no key until submitted or over); keys never appear in `GET /exams/{id}` or `/result` when false, except revealed questions |
| Deadlines | autosave and submit work inside the 60 s grace window (and a read inside it does not finalize); autosave at or after `acceptingUntil` is `409`; autosave after submit is `409`; the auto-submit-at-zero race (answers in the submit body are kept); lazy finalize at `acceptingUntil` with `SubmittedAt = closesAt`; `missed` derivation; idempotent submit |
| Generation | counts and source-size validation (`422` naming the file); daily counters are atomic and a refused job leaves no counter behind; a duplicate worker delivery exits at the claim; `Start` failure marks the job failed; a stale job reads as failed and cannot later become `done`; short batch retried once then failed; cancel before the first call and between calls; the first call runs alone then the rest run at most 3 at a time; questions are read from the second item only when `done` |
| Documents | MIME and size validation; complete rejects missing, wrong size, wrong magic bytes; delete leaves exams intact |
| Exam create | totalMarks computed, MCQ and free text shape validation, unknown assignee emails returned, practice-exam attempt row for creator, `maxPeeks` default and bounds, assignee caps, idempotent replay completes missing attempt rows, another creator's key is `409` |
| Peeks (later priority) | reveal charges once per question, `403` when off, `409` when out of peeks or the key is visible, revealed answers come back on resume |
| Printing | `GET /exams/{id}` on an exam that is over still returns the questions to the creator and to every non-declined assignee, with keys only when `keyVisible`; reading it starts no attempt |
| Models and usage | the default is `LLM_MODEL` if set and enabled, else the cheapest enabled model; an unknown or not-enabled `model` is `422`; the job records its model; each call adds tokens and cost to the model's month and day counters and to the job; `GET /ai/usage` returns aggregates with no user or file data, and `recent` is newest first; `GET /ai/models` lists only enabled models |
| Sessions | linking Google sets `SessionsValidAfter`; an older cookie is `401`; the same email linked to a different Google account is `409` |

Use a fake `Clock` for all time-dependent tests and the in-memory store. The generator is a fake that returns canned questions.

**Smoke test** (`make smoke`, uses `curl` against a base URL): sign up, `/me`, request an upload URL, `PUT` a small PDF, `complete`, list documents, create a generation job, poll until done, create an exam, `GET /exams?scope=open`, autosave, submit, fetch result. It exits non-zero on the first failure.

## 14. Build order (matches the Day 1 slices in `goals.md`)

1. **Scaffold and documents.** Go module, config, `Clock`, error helpers, `cmd/server`, `cmd/lambda`, health endpoint `GET /health` (no auth). `goals.md` requires a deployed health endpoint in the first hour of Day 1, so finish this step first and hand it to the process in `deployment-spec.md`. Then auth (signup, login, logout, `/me`, cookie, CSRF check), then documents (`upload-url`, `complete`, list, file URL, delete).
2. **Generation.** `Generator` interface and fake, job table, `/generate` endpoints, worker entry, in-process runner for local. Prove it with one real PDF and one real camera photo by midday before moving on.
3. **Exams.** Create, list scopes, assign, status.
4. **Taking.** Get exam with visibility rules, autosave, submit, lazy finalize, result, decline.
5. **Peeks (later priority).** `reveal`, half-mark scoring, `maxPeeks` bounds. Build last; cut first if time is short.

Build each slice against the in-memory store first, then add the DynamoDB implementation. Do not start slice N+1 until slice N passes its tests and the smoke test.

## 15. Keeping `frontend-spec.md` in sync

Section 8 of this file is the contract. `frontend-spec.md` section 8 lists the calls the frontend makes and points here. Whenever section 8 changes, update that list in the same commit. Facts the frontend depends on: `type` in `POST /documents/upload-url` is the MIME type; the auto-submit at zero sends its answers in the submit body; when the key is hidden the result has no correct or incorrect flags, so the UI must not draw them; unknown assignees are returned by create and assign and should be shown as a warning.

## 16. Acceptance checklist

- [ ] `go test ./...` passes with no AWS credentials.
- [ ] `GET /health` returns `200` with no authentication.
- [ ] Signup, login, logout, and `/me` work, and the cookie is `HttpOnly`, `Secure`, `SameSite=Lax` with no `Domain`.
- [ ] `POST /auth/google` with a valid token signs in an existing Google user, creates a new user only when a valid role is sent, and otherwise returns `422 role_required` without creating anything.
- [ ] A token for a different client ID, an expired token, a bad signature, or an unverified email is rejected with the same generic `401`.
- [ ] Linking a password account via Google removes its password, and the same email then cannot log in with the old password. A session cookie issued before the link is rejected afterwards, and the same email linked to a different Google account is `409`.
- [ ] A 20 MB PDF uploads straight to S3 and the upload never passes through the Lambda body; a larger file is refused with `422`.
- [ ] `complete` rejects a missing object, a wrong-size object, and a file whose bytes do not match its type.
- [ ] A user cannot read, list, delete, or use another user's document in generation (all `404` or `422`).
- [ ] A presigned file URL stops working after about 10 minutes, and no code path makes an object public.
- [ ] Generation returns `202` immediately, the poll shows progress, cancel stops it, a stale job reads as failed and cannot later turn `done`, and a duplicate worker delivery does nothing.
- [ ] `GET /ai/models` lists the enabled models cheapest first with one default; `POST /generate` with another enabled model uses it and records it; an unknown or not-enabled model is `422`.
- [ ] `GET /ai/usage` works for every role, shows live month spend against the budget, today's counts, per-model totals, running jobs and the latest 50 jobs, and contains no user names, file names, or question text.
- [ ] The daily generate, regenerate, and global limits are enforced atomically (`429`), and over-size source sets are refused up front with `422`.
- [ ] Generated questions match the requested counts per mark value, are in ascending order by default, and are shuffled when requested.
- [ ] Creating an exam with unknown assignee emails succeeds and returns them. A student can create an exam and assign only students (max 10); the same `Idempotency-Key` sent twice creates one exam.
- [ ] A user can decline an assigned exam, and it disappears from their lists.
- [ ] A student never receives a key field before section 9.4 allows it (verify the raw JSON).
- [ ] An attempt can still be autosaved and submitted for 60 seconds after `closesAt`, a submit with answers in the body at zero keeps them, and autosave from `acceptingUntil` on returns `409 exam_closed`.
- [ ] An in-progress attempt left past `acceptingUntil` appears as submitted (auto-submitted) on the next read and is graded.
- [ ] Submitting twice returns the same result.
- [ ] The result endpoint hides all correctness data when the key is hidden (except revealed questions) and still returns the score.
- [ ] Answer keys follow section 9.4: the creator sees the key, except while taking their own exam; everyone sees it once the exam is over.
- [ ] `GET /exams/{id}` returns everything the print view needs (questions for the creator and non-declined assignees, also after the exam is over; keys only when `keyVisible`) and has no side effects.
- [ ] (Later priority) Revealing an answer works as in 9.6 and is charged once per question.
- [ ] `make smoke BASE_URL=...` completes the full flow against any running backend (local or deployed).
- [ ] No endpoint uses a method other than GET, POST, PUT, or DELETE, and every response carries `Cache-Control: no-store`.
- [ ] No request path uses a DynamoDB `Scan`.
- [ ] The README explains how to run and test the backend locally.

## 17. Out of scope

Password reset, email verification for password accounts, other sign-in providers (only Google is supported), screens for linking or unlinking sign-in methods, parent-student linking and the parent progress view, document sharing, folders and tags, HEIC, editing or deleting an exam after creation, a teacher viewing a student's individual answers, AI-assisted grading of free text, per-attempt durations, notifications, analytics, rate limiting beyond section 7.5, secret rotation, multi-region, undoing a decline, points, streaks, levels and leaderboards, server-generated PDFs, and recording the results of a paper exam.

## 18. Decisions already made and open items

**Decided**
- Go, standard library first, one binary with `api` and `worker` modes and a local server entry point.
- REST only, with the conventions in section 8.0.
- DynamoDB is the database. No other database, no `Scan`.
- Question generation is a job with polling, one LLM call per mark value (the first alone, the rest at most 3 at a time), behind a `JobRunner` interface. Hard daily counters in DynamoDB are the cost guard.
- DynamoDB tables are on-demand, with lean index projections.
- Gemini and Claude both supported; the default is the cheapest enabled model; any signed-in user can choose the model per generation; there is no admin role; usage is visible to everyone, aggregate only.
- One deadline cutoff (`closesAt` + 60 s) for everything; one fixed answer-key rule; peeking for a half-marks penalty is a later-priority feature.
- Any role may create exams. Students assign only to students.
- Deadlines, key visibility, and finalization are computed on the server at read time.
- Deployment details live in `deployment-spec.md`, not here.
- Two ways in: email and password, and Google (ID token verified server-side). Linking is by verified email and removes the password. A new Google user must supply a role (`role_required`).

**Open, decide before or during the build**
- The LLM model's current page, image and request-size limits; set the caps in `AI-spec.md` section 12 to match.
- The monthly LLM budget (app-side $15, provider-side $20, set before the production deploy) and whether a few dozen generation jobs a month is enough.
- Regions, accounts, and Lambda concurrency reservations: `deployment-spec.md`.
- Google consent-screen status and authorized origins: `deployment-spec.md` section 8.0.
