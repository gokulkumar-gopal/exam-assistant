# Exam Assistant: Backend Spec

Audience: a coding agent building the backend. Read this whole file before writing code.

**Related files**
- `goals.md`: product goals, the 3-day constraint, architecture rules. Binding.
- `frontend-spec.md`: the frontend this backend serves. Its sections 8 and 8.1 define the API contract. Section 15 of this file lists the places where this spec needs the frontend spec to change, so read it before building the API.
- `exam-assistant-requirements.md`: user stories and acceptance criteria.
- `deployment-spec.md`: how the backend is provisioned and deployed (written separately). This file says **what the backend needs**; that file says **how it is set up**. Do not put deployment steps in this file.

---

## 1. Non-negotiable rules

1. **Go.** The whole backend is written in Go (1.22 or newer). No other language, no scripts in other languages in the runtime path.
2. **3-day, 1-person constraint.** Prefer the standard library. Add a dependency only if it saves real time (see section 3). No frameworks beyond what is listed.
3. **Minimal AWS footprint.** One compute service, one database, one S3 bucket. No VPC, no NAT gateway, no queues, no caches, no Cognito. Section 12 lists what the backend requires; `deployment-spec.md` covers how it is provisioned.
4. **The frontend contract is the API.** Endpoint names, shapes, and error behaviour in section 8 must match `frontend-spec.md`. Do not change names without updating that file first.
5. **Never leak answers.** A client must never receive `correctIndex` or `modelAnswer` unless section 9.4 says it may.
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
- Hides or shows answer keys according to each exam's visibility rule.

## 3. Stack

| Concern | Choice |
|---|---|
| Language | Go 1.22+ |
| HTTP | standard library `net/http` with `ServeMux` method and path patterns (`"POST /exams/{id}/submit"`) |
| Database | DynamoDB through the AWS SDK for Go v2 (`dynamodb`, `attributevalue`) |
| Other AWS | AWS SDK for Go v2 (`config`, `s3`, and `lambda` only for the Lambda job runner) |
| Lambda entry | `aws-lambda-go` plus `aws-lambda-go-api-proxy` `httpadapter` (handles the v2 event payload; used only by `cmd/lambda`) |
| IDs | `github.com/oklog/ulid/v2` (time-ordered, so sorting by ID sorts by creation time) |
| Passwords | `golang.org/x/crypto/bcrypt`, cost 10 |
| Google sign-in | `google.golang.org/api/idtoken` to validate Google ID tokens, behind an `IDTokenVerifier` interface (a fake in tests) |
| Session | `github.com/golang-jwt/jwt/v5`, HS256, in an `HttpOnly` cookie |
| PDF page count | `github.com/pdfcpu/pdfcpu` (page count only) |
| LLM | The official Anthropic Go SDK, behind an interface (section 7). Everything about the LLM is defined in `AI-spec.md` |
| Logging | `log/slog` JSON to stdout (goes to CloudWatch) |
| Tests | `testing` with table-driven tests and an in-memory fake store. No mocking library |
| Deployment | Not covered here. See `deployment-spec.md` |

Do not add: a web framework, an ORM, a DI container, a config library, a validation library, or a job library.

## 4. Database: DynamoDB

DynamoDB is the database. This is decided, so this file does not compare alternatives.

Rules for the data layer:
- **No `Scan`** in any request path. Every read is a `GetItem`, `Query`, or `BatchGetItem` on a key or index from section 6.3.
- **Keep volume low.** Capacity and billing mode are chosen in `deployment-spec.md`; the code must not assume either. It must tolerate throttling: use the SDK's retryer with exponential backoff (at most 5 attempts), and if a call still fails return `503 upstream_unavailable`.
- **Consistency.** Use strongly consistent reads (`ConsistentRead: true`) for `GetItem` and `Query` on base tables where a read follows a write: resuming an attempt, polling a job, reading a document after `complete`. Global secondary indexes are always eventually consistent, so a new exam can take a moment to appear in the creator's list (`creator-index`) or an assignee's list (`user-index`). The frontend refetches Home after publishing; this lag is accepted.
- **Item size.** Items are capped at 400 KB. Exams embed up to 60 questions and stay far below that. Jobs embed the generated questions for 24 hours and stay below it too.
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
    ClosesAt             time.Time
    AnswerKeyVisibility  string // "anytime" | "after_submit" | "after_close"
    Questions            []Question // embedded in the item
    CreatedAt            time.Time
}

type Attempt struct {            // one row per (exam, user): assignment AND attempt in one
    ExamID, UserID   string
    Status           string // "not_started" | "in_progress" | "submitted"
    Answers          map[string]string // questionID -> option text or free text
    LastQuestionIndex int
    Score            *int   // MCQ only; nil for free text
    StartedAt, SavedAt, SubmittedAt *time.Time
    AutoSubmitted    bool
    CreatedAt        time.Time
}

type Job struct {
    ID, OwnerID   string
    Status        string // "queued" | "running" | "done" | "failed" | "cancelled"
    Request       GenerateRequest
    Done, Total   int
    Questions     []Question
    FailedSources []FailedSource // {docId, name, reason}
    Error         string
    CancelRequested bool
    Usage         JobUsage // prompt version, model, effort, token counts, calls, kept/dropped (AI-spec.md section 11)
    CreatedAt, UpdatedAt time.Time
    TTL           int64    // 24 hours
}
```

**Derived status "missed"** is never stored. An attempt is `missed` when `closesAt <= now` and its status is `not_started`. An `in_progress` attempt past the deadline is **finalized on read** (section 9.2) into `submitted` with `AutoSubmitted = true`.

**Practice exams:** if the create request has no assignees, the creator gets an attempt row (a personal practice exam). If it has assignees, only they get attempt rows. The creator can add themself by email.

### 6.2 DynamoDB tables (physical)

Table names are prefixed with the stage, for example `exam-prod-users`. All keys are strings. Item sizes are small; questions are embedded in the exam item (60 questions is far below 400 KB).

| Table | Partition key | Sort key | Indexes | Notes |
|---|---|---|---|---|
| `users` | `id` | none | none | holds user items (`id` = user ID) email lock items (`id` = `email#<lowercased email>`, attribute `userId`), and Google lock items (`id` = `google#<sub>`, attribute `userId`) |
| `documents` | `ownerId` | `id` | none | every document access is owner-scoped, which also enforces privacy; `ttl` attribute enabled |
| `exams` | `id` | none | GSI `creator-index` on `creatorId` (sort `id`) | |
| `attempts` | `examId` | `userId` | GSI `user-index` on `userId` (sort `examId`) | project all attributes |
| `jobs` | `id` | none | GSI `owner-index` on `ownerId` (sort `id`) | `ttl` attribute enabled (24 h) |

Enable the DynamoDB TTL feature on `documents` and `jobs`.

### 6.3 Access patterns

| Need | Query |
|---|---|
| Login | Password: `users` GetItem `id = email#<email>` to get `userId`, then GetItem on the user. Google: GetItem `id = google#<sub>`, falling back to `email#<email>` |
| My documents | `documents` Query `ownerId = me` |
| One document | `documents` GetItem `(ownerId=me, id)` |
| Exams I created | `exams` GSI `creator-index` |
| My open/closed exams | `attempts` GSI `user-index` Query `userId = me`, then BatchGet the `exams`, then filter by `closesAt` and status in memory |
| Status table for an exam | `attempts` Query `examId`, then BatchGet `users` for names |
| My generation jobs today (rate limit) | `jobs` GSI `owner-index` Query, filter by `createdAt` |

### 6.4 Repository interfaces (`internal/store`)

Define one interface per entity (`UserStore`, `DocStore`, `ExamStore`, `AttemptStore`, `JobStore`) with only the methods the access patterns need. Provide the DynamoDB implementation and an in-memory fake for tests. Services depend only on the interfaces.

Use **conditional writes** for: creating a user (email uniqueness: write the user item, the `email#<email>` lock item, and (for Google users) the `google#<sub>` lock item in one `TransactWriteItems`, each with `attribute_not_exists(id)`; if the transaction is cancelled for that reason, return `409 conflict`), the attempt `submit` transition (only if status is not `submitted`), job status changes, and linking Google to an existing account (add the `google#<sub>` lock, set `GoogleSub`, remove `PasswordHash`, in one transaction conditioned on the user item still having no `GoogleSub`).

## 7. Question generation

> **`AI-spec.md` is authoritative for everything involving the LLM**: model and settings, how files are prepared and sent, prompts, output schema, validation, retries, failures, cost, and testing. This section keeps the job lifecycle and limits that belong to the backend. Where the two differ, `AI-spec.md` wins.

### 7.1 Interface

```go
type Generator interface {
    // Generate returns exactly count questions of the given marks, drawn from the sources.
    Generate(ctx context.Context, in GenInput) (GenOutput, error)
}
type GenInput struct {
    Sources []SourceFile // id, name, contentType, bytes
    Type    string       // "mcq" | "free"
    Marks   int          // 1..5
    Count   int
}
type GenOutput struct {
    Questions         []Question
    UnreadableSources []string // doc IDs the model could not read
}
```

One implementation calls the LLM; tests use a fake. The model comes from `LLM_MODEL` (default and options in `AI-spec.md` section 3). `GenInput` also carries an `Avoid []string` list and `GenOutput` carries `Usage`, `StopReason`, and `RequestID`; see `AI-spec.md` section 4 for the full shapes.

### 7.2 Job lifecycle

1. `POST /generate` validates the request and the caller's limits (section 7.5), writes a `Job` with status `queued`, starts the job through the `JobRunner` interface (section 5), and returns `202 {jobId}` immediately.
2. The worker marks the job `running`, loads the source files from S3 (owner-checked), then calls `Generate` **once per mark value that has a non-zero count**, sequentially. A count above 15 is split into chunks of 15, so a job makes at most 10 calls (`AI-spec.md` 5.3). After each call it validates the output, appends the questions, and updates `Done` and `UpdatedAt`.
3. Between calls the worker re-reads the job. If `CancelRequested`, it stops and sets `cancelled`.
4. After the last call the worker sorts the questions (ascending by marks, or randomly if `shuffle`), assigns each a new ID, sets `Status = done`.
5. On an unrecoverable error: `Status = failed` and a short human-readable `Error` (never a stack trace or raw provider error).
6. **Stale jobs:** if a job is `queued` or `running` and `UpdatedAt` is more than 5 minutes old, the read endpoint reports it as `failed` with "That took too long. Try again."

### 7.3 Sending files to the model

Defined in `AI-spec.md` section 5: how each file type is prepared (including image downscaling and EXIF orientation), labelling, size and page caps, and the message layout for prompt caching. The worker loads files from S3 (owner-checked); unreadable sources go into `FailedSources`; if none remain the job fails with "We couldn't read any of the selected files."

### 7.4 Prompt requirements and mark mapping

Defined in `AI-spec.md` sections 5.5, 6.1, 6.3 and 6.4 (system prompt, output schema, marks-to-difficulty table, multiple-choice and free-text rules).

### 7.5 Validation, retries, and limits

- After each call, validate and clean the output exactly as in `AI-spec.md` section 7 (including the server-side shuffle of multiple-choice options). If the batch is short, make one follow-up call for the missing questions; if it is still short, fail the job: "We couldn't generate enough questions from these files." Failure messages for refusals, timeouts and provider errors are in `AI-spec.md` section 8.
- Request limits: `counts` keys 1 to 5, each count 0 to 30, at least one non-zero, at most 60 questions in total, 1 to 10 sources, total marks (sum of marks times count) at most 200. Otherwise `422`.
- Per-user limits (configurable by env): at most 2 jobs running at once and at most 20 jobs per day. Otherwise `429 rate_limited`.
- LLM usage is the real running cost of this app and is not covered by the AWS free tier. The daily limit is the cost guard.

### 7.6 Regenerate

`POST /generate/regenerate` creates a job with `Total = 1` for the given marks and sources (and type), and uses the same polling endpoints. The result has one question.

## 8. API

All requests and responses are JSON (`Content-Type: application/json`) except where noted. Times are ISO 8601 UTC strings. **Every response that carries a deadline or status also carries `serverTime`.** Routes have no `/api` prefix (the Vercel rewrite adds and strips it).

### 8.0 REST conventions

- **Style.** Resource-oriented REST over HTTPS. Resources are nouns: `documents`, `exams`, an exam's `attempt`, generation `jobs`. Bodies are JSON (`Content-Type: application/json`), field names are camelCase.
- **Methods.** `GET` reads. `POST` creates a resource or triggers an action. `PUT` fully replaces a resource and is idempotent (the attempt). `DELETE` removes and is idempotent. No other methods are used.
- **Action endpoints.** Actions that are not plain create, read, update, or delete are modelled as `POST` to a sub-resource (`/documents/{id}/complete`, `/exams/{id}/submit`, `/exams/{id}/assign`, `/documents/upload-url`, `/generate/regenerate`). There is no RPC envelope and no method name in the body.
- **Status codes.** `200` OK, `201` created, `202` accepted (work continues in the background), `204` no content, and the error codes in 8.1.
- **`Location` header.** `201` and `202` responses set `Location` to the new resource path where one exists (for example `202` from `POST /generate` sets `Location: /generate/{jobId}`).
- **Collections** are objects with a named array (`{ "documents": [...] }`), never bare arrays. There is no pagination in the MVP; lists are capped (documents 500, exams 200), newest first.
- **Asynchronous work** is a job resource. `POST` returns `202` and a job ID. The client polls `GET /generate/{jobId}`. Nothing is streamed or pushed.
- **Caching.** Every response carries `Cache-Control: no-store`.
- **`GET` and state.** A `GET` normally changes nothing. The one accepted exception is lazy finalize (section 9.2): reading an expired attempt persists the auto-submit. The response is exactly what a pure computation of the same state would return, and the write is idempotent.
- **Versioning.** None. A breaking change is made in `frontend-spec.md` first (rule 4).

### 8.1 Errors

```json
{ "error": { "code": "validation", "message": "Title is required.", "fields": { "title": "Required" } } }
```

| HTTP | `code` | When |
|---|---|---|
| 401 | `unauthenticated` | no or invalid session |
| 403 | `forbidden` | authenticated but not allowed |
| 404 | `not_found` | missing, or not visible to this user (use 404, not 403, for other people's documents and exams) |
| 409 | `conflict` | duplicate email, already submitted |
| 409 | `exam_closed` | write after the deadline |
| 415 | `unsupported_media_type` | a request body is not `application/json` |
| 422 | `validation` | bad input; `fields` is optional |
| 422 | `role_required` | first Google sign-in for a new account with no role; the client asks for a role and retries |
| 429 | `rate_limited` | generation limits |
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
- Email is trimmed and lowercased. Password minimum 8 characters. Name 1 to 80 characters. Role must be `student`, `parent`, or `teacher`.
- Login failure always returns the same `401 unauthenticated` message "Email or password is incorrect." (never reveal which was wrong). Use a constant-time comparison and run bcrypt even for unknown emails and for Google-only accounts (compare against a dummy hash), so a password login against a Google-only account is indistinguishable from a wrong password.
- Signup with an email that already exists returns `409 conflict` "An account with that email already exists."
- The role is self-selected and unverified. That is acceptable for the MVP.

**Google sign-in (`POST /auth/google`).** `credential` is the ID token (a JWT) that the browser received from Google Identity Services. `role` is optional.

1. **Verify the token** through the `IDTokenVerifier` interface (production implementation: `idtoken.Validate(ctx, credential, GOOGLE_CLIENT_ID)`). It must check the signature against Google's published keys, that the audience equals the configured client ID, that the issuer is `accounts.google.com` or `https://accounts.google.com`, and that the token has not expired. Any failure returns `401 unauthenticated` "Google sign-in failed. Try again." and never says why.
2. Require `email_verified` to be true and `email` to be non-empty; otherwise return the same `401`.
3. Take `sub`, `email` (lowercased), and `name` (fall back to the part of the email before the `@`) **from the verified token only**. Ignore anything else the client sends except `role`.
4. Find or create the user:
   - **Known Google user:** `google#<sub>` exists. Sign them in (`200`). `role` is ignored.
   - **Existing password account with the same email:** link it. Set `GoogleSub`, write the `google#<sub>` lock, and **remove `PasswordHash`**, in one transaction. Sign them in (`200`). The password is removed because a password signup never proved control of the email; keeping it would let someone who pre-registered a victim's email keep access after the victim signs in with Google. The real owner now signs in with Google. Sessions already issued before the link stay valid until they expire (up to 7 days); this is a known MVP gap.
   - **No account:** if `role` is missing or not one of `student`, `parent`, `teacher`, return `422 role_required` and create nothing. Otherwise create the user (no password) with both lock items in one transaction and return `201`.
5. Set the session cookie exactly as for password sign-in.

The frontend retries a `role_required` response by sending the same credential again with a role (Google ID tokens last about an hour). The `user` object is the same shape as for password sign-in. The backend does not store the Google profile picture.

**Cookie:** name `session`, value a JWT with `sub` (user ID), `role`, `exp` (7 days). Attributes: `HttpOnly`, `Secure` (off only when `ENV=local`), `SameSite=Lax`, `Path=/`, `Max-Age=604800`, **no `Domain`**. Sign with `JWT_SECRET` (HS256).

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
- Allowed MIME types: `application/pdf` (PDF), `image/jpeg` (JPG), `image/png` (PNG), `text/plain` (TXT). Anything else is `422`. `sizeBytes` is 1 to 26214400 (25 MB), else `422`.
- Create a `pending` document row (with `ttl` of 24 hours), generate the key `users/{ownerId}/{docId}/{sanitizedName}`, and return a presigned **PUT** URL valid for 10 minutes. Sign `Content-Type` and `Content-Length` (the exact `sizeBytes`) into the URL so a different type or size is rejected by S3. `headers` is `{ "Content-Type": "<mime>" }` (the browser sets `Content-Length` itself).
- Sanitize the name: keep letters, digits, dot, dash, underscore, space; replace anything else; limit to 120 characters.

**Complete rules**
- `HeadObject` the key. If missing, `409 conflict` "The upload didn't finish. Try again." If the size differs from the declared size, delete the object and the row and return `422`.
- Sniff the first bytes (`%PDF-`, JPEG `FF D8 FF`, PNG signature; TXT must be valid UTF-8 for the first 4 KB). On mismatch, delete the object and row and return `422` "That file doesn't match its type."
- Compute `pages`: PDF via `pdfcpu` page count (download the object, at most 25 MB), images 1, TXT `ceil(characters / 3000)` with a minimum of 1.
- Set `status = ready` and remove `ttl`.
- Idempotent: calling `complete` on an already-ready document returns it.

**File URL:** presigned GET valid for 10 minutes, with `ResponseContentType` set to the stored MIME type and `ResponseContentDisposition: inline`.

**Delete:** delete the S3 object then the row. Exams made from the document are unaffected (questions snapshot the document name). Orphaned objects from uploads that never complete are accepted in the MVP.

**Not found vs forbidden:** because the table key includes `ownerId`, another user's document is simply not found (`404`).

### 8.4 Generation

| Method and path | Body and response |
|---|---|
| `POST /generate` | body `{ sourceIds: string[], type: "mcq"\|"free", counts: {"1":4,"2":3,...}, shuffle: boolean }`. Response `202 { jobId }` |
| `GET /generate/{jobId}` | response below |
| `DELETE /generate/{jobId}` | sets `CancelRequested`, returns `204` (idempotent) |
| `POST /generate/regenerate` | body `{ marks, sourceIds, type }`, response `202 { jobId }` |

Poll response:

```json
{
  "status": "running",
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

Only the job's owner can read or cancel it (`404` otherwise). All `sourceIds` must belong to the caller and be `ready` (`422` otherwise).

### 8.5 Exams

**Create:** `POST /exams`

```json
{
  "title": "Biology: Cells quiz",
  "type": "mcq",
  "closesInSeconds": 172800,
  "answerKeyVisibility": "after_close",
  "assigneeEmails": ["a@x.com"],
  "questions": [ { "marks": 1, "text": "…", "options": ["…","…","…","…"], "correctIndex": 2, "sourceDocId": "…" } ]
}
```
- Exactly one of `closesAt` (ISO) or `closesInSeconds` is required. The server computes the deadline from its own clock when `closesInSeconds` is used. The deadline must be at least 1 minute and at most 30 days in the future.
- Title 1 to 120 characters. 1 to 60 questions. Each question: marks 1 to 5, non-empty text; MCQ needs exactly 4 non-empty options and `correctIndex` 0 to 3; free text needs a non-empty `modelAnswer`. `type` must match every question.
- `totalMarks` is computed by the server as the sum of marks. (The frontend enforces that the sum equals the chosen target; the server does not know the target.)
- Only `teacher` and `parent` roles may create (`403` for students).
- Look up each assignee email. Create an attempt row (`not_started`) for each known user. Unknown emails are returned, not an error. If `assigneeEmails` is empty, create the attempt row for the creator.
- Response: `201 { id, unknownEmails: string[], serverTime }`.

**List:** `GET /exams?scope=open|closed|created`

```json
{ "serverTime": "...", "exams": [ {
  "id": "…", "title": "…", "createdBy": { "id": "…", "name": "Ms. Rao" },
  "closesAt": "…", "totalMarks": 30, "type": "mcq",
  "attemptStatus": "not_started|in_progress|submitted|missed",
  "score": 28, "maxScore": 30,
  "counts": { "submitted": 12, "inProgress": 3, "notStarted": 5 }
} ] }
```
- `open`: caller has an attempt row, `closesAt > now`, status `not_started` or `in_progress`.
- `closed`: caller has an attempt row and (`closesAt <= now` or status `submitted`). Status is `submitted` or `missed`.
- `created`: exams where the caller is the creator; `counts` is included only here; `attemptStatus`, `score` are omitted.
- Each list endpoint runs lazy finalize (section 9.2) on any expired in-progress attempt it touches.
- `score` is present only for submitted MCQ attempts (hidden-key rules do not apply to the total score).

**Take / view:** `GET /exams/{id}`

```json
{
  "serverTime": "...",
  "exam": { "id": "…", "title": "…", "type": "mcq", "totalMarks": 30, "closesAt": "…",
            "answerKeyVisibility": "after_close", "createdBy": { "id": "…", "name": "…" },
            "questions": [ { "id": "…", "marks": 1, "text": "…", "options": ["…"] } ] },
  "attempt": { "status": "in_progress", "answers": { "q1": "…" }, "lastQuestionIndex": 4, "savedAt": "…" },
  "keyVisible": false
}
```
- Visible to the creator and to users with an attempt row; everyone else gets `404`.
- `attempt` is `null` for a creator who has no attempt row.
- Questions are in stored order (ascending by marks, or as the creator arranged them).
- Keys (`correctIndex`, `modelAnswer`) are included only when section 9.4 allows. Otherwise they are omitted entirely.

**Autosave:** `PUT /exams/{id}/attempt` with `{ answers, lastQuestionIndex }`
- Replaces the whole answers map (idempotent). Keys must be existing question IDs; values are strings up to 5,000 characters; total body up to 200 KB.
- If `now >= closesAt`: `409 exam_closed` (and lazy finalize runs).
- If the status is `submitted`: `409 conflict`.
- First save moves `not_started` to `in_progress` and sets `StartedAt`.
- Response: `200 { savedAt, serverTime }`.

**Submit:** `POST /exams/{id}/submit` with optional `{ answers, lastQuestionIndex }`
- If `answers` is present, store them first. Then finalize (grade, set `submitted`, `SubmittedAt`).
- Accept submission until `closesAt + 60 seconds` (grace for the client's auto-submit at zero). After that: `409 exam_closed`. An expired in-progress attempt is auto-submitted by lazy finalize instead.
- Idempotent: an already-submitted attempt returns its existing result.
- Response: `200 { status: "submitted", score, maxScore, submittedAt, autoSubmitted, serverTime }`. `score` is `null` for free-text exams.

**Status (creator only):** `GET /exams/{id}/status`

```json
{
  "serverTime": "...",
  "exam": { "id": "…", "title": "…", "closesAt": "…", "totalMarks": 30 },
  "counts": { "submitted": 2, "inProgress": 1, "notStarted": 1 },
  "attempts": [ { "userId": "…", "name": "Aisha K.", "email": "…", "status": "submitted", "score": 34, "submittedAt": "…" } ]
}
```
- `status` per row is one of `not_started`, `in_progress`, `submitted`. (After the deadline a never-started attempt is reported as `not_started`; the frontend may label it "Missed".)
- `403` if the caller is not the creator.

**Assign more:** `POST /exams/{id}/assign` with `{ emails }`
- Creator only. `409 exam_closed` after the deadline.
- Response: `200 { assigned: [...], alreadyAssigned: [...], unknown: [...] }` (emails in each list).

**Result:** `GET /exams/{id}/result`

```json
{
  "serverTime": "...",
  "exam": { "id": "…", "title": "…", "type": "mcq", "totalMarks": 40, "closesAt": "…", "answerKeyVisibility": "after_close" },
  "status": "submitted",
  "score": 34, "maxScore": 40,
  "keyVisible": true,
  "keyAvailableAt": null,
  "questions": [ { "id": "…", "marks": 1, "text": "…", "options": ["…"],
                   "yourAnswer": "…", "correct": true, "correctAnswer": "…", "modelAnswer": null } ]
}
```
- Caller must have an attempt row. The caller must have submitted or the exam must have closed (an expired attempt is finalized first); otherwise `409 conflict` "Submit the exam to see results."
- When `keyVisible` is false, omit `correct`, `correctAnswer`, and `modelAnswer` from every question, set `keyAvailableAt` to the date it becomes visible (the exam's `closesAt` for `after_close`; `null` for `after_submit`, which is already visible once submitted), and still return `score` and `maxScore`.
- A never-started attempt on a closed exam returns `status: "missed"`, no score, and all `yourAnswer` empty.

## 9. Business rules

### 9.1 Roles and access

| Action | Student | Parent | Teacher |
|---|---|---|---|
| Documents (own) | yes | yes | yes |
| Generate questions | yes (own documents) | yes | yes |
| Create exams, assign | no | yes | yes |
| Take exams assigned to them | yes | yes | yes |
| View status of exams they created | n/a | yes | yes |

Documents are never shared in the MVP. Teachers cannot see students' documents.

### 9.2 Deadline enforcement and lazy finalize

There are no background jobs. Instead, `finalizeIfExpired(attempt, exam, now)` runs whenever an attempt is read through any endpoint (list, get, status, result) and on writes:
- If `status = in_progress` and `now >= closesAt`, grade the saved answers, set `submitted`, `SubmittedAt = closesAt`, `AutoSubmitted = true`. Use a conditional write (`status <> submitted`) so concurrent calls are safe.
- `not_started` attempts are left alone and reported as `missed` by derivation.
- The status endpoint may finalize many attempts; do the conditional writes concurrently with a small limit (for example 10).

### 9.3 Grading

- MCQ: a question is correct if the stored answer string equals `options[correctIndex]` exactly. Score is the sum of marks of correct questions. `maxScore = totalMarks`.
- Free text: no auto-grading. `score` is null. The frontend lets the student self-check and keeps that score locally.
- Unanswered questions score 0.

### 9.4 Answer key visibility

`keyVisible(exam, attempt, user, now)`:
- The creator: always true.
- `anytime`: true.
- `after_submit`: true if the caller's attempt is `submitted`.
- `after_close`: true if `now >= exam.ClosesAt`.

The rule is evaluated on the server at read time for `GET /exams/{id}` and `GET /exams/{id}/result`. Never include key fields in a response when it is false.

## 10. Security

- **Authorization on every handler.** Middleware loads the user from the cookie; each service method takes the user and checks ownership or role. Missing or foreign resources return `404`.
- **Input limits:** JSON bodies capped at 1 MB (200 KB for autosave) using `http.MaxBytesReader`. Reject unknown fields on create endpoints. Validate everything in section 8 before touching the store.
- **Presigned URLs** are short-lived (10 minutes) and scoped to one key; the bucket blocks all public access.
- **Secrets:** `JWT_SECRET` and `LLM_API_KEY` come from environment variables (how they are stored and injected is defined in `deployment-spec.md`). Never log them. Rotating is out of scope for the MVP.
- **Logging:** one structured line per request with request ID, user ID, method, path, status, duration. Never log passwords, tokens, cookies, answers, or document contents.
- **Passwords:** bcrypt cost 10. No password reset in the MVP.
- **Google tokens:** always verify audience, issuer, expiry, and signature server-side, and require `email_verified`. Never accept an email, name, or user ID from the client. Do not log or store the credential. `GOOGLE_CLIENT_ID` is not secret, but a token minted for a different client ID must be rejected, so use a separate client per stage.
- **Known MVP gaps (accepted):** no login throttling beyond Lambda concurrency, unverified roles, no email verification, no password reset. Minors' data needs a privacy review before real users (see `goals.md`).

## 11. Configuration

| Variable | Purpose |
|---|---|
| `ENV` | `local` or `prod`; `local` turns off the `Secure` cookie flag |
| `MODE` | `api` or `worker` (Lambda only) |
| `STAGE` | table name prefix, for example `prod` |
| `JWT_SECRET` | cookie signing key (32+ random bytes) |
| `ALLOWED_ORIGINS` | comma-separated origins for the CSRF check (the Vercel domain and `http://localhost:3000`) |
| `S3_BUCKET` | the uploads bucket |
| `WORKER_FUNCTION_NAME` | the worker Lambda to invoke |
| `LLM_API_KEY`, `LLM_MODEL` | model access (default `claude-opus-5-5`) |
| `LLM_EFFORT`, `LLM_TIMEOUT_SECONDS`, `GEN_MAX_*` caps | AI settings; names and defaults in `AI-spec.md` section 12 |
| `GOOGLE_CLIENT_ID` | the Google OAuth client ID that sign-in tokens must be issued for |
| `GEN_MAX_RUNNING`, `GEN_MAX_PER_DAY` | per-user limits (defaults 2 and 20) |

Load once at startup into a single struct; fail fast with a clear message if a required variable is missing.

## 12. What the backend requires from deployment

This section lists requirements only. How to provision them (tools, templates, accounts, regions, domains, pipelines) belongs in `deployment-spec.md`.

**Runtime**
- Two entry points exist: HTTP (`MODE=api`, or `cmd/server`) and job (`MODE=worker`, Lambda only). The HTTP path must answer every request within 29 seconds. A worker invocation may run up to 10 minutes.
- Must be reachable over HTTPS at the URL set as `API_URL` on the frontend (`frontend-spec.md` section 8.1), and must pass `Set-Cookie` and `Cookie` through unchanged.
- Must have outbound internet access to the LLM API and to Google (the backend fetches Google's public signing keys to verify sign-in tokens).
- Logs go to stdout as JSON lines.
- `GET /health` (no auth) returns `200 {"status":"ok"}`.

**Configuration:** every variable in section 11 must be set.

**DynamoDB:** the five tables, keys, indexes, and TTL attributes in section 6.2, exactly as written. Table names are `<STAGE>-<name>` (for example `prod-users`, `prod-documents`).

**S3**
- One private bucket with all public access blocked and server-side encryption on.
- Key layout `users/{ownerId}/{docId}/{sanitizedName}`.
- CORS allowing the frontend origin(s) and `http://localhost:3000` for the methods `GET`, `PUT`, and `HEAD`, all request headers, and exposing `ETag`. The browser uploads and views files directly, so uploads fail without this.
- Presigned URLs last 10 minutes.

**Permissions the code uses** (grant these and nothing broader, scoped to the project's own resources):
- DynamoDB: `GetItem`, `PutItem`, `UpdateItem`, `DeleteItem`, `Query`, `BatchGetItem`, `ConditionCheckItem` on the five tables and their indexes. (`TransactWriteItems` has no permission of its own; it is authorised through the underlying write actions.) The worker needs only `GetItem`, `PutItem`, `UpdateItem`, `Query`, `BatchGetItem`, and only on `documents` and `jobs`.
- S3: `s3:PutObject` (for presigning uploads), `s3:GetObject` (reads, presigned views, and `HeadObject`), `s3:DeleteObject`.
- Lambda, only for `LambdaRunner`: invoke permission on the worker function.

**Things the deployment should avoid on purpose:** a VPC without internet egress, NAT gateways, and any service not needed above. Each adds setup time or cost, and goals rule 7 forbids extras.

## 13. Testing

Write tests as you build each slice. They must pass with `go test ./...` and need no AWS access.

| Area | Must test |
|---|---|
| Auth | password signup and login; Google sign-in with a fake verifier: new user with a role (`201`), new user without a role (`role_required`, nothing created), existing Google user (`200`, role ignored), linking by verified email (password removed), unverified email rejected, wrong audience or expired token rejected; a Google-only account cannot log in with a password and gets the same error as a wrong password |
| Authorization | other users' documents and exams are `404`; students cannot create exams; only the creator reads status |
| Grading | MCQ scoring, unanswered, free text gives a nil score |
| Visibility | all four cases of 9.4; keys never appear in `GET /exams/{id}` or `/result` when false |
| Deadlines | autosave after deadline is `409`; submit within grace works; lazy finalize on an expired in-progress attempt; `missed` derivation; idempotent submit |
| Generation | counts validation and limits; short batch retried once then failed; duplicate dropping; ordering ascending and shuffled; cancel between batches; stale job reads as failed |
| Documents | MIME and size validation; complete rejects missing, wrong size, wrong magic bytes; delete leaves exams intact |
| Exam create | totalMarks computed, MCQ and free text shape validation, unknown assignee emails returned, practice-exam attempt row for creator |

Use a fake `Clock` for all time-dependent tests and the in-memory store. The generator is a fake that returns canned questions.

**Smoke test** (`make smoke`, uses `curl` against a base URL): sign up, `/me`, request an upload URL, `PUT` a small PDF, `complete`, list documents, create a generation job, poll until done, create an exam, `GET /exams?scope=open`, autosave, submit, fetch result. It exits non-zero on the first failure.

## 14. Build order (matches the Day 1 slices in `goals.md`)

1. **Scaffold and documents.** Go module, config, `Clock`, error helpers, `cmd/server`, `cmd/lambda`, health endpoint `GET /health` (no auth). `goals.md` requires a deployed health endpoint in the first hour of Day 1, so finish this step first and hand it to the process in `deployment-spec.md`. Then auth (signup, login, logout, `/me`, cookie, CSRF check), then documents (`upload-url`, `complete`, list, file URL, delete).
2. **Generation.** `Generator` interface and fake, job table, `/generate` endpoints, worker entry, in-process runner for local. Prove it with one real PDF and one real camera photo by midday before moving on.
3. **Exams.** Create, list scopes, assign, status.
4. **Taking.** Get exam with visibility rules, autosave, submit, lazy finalize, result.

Build each slice against the in-memory store first, then add the DynamoDB implementation. Do not start slice N+1 until slice N passes its tests and the smoke test.

## 15. Required changes to `frontend-spec.md`

The frontend spec was written before this backend spec. These items are missing or different there. Update that file (or confirm each change) before building the API:

1. **Auth endpoints.** Done: `frontend-spec.md` now lists signup, login, logout, `POST /auth/google` (with the `role_required` retry), and `GET /api/me`.
2. **Routes and the rewrite.** The frontend table shows `/api/...`; the backend routes have no `/api` prefix, because the rewrite strips it. No frontend change is needed beyond keeping the rewrite as written.
3. **Upload `type`.** In `POST /documents/upload-url`, `type` is the **MIME type** (for example `application/pdf`), not the short label ("PDF").
4. **Complete step.** The frontend's `Doc` needs no URL; confirmed.
5. **Missing endpoint.** Add `GET /exams/{id}/result`, which the Result screen needs.
6. **`GET /exams/{id}` shape.** It returns the exam, the caller's attempt (for resume), `keyVisible`, and `serverTime`. The frontend spec only says "GET /api/exams/:id".
7. **Autosave and submit.** `PUT /exams/{id}/attempt` and `POST /exams/{id}/submit` (frontend spec lists them with these paths already); confirm the body shapes in section 8.5.
8. **Create exam deadline.** The frontend should send `closesInSeconds` (from the "Closes in" select) or an absolute `closesAt` for the date picker, not compute `closesAt` from the device clock.
9. **Hidden-key results.** When the key is hidden, the backend also omits per-question correct and incorrect flags (not just the correct answer), so the frontend must not render the ✓ or ✗ pills in that case.
10. **Unknown assignees.** Publish and assign return the emails that did not match a user; the frontend should warn about them.
11. **Practice exams.** An exam with no assignees creates an attempt for the creator, so it appears in the creator's Open list.

## 16. Acceptance checklist

- [ ] `go test ./...` passes with no AWS credentials.
- [ ] `GET /health` returns `200` with no authentication.
- [ ] Signup, login, logout, and `/me` work, and the cookie is `HttpOnly`, `Secure`, `SameSite=Lax` with no `Domain`.
- [ ] `POST /auth/google` with a valid token signs in an existing Google user, creates a new user only when a valid role is sent, and otherwise returns `422 role_required` without creating anything.
- [ ] A token for a different client ID, an expired token, a bad signature, or an unverified email is rejected with the same generic `401`.
- [ ] Linking a password account via Google removes its password, and the same email then cannot log in with the old password.
- [ ] A 20 MB PDF uploads straight to S3 and the upload never passes through the Lambda body.
- [ ] `complete` rejects a missing object, a wrong-size object, and a file whose bytes do not match its type.
- [ ] A user cannot read, list, delete, or use another user's document in generation (all `404` or `422`).
- [ ] A presigned file URL stops working after about 10 minutes, and no code path makes an object public.
- [ ] Generation returns `202` immediately, the poll shows progress, cancel stops it, and a stale job reads as failed.
- [ ] Generated questions match the requested counts per mark value, are in ascending order by default, and are shuffled when requested.
- [ ] Creating an exam with unknown assignee emails succeeds and returns them.
- [ ] A student never receives a key field before section 9.4 allows it (verify the raw JSON).
- [ ] Autosave after the deadline returns `409 exam_closed`.
- [ ] An in-progress attempt left past the deadline appears as submitted (auto-submitted) on the next read and is graded.
- [ ] Submitting twice returns the same result.
- [ ] The result endpoint hides all correctness data when the key is hidden and still returns the score.
- [ ] `make smoke BASE_URL=...` completes the full flow against any running backend (local or deployed).
- [ ] No endpoint uses a method other than GET, POST, PUT, or DELETE, and every response carries `Cache-Control: no-store`.
- [ ] No request path uses a DynamoDB `Scan`.
- [ ] The README explains how to run and test the backend locally.

## 17. Out of scope

Password reset, email verification for password accounts, other sign-in providers (only Google is supported), screens for linking or unlinking sign-in methods, parent-student linking and the parent progress view, document sharing, folders and tags, HEIC, editing or deleting an exam after creation, a teacher viewing a student's individual answers, AI-assisted grading of free text, per-attempt durations, notifications, analytics, rate limiting beyond section 7.5, secret rotation, multi-region.

## 18. Decisions already made and open items

**Decided**
- Go, standard library first, one binary with `api` and `worker` modes and a local server entry point.
- REST only, with the conventions in section 8.0.
- DynamoDB is the database. No other database, no `Scan`.
- Question generation is a job with polling, one LLM call per mark value, behind a `JobRunner` interface.
- Deadlines, key visibility, and finalization are computed on the server at read time.
- Deployment details live in `deployment-spec.md`, not here.
- Two ways in: email and password, and Google (ID token verified server-side). Linking is by verified email and removes the password. A new Google user must supply a role (`role_required`).

**Open, decide before or during the build**
- The LLM model's current page, image and request-size limits; set the caps in `AI-spec.md` section 12 to match.
- The LLM budget (the per-user daily limit is the guard).
- The changes to `frontend-spec.md` listed in section 15.
- Anything about capacity mode, free-tier eligibility, regions, and accounts: now `deployment-spec.md`'s job.
- Google consent-screen status and authorized origins: `deployment-spec.md` section 8.0.
