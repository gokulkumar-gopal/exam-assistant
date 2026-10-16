# Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Build the Go REST backend (auth, documents, AI question generation, exams, attempts, usage) that `backend-spec.md` section 8 defines, testable with `go test ./...` and no AWS access.

**Architecture:** Handler -> service -> store layering over repository interfaces, each with an in-memory fake and a DynamoDB implementation. One binary with two entry points (`cmd/server` local with `InProcessRunner`, `cmd/lambda` with `MODE=api|worker` and `LambdaRunner`). Generation is a polled job resource; a `Generator` interface hides Anthropic and Gemini behind one prompt, schema and validation.

**Tech Stack:** Go 1.24+, `net/http` ServeMux, AWS SDK v2 (dynamodb, attributevalue, s3, lambda), aws-lambda-go + httpadapter, oklog/ulid/v2, x/crypto/bcrypt, golang-jwt/jwt/v5, google.golang.org/api/idtoken, pdfcpu, anthropic-sdk-go, google.golang.org/genai, log/slog.

**Spec:** `backend-spec.md` (section 8 is the API contract), `AI-spec.md` (wins on everything LLM), `deployment-spec.md` sections on env, IAM and tables (read-only here), `frontend-spec.md` section 8 (list of calls).

**Pacing:** "3 days" in `goals.md` is guidance only. Milestones are ordered by dependency, not by day; the owner may finish all of them in 2 days. P1 work is marked **[P1, cuttable]**; cut order is M5b (peeks) first, then the usage page endpoint of M5a. The spend counter that feeds the budget guard is P0 and lives in M2.

---

## Global Constraints

- Go 1.24+, stdlib `net/http`; no web framework, ORM, DI container, config library, validation library or job library (backend 3).
- REST only; no GraphQL/gRPC/WebSocket/SSE/long-polling (backend 1.8). Routes have no `/api` prefix. Every response: `Cache-Control: no-store`; deadline/status responses carry `serverTime`.
- DynamoDB only; **no `Scan`**, no `BatchWriteItem`; index projections `INCLUDE`, never `ALL`; `ConsistentRead: true` on base-table reads that follow a write; SDK retryer at most 5 attempts, then `503 upstream_unavailable` (backend 4).
- Server time is the only time. Services take a `Clock`, never `time.Now()` (backend 5.1).
- Never leak `correctIndex`/`modelAnswer` except per backend 9.4 and revealed questions (backend 1.5).
- Errors: `{error:{code,message,fields?}}`; codes `unauthenticated 401, forbidden 403, not_found 404, conflict 409, exam_closed 409, unsupported_media_type 415, validation 422, role_required 422, rate_limited 429, upstream_unavailable 502/503, internal 500`. `message` never contains internals.
- Missing or foreign documents/exams are `404`, not `403`.
- Auth: bcrypt cost 10; password 8-72 bytes; name 1-80 chars; email trimmed, lowercased; login failure always "Email or password is incorrect." and bcrypt runs against a dummy hash for unknown/Google-only accounts. Cookie `session`: JWT HS256 with `sub`, `role`, `iat`, `exp` 7 days; `HttpOnly`, `Secure` (off only when `ENV=local`), `SameSite=Lax`, `Path=/`, `Max-Age=604800`, no `Domain`. Reject `iat < SessionsValidAfter`.
- CSRF: for POST/PUT/PATCH/DELETE, `403` if `Origin` present and not in `ALLOWED_ORIGINS`; absent Origin allowed.
- Body caps: 1 MB (400 KB for autosave) via `http.MaxBytesReader`; unknown fields rejected on create endpoints; non-JSON body `415`.
- Upload: MIME `application/pdf|image/jpeg|image/png|text/plain`, `sizeBytes` 1..20971520; presigned PUT 10 min with `Content-Type` and `Content-Length` signed; pending row TTL 24 h; key `users/{ownerId}/{docId}/{sanitizedName}`, name sanitized to letters, digits, `.-_ ` and space, max 120 chars; presigned GET 10 min, `inline`.
- Generation caps: sources 1..10, total bytes <= 20971520, total PDF pages <= 40, input tokens <= 80000, counts keys 1..5 each 0..30, at least one non-zero, <= 60 questions, total marks <= 200, 15 questions per call, <= 10 first-attempt calls, <= 20 with retries.
- Job: claim `queued->running` conditional; 8-minute deadline; heartbeat ~20 s; stale after 6 minutes; every post-claim status write conditional; first call alone then <= 3 concurrent; SDK max retries 1; per-call timeout `LLM_TIMEOUT_SECONDS` (120); questions in item `<jobId>#questions`; job TTL 24 h.
- Quotas (UTC day/month; TTL day items 3 d, month items 40 d): generate 3/user/day, regen 10/user/day, global 10 jobs/day, `LLM_MONTHLY_BUDGET_USD` 15; running soft limit `GEN_MAX_RUNNING` 2.
- Deadline: `acceptingUntil = closesAt + 60s` is the single cutoff for autosave, submit, finalize, list classification, key "over". `SubmittedAt = min(now, closesAt)`; auto-finalized `SubmittedAt = closesAt`.
- Exam create: title 1-120, 1-60 questions, marks 1-5, deadline 1 minute..30 days ahead, `maxPeeks` 0..10 and <= question count (default 3 self-practice, 0 when assigning), assignees: teacher/parent <= 50 anyone, student <= 10 students only, non-student reported as unknown; `Idempotency-Key` 8-64 chars `[A-Za-z0-9_-]`, equals exam ID.
- Autosave: answers values <= 5,000 chars; keys must be question IDs; conditional `status <> submitted`.
- Scores in integer half-marks (`ScoreHalves`, `ScoreBeforePeeksHalves`), JSON = halves/2.
- Logging: JSON `slog`, one line per request (request ID, user ID, method, path, status, duration); never log passwords, tokens, cookies, answers, document contents, prompts, model output or question text.
- Secrets: API has `JWT_SECRET`, not `LLM_API_KEY`/`GEMINI_API_KEY`; worker the reverse. `ENV` is `local|production`; tables `<STAGE>-<name>`.
- Commit messages end with the attribution line from the session's system reminder.

## Review Focus

Failure modes the specs imply but no table row in backend 13 pins down. Each is tested in the owning task.

1. **Boundary instants.** `now == closesAt`, `now == acceptingUntil - 1ns`, `now == acceptingUntil`: autosave/submit/finalize/classification must all agree (`>=` means over). Owner: B-4.3, B-4.4, B-3.3.
2. **Heartbeat and status races.** A heartbeat tick arriving after the worker set `failed`/`cancelled`/`done` must not resurrect or touch the status; a duplicate claim after `done` exits. Owner: B-2.4.
3. **TransactionCanceled reasons.** A cancelled create-job transaction must map to the right message (per-user limit vs global limit vs budget), and a refused job must leave no quota counter changed. Owner: B-2.2.
4. **Hostile file names and types.** Names like `../../x.pdf`, `""`, 300 characters, Unicode, and a `.pdf` name with `image/png` MIME must produce a safe key and the right `422`. Owner: B-1.5, B-1.6.
5. **Answer strings that are not options.** MCQ answer equal to an option after trimming/case change, an answer not in options, and a duplicate-text option pair created by a client: grading is exact-match only, and create rejects duplicate options. Owner: B-3.2, B-4.4.

## File Structure

Paths under `backend/` (backend 5.1). `internal/apperr` is added so services never import `api`.

| Path | Responsibility |
|---|---|
| `cmd/server/main.go` | local HTTP server, `InProcessRunner` |
| `cmd/lambda/main.go` | `MODE=api` (httpadapter) or `MODE=worker` (event `{jobId}`) |
| `cmd/ai-eval/main.go` | golden-set command (AI-spec 13.2), real API, manual only |
| `internal/config/config.go` | load env once; fail-fast validation |
| `internal/clock/clock.go` | `Clock`, `Real`, `Fake` |
| `internal/apperr/apperr.go` | typed errors with HTTP status, code, message, fields |
| `internal/api/` | `router.go`, `middleware.go` (request ID, logging, CSRF, auth, body caps), `respond.go`, one handler file per area |
| `internal/auth/` | passwords, sessions, Google verifier interface, signup/login/google service |
| `internal/docs/` | document service, S3 `Presigner` interface, sniffing, page count |
| `internal/gen/` | `catalog.go`, `generator.go`, `runner.go`, `worker.go`, `quota.go`, `validate.go`, `sources.go`, `anthropic.go`, `gemini.go`, `prompt/` |
| `internal/exams/` | create, assign, decline, lists, take, finalize, grade, keyvisible, result, reveal, usage |
| `internal/store/` | `interfaces.go`, `memory/`, `dynamo/` |
| `scripts/smoke.sh`, `Makefile`, `README.md` | smoke test, targets (`build test run smoke ai-eval`), local run notes |
| `testdata/ai/` | golden-set files (owner supplies) |

---

## M0 Skeletons

### Task B-0.1: Scaffold, config, clock, errors, health, entry points
**Spec:** backend 5, 5.1, 8.0, 8.1, 11, 12; AI-spec 12
**Needs:** none
**Files:** Create `backend/go.mod`, `internal/config/config.go`, `internal/clock/clock.go`, `internal/apperr/apperr.go`, `internal/api/{router,middleware,respond}.go`, `cmd/server/main.go`, `cmd/lambda/main.go`; Test `internal/config/config_test.go`, `internal/api/api_test.go`, `internal/clock/clock_test.go`
**Interfaces:**
Produces: `clock.Clock{ Now() time.Time }`, `clock.Real{}`, `clock.NewFake(t time.Time) *Fake` with `Set`, `Advance(d)`; `apperr.Error{Status int; Code, Message string; Fields map[string]string}` with constructors `apperr.NotFound()`, `Forbidden(msg)`, `Conflict(msg)`, `ExamClosed()`, `Validation(msg, fields)`, `RateLimited(msg)`, `Unauthenticated(msg)`, `Upstream()`, `Internal(err)`; `config.Load(getenv func(string) string) (Config, error)`; `api.NewRouter(deps Deps) http.Handler`; `api.WriteJSON(w, status, v)`, `api.WriteError(w, r, err)`, `api.Decode(w, r, &v, maxBytes)`.
- [ ] Write failing tests: `TestLoadConfigDefaults` (empty optional vars give `GEN_MAX_PER_DAY=3`, `GEN_MAX_REGEN_PER_DAY=10`, `GEN_MAX_GLOBAL_PER_DAY=10`, `GEN_MAX_RUNNING=2`, `LLM_MONTHLY_BUDGET_USD=15`, `LLM_TIMEOUT_SECONDS=120`, `LLM_EFFORT=medium`, `GEN_MAX_TOTAL_BYTES=20971520`, `GEN_MAX_PDF_PAGES=40`); `TestLoadConfigFailsFast` (missing `ENV`, `STAGE`, `S3_BUCKET`; api mode missing `JWT_SECRET`, `ALLOWED_ORIGINS`, `GOOGLE_CLIENT_ID`; worker mode with `gemini` enabled and no `GEMINI_API_KEY`; no provider enabled; `LLM_MODEL` set but not enabled — the model check is wired in B-2.1); `TestHealth` (`GET /health` 200 `{"status":"ok"}`, `Cache-Control: no-store`); `TestErrorShape` (`apperr.Validation` renders `{"error":{"code":"validation","message":...,"fields":{...}}}` with 422); `TestInternalErrorHidesDetail`; `TestNonJSONBody415` on a POST with `text/plain`; `TestBodyCap` (1 MB+1 byte gives 422); `TestFakeClockAdvance`.
- [ ] Run `go test ./...`; expect FAIL (packages missing).
- [ ] Implement the signatures above. `Config` is one struct including `Mode`, `Providers []string`, all `GEN_MAX_*`; `Load` takes `getenv` so tests pass a map. Request middleware assigns a ULID request ID, logs one slog line, recovers panics into `500 internal`.
- [ ] Run `go test ./...`; expect PASS. `cmd/server` and `cmd/lambda` compile (`go build ./...`).
- [ ] Commit: "Backend scaffold: config, clock, errors, health".

### Task B-0.2: Makefile and local run
**Spec:** backend 5.1, 13; deployment-spec Makefile note (`-include .env.deploy.$(STAGE)`)
**Needs:** B-0.1
**Files:** Create `backend/Makefile`, `backend/README.md`, `backend/scripts/smoke.sh` (health-only stub, extended in B-4.6)
**Interfaces:** Produces make targets `build`, `test`, `run`, `smoke BASE_URL=...`, `ai-eval`.
- [ ] Verification: `make test` on a fresh clone with no `.env.deploy.*` file succeeds (the include is `-include`); `make run` serves `/health` on `:8080`; `make smoke BASE_URL=http://localhost:8080` prints OK and exits 0; with the server stopped it exits non-zero.
- [ ] Commit: "Makefile, README, smoke stub".

### Milestone check M0
`cd backend && go test ./... && go build ./... && make run &` then `curl -s localhost:8080/health` returns `{"status":"ok"}`. Deployed check is D-0 (`curl $API_URL/health`).

---

## M1 Documents (auth and documents)

### Task B-1.1: Store interfaces and memory fake for users and documents
**Spec:** backend 6.1, 6.3, 6.4
**Needs:** B-0.1
**Files:** Create `internal/store/interfaces.go`, `internal/store/memory/{users,docs}.go`; Test `internal/store/memory/memory_test.go`
**Interfaces:**
Produces: entity structs from backend 6.1 in package `store` (`User`, `Document`, later `Question`, `Exam`, `Attempt`, `Job`);
```go
type UserStore interface {
    CreateUser(ctx, u User) error                 // ErrConflict on email or google lock clash
    CreateGoogleUser(ctx, u User) error           // writes user + email lock + google lock atomically
    LinkGoogle(ctx, userID, sub string, validAfter int64) error // atomic: lock, set GoogleSub, clear PasswordHash, set SessionsValidAfter; ErrConflict if user already has GoogleSub
    GetByID(ctx, id string) (User, error)         // ErrNotFound
    GetByEmail(ctx, email string) (User, error)
    GetByGoogleSub(ctx, sub string) (User, error)
    GetManyByEmail(ctx, emails []string) (map[string]User, error)
    GetManyByID(ctx, ids []string) (map[string]User, error)
}
type DocStore interface {
    PutDoc(ctx, d Document) error
    GetDoc(ctx, ownerID, id string) (Document, error)
    ListReady(ctx, ownerID string, limit int) ([]Document, error) // newest first, cap 500
    GetDocs(ctx, ownerID string, ids []string) ([]Document, error)
    MarkReady(ctx, ownerID, id string, pages int) error
    DeleteDoc(ctx, ownerID, id string) error
}
var ErrNotFound, ErrConflict, ErrPrecondition error
```
- [ ] Write failing tests: `TestCreateUserEmailUnique`, `TestGoogleUserLocks` (second user with same sub is `ErrConflict`), `TestLinkGoogleAtomic` (hash cleared, `SessionsValidAfter` set, second link `ErrConflict`), `TestListReadyNewestFirstExcludesPending`, `TestDocsAreOwnerScoped` (other owner gets `ErrNotFound`).
- [ ] Run; expect FAIL. Implement interfaces and memory fakes (mutex-guarded maps). Run; expect PASS.
- [ ] Commit: "Store interfaces and memory fake: users, documents".

### Task B-1.2: DynamoDB implementation of UserStore and DocStore
**Spec:** backend 4, 6.2, 6.4, 12 (permissions)
**Needs:** B-1.1
**Files:** Create `internal/store/dynamo/{client,users,docs}.go`; Test `internal/store/dynamo/users_test.go` (uses a small `DynamoAPI` interface fake that records requests; no AWS)
**Interfaces:** Consumes `store.UserStore`, `store.DocStore`. Produces `dynamo.New(api DynamoAPI, stage string) *Stores`; table names `<STAGE>-<name>`; `DynamoAPI` is the subset of SDK methods used (`GetItem, PutItem, UpdateItem, DeleteItem, Query, BatchGetItem, TransactWriteItems`).
- [ ] Failing tests with the recording fake: `TestCreateUserTransaction` (one `TransactWriteItems` with user item + `email#<email>` lock, each `attribute_not_exists(id)`); `TestCreateGoogleUserHasThreeItems`; `TestLinkGoogleCondition` (condition requires user item has no `GoogleSub`; update removes `passwordHash`); `TestCancelledTransactionIsConflict`; `TestGetDocUsesConsistentRead`; `TestListReadyIsQueryNotScan` (asserts the recorder never saw a Scan and used key `ownerId`).
- [ ] Run; expect FAIL. Implement with `attributevalue` marshalling; map `ConditionalCheckFailed`/`TransactionCanceled` to `store.ErrConflict`/`ErrPrecondition`. Run; expect PASS.
- [ ] Commit: "DynamoDB user and document stores".

### Task B-1.3: Password auth, sessions, CSRF, auth middleware
**Spec:** backend 8.2, 10; Review Focus none; Global Constraints (auth)
**Needs:** B-1.1, B-0.1
**Files:** Create `internal/auth/{password,session,service}.go`, `internal/api/auth_handlers.go`; Modify `internal/api/middleware.go`; Test `internal/auth/auth_test.go`, `internal/api/auth_http_test.go`
**Interfaces:**
Produces: `auth.Service{Signup(ctx, SignupInput) (store.User, error); Login(ctx, email, password string) (store.User, error)}`; `auth.Sessions{Issue(u store.User, now time.Time) (cookie *http.Cookie); Parse(token string, now time.Time) (Claims, error)}`; `api.RequireUser` middleware putting `store.User` in context (loads the user each request, rejects `iat < SessionsValidAfter`); routes `POST /auth/signup`, `POST /auth/login`, `POST /auth/logout`, `GET /me`.
- [ ] Failing tests: `TestSignupCreatesUserAndCookie` (201, cookie attributes per Global Constraints); `TestSignupDuplicateEmail409` ("An account with that email already exists."); `TestSignupValidation` (password of 7 bytes, 73 bytes, a 30-character password of 3-byte runes is rejected as 90 bytes, role `admin`, empty name, name of 81 chars, each `422` with `fields`); `TestEmailNormalised` (`  A@X.com ` stores `a@x.com`); `TestLoginWrongPasswordAndUnknownEmailIdentical` (same status and body); `TestLoginRunsBcryptForUnknownEmail` (injected hasher counts calls); `TestGoogleOnlyAccountPasswordLoginIdentical`; `TestLogoutClearsCookie204`; `TestMe401WithoutCookie`; `TestMeRejectsOldIat` (iat = validAfter-1 is 401, iat = validAfter is accepted); `TestRejectsAlgNoneAndWrongKey`; `TestCSRFRejectsForeignOrigin` (403), `TestCSRFAllowsNoOrigin`, `TestCSRFAllowsConfiguredOrigin`; `TestCookieSecureOffOnlyLocal`.
- [ ] Run; expect FAIL. Implement; `Hasher` interface (`Hash`, `Compare`) so the dummy-hash test is observable; JWT verify pins `HS256`.
- [ ] Run; expect PASS. Commit: "Password auth, sessions, CSRF".

### Task B-1.4: Google sign-in and account linking
**Spec:** backend 8.2 (Google), 10; D6
**Needs:** B-1.3
**Files:** Create `internal/auth/google.go`, `internal/api/google_handler.go`; Test `internal/auth/google_test.go`
**Interfaces:** Produces `auth.IDTokenVerifier{ Verify(ctx, credential string) (GoogleClaims, error) }` (`GoogleClaims{Sub, Email, Name string; EmailVerified bool}`), production `auth.NewIDTokenVerifier(clientID string)` over `idtoken.Validate`; `auth.Service.Google(ctx, credential, role string) (user store.User, created bool, err error)`; route `POST /auth/google`.
- [ ] Failing tests (fake verifier): `TestGoogleNewUserWithRole201`; `TestGoogleNewUserWithoutRoleIs422RoleRequiredAndCreatesNothing`; `TestGoogleInvalidRoleIsRoleRequired`; `TestGoogleKnownUser200RoleIgnored`; `TestGoogleLinksPasswordAccount` (hash removed, `SessionsValidAfter` set to now, response sets a fresh cookie whose `iat >= SessionsValidAfter`, an older cookie then gets 401 on `/me`); `TestGoogleDifferentSubSameEmail409` with message "This email is already linked to another sign-in. Use the original sign-in method." and no change; `TestGoogleUnverifiedEmailRejected`, `TestGoogleEmptyEmailRejected`, `TestGoogleVerifierErrorIs401Generic` ("Google sign-in failed. Try again."); `TestGoogleNameFallsBackToEmailLocalPart`; `TestGoogleIgnoresClientSuppliedEmailAndName`; `TestGoogleOnlyUserCannotPasswordLogin` (same 401 as wrong password).
- [ ] Run; expect FAIL. Implement. The production verifier checks audience equals `GOOGLE_CLIENT_ID` and issuer is `accounts.google.com` or `https://accounts.google.com` (add an explicit issuer check after `Validate`).
- [ ] Run; expect PASS. Commit: "Google sign-in with verified-email linking".

### Task B-1.5: Documents: upload-url
**Spec:** backend 8.3 (upload URL rules), 12 (S3)
**Needs:** B-1.3
**Files:** Create `internal/docs/{service,names}.go`, `internal/api/docs_handlers.go`; Test `internal/docs/service_test.go`
**Interfaces:**
Produces: `docs.Blob interface { PresignPut(ctx, key, contentType string, size int64, ttl time.Duration) (url string, err error); PresignGet(ctx, key, contentType string, ttl time.Duration) (url string, err error); Head(ctx, key string) (size int64, err error) /* store.ErrNotFound if missing */; Get(ctx, key string, max int64) ([]byte, error); Delete(ctx, key string) error }` with a memory fake; `docs.Service.UploadURL(ctx, user, in UploadInput) (UploadOut, error)`; `docs.SanitizeName(string) string`; route `POST /documents/upload-url` returning `201 {docId, uploadUrl, headers:{"Content-Type":mime}}`.
- [ ] Failing tests: `TestUploadURLCreatesPendingRowWith24hTTL`; `TestUploadURLKeyShape` (`users/<owner>/<docId>/<name>`); `TestUploadURLRejectsMime` (`application/zip`, `image/heic`, `""`); `TestUploadURLSize` (0, -1 and 20971521 are 422; 1 and 20971520 accepted); `TestSanitizeName` table: `../../x.pdf` has no `/` and no leading dots-only segments, `""` becomes `file`, 300 chars is cut to 120, `rés umé.pdf` keeps only allowed characters; `TestPresignParams` (10 minutes, signs the exact size and content type).
- [ ] Run; expect FAIL. Implement; the S3 implementation (`Blob` over `s3.PresignClient`) lands in the same file set `internal/docs/s3blob.go`, covered by a request-recording unit test (`TestS3PresignSignsContentLength`).
- [ ] Run; expect PASS. Commit: "Document upload URLs".

### Task B-1.6: Documents: complete (verify, sniff, pages)
**Spec:** backend 8.3 (Complete rules)
**Needs:** B-1.5
**Files:** Create `internal/docs/{complete,sniff,pages}.go`; Test `internal/docs/complete_test.go`
**Interfaces:** Produces `docs.Service.Complete(ctx, user, docID string) (store.Document, error)`; `docs.Sniff(first []byte, mime string) bool`; `docs.CountPages(mime string, data []byte) (int, error)`; route `POST /documents/{id}/complete`.
- [ ] Failing tests: `TestCompleteMissingObject409` ("The upload didn't finish. Try again."); `TestCompleteSizeMismatchDeletesObjectAndRow422`; `TestCompleteMagicBytesMismatch422` ("That file doesn't match its type."; row and object deleted), including a `.pdf` name sent as `image/png` and a PNG body declared as PDF; `TestCompleteTXTMustBeUTF8First4KB`; `TestPagesPDF` (real 3-page fixture gives 3), `TestPagesImage1`, `TestPagesTXT` (`ceil(chars/3000)`, minimum 1: 0 chars=1, 3001 chars=2); `TestCompleteIdempotent` (second call returns the ready doc, no second `Get`); `TestCompleteOtherUsersDoc404`; `TestCompleteClearsTTL`.
- [ ] Run; expect FAIL. Implement.
- [ ] Run; expect PASS. Commit: "Document complete: verify, sniff, count pages".

### Task B-1.7: Documents: list, file URL, delete
**Spec:** backend 8.3
**Needs:** B-1.6
**Files:** Modify `internal/docs/service.go`, `internal/api/docs_handlers.go`; Test `internal/api/docs_http_test.go`
**Interfaces:** Produces `GET /documents` (`{documents, serverTime}`, ready only), `GET /documents/{id}/file` (`{url, expiresAt}`, `ResponseContentType` = stored MIME, disposition inline), `DELETE /documents/{id}` (204, object then row).
- [ ] Failing tests: `TestListDocsReadyOnlyNewestFirst`; `TestFileURL404ForOtherUser`; `TestFileURLExpiresAt10Min` (fake clock); `TestDeleteRemovesObjectThenRow` (order recorded); `TestDeleteIdempotent204`; `TestDeleteKeepsExistingExamQuestions` (added to exam tests in B-3.2 once exams exist: mark with a TODO-free cross-reference test in B-3.2).
- [ ] Run; expect FAIL. Implement. Run; expect PASS. Commit: "Document list, file URL, delete".

### Milestone check M1
`go test ./...` passes. `make run`; `curl -c jar -X POST localhost:8080/auth/signup -d '{"name":"A","email":"a@x.com","password":"password1","role":"student"}' -H 'Content-Type: application/json'` gives 201; `curl -b jar localhost:8080/me` gives the user; `curl -b jar -X POST .../documents/upload-url ...` gives 201 with `uploadUrl`. (Smoke steps extend in B-4.6.) Deployed check: D-1.

---

## M2 Generation

### Task B-2.1: Model catalog, price table, `GET /ai/models`
**Spec:** AI-spec 3.1, 3.2, 12; backend 8.6, 11
**Needs:** B-0.1, B-1.3
**Files:** Create `internal/gen/catalog.go`, `internal/api/ai_handlers.go`; Modify `internal/config/config.go`; Test `internal/gen/catalog_test.go`
**Interfaces:**
Produces:
```go
type Model struct{ ID, Name, Provider, Tier string; InPerMTok, OutPerMTok float64 }
func Catalog() []Model                                    // cheapest first: gemini-3.1-flash-lite, claude-haiku-4-5, claude-sonnet-5-5, claude-opus-5-5
func Enabled(providers []string) []Model
func DefaultModel(enabled []Model, llmModel string) (Model, error) // LLM_MODEL if set and enabled, else cheapest; error if set but not enabled
func CostMicros(m Model, u Usage) int64                   // whole micro-dollars: in, out, cacheWrite x1.25, cacheRead x0.1 (Anthropic); Gemini no discount unless cacheRead>0
func ApproxJobCost(m Model) (small, heavy float64)        // small = 15k in + 4x3k out; heavy = 60k in + 5x4k out, no cache discount
type Usage struct{ Input, Output, CacheRead, CacheWrite int64 }
```
route `GET /ai/models` -> `{models:[{id,name,provider,tier,priceInPerMTok,priceOutPerMTok,approxJobCost:{small,heavy},default}], serverTime}`.
- [ ] Failing tests: `TestCatalogOrderCheapestFirst`; `TestEnabledByProviders` (`anthropic` only gives haiku, sonnet, opus; `gemini,anthropic` gives four); `TestDefaultIsCheapestEnabled` (gemini+anthropic gives gemini-3.1-flash-lite, anthropic only gives claude-haiku-4-5); `TestDefaultHonoursLLMModel`; `TestDefaultErrorsWhenNotEnabled`; `TestCostMicrosGemini` (1,000,000 in + 1,000,000 out = 1,750,000); `TestCostMicrosOpusWithCache` (cacheWrite 1M = 5,000,000, cacheRead 1M = 400,000); `TestApproxJobCostGeminiSmall` (about 0.03 after rounding to cents: assert 0.03 within 0.005; heavy within 0.01 of 0.10); `TestModelsEndpointOnlyEnabledExactlyOneDefault`; `TestModelsRequiresLogin`; `TestStartupFailsWhenNoProvider`.
- [ ] Run; expect FAIL. Implement. Provider names `gemini`, `anthropic`. Model IDs and prices are unverified: add a `// verify IDs and prices before launch` note only in the plan's Open questions, not in code.
- [ ] Run; expect PASS. Commit: "Model catalog and /ai/models".

### Task B-2.2: Job store, quota transaction, budget guard
**Spec:** backend 6.1 (Job), 6.2, 7.2 (step 1), 7.5, 6.4; AI-spec 10.4
**Needs:** B-1.2 (dynamo client), B-2.1
**Files:** Create `internal/store/memory/jobs.go`, `internal/store/dynamo/jobs.go`, `internal/gen/quota.go`; Test `internal/gen/quota_test.go`, `internal/store/dynamo/jobs_test.go`
**Interfaces:**
Produces:
```go
type JobStore interface {
    CreateJobWithQuota(ctx, j Job, q QuotaCheck) error        // atomic; returns *QuotaError{Kind: "user"|"regen"|"global"|"budget"}
    GetJob(ctx, id string) (Job, error)                       // consistent
    ClaimJob(ctx, id string, now time.Time) (bool, error)     // queued->running conditional
    Heartbeat(ctx, id string, now time.Time, p Progress) error // only while status=running
    FinishJob(ctx, id string, to string, upd JobFinish) (bool, error) // conditional: running->done|failed; never overwrites failed/cancelled
    RequestCancel(ctx, ownerID, id string) error
    MarkFailedIfQueuedOrRunning(ctx, id, msg string) error
    PutQuestions(ctx, jobID string, qs []Question) error     // item <jobId>#questions
    GetQuestions(ctx, jobID string) ([]Question, error)
    ListOwnerJobs(ctx, ownerID string) ([]JobSummary, error)  // owner-index
    AddSpend(ctx, month string, micros int64) (total int64, err error) // quota#global#<yyyymm>.spendMicros ADD
    SpendThisMonth(ctx, month string) (int64, error)
}
type QuotaCheck struct{ UserID, Day, Month string; Regen bool; MaxUser, MaxRegen, MaxGlobal int; BudgetMicros int64 }
```
- [ ] Failing tests: `TestCreateJobIncrementsCountersAtomically`; `TestUserDailyGenerateLimit3` (4th is `QuotaError{user}`); `TestRegenCounterSeparate10`; `TestGlobalLimit10`; `TestBudgetRefusalWhenSpendAtOrAboveBudget` (spend == budget refuses; budget-1 allows); `TestRefusedJobLeavesNoCounterBehind` (counters unchanged, no job item); `TestQuotaErrorKindMapsFromCancellationReasons` (dynamo fake returns `CancellationReasons` array positions; assert kind per position); `TestQuotaItemsHaveTTLs` (day items 3 d, month items 40 d); `TestAddSpendIsADD` (expression is `ADD spendMicros :n`); `TestHandlerMessages` (429 `rate_limited`, budget message "The monthly AI budget has been used. Try again next month.").
- [ ] Run; expect FAIL. Implement memory and dynamo versions (single `TransactWriteItems` with job put + quota Updates with `ConditionExpression`s; never BatchWriteItem).
- [ ] Run; expect PASS. Commit: "Job store, daily quotas, monthly budget guard".

### Task B-2.3: `POST /generate`, `GET /generate/{id}`, `DELETE /generate/{id}`
**Spec:** backend 7.2 (1, 8), 7.5, 8.4
**Needs:** B-2.2, B-1.7
**Files:** Create `internal/gen/service.go`, `internal/api/gen_handlers.go`; Test `internal/gen/service_test.go`
**Interfaces:**
Consumes `store.JobStore`, `store.DocStore`, `gen.JobRunner{ Start(ctx, jobID string) error }`, `DefaultModel`, `Enabled`. Produces `gen.Service.Create(ctx, user, GenerateRequest) (jobID string, err error)`, `Poll(ctx, user, jobID) (PollView, error)`, `Cancel(ctx, user, jobID) error`; routes per backend 8.4 with `Location: /generate/{jobId}` on 202.
- [ ] Failing tests: `TestCountsValidation` (key `"6"`, count 31, all zero, 61 total, total marks 201 each 422; 60 total with marks <= 200 accepted); `TestSourcesMustBeOwnedAndReady` (other user's id, pending doc: 422); `TestSourceCountOver10`; `TestTotalBytesOver20MB422Generic` ("These files are too large to use together. Select fewer or smaller files."); `TestSinglePDFOverPagesCapNamesFile` (`fields.sourceIds` contains the file name, 41 pages); `TestPagesSumOver40`; `TestModelDefaultsAndUnknown422` (`fields.model`, and an enabled-by-catalog but provider-disabled model is 422); `TestCreateReturns202AndLocation`; `TestRunningLimitSoft2` (two non-stale queued/running jobs block a third with 429; a job `UpdatedAt` 6m01s old is stale and does not count); `TestStartFailureMarksJobFailedAndReturns503` ("We couldn't start that. Try again."); `TestPollOwnerOnly404`; `TestPollDoneReadsQuestionsItemOnlyWhenDone` (fake counts `GetQuestions` calls: 0 while running/failed, 1 when done); `TestPollStaleReportsFailedTooLong` ("That took too long. Try again."); `TestStaleJobCannotLaterBecomeDone` (covered with B-2.4 worker); `TestCancelSetsFlagIdempotent204`; `TestPollIncludesModelAndProgress`.
- [ ] Run; expect FAIL. Implement; validation order: shape, caps, documents, model, then quota transaction, then `Start`.
- [ ] Run; expect PASS. Commit: "Generate endpoints: create, poll, cancel".

### Task B-2.4: JobRunner implementations and worker claim/heartbeat/deadline
**Spec:** backend 5, 7.2 (2, 3, 5, 6, 7), 12 (Lambda); Review Focus 2
**Needs:** B-2.3
**Files:** Create `internal/gen/runner.go`, `internal/gen/worker.go` (skeleton `Worker.Run`), `cmd/lambda/worker.go`; Modify `cmd/server/main.go`; Test `internal/gen/worker_test.go`
**Interfaces:**
Produces `gen.InProcessRunner{Worker *Worker}`, `gen.LambdaRunner{Client LambdaInvoker; FunctionName string}` (`InvocationType: Event`, payload `{"jobId":"…"}`), `gen.Worker{Jobs, Docs, Blob, Gens GeneratorFactory, Clock, Cfg}` with `Run(ctx context.Context, jobID string) error`, `heartbeatInterval` and `jobDeadline` as struct fields (defaults 20 s and 8 minutes, injectable).
- [ ] Failing tests: `TestClaimOnlyWhenQueued` (second `Run` on same job exits without calling the generator); `TestDuplicateDeliveryAfterDoneExits`; `TestDeadlineFailsJobWithTooLongMessage` (fake clock/short deadline injected); `TestHeartbeatWritesUpdatedAt` (tick advances `UpdatedAt` during a blocked generator call); `TestHeartbeatStopsAndDoesNotTouchStatusAfterFinish` (Review Focus 2: after `failed`/`cancelled`/`done`, a late tick writes nothing — `Heartbeat` is conditional on `running`); `TestLateWorkerCannotResurrectStaleFailedJob` (a job the read path reported failed and `MarkFailedIfQueuedOrRunning` persisted is not overwritten to `done`); `TestLambdaRunnerInvokesAsyncWithJobID`; `TestLambdaRunnerErrorPropagates`; `TestWorkerModeEventParsed` (`cmd/lambda` decodes `{jobId}`).
- [ ] Run; expect FAIL. Implement. `Run` derives `ctx` with the deadline; heartbeat goroutine is cancelled before `FinishJob` is called.
- [ ] Run; expect PASS. Commit: "JobRunner implementations and worker lifecycle".

### Task B-2.5: Prompt package and source preparation
**Spec:** AI-spec 4, 5.1-5.5, 6.1; backend 7.3
**Needs:** B-2.1
**Files:** Create `internal/gen/generator.go` (types), `internal/gen/prompt/prompt.go`, `internal/gen/sources.go`; Test `internal/gen/prompt/prompt_test.go`, `internal/gen/sources_test.go`
**Interfaces:**
Produces the AI-spec 4 types exactly: `Generator`, `GenInput{Sources []SourceFile; Type string; Marks, Count int; Avoid []string; Model string}`, `GenOutput{Questions []RawQuestion; UnreadableSources []string; Usage Usage; StopReason, RequestID string}`, `SourceFile{Label, ContentType string; Bytes []byte}`; `prompt.PromptVersion` const (e.g. `"2026-10-v1"`); `prompt.System() string`; `prompt.Instruction(in GenInput) string`; `prompt.Schema(kind string) map[string]any` (mcq/free per AI-spec 6.1); `gen.PrepareSources(docs []store.Document, blobs map[string][]byte) (sources []SourceFile, labelToDoc map[string]store.Document, failed []FailedSource)` (stable order by document ID, labels `doc-1…`); `gen.Downscale(img []byte) ([]byte, error)` (longest side > 2000 px, EXIF orientation applied first, JPEG ~85 if > 4 MB or downscaled, never upscale).
- [ ] Failing tests: `TestSourcesStableOrderAndLabels`; `TestLabelsNeverContainFileNames`; `TestSystemPromptHasRequiredClauses` (contains each AI-spec 5.5 topic keyword: grounding, untrusted content, self-contained, plain text, language, marks mapping, unreadableSources, honest counts; no timestamps); `TestSystemPromptIdenticalAcrossCalls`; `TestInstructionCarriesTypeMarksCountAvoid`; `TestSchemasMatchSpec` (required fields, `additionalProperties:false`, mcq `options` length 4); `TestDownscaleOversizedLandscape` (3000x1000 gives longest side 2000, aspect kept); `TestDownscaleNeverUpscales`; `TestExifRotationApplied` (fixture with orientation 6 comes out with swapped dimensions); `TestSmallJPEGUntouched`; `TestPNGOver4MBReencodedJPEG`; `TestTXTInvalidUTF8Unreadable` (goes to failed, reason set); `TestTXTControlCharsStripped`; `TestNoReadableSourcesFailsWith` ("We couldn't read any of the selected files.").
- [ ] Run; expect FAIL. Implement. Image libraries (`golang.org/x/image`, an EXIF reader) are the justified dependency exception: state the one-line reason in the commit message.
- [ ] Run; expect PASS. Commit: "Prompt package and source preparation (adds image libs: downscale and EXIF)".

### Task B-2.6: Output validation, dedupe, shuffle, ordering
**Spec:** AI-spec 6.2, 7.1-7.3
**Needs:** B-2.5
**Files:** Create `internal/gen/validate.go`; Test `internal/gen/validate_test.go`
**Interfaces:**
Produces `gen.Validate(raw []RawQuestion, req CallSpec, labels map[string]store.Document, avoid []string) (kept []store.Question, dropped int)` where `CallSpec{Type string; Marks int}`; `gen.ShuffleOptions(q *store.Question, r io.Reader)` (crypto/rand reader injectable); `gen.OrderQuestions(qs []store.Question, shuffle bool, r io.Reader) []store.Question`; `gen.NearDuplicate(a, b string) bool` (normalised equal, or word-set similarity >= 0.8).
- [ ] Failing tests (table-driven, one row per AI-spec 7.1 check): marks mismatch dropped; text empty/601 chars dropped, 600 kept; HTML tag and Markdown fence dropped; control chars stripped; unknown `source` label dropped, known label maps to right `SourceDocID` and `SourceDocName`; MCQ with 3 options, 5 options, empty option, option 201 chars, case-insensitive duplicate options, `correctIndex` 4 or -1 each dropped; free text empty `modelAnswer` and 1,201 chars dropped; exact and 0.8-similar duplicates dropped, including against `avoid`; `TestShuffleKeepsCorrectAnswer` (run with 200 seeds: option text at `CorrectIndex` is unchanged); `TestShuffleUsesInjectedRandomSource`; `TestOrderAscendingByMarks` and `TestOrderRandomWhenShuffle`; `TestNewIDsAssignedByWorkerNotModel`.
- [ ] Run; expect FAIL. Implement. Run; expect PASS. Commit: "Validate and clean generated questions".

### Task B-2.7: Worker orchestration: calls, retries, spend, finish
**Spec:** backend 7.2 (4, 5), 7.5; AI-spec 5.3, 7.4, 8, 10.2, 10.4, 11
**Needs:** B-2.4, B-2.6
**Files:** Modify `internal/gen/worker.go`; Create `internal/gen/usage.go`; Test `internal/gen/worker_flow_test.go`
**Interfaces:**
Produces `gen.UsageRecorder interface{ RecordCall(ctx, jobID, model string, u Usage, costMicros int64, kept, dropped int) error }` with the M2 implementation `SpendRecorder` (adds `costMicros` to `quota#global#<yyyymm>` via `JobStore.AddSpend` and updates the job's `Usage` field); `GeneratorFactory func(model string) (Generator, error)`; `gen.PlanCalls(counts map[int]int, perCall int) []CallSpec` (one call per mark with non-zero count, count > 15 split into chunks of 15); the `TokenCounter` optional method `CountTokens(ctx, GenInput) (int, error)` on generators.
- [ ] Failing tests with a fake generator that records call order and concurrency: `TestPlanCallsChunksOf15` (30 x 1-mark gives two calls of 15; 5 marks all >15 gives 10 calls); `TestFirstCallRunsAloneThenAtMost3Concurrent` (max observed concurrency 3, first call observed alone); `TestCapsFailBeforeAnyCall` (bytes, pages, tokens 80,001 via fake `CountTokens`: no generator call, message "These files are too large to use together. Select fewer or smaller files."); `TestShortBatchOneFollowUpWithAvoid` (follow-up count equals missing, `Avoid` contains kept and dropped texts); `TestSecondShortfallFails` ("We couldn't generate enough questions from these files."); `TestMaxTokensRetriesOnceWithHalfCount`, repeated fails; `TestRefusalFailsBatchGenericMessage` ("We couldn't generate questions from these files."); `TestProvider429AfterRetriesMessage` ("The AI service is busy. Try again in a minute."); `TestCallTimeoutMessage`; `TestBadKey401GenericAndLoggedNotInJob`; `TestInvalidJSONCountsAsMissing`; `TestLaterCallsReceiveEarlierQuestionsInAvoid`; `TestSpendAddedAfterEveryCallIncludingRetries` (counter equals sum of `CostMicros`); `TestFailedCallWithoutUsageChargesCountedInput`; `TestBudgetExhaustedBeforeCallFailsJobWithBudgetMessage` and questions not kept; `TestCancelBeforeFirstCallAndBetweenCalls` (status `cancelled`, no `done`); `TestFinishWritesQuestionsItemBeforeDone` (fake records order); `TestDoneOnlyIfRunningAndNotCancelRequested`; `TestUnreadableSourcesLandInFailedSources`; `TestJobRecordsPromptVersionModelEffortAndUsage`; `TestNoQuestionTextOrPromptInLogs` (capture slog buffer).
- [ ] Run; expect FAIL. Implement `Worker.Run` body after the claim: cancel check, load sources (owner-checked, `Docs.GetDocs(job.OwnerID, ids)`), prepare, caps, calls, validate, finish. Concurrency via `errgroup`-style semaphore of 3 (stdlib `sync` only).
- [ ] Run; expect PASS. Commit: "Worker orchestration with retries, spend and cancel".

### Task B-2.8: Anthropic generator
**Spec:** AI-spec 2, 3.3, 3.4, 5.4, 9.4
**Needs:** B-2.5
**Files:** Create `internal/gen/anthropic.go`; Test `internal/gen/anthropic_test.go` (against an `httptest.Server` the SDK is pointed at via its base-URL option; no network)
**Interfaces:** Produces `gen.NewAnthropic(apiKey string, cfg AnthropicConfig) Generator` (implements `Generator` and `CountTokens`); `AnthropicConfig{Effort string; Timeout time.Duration; BaseURL string}`.
- [ ] **Before coding**, check each AI-spec 3.4 item against the SDK source (structured output via `output_config.format`, image blocks, `count_tokens`, refusal fallbacks). If any is missing, stop and report; do not hand-write HTTP.
- [ ] Failing tests asserting on the recorded request body: `TestRequestOmitsSamplingThinkingPrefill` (no `temperature`, `top_p`, `top_k`, no `thinking`, last message role is `user`); `TestEffortSetFromConfigAndOmittedWhenEmpty`; `TestMaxTokens16000`; `TestSDKMaxRetries1AndTimeout120`; `TestMessageLayoutSystemThenSourcesThenBreakpointThenInstruction` (`cache_control` on the last source block only; `Source doc-N:` text parts precede each file); `TestPDFAndImageAndTextBlocks`; `TestStructuredOutputSchemaSent`; `TestParsesUsageIncludingCacheTokens`; `TestStopReasonRefusalMaps`; `TestMaxTokensStopReasonMaps`; `TestNoFileNamesInRequest` (marshal body, assert no document name).
- [ ] Run; expect FAIL. Implement. Run; expect PASS. Commit: "Anthropic generator".

### Task B-2.9: Gemini generator
**Spec:** AI-spec 3.5, 5.4, 9.2, 10.4
**Needs:** B-2.5
**Files:** Create `internal/gen/gemini.go`; Test `internal/gen/gemini_test.go` (SDK pointed at an `httptest.Server`)
**Interfaces:** Produces `gen.NewGemini(apiKey string, cfg GeminiConfig) (Generator, error)`; `GeminiConfig{Effort string; Timeout time.Duration; BaseURL string}`; `gen.NewFactory(cfg config.Config) GeneratorFactory` choosing the provider from the model's catalog entry and failing with a generic error when the provider key is missing.
- [ ] **Before coding**, confirm in the `genai` SDK docs, for real: JSON-schema response format with JSON MIME type, inline PDF and image parts, the thinking setting and how `LLM_EFFORT` maps, usage fields (input, output including thinking, cached), safety/blocked finish reasons, token counting. Field names are not guessed; if one is missing, stop and report.
- [ ] Failing tests: `TestSameLabelledLayoutAsClaude`; `TestResponseSchemaAndJSONMime`; `TestThinkingMappedFromEffort` (low/medium/high, empty = unset); `TestNoSamplingParams`; `TestNoExplicitCacheCreated` (no cached-content request issued); `TestUsageIncludesThinkingInOutput`; `TestCachedTokensBilledAtCacheReadPrice` (via `CostMicros`); `TestSafetyBlockMapsToRefusal`; `TestEmptyCandidateMapsToRefusal`; `TestFactoryPicksProviderFromModel`; `TestFactoryErrorsWhenKeyMissing` (job later fails with a generic message and the factory error is logged, never put in `Job.Error`).
- [ ] Run; expect FAIL. Implement. Run; expect PASS. Commit: "Gemini generator and provider factory".

### Task B-2.10: Regenerate
**Spec:** backend 7.6, 8.4; AI-spec 3.2
**Needs:** B-2.7
**Files:** Modify `internal/gen/service.go`, `internal/api/gen_handlers.go`; Test `internal/gen/regenerate_test.go`
**Interfaces:** Produces `gen.Service.Regenerate(ctx, user, RegenerateRequest{Marks int; SourceIDs []string; Type string; Avoid []string; Model string}) (jobID string, err error)`; route `POST /generate/regenerate`.
- [ ] Failing tests: `TestRegenerateAvoidLimits` (61 strings and a 601-character string are 422; 60 x 600 accepted); `TestRegenerateCountsAgainstRegenNotGenerate` (counters: regen +1, generate unchanged); `TestRegenerateJobKindAndTotal1`; `TestRegenerateProducesOneQuestionAndAvoidIsPassed` (worker flow with fake generator); `TestRegenerateModelOptionalAndValidated`; `TestRegenerateSameSourceCaps`.
- [ ] Run; expect FAIL. Implement. Run; expect PASS. Commit: "Regenerate endpoint".

### Task B-2.11: ai-eval golden-set command
**Spec:** AI-spec 13.2, 13.3, 13.4
**Needs:** B-2.8, B-2.9
**Files:** Create `cmd/ai-eval/main.go`, `testdata/ai/README.md` (lists the 8 file kinds the owner supplies); Test `cmd/ai-eval/main_test.go`
**Interfaces:** Produces `make ai-eval MODEL=<catalog id>`; flags `-model`, `-dir testdata/ai`, `-types mcq,free`; report columns: file, type, requested, kept after retry, first-pass kept %, dropped, calls, tokens (in/out/cacheRead), cost USD, seconds, refusals, `PromptVersion`.
- [ ] Failing tests with the fake generator: `TestReportContainsPerFileRows`; `TestRefusesToRunWithoutExplicitModelFlag`; `TestRunsEveryFileForBothTypes`; `TestReportIncludesPromptVersion`; `TestNeverRunsUnderGoTest` (the command exits with a message unless env `AI_EVAL=1`).
- [ ] Run; expect FAIL. Implement. Run; expect PASS. Commit: "ai-eval golden-set command".

### Milestone check M2
`go test ./...` passes. With `LLM_PROVIDERS=gemini` and a paid-tier key in `.env`, `make run`, sign in, upload a small PDF, `POST /generate` with `{"sourceIds":[...],"type":"mcq","counts":{"1":4,"2":3},"shuffle":false}` returns 202, polling `GET /generate/{jobId}` reaches `done` with 7 questions within a few minutes, and `GET /ai/models` marks Gemini Flash-Lite `default:true`. A fourth generate in one UTC day returns 429. Deployed check: D-2.

---

## M3 Exams

### Task B-3.1: Exam and attempt stores (memory and DynamoDB)
**Spec:** backend 6.1, 6.2, 6.3, 6.4
**Needs:** B-1.2
**Files:** Create `internal/store/memory/{exams,attempts}.go`, `internal/store/dynamo/{exams,attempts}.go`; Test `internal/store/memory/exams_test.go`, `internal/store/dynamo/exams_test.go`
**Interfaces:**
Produces
```go
type ExamStore interface {
    PutExam(ctx, e Exam) error                             // attribute_not_exists(id); ErrConflict
    GetExam(ctx, id string) (Exam, error)                  // consistent
    GetExams(ctx, ids []string) (map[string]Exam, error)   // BatchGet, 100 per call
    ListByCreator(ctx, creatorID string, limit int) ([]ExamSummary, error) // creator-index, newest first
}
type AttemptStore interface {
    PutAttemptIfAbsent(ctx, a Attempt) (created bool, err error)  // attribute_not_exists(userId)
    GetAttempt(ctx, examID, userID string) (Attempt, error)
    ListByUser(ctx, userID string) ([]AttemptSummary, error)      // user-index
    ListByExam(ctx, examID string) ([]Attempt, error)
    Save(ctx, examID, userID string, answers map[string]string, lastIdx int, now time.Time) error // status<>submitted
    Submit(ctx, a Attempt) error                                   // conditional status<>submitted
    Decline(ctx, examID, userID string) error                      // only from not_started (or already declined)
    Reveal(ctx, examID, userID, questionID string, max int) (RevealResult, error) // conditional, see B-5b.1
}
```
- [ ] Failing tests: `TestPutExamConflictOnExistingID`; `TestCreatorListNewestFirstCap50`; `TestUserIndexProjectionExcludesAnswers` (dynamo recorder asserts INCLUDE attribute list in the table definition helper used by tests, and that `Save` does not touch indexed attributes); `TestPutAttemptIfAbsentIgnoresDuplicate`; `TestSaveConditionalOnNotSubmitted` (`ErrPrecondition` after Submit); `TestSaveStartsAttemptSetsStartedAtOnce`; `TestDeclineOnlyFromNotStarted`; `TestNoBatchWriteItemUsed`.
- [ ] Run; expect FAIL. Implement. Run; expect PASS. Commit: "Exam and attempt stores".

### Task B-3.2: Create exam
**Spec:** backend 8.5 (Create), 9.1; Review Focus 5; D11
**Needs:** B-3.1, B-1.3
**Files:** Create `internal/exams/{service,create}.go`, `internal/api/exam_handlers.go`; Test `internal/exams/create_test.go`
**Interfaces:** Produces `exams.Service.Create(ctx, user, CreateInput, idemKey string) (CreateResult{ID string; UnknownEmails []string; Replay bool}, error)`; route `POST /exams` (`201`, replay `200`).
- [ ] Failing tests: `TestTotalMarksComputedFromQuestions`; `TestDeadlineExactlyOneOfClosesAtAndClosesInSeconds`; `TestDeadlineBounds` (59 s and 30 d + 1 s are 422; 60 s and 30 d accepted); `TestServerClockUsedForClosesInSeconds`; `TestTitleAndQuestionCountBounds` (121 chars, 0 and 61 questions); `TestMCQShape` (3 options, empty option, `correctIndex` 4, **duplicate option texts after trim/lowercase: 422**, free-text with options); `TestTypeMustMatchEveryQuestion`; `TestServerMintsQuestionIDsIgnoringClientIDs`; `TestSourceFieldsTruncatedOrRejectedOver120AndNeverAuthorise`; `TestMaxPeeksDefaults` (3 with no assignees, 0 with assignees) and bounds (11 and > question count are 422); `TestPracticeExamCreatesCreatorAttemptRow`; `TestAssigneeRowsOnlyNotCreator`; `TestCreatorCanAddSelfByEmail`; `TestTeacherCap50And51Is422`; `TestStudentCap10`; `TestStudentAssigningToTeacherReportedAsUnknownIdenticalToUnregistered` (response bytes equal); `TestUnknownEmailsReturnedNotError`; `TestIdempotencyKeyBecomesExamID`; `TestIdempotencyKeyFormat` (7 and 65 chars, `a b` rejected); `TestReplayBySameCreatorCompletesMissingRowsAnd200`; `TestReplayWithDifferentBodyReturnsExistingAndDoesNotDuplicate`; `TestOtherCreatorsKey409`; `TestAttemptRowsWrittenAfterExamItem` (order recorded) and concurrency <= 10; `TestAnyRoleMayCreate`; `TestDeletedDocumentDoesNotAffectExam` (completes B-1.7 cross-reference).
- [ ] Run; expect FAIL. Implement.
- [ ] Run; expect PASS. Commit: "Create exams".

### Task B-3.3: List exams (`open`, `closed`, `created`)
**Spec:** backend 8.5 (List), 6.1 (missed derivation), 9.2; Review Focus 1
**Needs:** B-3.2
**Files:** Create `internal/exams/list.go`; Test `internal/exams/list_test.go`
**Interfaces:** Produces `exams.Service.List(ctx, user, scope string) ([]ListItem, error)`, `exams.Classify(exam Exam, a Attempt, now time.Time) string` (`open|closed`), `exams.DerivedStatus(exam, a, now) string`; route `GET /exams?scope=`.
- [ ] Failing tests: `TestOpenIncludesNotStartedAndInProgressBeforeAcceptingUntil`; `TestGraceWindowStillOpen` (`now = closesAt+30s` is open); `TestBoundaryAtAcceptingUntilIsClosed` (`now == closesAt+60s` is closed, `closesAt+60s-1ns` is open); `TestClosedHasSubmittedAndMissed`; `TestSubmittedBeforeCloseIsClosedImmediately`; `TestDeclinedInNeitherList`; `TestCreatedCap50NewestFirstWithCountsAndNoAttemptFields`; `TestCountsIncludeDeclined`; `TestCountsQueriesRun10AtATime` (recorded concurrency <= 10); `TestScoreOnlyForSubmittedMCQ` and `maxScore` integer; `TestListUsesGSIThenBatchGet` (no Scan); `TestBadScope422`; `TestCreatedByShownForAssignee`.
- [ ] Run; expect FAIL. Implement (finalize hook is a no-op `Finalizer` interface here, wired in B-4.4). Run; expect PASS. Commit: "Exam lists".

### Task B-3.4: Decline and assign more
**Spec:** backend 8.5 (Decline, Assign more), D8
**Needs:** B-3.3
**Files:** Create `internal/exams/{decline,assign}.go`; Test `internal/exams/assign_test.go`
**Interfaces:** Produces `exams.Service.Decline(ctx, user, examID) error`, `Assign(ctx, user, examID string, emails []string) (AssignResult{Assigned, AlreadyAssigned, Unknown []string}, error)`; routes `POST /exams/{id}/decline` (204), `POST /exams/{id}/assign` (200).
- [ ] Failing tests: `TestDeclineFromNotStarted204Idempotent`; `TestDeclineInProgressOrSubmitted409`; `TestDeclineByNonAssignee404`; `TestDeclinedUserGets404OnGetExam` (asserted again in B-4.2); `TestAssignCreatorOnly` (other user 404, per Global Constraints foreign = 404); `TestAssignAfterAcceptingUntil409ExamClosed`; `TestAssignRoleRulesAndCapCountAllAttemptRows` (student creator with 9 rows plus 2 new is 422; teacher at 50); `TestAssignAlreadyAssignedListed`; `TestAssignUnknownIncludesNotAllowedForStudentCreator`; `TestAssignAfterDeclineStaysDeclined`.
- [ ] Run; expect FAIL. Implement. Run; expect PASS. Commit: "Decline and assign".

### Task B-3.5: Creator status
**Spec:** backend 8.5 (Status), 9.2
**Needs:** B-3.3
**Files:** Create `internal/exams/status.go`; Test `internal/exams/status_test.go`
**Interfaces:** Produces `exams.Service.Status(ctx, user, examID) (StatusView, error)`; route `GET /exams/{id}/status`.
- [ ] Failing tests: `TestStatusCreatorOnlyElse403` (existing exam, non-creator is 403 per spec; unknown exam 404); `TestStatusCountsAndRows` (names from `GetManyByID`); `TestStatusRowsNeverContainAnswers`; `TestStatusNeverStartedAfterOverStaysNotStarted`; `TestStatusDeclinedRow`; `TestStatusScoreForSubmittedMCQ`.
- [ ] Run; expect FAIL. Implement. Run; expect PASS. Commit: "Creator status view".

### Milestone check M3
`go test ./...` passes. Two users via `make run`: A creates an exam assigning B (`POST /exams`), B's `GET /exams?scope=open` lists it (allow a moment for the index), A's `scope=created` shows counts `notStarted:1`, B declines (204) and the exam disappears from B's lists. Deployed check: D-3 (indexes exist).

---

## M4 Taking and printing

### Task B-4.1: Key visibility rule
**Spec:** backend 9.4, 8.5 (Take); Review Focus 1
**Needs:** B-3.1
**Files:** Create `internal/exams/keyvisible.go`; Test `internal/exams/keyvisible_test.go`
**Interfaces:** Produces `exams.KeyVisible(exam store.Exam, a *store.Attempt, userID string, now time.Time) bool` and `exams.IsOver(exam, now) bool` (`now >= closesAt+60s`).
- [ ] Failing tests (table): over for anyone with access true; at `acceptingUntil-1ns` false for an assignee; creator with no attempt row true; creator whose own attempt `submitted` true; creator `in_progress` false; creator `not_started` false; creator who is also assignee (assigned by email) same as own attempt; boundary `now == acceptingUntil` is true.
- [ ] Run; expect FAIL. Implement. Run; expect PASS. Commit: "Answer key visibility".

### Task B-4.2: `GET /exams/{id}`
**Spec:** backend 8.5 (Take / view), 9.4, 9.5 (printing), 6.1
**Needs:** B-4.1, B-3.4
**Files:** Create `internal/exams/take.go`; Test `internal/exams/take_test.go`
**Interfaces:** Produces `exams.Service.Get(ctx, user, examID) (GetView, error)` where `GetView{Exam ExamView; Attempt *AttemptView; KeyVisible bool}`; question DTO omits key fields unless `KeyVisible` or listed in `Revealed`; route `GET /exams/{id}`.
- [ ] Failing tests: `TestGetVisibleToCreatorAndNonDeclinedAssignee`; `TestStrangerAndDeclinedUserGet404`; `TestAttemptNilForCreatorWithoutRow`; `TestKeysAbsentWhileHidden` (marshalled JSON contains neither `correctIndex` nor `modelAnswer` anywhere, for MCQ and free); `TestKeysPresentWhenKeyVisible`; `TestRevealedQuestionKeysIncludedWhileHidden` (seeded attempt); `TestResumeReturnsAnswersLastIndexSavedAt`; `TestGetIncludesAcceptingUntilMaxPeeksCreatedBy`; `TestGetDoesNotStartAttemptOrChargePeek` (printing, backend 9.5); `TestClosedExamStillReturnsQuestionsToCreatorAndAssignees`; `TestQuestionsInStoredOrder`.
- [ ] Run; expect FAIL. Implement. Run; expect PASS. Commit: "Take and view exam".

### Task B-4.3: Autosave
**Spec:** backend 8.5 (Autosave), 9.2; Review Focus 1
**Needs:** B-4.2
**Files:** Create `internal/exams/autosave.go`; Test `internal/exams/autosave_test.go`
**Interfaces:** Produces `exams.Service.Autosave(ctx, user, examID string, in AutosaveInput) (savedAt time.Time, err error)`; route `PUT /exams/{id}/attempt` with 400 KB body cap.
- [ ] Failing tests: `TestAutosaveFirstSaveStartsAttempt`; `TestAutosaveReplacesWholeMap` (idempotent: same body twice, same state); `TestAutosaveRejectsUnknownQuestionIDs` (422); `TestAutosaveValueOver5000Chars422` (5000 accepted); `TestAutosaveBodyOver200KB` (413 -> 422 per body cap rule); `TestAutosaveInsideGraceWindowAccepted`; `TestAutosaveAtAcceptingUntilIs409ExamClosed` and runs lazy finalize (wired B-4.4); `TestAutosaveJustBeforeAcceptingUntilAccepted`; `TestAutosaveAfterSubmit409Conflict`; `TestAutosaveOnDeclinedOrNoAttempt404`; `TestAutosaveCreatorWithoutAttempt404`; `TestKnownGapDocumented` (an older PUT after a newer one overwrites; asserts current behaviour so a change is deliberate).
- [ ] Run; expect FAIL. Implement. Run; expect PASS. Commit: "Autosave".

### Task B-4.4: Grading, submit, lazy finalize
**Spec:** backend 8.5 (Submit), 9.2, 9.3; Review Focus 1, 5
**Needs:** B-4.3
**Files:** Create `internal/exams/{grade,finalize,submit}.go`; Modify `internal/exams/{list,status,autosave}.go` to call the `Finalizer`; Test `internal/exams/{grade,submit}_test.go`
**Interfaces:**
Produces `exams.Grade(exam store.Exam, answers map[string]string, revealed []string) (scoreHalves, beforeHalves *int)` (nil for free text); `exams.Finalizer.FinalizeIfExpired(ctx, a store.Attempt, e store.Exam, now time.Time) (store.Attempt, error)`; `exams.Service.Submit(ctx, user, examID string, in *SubmitInput) (SubmitResult, error)`; route `POST /exams/{id}/submit`.
- [ ] Failing tests: grading table `TestGradeMCQHalfMarks` (all correct = 2x total halves; unanswered 0; wrong 0; answer `"A "` with trailing space or different case is wrong — exact match only; answer text not among options is 0; correct and revealed = half of marks, `before` = full); `TestGradeFreeTextNil`; `TestSubmitStoresBodyAnswersThenGrades` (auto-submit race: answers only in body are kept); `TestSubmitSubmittedAtIsMinOfNowAndClosesAt` (manual submit at closesAt+30 s records `closesAt`; before closesAt records now); `TestSubmitInGraceWindowAccepted`; `TestSubmitAtAcceptingUntilExamClosed409`; `TestSubmitIdempotentReturnsExisting`; `TestSubmitNotStartedWithBodyStartsAndSubmits`; `TestSubmitWithoutAnswersUsesSaved`; `TestFinalizeAtAcceptingUntilGradesSavedAnswers` (`SubmittedAt = closesAt`, `AutoSubmitted = true`); `TestFinalizeNotBeforeAcceptingUntil` (a read at closesAt+59 s does not finalize); `TestFinalizeLeavesNotStartedAndDeclined`; `TestFinalizeConcurrentCallsSafe` (10 goroutines, one write wins, same result); `TestListAndStatusFinalizeExpired` (status endpoint uses concurrency <= 10); `TestSubmitResponseFreeTextNullScores`.
- [ ] Run; expect FAIL. Implement. Run; expect PASS. Commit: "Grading, submit and lazy finalize".

### Task B-4.5: Result
**Spec:** backend 8.5 (Result), 9.3, 9.4
**Needs:** B-4.4
**Files:** Create `internal/exams/result.go`; Test `internal/exams/result_test.go`
**Interfaces:** Produces `exams.Service.Result(ctx, user, examID) (ResultView, error)`; route `GET /exams/{id}/result`.
- [ ] Failing tests: `TestResultBeforeSubmitAndNotOver409` ("Submit the exam to see results."); `TestResultAfterOverFinalizesFirst`; `TestResultHidesCorrectnessWhenKeyHidden` (submitted, exam still open: JSON has no `correct`, `correctAnswer`, `modelAnswer`; `keyAvailableAt == acceptingUntil`; scores still returned); `TestResultShowsKeyWhenOver`; `TestResultMissedStatusNoScoreEmptyAnswers`; `TestResultDeclined404`; `TestResultScoreHalfMarksRendered` (69 halves = 34.5; `scoreBeforePeeks`; `maxScore` integer); `TestResultRevealedQuestionsKeepKeyWhenHidden` (seeded revealed); `TestResultFreeTextScoresNull`; `TestResultCreatorCanReadOwnWhenKeyVisible`.
- [ ] Run; expect FAIL. Implement. Run; expect PASS. Commit: "Exam result".

### Task B-4.6: Smoke script
**Spec:** backend 13 (Smoke test)
**Needs:** B-4.5
**Files:** Modify `backend/scripts/smoke.sh`, `backend/Makefile`
**Interfaces:** Produces `make smoke BASE_URL=...` running, in order: signup, `/me`, upload-url, `PUT` of a small generated PDF to the presigned URL, `complete`, list documents, `POST /generate`, poll until `done` (up to 8 minutes), `POST /exams` (practice), `GET /exams?scope=open` (retry up to 10 s for the index), autosave, submit, `GET /exams/{id}/result`; exit non-zero on the first failure and print the failing step.
- [ ] Verification: against `make run` with `LLM_PROVIDERS=gemini` (or against the fake generator server flag `-fake-llm`, added to `cmd/server` for offline smoke), the script prints each step and exits 0; with the server stopped exits non-zero naming the first step; breaking one step (invalid cookie) fails at that step.
- [ ] Commit: "Smoke test".

### Milestone check M4
`go test ./...` passes and `make smoke BASE_URL=http://localhost:8080` exits 0. Printing support: `GET /exams/{id}` on a closed exam returns questions to the creator with `keyVisible: true`. Deployed check: D-4 smoke against the dev stack.

---

## M5 Later priority

### Task B-5a.1: Per-model usage counters and day-index  **[P1, cuttable after peeks]**
**Spec:** backend 6.2, 8.6; AI-spec 11; D5 usage storage
**Needs:** B-2.7
**Files:** Create `internal/store/{memory,dynamo}/usage.go`; Modify `internal/gen/usage.go`, `internal/store/dynamo/jobs.go`; Test `internal/gen/usage_test.go`, `internal/store/dynamo/usage_test.go`
**Interfaces:**
Produces `store.UsageStore{ AddUsage(ctx, month, day, model string, d UsageDelta) error; GetUsage(ctx, month, day string, models []string) (UsageSnapshot, error); RecentToday(ctx, day string, limit int) ([]JobSummary, error) }` with `UsageDelta{Jobs, Calls, InTok, OutTok, CacheReadTok, CacheWriteTok, CostMicros int64}`; items `usage#<yyyymm>#<modelId>` (40 d TTL) and `usage#<yyyymmdd>#<modelId>` (3 d TTL); jobs get `day` and `model` attributes; `gen.CountersRecorder` replaces `SpendRecorder` and also calls `AddUsage` (jobs +1 on the first call of a job only).
- [ ] Failing tests: `TestEveryCallAddsTokensAndCostToMonthAndDayCounters`; `TestJobsCounterIncrementsOncePerJob` (a four-call job adds 1); `TestCountersUseADDNotPut`; `TestTTLs`; `TestJobItemCarriesDayAndModelAtCreation`; `TestRecentTodayQueriesDayIndexNewestFirstLimit50` (recorder: Query on `day-index`, `ScanIndexForward=false`, `Limit=50`, never Scan); `TestDayIndexProjectionIsInclude`.
- [ ] Run; expect FAIL. Implement. Run; expect PASS. Commit: "Usage counters and day index".

### Task B-5a.2: `GET /ai/usage`  **[P1, cuttable after peeks]**
**Spec:** backend 8.6, 10; AI-spec 11
**Needs:** B-5a.1, B-2.1
**Files:** Create `internal/exams/usage.go` (or `internal/gen/usageview.go`), `internal/api/ai_handlers.go` (extend); Test `internal/gen/usageview_test.go`
**Interfaces:** Produces `gen.UsageService.View(ctx, now time.Time) (UsageView, error)`; route `GET /ai/usage`.
- [ ] Failing tests: `TestUsageShapeMatchesSpec` (marshalled keys equal the 8.6 example); `TestMonthSpendFromCountersAndBudgetFromConfig` (`budgetUsd` 15, `spendUsd` = micros/1e6); `TestTodaySumsPerModelDayCounters`; `TestByModelListsEveryCatalogModel`; `TestRunningExcludesStaleAndFinished`; `TestRecentNewestFirstMax50`; `TestAggregateOnlyNoUserOrDocOrQuestionData` (marshal and assert absence of owner IDs, document names, question text from seeded data); `TestRequiresLoginAnyRole`; `TestNoStoreHeader`; `TestEmptyUsageAllZeros`; `TestDoesNotScan`.
- [ ] Run; expect FAIL. Implement. Run; expect PASS. Commit: "AI usage endpoint".

### Task B-5b.1: Peek (reveal) and peek scoring  **[P1, cut first]**
**Spec:** backend 8.5 (Reveal), 9.3, 9.6; D10
**Needs:** B-4.5
**Files:** Create `internal/exams/reveal.go`; Modify `internal/store/{memory,dynamo}/attempts.go` (`Reveal`), `internal/exams/{take,result,grade}.go` if the earlier tasks stubbed revealed handling; Test `internal/exams/reveal_test.go`
**Interfaces:** Produces `exams.Service.Reveal(ctx, user, examID, questionID string) (RevealView{QuestionID string; CorrectIndex *int; ModelAnswer string; PeeksLeft int}, error)`; route `POST /exams/{id}/reveal`.
- [ ] Failing tests: `TestRevealChargesOncePerQuestionIdempotent`; `TestRevealReturnsCorrectIndexForMCQAndModelAnswerForFree`; `TestRevealMaxPeeks0Is403` ("Peeking is turned off for this exam."); `TestRevealNoPeeksLeft409` ("No peeks left."); `TestRevealKeyAlreadyVisible409` ("The answer key is already visible."); `TestRevealAfterAcceptingUntil409ExamClosed`; `TestRevealStartsNotStartedAttempt`; `TestRevealWithoutAttemptOrDeclined404`; `TestRevealUnknownQuestion404or422` (422 `fields.questionId`); `TestRevealConditionalUpdateRaceNeverExceedsMax` (20 goroutines, max 3: exactly 3 charged); `TestRevealedAnswersReturnedOnResume` (via `GET /exams/{id}`); `TestRevealedCorrectScoresHalfAndBeforePeeksFull`; `TestRevealedWrongScoresZero`; `TestResultPeeksUsedAndRevealedFlag`.
- [ ] Run; expect FAIL. Implement (dynamo condition `size(revealed) < :max AND NOT contains(revealed, :q)`). Run; expect PASS. Commit: "Peek at an answer".

### Milestone check M5
`go test ./...` passes. M5a: `curl -b jar localhost:8080/ai/usage` after one generation shows `month.jobs 1`, `byModel` calls > 0, `recent` length 1. M5b: reveal one question in a practice exam (`maxPeeks` 3), then `GET /exams/{id}` shows it in `attempt.revealed` with `peeksLeft 2`; submit and `GET .../result` shows `peeksUsed 1` and `score < scoreBeforePeeks` when the revealed answer was correct.

---

## PROD Production deploy

### Task B-6.1: Production readiness checks in code
**Spec:** backend 10, 11, 12; AI-spec 12; deployment-spec gates
**Needs:** B-2.9, B-4.6
**Files:** Modify `internal/config/config.go`, `internal/api/middleware.go`; Test `internal/config/prod_test.go`, `internal/api/logging_test.go`
**Interfaces:** Produces startup validation for `ENV=production`.
- [ ] Failing tests: `TestProductionRequiresSecureCookieAndHTTPSOrigins` (`ALLOWED_ORIGINS` containing `http://` other than `http://localhost:3000` is an error); `TestProductionRejectsShortJWTSecret` (< 32 bytes); `TestWorkerStartupFailsMissingProviderKey` (worker with enabled provider and empty key exits with a clear log); `TestAPIModeNeverReadsLLMKeys` (API config load ignores `LLM_API_KEY`/`GEMINI_API_KEY` and does not require them); `TestLogsNeverContainSecretsCookiesOrBodies` (drive a login and an upload through the router with a captured logger).
- [ ] Run; expect FAIL. Implement. Run; expect PASS. Commit: "Production config checks".

### Milestone check PROD (backend part)
Against the prod stack (D-6): `curl $API_URL/health` is 200, `make smoke BASE_URL=$PROD_API_URL` is 0, CloudWatch shows JSON request lines with no cookies or bodies.

---

## FIX Test and fix

### Task B-7.1: Golden-set run on the cheap models
**Spec:** AI-spec 13.2, 13.3, 13.4, 17 (open: cheap-default quality)
**Needs:** B-2.11 and the owner's `testdata/ai/` files
**Files:** Create `backend/testdata/ai/report-<model>-<date>.md` (the printed report saved by the owner); Modify `internal/gen/prompt/prompt.go` only if a target fails
- [ ] Run `AI_EVAL=1 make ai-eval MODEL=gemini-3.1-flash-lite` then `MODEL=claude-haiku-4-5`. Check each AI-spec 13.3 target. Record the human check of about 30 sampled questions.
- [ ] If a target fails, change the prompt once, bump `PromptVersion`, re-run both models, compare to the previous report (AI-spec 13.4). Do not tune on one failing file.
- [ ] Commit: "Golden-set reports for cheap models".

### Task B-7.2: Cost and time check, bug fixing
**Spec:** AI-spec 10.3, 10.4; backend 7.5
**Needs:** B-7.1
**Files:** Modify as needed
- [ ] Compare recorded cost per job from the reports with AI-spec 10.3 and the provider usage page; adjust `LLM_MONTHLY_BUDGET_USD` or the catalog price rows if the estimate is off by more than 25%.
- [ ] For every bug found during manual testing add a failing test first, then fix. Run `go test ./...` and `make smoke` after each fix.
- [ ] Commit per fix.

### Milestone check FIX
Targets in AI-spec 13.3 met or accepted by the owner in writing; `go test ./...` green; `make smoke` green on the prod stack.

---

## Spec coverage

| Spec section | Tasks |
|---|---|
| backend 1, 3, 5, 5.1 (rules, stack, layout) | B-0.1, B-0.2 |
| backend 4, 6.2, 6.3, 6.4 (DynamoDB) | B-1.2, B-2.2, B-3.1, B-5a.1 |
| backend 6.1 entities | B-1.1, B-2.2, B-3.1 |
| backend 7.1-7.6 jobs and regenerate | B-2.2, B-2.3, B-2.4, B-2.7, B-2.10 |
| backend 8.0, 8.1 conventions, errors | B-0.1 |
| backend 8.2 auth, Google, CSRF | B-1.3, B-1.4 |
| backend 8.3 documents | B-1.5, B-1.6, B-1.7 |
| backend 8.4 generation API | B-2.3, B-2.10 |
| backend 8.5 exams | B-3.2-B-3.5, B-4.2-B-4.5, B-5b.1 |
| backend 8.6 models, usage | B-2.1, B-5a.1, B-5a.2 |
| backend 9.1-9.6 rules | B-3.2, B-3.4, B-4.1-B-4.5, B-5b.1 |
| backend 10 security | B-1.3, B-6.1 |
| backend 11 config | B-0.1, B-2.1, B-6.1 |
| backend 12 deployment needs | B-0.1 (entry points, health), B-1.5 (S3), D-track |
| backend 13 testing, smoke | every task, B-4.6 |
| AI-spec 3 catalog, models | B-2.1, B-2.8, B-2.9 |
| AI-spec 4, 5, 6 | B-2.5 |
| AI-spec 7, 8 | B-2.6, B-2.7 |
| AI-spec 9 (privacy, logging) | B-2.5, B-2.7, B-6.1 |
| AI-spec 10 cost | B-2.1, B-2.2, B-2.7, B-7.2 |
| AI-spec 11 records | B-2.7, B-5a.1 |
| AI-spec 12 config | B-0.1, B-2.1 |
| AI-spec 13 testing, golden set | B-2.11, B-7.1 |

## Open questions

1. **Exact model IDs and prices** (`gemini-3.1-flash-lite`, `claude-haiku-4-5`, `claude-sonnet-5-5`, `claude-opus-5-5`) are unverified (AI-spec 3.2). B-2.1 encodes them; confirm before M2's milestone check.
2. **Gemini and Anthropic SDK capabilities** (AI-spec 3.4, 3.5) are not confirmed. B-2.8 and B-2.9 stop and report if a field is missing, which would change the plan.
3. **`GEN_MAX_PDF_PAGES`**: backend 8.4 and AI-spec 12 say 40; `/tmp` decision D7 said 100. The plan follows the specs (40).
4. **Status of a non-creator on `GET /exams/{id}/status`**: spec says `403`, while the global rule says foreign exams are `404`. The plan follows the explicit `403` (B-3.5); decide whether to unify.
5. **Autosave body over 400 KB**: `MaxBytesReader` yields 413 by default but the error table has no 413. The plan returns `422 validation`; confirm.
6. **Offline smoke**: backend 13 describes smoke against a real LLM; B-4.6 adds a `-fake-llm` server flag so smoke can run with no spend. This flag is not in the spec.
7. **Cross-task stubs**: B-3.3 uses a no-op `Finalizer` until B-4.4, and B-4.2/B-4.4 handle `Revealed` as empty until B-5b.1; if M5b is cut, the revealed branches stay but are never reached.
8. **Image libraries** for downscale and EXIF (AI-spec 5.1) need a specific package choice and a one-line justification in the commit (goals rule 10).
