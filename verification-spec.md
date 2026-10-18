# Exam Assistant: Verification Spec

Audience: the coding agent implementing tests, the agent running verification, and the owner approving release.

**Purpose:** repeatedly upload study material, generate and publish exams, solve them independently, take them as a student, verify the answer keys and scores, and repair regressions until every required check passes. Prove application correctness, AI quality, and deployed behavior separately.

**Status:** implementation specification. At creation, this repository contains product specifications and a prototype, not an implemented application or test harness. Commands, fixtures, and reports below must be implemented before they can provide evidence. This file is not a record of tests having passed.

**Review baseline:** updated on October 16, 2026 against product-spec commits `17a523b` (review decisions and printing) and `780a366` (monthly budget, model choice, and usage), following the earlier `5c381cc` baseline. Re-read current owning specs on every run; this revision is not a claim that model IDs, provider capabilities, prices, or product behavior have been verified live.

## 1. Authority, scope, and non-negotiable rules

### 1.1 Related specifications

| File | Owns |
|---|---|
| `goals.md` | Binding architecture, P0 scope, three-day constraint, Deferred list |
| `frontend-spec.md` | UI behavior, API call inventory and rewrite, print layout, approved visual-reference precedence |
| `backend-spec.md` | Authoritative API contract (section 8), Go services, storage, authorization, attempts, grading, job lifecycle |
| `AI-spec.md` | Provider adapter, preprocessing, prompts, output validation, AI failures, limits, evaluation |
| `deployment-spec.md` | AWS/Vercel configuration, deployment, smoke testing, operations |
| `exam-assistant-requirements.md` | Supporting user stories; does not reinstate features deferred by `goals.md` |
| This file | Verification procedures, test IDs, evidence, loop behavior, acceptance decisions |

This file does not silently change product behavior. If product specifications conflict, record a decision under section 2 and update the owning specification before encoding an expectation. Change backend section 8 first, then update the frontend API inventory in the same change. `AI-spec.md` takes precedence over backend section 7 on LLM behavior, as those files already require.

### 1.2 Verification rules

1. **No false green.** Missing tests, missing fixtures, disabled services, unreviewed AI output, and unavailable accounts are not passes.
2. **Independent correctness.** Never use a generated answer key to prove that the same key is correct. Independently solve questions, then compare.
3. **No weakening to pass.** Do not delete assertions, lower thresholds, skip cases, expand timeouts without justification, or change expectations to match a defect.
4. **Separate modes.** Deterministic tests use fakes. Live acceptance uses real AWS, the actual browser, and the actual configured provider. Report the mode explicitly.
5. **Real user actions.** The principal live acceptance journey uploads, generates, publishes, answers, resumes, and submits through the UI. API helpers may prepare accounts and inspect evidence, but may not substitute for the UI behavior under test.
6. **Protect data.** Use synthetic or owner-approved non-sensitive material and dedicated test accounts. Never use real minors' work or accounts as routine fixtures.
7. **Bound spending and execution.** No live AI calls without an explicit approved budget. No infinite polling, paid retries, or destructive cleanup.
8. **Stay small.** No new runtime service, queue, database, evaluation platform, or custom CI infrastructure. Use repository-native Go tests, frontend tests, browser automation, scripts, and Make targets.
9. **Verify fixes.** A code change is not a fix until the failing case and affected regressions pass.
10. **Deployment is separate authority.** Permission to verify/fix does not itself authorize production deployment, publishing commits, changing cloud permissions, or modifying provider billing.

### 1.3 Scope

All P0 requirements are required, including student creation/assignment/decline, printing, per-job model choice, and shared-budget enforcement. An unimplemented P0 requirement is a failure, not automatically out of scope. Only an explicit owner-approved scope change in `goals.md` can defer it.

P1/Deferred features are not MVP gates unless explicitly brought into scope. Examples: parent linking, folders/tags, individual question regeneration/editing/deletion, manual free-text marking, exam editing/deletion, and HEIC support. Existing exposed deferred endpoints must still obey authentication, authorization, and input safety requirements.

Peeking/gamification and the AI usage dashboard are P1: peeks are cut first, then the dashboard. Core model selection, budget/usage counters, API score representation, `maxPeeks`/revealed data shapes, and fixed key visibility are not deferred with those screens. Include the full peek/usage feature cases only when enabled in the approved scope. If `/ai/usage` or `/reveal` is exposed, its access/privacy contract applies even when its UI is deferred.

Browser printing and Save as PDF are P0. Server-generated PDFs, true offline/PWA operation, paper-result entry, and undoing a decline remain deferred. Do not implement them to satisfy a print test.

Explicitly accepted gaps in current owning specs (such as sequence-free autosave and soft running-job admission) are baseline limitations, not new defects to silently fix or new exceptions to invent. Verify the specified behavior, record the risk in every release summary, and never claim protection that does not exist. Unexpected regressions beyond that baseline remain failures. Owner-approved baseline limitations do not turn correctly specified tests into `ACCEPTED_GAP`; that status is for unmet required expectations.

The verification harness must remain runnable locally. Test reports and fixtures may use local disk; this does not permit application runtime persistence on local disk.

### 1.4 Test migration from the previous verification revision

This revision intentionally replaces obsolete expectations; these are traceable product changes, not permission for the repair loop to weaken tests on its own.

| Previous expectation | Current replacement |
|---|---|
| Frontend owns API contract | Backend section 8 owns it; frontend inventory/rewrite follows |
| Students cannot create | All roles create; assignment target/caps depend on creator role |
| Creator always sees keys / three visibility modes | Fixed visibility including creator-taking exception and acceptingUntil |
| Finalize at closesAt with a separate submit grace | All acceptance/finalization/key timing uses acceptingUntil |
| 25 MB upload / 100 PDF pages / 200k tokens | 20 MB upload/aggregate; 100 PDF pages and 200k input prefix tokens per job (restored 2026-10-17), with optional per-PDF page ranges that count only the range |
| Sequential provider calls | Cache-warm first call then <=3 concurrent with shared dedup/accounting |
| Five-minute stale rule / old retry defaults | 20s heartbeat, >6min stale, 120s attempts +1 SDK retry, 8min job |
| Single Anthropic adapter and deployment-only model choice | Two official adapters and signed-in per-job catalog selection |
| Hard running-job limit / no monthly meter | Soft running admission, hard daily quotas, shared monthly estimate |
| Pre-link cookies remain valid | Older cookies revoked using SessionsValidAfter |
| Delayed older autosave must never win | Explicit accepted limitation, while submitted-state/final-submit safety stays required |
| Printing absent | P0 print route, paper layout, A4/Letter, native dialog and key-safety coverage |

When a harness already exists, migrate its case manifest with these source-linked replacements. Keep old results immutable, record superseded assertions, and rerun replacement cases; never relabel an old pass as fresh evidence for changed behavior.

## 2. Specification decisions and readiness preflight

Run this before implementing ambiguous tests and at the start of every release verification.

### 2.1 Decision register

Seed `verification/decisions.md` with these items. Retain stable IDs and distinguish decisions already made in the product specs from genuinely open policy. Record status, chosen rule, source, affected sections, and any further approval needed. Do not ask the owner to re-decide requirements already resolved in the current files.

| ID | Current status / rule | Required evidence or remaining action |
|---|---|---|
| DEC-01 | **Resolved in backend 9.2:** `acceptingUntil = closesAt + 60s`; reads never finalize before it; saves/submissions allowed while `now < acceptingUntil`; at/after cutoff finalize/reject new work. Auto-submit includes current answers directly. | Boundary/race tests and `SubmittedAt = min(now, closesAt)` for manual submission, `closesAt` for auto-finalization. |
| DEC-02 | **Resolved in backend 8.5/9.1:** all roles may create; students assign only to students, max 10; parent/teacher assign to any registered role, max 50. | Matching navigation, API assignment, indistinguishable unknown/non-student responses, and cumulative caps. |
| DEC-03 | **Clarified in backend 7.2 and AI 3.3/5.3:** at most 10 initial logical calls, one content retry per initial call (at most 20 logical calls), and one SDK retry per logical call. | Track initial/content/transport attempts separately (up to 40 transport attempts if all logical calls are used, excluding separate token counting); 8-minute deadline may stop sooner. Test that shortfall and truncation do not stack retry allowances. |
| DEC-04 | Truncation: after retrying with half the requested count, specify how remaining questions are recovered without violating exact counts or retry/call limits, including rounding/minimum count for a one-question call. | `max_tokens` scenarios either finish with the full requested distribution or fail cleanly; no zero-count provider call. |
| DEC-05 | **Resolved contract in backend 7.2:** heartbeat about every 20s; stale only after more than 6 minutes without update; 120s per provider attempt, at most one SDK retry; 8-minute job context within 10-minute Lambda; claimed/terminal transitions conditional. A stale failure must never later become done. | Verify heartbeat during calls, deadline propagation including backoff, first-call barrier then concurrency <=3, persisted terminal-state protection, and cancellation races. |
| DEC-06 | Finalize quantitative AI gates in section 10.4, including difficulty/coverage scoring. AI section 13.3 calls its targets proposed. | Owner-approved gate configuration, fixed before the scored run. |
| DEC-07 | **Applied in current frontend/backend:** backend owns API contract; fixed visibility replaces `answerKeyVisibility`; AI notice/model choice, result/decline endpoints, and deferred `avoid` payload are documented. | Contract drift tests; only newly discovered disagreements remain open. |
| DEC-08 | **Open spending-risk approval:** the app checks/adds an estimate, not reserved spend. Multiple in-flight calls across jobs/users can overshoot $15; $20 per provider is not a combined $20 cap. | Quantify exposure with concurrency tests, disclose estimate-only/headroom/alert limitations, and get an owner-approved total spending-control plan. Do not silently add a global reservation service or assert a guaranteed $20 total. |
| DEC-09 | **Open model-setting normalization:** shared `LLM_EFFORT=medium` versus selectable Haiku's instruction to omit effort; exact Gemini effort/token-count capabilities need preflight. | Define per-model supported settings in the catalog/adapter and resolve Gemini 3.5's allowed estimate fallback versus AI 10.2's mandatory counting wording. No guessed SDK fields or silent capability downgrade. |
| DEC-10 | **Open idempotency payload policy:** backend defines replay by the same creator but not reuse of a key with a different request body. | Define reject-versus-original-payload behavior and test it; a replay must not change existing exam questions/deadline or unexpectedly assign a changed payload. |
| DEC-11 | **Open small-practice default:** omitted `maxPeeks` defaults to 3, but it must also be <= question count. | Decide behavior for one/two-question practice exams (clamp, validation, or explicit request value), align owning specs, and test without requiring P1 reveal UI. |
| DEC-12 | **Open versioning wording:** AI 13.4 requires a PromptVersion bump for model/effort changes, while model selection is now an ordinary per-job user action. | Distinguish release/configuration/prompt changes from a user choosing an already-certified catalog model. Preserve per-job model/effective-effort fingerprints; do not require a global prompt deployment after every user selection. Align owning wording before making version-change assertions. |
| DEC-13 | **Open creator/decline precedence:** creators may assign themselves, but getter rules both authorize creators and say a decliner gets 404; result requires a non-declined attempt. | Define whether a creator may decline their own attempt and what creator view/status/print versus attempt/result access remains. Test the explicit policy rather than assuming creator rights or decliner denial always wins. |

Work on unrelated, unambiguous cases while decisions are pending. A required unresolved decision blocks the affected cases and prevents an `ALL_PASS` release verdict. Never guess the product policy inside a test.

### 2.2 Provider capability preflight

Treat model IDs, prices, supported request settings, SDK fields, request/image/page limits, retention terms, and spending controls in the specs as claims to check, not established provider facts.

Before paid tests:

- Verify the selected model is actually available to the configured account.
- Pin each enabled provider's official Go SDK (`github.com/anthropics/anthropic-sdk-go`, `google.golang.org/genai`) and demonstrate compilation of the actual required request/response paths.
- Check request settings against official documentation for each selectable model/SDK. Do not silently substitute a model/provider, improvise fields, enable tools, or hand-write HTTP as a fallback. Catalog IDs/prices are intentionally in code; forbid duplicate scattered constants, not the catalog itself.
- Record official source attribution and access date, selected model, effort, API/SDK versions, current limits, and pricing used in cost estimates.
- Verify Anthropic structured output, attachments, token counting, explicit cache control, refusal parsing, and optional server-side fallback. Verify Gemini JSON schema/MIME, inline parts, thinking mapping, usage including thinking/cache, safety blocks/empty candidates, counting or approved fallback. Gemini must not use explicit context caching.
- Confirm privacy review and both enabled providers' terms before real-user data. Confirm the Gemini project is billing-enabled/paid-tier using project evidence, not a claim inferred from an API response. Synthetic-data verification does not complete a privacy review.
- Confirm what account-level spending controls actually exist. An alert or a delayed billing display is not a synchronous hard cap.

An invalid model name or unsupported capability is a configuration/specification blocker, not a reason to fabricate successful provider results.

### 2.3 Environment preflight

Record without secrets:

- Git commit, dirty-tree diff hash, hashes of owning specs, fixture set, and verification harness.
- Frontend/backend build identifiers; dev stack, region, origin, and deployment identity.
- Enabled provider/model catalog, default/override, per-model effective effort, prompt version, both SDK versions, price-table version, all effective caps, and worker timeout.
- Production/dev isolation, intended AWS identity, and exact allowed origins.
- Availability of app, fixtures, dedicated accounts, browser, and required permissions.
- Mock flags: expected for deterministic/local mock suites; **off** for live acceptance.
- Agreement of frontend and backend Google client IDs for the stage.
- All model-selection/budget settings agree between API and worker. Keys are present only on workers for enabled providers; API startup must not require provider secrets and worker startup must not require the API's session secret. Check actual config, not local intent.

Refuse mutation if the target environment is unknown or differs from the approved target.

### 2.4 Current contract baseline

These are current product defaults, not proposed overrides:

| Concern | Expected contract |
|---|---|
| Upload | 1–20,971,520 bytes (20 MB) per file, PDF/TXT/JPG/PNG |
| Generation sources | At most 10 files, 20,971,520 bytes total, 100 PDF pages (a PDF with a `pageRanges` entry counts its range length and is excluded from the API byte total); metadata validation before creating job/quota increments, including `pageRanges` (key in `sourceIds`, PDF only, `1 <= from <= to <= pages`); 200,000-token prefix cap per AI spec |
| Generation timing | Usually about 2 minutes (measured target); UI cancels at 6 minutes or 3 consecutive poll failures; stale after >6 minutes without heartbeat; 8-minute worker context; 10-minute Lambda |
| Scheduling | First provider call alone; remaining work at most 3 concurrent calls; one shared final count/dedup validation |
| Daily quotas | Generate 3/user, regenerate 10/user, all jobs 10 globally, keyed by UTC date; atomic transaction with job creation |
| Running admission | Soft default 2/user via eventually consistent GSI; not a hard concurrency guarantee |
| Monthly spend | Shared estimate, default $15 via `LLM_MONTHLY_BUDGET_USD`, UTC month; new-job transaction and pre-call checks; micro-dollar accounting |
| Model choice | All signed-in roles, per job; enabled catalog only; explicit enabled `LLM_MODEL` override or cheapest enabled; no admin-only gate |
| Exam cutoff/key | `acceptingUntil = closesAt + 60s`; fixed creator/over rule, including unfinished creator-attempt exception; no visibility setting |
| Score | Store integer half-marks; API number may end in `.5`; free-text scores null; unrevealed MCQ behavior unchanged |
| Assignment | Student-to-student max 10, parent/teacher-to-any max 50; cap counts all attempt rows; decline only before start |
| Storage | On-demand DynamoDB; small projected indexes; job metadata and `<jobId>#questions` separate; counter TTL differs from job TTL |

Test configured overrides as well as these defaults. A model/provider being enabled is not evidence of acceptable question quality.

## 3. Harness, commands, and execution profiles

### 3.1 Suggested layout

```text
verification-spec.md
verification/
  cases.yaml                    # required IDs, layer, command, source, scope
  decisions.md                  # approved policies and unresolved conflicts
  run-config.example.json       # non-secret settings and safety limits
  scripts/                      # orchestration, report validation, safe cleanup
  e2e/                          # browser cases, helpers, fixture uploads
  schemas/                      # result/run/assessment validation
backend/
  internal/.../*_test.go         # tests beside production packages
  testdata/ai/
    manifest.json               # fixture hashes and expected outcomes
    sources/                    # 8–10 non-sensitive source files
    references/                 # independently authored facts/solutions/evidence
    responses/                  # synthetic provider-response fixtures
.verification/runs/<run-id>/     # ignored/private execution artifacts
```

Directories are a harness contract, not claims that they already exist. Keep fixtures/references in Git only when safe and intentionally reviewed. Ignore runtime artifacts and credentials before running.

`cases.yaml` must map every in-scope P0 acceptance criterion to one or more test IDs. Each entry includes source section, layer, required profiles, setup, steps, assertion, timeout, and expected evidence. The IDs below are families; expand each table row and each boundary variant into individually reportable child IDs.

### 3.2 Commands to implement

Root-level Make targets orchestrate the frontend/backend targets. Document exact commands in the repository README. Missing tools or targets must exit nonzero; never provide placeholder success targets.

| Command | Contract |
|---|---|
| `make verify` | No cloud or paid network calls. Go build/tests, `go vet`, race-enabled tests where supported, frontend typecheck/lint/tests/production build, fixture and case-manifest validation, both SDK-adapter tests, quota/cost arithmetic, fixed-visibility and print-component tests. |
| `make verify-integration` | Explicit dev target only; real DynamoDB/S3 repository and concurrency tests. Provider transport may be stubbed and must be labelled. |
| `make verify-e2e` | Browser suite against controlled local/dev app with deterministic generation; real UI interactions, model choice, print-to-PDF checks, explicitly labelled provider mode. |
| `make verify-live` | Explicit opt-in; principal UI journey with actual provider, deployed browser-to-Vercel-to-AWS chain, independent solving, and score checks. |
| `make ai-eval` | Explicit opt-in; resumable golden-set campaign for an explicit model matrix through the application generation pipeline. Persist jobs, model outcomes, quota schedule and spend; never silently evaluate only one selectable model. |
| `make smoke BASE_URL=...` | Existing backend smoke contract; `BASE_URL` includes `/api` when using Vercel and omits it for direct backend access. |
| `make verify-release` | Aggregate all required layer results and review evidence for one candidate; does not deploy or silently initiate paid work. |
| `make verify-loop` | Resume/run the bounded repair procedure in section 13. A script may orchestrate; an agent performs diagnosis and edits. |

Required Go baseline: `go test ./...`, `go vet ./...`, and a separate supported-environment `go test -race ./...`. Report inability to run race checks rather than presenting an ordinary test run as race coverage.

Every command:

- Returns nonzero for a failure, missing prerequisite, incomplete review, or blocked required case.
- Produces machine-readable results and a concise readable summary.
- Uses timeouts and bounded waits. Never reports green merely because no tests were discovered.
- Records start/end time, expected/discovered/executed counts, and individual case IDs.
- Propagates child exit codes; preserve them through shell pipes.
- Has no automatic production deployment, paid evaluation, destructive teardown, or credential printing.

### 3.3 Profiles

| Profile | Required layers | Meaning |
|---|---|---|
| `fast` | Deterministic build, API/service/store-fake, SDK-adapter, component tests | Safe edit/fix loop; not a release claim. |
| `integration` | `fast` + dev DynamoDB/S3 + controlled browser suite | Storage and UI evidence; not live AI certification. |
| `release` | All of the above + live journey + golden eval + reviews + deployed/config checks | May produce `ALL_PASS` only under section 12. |

Subsets may be run to diagnose, but a passing subset never satisfies a broader profile. A release candidate must include all required case IDs.

### 3.4 Example run configuration

This is harness configuration, not a new application configuration system:

```json
{
  "profile": "release",
  "targetEnvironment": "dev",
  "frontendUrl": "<approved deployed origin>",
  "apiBaseUrl": "<approved deployed origin>/api",
  "allowLiveAi": false,
  "approvedAiBudgetUsd": 0,
  "maxLiveJobs": 0,
  "modelMatrix": [],
  "priceTableVersion": "<verified catalog price version>",
  "enablePeekFeatureCases": false,
  "enableUsageDashboardCases": false,
  "maxIterations": 10,
  "maxElapsedMinutes": 120,
  "maxIdenticalBlockerIterations": 3,
  "cleanupRunOwnedData": true
}
```

Defaults deny paid calls. The owner explicitly enables live AI and sets positive job/spending limits. Ten iterations/two hours are execution safety defaults, not product requirements and not permission to stop early and claim success. On exhaustion, save state and report incomplete; the owner can authorize another run.

`modelMatrix` must include every selectable enabled model for release quality certification, with its effective settings. A smaller diagnostic matrix is permitted but is not release evidence for omitted models. `allowLiveAi` is permission to spend within the harness budget, not permission to bypass the application's daily or monthly limits.

Store passwords, cookies, cloud credentials, and API keys outside the run configuration. The configuration must name secret sources without containing secret values.

## 4. Isolation, accounts, fixtures, and reset

### 4.1 Accounts

Use dedicated identities for:

- `teacher-a`: creator and source owner.
- `parent-a`: permitted creator role without relying on deferred parent linking.
- `student-a`: main independent-solve attempt.
- `student-b`: controlled-score and missed-attempt cases; additional dedicated students as needed.
- `student-creator`: separate student practice/assignment/decline tests, including creator-taking visibility.
- `outsider`: authenticated but unassigned and not the source owner.
- Separate Google test identities for new-user, existing-user, and password-to-Google-link cases.

Use separate browser contexts/profiles for creator, students, and outsider. Do not transfer cookies or teacher responses into student contexts. No real personal passwords or real student accounts.

Provision accounts idempotently outside the tested signup cases. Use unique disposable identities for signup/link tests. If account deletion is not a product feature, reuse a bounded pool or remove only run-owned dev records using approved test cleanup; never add an account-deletion feature just for testing.

### 4.2 Fixture inventory

Build the following 8–10 files, combining suitable categories where needed:

| ID | Source | Purpose |
|---|---|---|
| FX-01 | Several-page text PDF with independently checked maths/physics content | Definite calculations, multi-page viewing, all marks |
| FX-02 | Biology and chemistry text PDF | Legitimate coursework, refusal regression |
| FX-03 | Scanned version of known printed material | Vision reading against the same ground truth |
| FX-04 | Clear phone-style JPEG/PNG, including EXIF-rotated variant | Image reading and orientation |
| FX-05 | Deliberately unreadable dim/blurred page | Honest unreadability, no invented questions |
| FX-06 | Handwritten page with a curated readable subset | Handwriting and uncertainty |
| FX-07 | UTF-8 TXT with Unicode maths and a supported non-English or mixed-language section | Language, plain text, Unicode |
| FX-08 | Study material containing an injection instruction and a unique sentinel | Untrusted-content behavior |
| FX-09 | Tables/diagrams with known underlying facts | Self-contained questions without access to figures |
| FX-10 | Very sparse material | Insufficient-content handling |

Create additional synthetic size/type/corruption and HTML/Markdown response variants as byte fixtures, not additional paid golden-set sources. Size fixtures should cover 0, 1, limit−1, limit, limit+1 bytes as applicable. Large upload-only fixtures need not be used in paid evaluation.

Every source manifest entry contains hash, MIME, bytes/pages, language, expected behavior, supported topics/facts, source evidence locations, fixture version, and permission/provenance.

Predeclare expected outcome: `success`, `partial_source_success`, `unreadable_failure`, or `insufficient_content_failure`. A poor photo that remains readable may succeed correctly; do not require failure merely because it is a photo. Use a separately curated genuinely unreadable fixture for the mandatory failure case.

References must not be supplied to the application generator. No sensitive content. Synthetic provider fixtures must exercise malformed/invalid responses, not rely on the real model producing them.

### 4.3 Isolation and cleanup

- Assign a unique `runId` and use it in disposable titles and test-data manifests.
- Record IDs for created docs, jobs, exams, attempts, and test identities.
- Reset logical state before each case; use fresh exams/attempts for terminal transitions.
- Do not depend on asynchronous TTL deletion to make a rerun clean.
- Respect DynamoDB GSI eventual consistency with bounded condition waits; do not add arbitrary sleeps or unbounded retries.
- Never reset quota/spend/usage counters as ordinary cleanup. Synthetic limit tests use isolated dev fixture namespaces and fake clocks; live runs consume actual daily/monthly allowance. Preserve spend evidence even if the corresponding job expires or documents are removed.
- Delete run-owned documents through product APIs where possible.
- For exams/attempts without delete APIs, use approved dev-only cleanup or a bounded reusable fixture pool. Never truncate tables, empty shared buckets, or run `make destroy`.
- Validate account/stage/run ownership immediately before cleanup. Record cleanup failures; preserve failed-case evidence.
- Test production only with explicit authorization, dedicated identities, and non-destructive operations. Run rate-limit/concurrency/fault campaigns on dev, not production.

## 5. Deterministic application verification

Use fake `Clock`, fake generator, fake Google verifier, and in-memory repositories. No AWS access or provider calls. Tests must inject controlled failures and concurrency; do not sleep for real deadlines.

| ID | Required scenarios and assertions |
|---|---|
| APP-AUTH-01 | Signup/login/logout and `/me`; password 8–72 bytes including multibyte boundary cases; role/name boundaries; trimmed/lowercase email; duplicate email; generic login failure; Google-only password login fails generically; logout clears cookie. |
| APP-AUTH-02 | Google existing/new users; new user without valid role creates nothing; verified-email linking removes password and sets `SessionsValidAfter` atomically; a different Google subject for already-linked email gives 409 without mutation; invalid signature/issuer/audience/expiry or unverified/missing email rejected; client-supplied identity ignored. Test verifier invocation and adapter behavior, not just a fake configured to return an error. |
| APP-AUTH-03 | Invalid/expired/tampered sessions and unexpected JWT algorithm rejected; `iat` older than `SessionsValidAfter` rejected on every authenticated path; fresh link cookie works, revoked cookie cannot mutate. Include same-second timestamp boundaries and missing-`iat` handling; report any timestamp-precision loophole rather than silently accepting it. Production cookie attributes/local exception and Origin validation; accepted missing-Origin and Google nonce/replay limitations remain disclosed. |
| APP-ACCESS-01 | Owner/assignee/creator/outsider/anonymous matrix on documents, file URLs, jobs, cancel, exam read/list/result/status/assign/save/submit. Assert resource-specific 401/403/404/422 semantics; roles do not confer access to other users' documents. |
| APP-DOC-01 | MIME, filename, byte boundaries; `pending` absent from library; missing object, wrong size, wrong magic bytes; malformed PDF; complete idempotence; sanitized names; search/sort behavior; valid final library row. |
| APP-DOC-02 | Delete confirmation in UI, API ownership, repeat-delete contract, object/row failures and retry; generated exams retain source-name snapshots after source deletion. |
| APP-EXAM-01 | All roles may create; title/type/question/mark/deadline validation; server total and newly minted question IDs; source ID/name are bounded display-only strings, not ownership lookups; zero assignees creates creator attempt; nonempty list creates allowed assignee rows only. Fixed visibility: no `answerKeyVisibility` control/accepted create field. Core `maxPeeks` shape/default/bounds per DEC-11, even if reveal is deferred. |
| APP-ASSIGN-01 | Student assigns only registered students, max 10; parent/teacher any registered roles, max 50; non-student account and unknown email indistinguishable for student creator. Duplicate/self emails, adding more, cumulative cap including existing/declined rows, and unallowed/unknown response arrays. Role is loaded from session/user, not trusted request input. |
| APP-PUBLISH-01 | Idempotency-Key 8–64 allowed characters; create 201, same-owner replay 200 with same exam ID; partial attempt-write failure repaired by replay without duplicates; foreign creator collision 409; changed-body replay per DEC-10. Stable frontend key on retry, new key for a genuinely new publish. |
| APP-DECLINE-01 | Not-started assignee can decline; repeated decline 204; started/submitted cannot decline; no undo; disappear from assignee Open/Closed and non-creator exam/result/print access is 404; creator counts/status show declined; lazy finalize never touches declined. Test decline versus first-save/reveal races, and creator-as-assignee behavior under DEC-13. |
| APP-ATTEMPT-01 | First save starts; full-map replacement; last position restored; unknown question IDs, answer/body limits; repeat save; submitted immutability; save/submit ordering and concurrent saves. |
| APP-TIME-01 | Fake clock at `closesAt−1ms`, `closesAt`, `closesAt+1ms`, `acceptingUntil−1ms`, `acceptingUntil`, `acceptingUntil+1ms`. Grace is open; reads do not finalize; saves and submit accept final answers while cutoff not reached. At cutoff reject new work and lazily finalize in-progress, derive missed for never-started; list/get/status/result/write agree. Auto-submit races an older autosave: submitted result immutable; direct final-answer submit body is kept. Verify submission timestamps and repeat-read stability. |
| APP-GRADE-01 | Independent MCQ oracle: exact option-text comparison, all correct/wrong/mixed/blanks, option index zero, multi-mark sum; integer half-mark storage maps to numeric JSON, never string/rounding loss; `scoreBeforePeeks` equals unpenalized sum. Core no-peek scores unchanged; free-text both scores null; repeated/concurrent submission stable. |
| APP-KEY-01 | Use fixed matrix in section 5.1, not removed anytime/after-submit/after-close modes. Inspect raw exam/result and other accessible responses for unauthorized keys/correctness. Total score is allowed where specified. With P1 peeks, only actually revealed questions have the documented exception; another user's reveals confer no access. |
| APP-PRINT-01 | Print uses exam GET and `/me` only; eligible non-declined assignees retain access after exam over. GET/print does not start an attempt or consume peeks; existing lazy-finalize-at-cutoff is the sole specified read-side write. Creator-taking key suppression applies to print too. |
| APP-MODEL-01 | All signed-in roles can GET enabled catalog and choose per job; exactly one default, configured enabled override or cheapest; unknown/disabled choice 422 `fields.model` before quota/job writes; choice never changes global/other-session default. Provider factory and persisted job/poll model match request; API keys never returned. |
| APP-QUOTA-01 | Atomic generate 3/user/day, regenerate 10/user/day, all jobs 10/day; accepted failures still consume quota; rejected transaction changes no counters/job. Boundaries and UTC rollover independent of eventual TTL. Running limit 2 is explicitly soft via GSI; do not require strict admission under concurrency. |
| APP-BUDGET-01 | $15 shared monthly estimate; below/at/above threshold creation and pre-call guard; 429 exact message, no refused-job counters; a running job stops before further calls when exhausted, no partial exam. Micro-dollar arithmetic uses model/catalog/cache/thinking usage; usage-free failure charges counted input per spec rather than zero. Month rollover/TTL, missing counter, failed calls/retries, parallel atomic ADDs, aggregate model/day/month totals. Budget is an estimate, not a hard reservation cap. |
| APP-USAGE-01 | Core counters accumulate jobs/calls/tokens/cost for appropriate day/month/model, independent of 24h job expiry; no double counting on duplicate worker delivery. If usage endpoint is in scope/exposed: all signed-in roles, no anonymous access, safe aggregates only, recent newest 50 of today, stale running excluded; no owner IDs/names, filenames, contents or keys. |
| APP-API-01 | Endpoint paths, methods, collection/response/error shapes, `serverTime`, `Location`, `Cache-Control: no-store`, invalid JSON, unknown create fields, content type/body limits, safe errors, unsupported methods. |
| APP-RECOVERY-01 | Dependency failure returns specified safe error without clearing input or corrupting terminal state; user-triggered publish replay completes the same exam. Ordinary serial autosave/resume must work. The accepted delayed-older-PUT overwrite is characterized in section 5.2, not an unconditional no-overwrite gate. |

Assert observable outcomes and stored state. A coverage percentage is supplementary, not the completion criterion.

### 5.1 Fixed answer-key matrix

All cases assume the caller otherwise has resource access. The cutoff does not make an outsider or decliner authorized.

| Caller/state | Before `acceptingUntil` | At/after `acceptingUntil` |
|---|---|---|
| Creator, no attempt | Key visible | Key visible |
| Creator with not-started/in-progress own attempt | Key hidden | Key visible; expired in-progress finalized |
| Creator with submitted own attempt | Key visible | Key visible |
| Non-creator assigned, not-started/in-progress | Key hidden | Key visible; missed/finalized as applicable |
| Non-creator assigned, submitted | Key hidden despite own submission | Key visible |
| Outsider or declined non-creator | 404, no questions/key | 404, no questions/key |

Use exam GET before submission; result may correctly return 409 for an unfinished attempt. A revealed answer (P1) is question-scoped, not full-key visibility. Print answer-key selection requires full `keyVisible`, not merely one revealed answer. Creator generation preview can contain keys before publishing; creator-taking suppression does not erase knowledge already seen in that preview.

### 5.2 Accepted autosave limitation

Backend section 8.5 explicitly allows a delayed older full-map PUT to overwrite a newer one because no sequence/version is sent. Preserve a characterization test demonstrating this, label it `KNOWN_LIMITATION`, and list the data-loss risk in the release summary. Do not turn it into a requirement to implement sequencing without approval.

Still block regressions in normal saving/resume, lost current answers on the zero-time submit body, saves changing a submitted attempt, or a misleading “Saved” indicator when the acknowledged request did not save. If the owner upgrades stale-save protection to a requirement, update the backend contract and replace the characterization with a failing acceptance case before fixing.

## 6. Deterministic AI verification

### 6.1 Worker and validation cases

| ID | Required scenarios and assertions |
|---|---|
| AI-INPUT-01 | API validates ready owned source metadata, model, aggregate bytes/pages before job/quota writes without S3 access; source too many pages gives 422 naming that file, aggregate overflow gives specified set message. Worker rechecks owned sources, stable order, opaque label-to-ID/name mapping; unknown/unreadable labels and no source substitution. |
| AI-PREP-01 | PDF page counting; corrupt/encrypted/unreadable input handled safely; UTF-8/control handling; EXIF before downscale; downscale when longest side >2000px even if file <4 MB; JPEG re-encode when >4 MB or downscaled. No upscaling; rotated non-square images and alpha/format conversion; actual encoded output meets provider limits. |
| AI-CAP-01 | Source count 10, raw bytes 20,971,520, PDF pages 100, input tokens 200,000, question totals/counts/marks and call cap; boundary−1/boundary/boundary+1. Page ranges: a 300-page PDF with range 1-100 passes and 1-101 fails; invalid ranges (from>to, to>pages, from<1, non-PDF, key not in `sourceIds`) return 422 `fields.pageRanges`; the bytes sent to the provider are the trimmed PDF. Invalid metadata creates no job/counters/provider requests. Token cap may require counting; Gemini fallback only if DEC-09 approved. No generation when over cap. |
| AI-VALID-01 | Every check in AI section 7.1 accepts valid boundary input and rejects invalid input: type/schema/marks, empty/length text, HTML/fences, controls, source, options, index, model answer. Include Unicode character-count semantics and extra/missing fields. |
| AI-DUP-01 | Normalized duplicates, case/punctuation/whitespace variants, similarity below/at/above 0.8, duplicates across batches/mark values and against `Avoid`. Define/token-test normalization deterministically; include short questions where a heuristic may over-reject. |
| AI-CHUNK-01 | Counts 1/15/16/30 per mark and mixed distributions under total limits. At most 10 initial logical calls/20 including content retries; each requested count <=15. First call alone, then maximum 3 concurrent; concurrent batches may not yet know each other's texts, so final shared dedup/count validation must catch cross-batch overlap. Replenishment obeys existing retry budget or job fails; never accept short/duplicate output. |
| AI-RETRY-01 | Mixed valid/invalid batch, zero valid, unknown sources, overproduction, invalid JSON; one shortfall follow-up for missing count; kept/dropped texts enter `Avoid` where specified; another shortfall fails. Truncation and shortfall share the one content-retry allowance under DEC-04; each logical call has max one SDK retry. Preserve exact counts or fail, never pad/publish partial output. |
| AI-SHUFFLE-01 | Options retain exactly the same multiset and semantic correct answer after permutation; update `correctIndex`, including initial index zero. Use injected deterministic permutations for exhaustive four-option cases. Production uses the required random source. Identity is a valid permutation: no flaky “must change every time” assertion. |
| AI-ORDER-01 | Ascending marks default; requested shuffle is a valid permutation preserving IDs/content/counts. Test behavior with deterministic randomness rather than a statistical requirement on one run. |
| AI-FAIL-01 | `refusal`, `max_tokens`, bad JSON/schema, timeout, 401/403, exhausted 429/5xx, cancellation, source-fetch failure. Assert exact approved end-user message, bounded calls, safe details, and no partial exam. Combine truncation and shortfall errors under DEC-03/04. |
| AI-JOB-01 | Atomic job/quota creation; Start failure -> failed +503 with accepted quota retained; conditional queued->running claim admits one worker. Heartbeat ~20s even during calls, stale only after >6min, 8min context cancels all in-flight work within 10min Lambda; failed/cancelled/stale never overwritten done. Cancellation observed prevents new dispatch; at most already in-flight calls can still bill and their usage is accounted. Question item written before done, polled only when done; losing finish race never exposes partial questions. |
| AI-USAGE-01 | Per-job/per-call counters, selected model/provider, effective effort, prompt version, request ID, usage/cache tokens, thinking billed correctly, kept/dropped/durations/failures. Usage-free failure charges counted input per app estimate while retaining uncertainty in billing evidence; no invented provider usage. Concurrent job/counter writes avoid lost updates/double-counting; metadata field names match `day-index` projected attributes. |
| AI-FACTORY-01 | Anthropic-only, Gemini-only, both, no-enabled-provider configs; enabled default override, empty-default cheapest, invalid override fail-fast; selected provider invoked once through matching SDK, no silent failover. API config works without either secret; worker requires keys for every enabled provider. |
| AI-BUDGET-01 | Shared monthly estimate checked before each dispatch; exact micro-dollar update for normal/retry/refusal/unknown-usage outcomes and model cache prices; accounting never overwritten by parallel completions. Test threshold crossing while 3 calls/jobs are in flight; report overshoot exposure under DEC-08 instead of claiming serialized reservation. |
| AI-PRIV-01 | Distinct synthetic identity/filename/internal-ID markers omitted from requests outside selected file contents; only selected owned source bytes sent. Logs contain none of the forbidden content in AI section 9.4. Client JSON/bundles/storage contain no provider key, system prompt, raw provider envelope, or authentication secret; the intentional HttpOnly session `Set-Cookie` header and visibility-authorized validated question/key fields remain permitted. |

Define behavior for a call returning too many valid questions before implementation; it must not inflate final counts. If the owning spec is insufficient, add a decision, not an unreviewed test-only rule.

### 6.2 Official SDK adapter boundary

Exercise both actual adapters through their pinned official Go SDKs with fake HTTP transports or local test servers. This is test interception, not a raw-HTTP production implementation. Run model-specific cases for every catalog model, even when live evaluation is budget-blocked.

| ID | Required evidence |
|---|---|
| SDK-01 | Anthropic request per selected model: configured catalog ID, JSON schema, PDF/image/text blocks, max output tokens, approved thinking/effort. Omit effort for Haiku per resolved policy, not blindly medium for every Claude model; forbidden sampling/prefill/forced-tool settings absent. |
| SDK-02 | Anthropic stable prefix/cache breakpoint; first completed call precedes parallel work; labels stable and `Avoid`/marks changes do not change cache prefix. Gemini preserves labelled source order but sends no Anthropic cache-control and creates no explicit caching resources. |
| SDK-03 | Mixed-attachment token counting for supported providers; over-token stops generation. Any Gemini estimate fallback must have approved conservative limits and visible accounting flags; do not silently label estimate measured. |
| SDK-04 | Normalize Anthropic end_turn/refusal/max_tokens and Gemini success/safety block/empty candidate/truncation to shared outcomes. Parse usage/thinking/cache/request IDs without double-counting; context cancellation and 120s attempt timeout propagate; explicit max one transport retry under overall 8min context, no second transient retry layer. |
| SDK-05 | Configuration validates enabled providers, per-mode required secrets, enabled override/default, effort compatibility, budget/caps and model price catalog. Changing supported values changes adapter behavior; request capture only in isolated synthetic tests. |
| SDK-06 | Gemini JSON MIME/schema, inline PDF/JPEG/PNG/text parts, approved thinking mapping and safety finish reasons. No Files API, tools, explicit context caching, free-tier key, or cross-provider fields. Unknown response finishes fail safely instead of creating empty exams. Paid-tier project proof belongs to preflight, not fake transport tests. |

Fuzz parsers/validators and malformed-byte preprocessing with a bounded corpus and runtime. Add each found crash as a regression. Network-disabled tests must fail if any code unexpectedly reaches the live provider.

## 7. Real AWS integration verification

Run on the approved isolated dev stack. Reuse repository contract cases against both the in-memory and DynamoDB implementations.

| ID | Required scenarios |
|---|---|
| AWS-STORE-01 | Create/read/update/query behavior, conditional failures, strongly consistent base reads, bounded GSI lag, multi-page DynamoDB responses, BatchGet unprocessed keys, SDK throttling/exhaustion error mapping. Do not confuse API list caps with DynamoDB response-page size. |
| AWS-AUTH-01 | Simultaneous same-email signup and competing Google-link attempts create one identity/lock mapping; `SessionsValidAfter` and password removal atomic; old cookie denied after link and different-subject email conflict leaves data unchanged. |
| AWS-ATTEMPT-01 | Concurrent autosave, manual submit, and lazy finalize; exactly one stable terminal result under resolved deadline policy. No successful late overwrite. |
| AWS-JOB-01 | Worker claim/duplicate delivery, heartbeat/terminal races, atomic per-user/kind and global daily quotas, shared month estimate. Refused transaction creates nothing, accepted Start/LLM failure still consumes quotas. Soft running limit race characterized, not falsely tested as hard. |
| AWS-S3-01 | Real presigned PUT/GET, matching MIME/length, wrong headers/bytes, complete validation, private anonymous fetch denied, URL expiration, deletion and retry. |
| AWS-DATA-01 | Maximum legal exam/job item payloads serialize within DynamoDB item-size limits, including long free answers, Unicode, question names, and storage overhead. If product limits allow oversized items, report a spec/implementation defect rather than silently reducing fixture size. |
| AWS-ACCESS-01 | No request-path `Scan`; table/bucket/function-scoped permissions sufficient for actual operations, including transactions. Check effective deployed policy, not only YAML intent. |
| AWS-PUBLISH-01 | Exam-first and conditionally written attempt rows, 10 at a time; partial failure then same-key replay completes all intended assignments, no changed exam. Other-creator key clash denied; assignee caps persist across assign-more and declines. |
| AWS-QUOTA-01 | Barrier-based concurrent jobs at user/global daily threshold admit no more than hard remaining allowance; UTC rollover independent of delayed TTL; shared month/day per-model atomic ADDs survive parallel completion and duplicate worker delivery. Characterize non-reserved monthly overshoot separately from hard daily counters. |
| AWS-LAYOUT-01 | All tables PAY_PER_REQUEST, intended INCLUDE projections (not ALL); metadata-only job polling never reads `<jobId>#questions` until done, terminal read returns it, and its failure/missing record is not a false successful empty exam. Both items 24h TTL, daily quotas 3d/month counters 40d; question items have no day/owner index membership. |

Use test barriers for concurrency and assert outcomes, not merely “no exception.” A deployed worker must not contain a public test mode that bypasses auth or provider selection.

## 8. Browser verification

Use Playwright or the available browser-control tools. Automated selectors should use accessible roles/labels or stable test IDs. Condition waits must be bounded. UI tests must not replace failures with direct database edits.

| ID | Journey / expected result |
|---|---|
| UI-AUTH-01 | Both signin methods, role-required retry, generic error, blocked Google script leaves email usable, refresh persists session, logout removes access. Real Google popup/account consent gets a witnessed manual case if automation is unavailable; a fake token cannot satisfy it. |
| UI-DOC-01 | Multi-file input, real progress, retry one failed upload without duplicate ready rows; PDF/TXT/JPG/PNG accepted, other/oversized rejected; viewer multi-page scroll/image zoom; confirmation before delete; expired viewer URL refreshed once. |
| UI-GEN-01 | Source count/caps; under/exact/over marks target; type, async queued/running progress, preview, order/shuffle; 1.5s polling stops on terminal/navigation. Cancel, leaving, 3 consecutive poll failures or >6min send DELETE before abandoning; source selection retained on 422. AI disclosure visible; 429 shows exact message with no automatic retry button. |
| UI-MODEL-01 | All roles see catalog/default/cost hints, pricier notice and session-local choice; chosen `model` sent/returned. Catalog fetch failure/empty hides select, omits model and backend uses default. Model 422 refetches catalog/resets to default with notice; does not retry paid work automatically. Deferred regeneration, if enabled, retains original generation's model and avoid list. |
| UI-EXAM-01 | All roles including students see Create and can publish practice exams; no key-visibility selector. Known/unknown/disallowed assignees and 10/50 helper/caps; Open/Closed/Created and declined counts; only creator sees status/assign controls. Publish retry reuses Idempotency-Key. |
| UI-DECLINE-01 | Confirmation for not-started assignee; decline removes both lists and direct exam/result/print access; creator sees declined status/count. Already-started attempts cannot decline; retry after lost decline response is idempotent, no undo. |
| UI-RETAKE-01 | After submitting, Retake from the Closed row and from the result page starts attempt 2 (fresh answers, fresh peek budget, header "Attempt 2"); the result page lists every attempt with marks and View opens that attempt's own answers; Home shows best score and attempt count; Retake after the deadline is refused (`409 exam_closed`, no Retake button); a second Retake while an attempt is unfinished resumes it and creates nothing; at 50 attempts the limit message shows; a decliner has no Retake; the creator's status table shows best score, attempt count and an expandable per-attempt list, and no answers anywhere. Per-question correctness stays hidden for non-creators until `acceptingUntil` on every attempt. |
| UI-ASSIGN-SELF-01 | On Create, ticking **Assign to me** (alone) publishes an exam that appears in the creator's own Open list with peeks default 3; ticking it with other emails assigns both, the creator appears as a row on the status page, and the default peeks is 0; typing the creator's own email as well creates one row; the Assign dialog on an exam where the creator has no row offers **Assign to me**, and hides it once they have one. |
| UI-TAKE-01 | Answer selection/text, debounced save, truthful “Saved” state, navigation, review unanswered, submit confirmation with blanks, save/exit/resume across a new login, correct last position. |
| UI-TIME-01 | Refresh/device clock offset; at timer zero show Closing… and POST submit with current answers directly, no preceding autosave dependency. A read in grace does not finalize, Open remains open; cutoff 409 redirects correctly. One real short-deadline attempt and one never-started/missed case. |
| UI-RECOVERY-01 | Fail reads/autosave/publish/submit/delete/decline; preserve input and specified retry UI; stable publish key and submit final answers. Delayed/reordered save characterization follows accepted gap section 5.2, not an invented sequencing guarantee. Submitted attempts remain immutable; lost-success responses do not duplicate exams/submissions. |
| UI-KEY-01 | Fixed section 5.1 matrix in raw responses/network/DOM/storage; creator-taking suppressed, submitted non-creator still hidden until acceptingUntil. No correctness pills when hidden, except documented revealed-answer display. Score visible where allowed; no visibility setting; uses canonical server data. |
| UI-RENDER-01 | Generated text never executed as HTML; Unicode maths readable; no Markdown/LaTeX dependency; valid source names shown without exposing source files to assignees. |
| UI-MOBILE-01 | All routes at 375 px: no horizontal page overflow; >=40 px actionable touch targets; usable scrolling/keyboard/focus; text labels accompany status colors. Compare relevant screens against approved prototype, including computed font stack. |
| UI-DEVICE-01 | Real phone camera capture/upload/view and one desktop flow. A file chooser with a preselected photo proves upload, not actual camera capture; report these separately. |
| UI-PRINT-01 | Print links from Home/exam/result/status, correct route and GET-only fetch, toolbar choices/preview and window.print; see detailed paper cases below. All roles, closed exams and creator-taking key exception included. |
| UI-USAGE-01 | P1 dashboard when in scope: every role, no admin gate; polls every 5s, pauses hidden/refreshes visible, last successful serverTime; 80% warning/100% exhausted meter, day/model/recent/running, empty state and failed-refresh preserves data; no identifying/content data. |
| UI-PEEK-01 | P1 when in scope: stepper/default 0 assigned or 3 practice subject to bounds, confirmation, repeat free, persisted revealed answers/peeks left, half-mark results, before/after penalty, no-peeks badge only when maxPeeks>0 and none used. See section 8.2. |

The required automated browser baseline is desktop Chromium and a 375 px mobile viewport. Record actual browser/device versions. Real-phone evidence remains required for camera behavior; unavailable hardware is blocked, not passed by emulation.

### 8.1 Paper/print acceptance (P0)

Generate print-to-PDF artifacts using the browser's print rendering, then inspect rendered pages as well as extracted text. A screenshot of the screen preview alone is insufficient. Store non-sensitive PDFs/page images privately with the run evidence. A witnessed native-dialog check confirms Print opens the dialog and Save as PDF works; automation may intercept the dialog for other cases but must record that substitution.

| ID | Required paper cases |
|---|---|
| PRINT-01 | MCQ/free text, small and 60-question long exams, A4 and Letter; no fixed page heights, clipping, split question blocks, orphan numbers, overlap, footer collisions or blank accidental pages. Title, total, creator, name/date/score fields correct; Unicode maths preserved; black-on-white Helvetica 11pt body, specified margins, controls/toolbar/shadows/deadlines absent. |
| PRINT-02 | MCQ question options A–D, bracketed marks, answer sheet new page and optional; >10 questions uses two columns and proper circles. Free text has 3 ruled lines/mark at specified 8mm spacing, no MCQ answer-sheet choice. Selected parts only, question paper cannot all be deselected. |
| PRINT-03 | Full visible key only, own page after other selected parts; option letter/text matches current stored option order; free-text model answer/marks correct. Hidden key checkbox disabled and no key text in print DOM/PDF. A single revealed answer never enables printing the full key. |
| PRINT-04 | Creator-no-attempt versus creator-taking versus submitted creator; non-creator before/after acceptingUntil; outsider/declined 404; closed non-declined exams printable. No starting attempt, reveal charge, print recording or other write; lazy expiry finalization remains allowed where backend GET already requires it. |
| PRINT-05 | Native Print and Save as PDF available, absence of window.print handled; print opens from taking screen in new tab without losing input; output name-line option works; page-number support is optional and not a blocker. No new PDF backend endpoint, paper-score storage or offline mode required. |

If legal maximum content cannot fit a complete question block on one page, report the conflict between input/layout bounds and the no-split requirement; do not reduce fixture size, hide content, or accept clipping to pass.

### 8.2 Conditional P1 reveal verification

Add these required cases only when reveal is in scope/enabled. Baseline fields, null/numeric scores, and fixed key rules remain core even when it is deferred.

| ID | Required outcome |
|---|---|
| PEEK-01 | Attempt required, only not-started/in-progress before acceptingUntil; reveal starts attempt; maxPeeks=0 ->403; full-key already visible or new question over quota ->409; invalid question/access denied correctly. |
| PEEK-02 | Repeated same question free, including retries after lost response; concurrent distinct reveals cannot exceed allowance; another user's revealed set never leaks. Already-revealed answers survive reload/resume without another charge. |
| PEEK-03 | Wrong/unanswered revealed scores zero, correct revealed earns half, unpeeked correct full; 1-mark peek ->0.5, odd/even marks exact; scoreBeforePeeks unaffected. Integer halves persist; APIs/UI/print key option letters stay aligned after option shuffling. Free text retains null server scores/local half cap only. |
| PEEK-04 | Reveal/cancel/submit/decline/cutoff races preserve one terminal state and bounded allowance; no peek possible after submit/closure; badge/penalty/counter/disabled states match canonical data. |

## 9. Principal live acceptance: upload, create, solve, take, verify

This journey is the central answer to “does the product actually work?” Run it on the deployed frontend against the intended dev backend and actual provider, with mocks off.

### 9.1 Setup and independent solution protocol

1. Use `teacher-a` and a fresh fixture version with definite, independently verified facts/calculations. Keep a separate photo journey.
2. Use assigned exams with `maxPeeks: 0`; non-creator students receive no keys until `acceptingUntil` under the fixed rule. There is no `answerKeyVisibility` field. Creator-taking tests use a separate fresh context and do not claim that earlier generation-preview knowledge was erased.
3. Isolate the solver from creator previews, provider output, answer keys, and generated `correctIndex`/`modelAnswer`. The creator runner passes only student-visible question text/options and question IDs to the solver.
4. The solver first answers from the question alone. Then validate that reasoning against the original source/reference, without reading the generated key. This separates self-containedness from grounding.
5. Record answer text, reasoning, source evidence, confidence, ambiguity, and expected marks. Freeze/hash the solution artifact before opening keys.
6. Only after freezing solutions compare against generated keys. A key-aware agent must not claim a later answer was blind; use an isolated solver context, a human, or a fresh held-out exam. No need for parallel agents or an additional runtime service.
7. Uncertain, ambiguous, unsupported, or disputed solutions require independent/human adjudication. Another model's agreement alone is not a proof.

### 9.2 Live journey steps

| Step | Actor/action | Required assertion/evidence |
|---|---|---|
| LIVE-01 | Teacher signs in, uploads FX-01, opens it | Real S3 upload, correct library entry/type/pages, working viewer; no file body through Vercel/API. |
| LIVE-02 | Configure MCQ counts `{"1":2,"2":2,"3":2,"4":2,"5":2}`, select enabled model from catalog | 10 questions, 30 marks, every mark category, disclosure/cost hint and correct default; record exact selected provider/model/settings. |
| LIVE-03 | Generate within approved job budget and UI polling window | Actual selected provider/request metadata, async progress; exact counts or fail case; valid attributed self-contained questions; no hardcoded success. First standalone call then bounded parallel work verified in lower layers. |
| LIVE-04 | Publish with enough time for solve/review, assign dedicated students, maxPeeks=0 | No visibility control; students see exam, unknown/disallowed email warnings checked separately; outsider cannot open exam or source files. |
| LIVE-05 | Student-a opens; isolated solver produces frozen answers | Student-visible questions only, independent solutions/evidence; no key fields in student responses. |
| LIVE-06 | Student-a enters some answers, waits for saved confirmation, exits, starts a new login/session, resumes | Answers and position restored exactly; remaining time tracks original deadline. |
| LIVE-07 | Student-a completes and submits independent answers | Stored answers equal submitted values; submitted result stable; hidden per-question correctness remains absent until close. |
| LIVE-08 | Separate controlled students use the scoring patterns below | Exact expected totals from independent fixture/key evidence; unanswered confirmation and idempotent submit checked. |
| LIVE-08b | One controlled student submits, retakes twice with different answer patterns, and reads each attempt | Each attempt's stored answers and exact total match its submitted pattern; earlier attempts unchanged after later ones; best score and attempt count correct on Home and in the creator's status; retake refused after `acceptingUntil`; per-question correctness absent for every attempt until the cutoff. |
| LIVE-09 | Teacher reads per-assignee status; separate not-started assignee declines | Counts include declined, decliner loses access; started/submitted cannot decline. Never-started and timed-out variants follow acceptingUntil semantics. |
| LIVE-10 | After acceptingUntil, inspect canonical key/results and compare | Non-creator key hidden even after own early submission; full key visible only after cutoff. Classify AI/scoring/visibility/UI defects; frozen solution hash and totals. |
| LIVE-11 | Repeat a smaller live path with FX-04 image and a free-text exam | Correct source handling; model answer grounded; `score` null, no claim of AI grading. |
| LIVE-12 | Student-creator publishes/takes practice exam; prints assigned MCQ/free text | All-role create works; creator-taking key hidden until submitted/over, full key visible afterward. Real Print dialog and A4/Letter PDF evidence; no attempt start from print. Reuse already generated approved outputs where legitimate, not extra paid generation just for print. |

Use a short-deadline separate exam for expiration; do not rush the independent solver to fit a one-minute exam. Do not change deployed server time or write attempts directly to simulate live expiry.

The principal live journey certifies the tested model only. Section 10 supplies the broader selectable-model quality matrix. Do not reuse a later generated key to retroactively “solve” a student attempt.

### 9.3 Controlled score verification

For a fixed deterministic 10-question, 30-mark exam, use independently authored keys, not generated ones:

- All correct: expected 30/30.
- All wrong: expected 0/30.
- All unanswered: expected 0/30.
- Mixed: one correct and one wrong/blank at each mark value: expected `1+2+3+4+5 = 15/30`.

Use maxPeeks=0 for this baseline. With P1 enabled, add a controlled half-mark example: reveal one correct 1-mark question in the all-correct exam ->29.5/30 with scoreBeforePeeks=30/30; a wrong revealed answer still earns zero. Assert JSON numeric representation and UI formatting separately.

The complete pattern matrix runs without paid generation. Repeat at least one mixed pattern through the live-generated exam. If its independent key differs from the generated key, record both semantic expected score and mechanical expected score:

```text
semanticExpectedScore = sum of marks where the answer is independently correct
mechanicalExpectedScore = sum of marks where saved answer exactly matches stored key
actualScore = result returned by application
```

- Actual differs from mechanical: grading/storage/option-remapping defect.
- Mechanical differs from semantic: AI question/key defect, even if grading code behaves correctly.
- UI differs from canonical API: presentation/stale-state defect.
- Early keys/correctness appear: visibility/privacy defect.

Never report the journey fully passed because the grading implementation correctly applied a wrong generated key.

## 10. Golden-set AI evaluation

### 10.1 Execution plan

Run `make ai-eval` manually with explicit live permission, fixture selection, model/provider/effective-effort/prompt identity, price-table version, approved job count, and budget. Exercise the actual preprocessing/worker/validator pipeline, not an unrelated evaluation-only prompt. Test both official adapters through the same rubric and validator.

Baseline: each golden fixture in both MCQ and free-text modes using two questions at each mark value (10 questions, 30 marks). For fixtures declared insufficient/unreadable, use their predeclared request and expected failure rather than demanding invented output.

The manifest also includes:

- A multi-source combination for source attribution and cross-source duplicates.
- A readable-plus-unreadable combination for `failedSources` and partial-source success.
- Two held-out fixtures not routinely used for prompt tuning (they may be designated members of the 8–10 set).
- Two additional repetitions of a small readable PDF/photo subset to expose variability.

Calculate the full workload for **every enabled/selectable model**, not just the default. Four models times ten sources times two types is 80 baseline jobs before mixed-source cases, repeats, and live journeys. At the default global limit of 10/day this requires at least eight UTC-day allowances; more accounts do not bypass that global limit. The per-user generation limit is 3/day and monthly budget is shared across all these jobs.

The complete campaign therefore cannot be assumed to fit one two-hour loop or even the project's three days. Choose an explicit owner-approved plan before starting:

- Use a dedicated isolated verification dev stack/account with its own approved actual-provider spend budget and a bounded test configuration that permits the planned workload. Test the production-default 3/10/10 quotas separately; record every dev override and never carry it to production.
- Or keep production-equivalent quotas and persist/resume the campaign across UTC days, without erasing counters, expiring evidence, or resetting spending on resume.
- Or launch with a smaller **explicitly enabled catalog** whose full required evaluation fits the approved budget, updating owning config/spec decisions. A diagnostic subset alone is not certification of untested selectable models.

Default policy is no quota overrides; the first option requires explicit owner approval, not an automated “fix” for 429. If time/budget is insufficient, report the campaign incomplete rather than shrinking the manifest silently. Price conservative worst cases before approval and record measured usage afterward.

### 10.1.1 Model and provider matrix

For release, include each enabled catalog row with its effective supported settings, model ID, SDK/provider, default/override status, fixture/results counts, reviewed-quality scores, and cost/time. Anthropic-only and Gemini-only deployments need evidence for their enabled rows; a disabled provider can be marked out of deployed release scope but its adapter's deterministic tests remain required.

- Cheap default quality must pass independently; the presence of an expensive fallback does not excuse invalid default questions.
- Switching models per request is supported user behavior, not an owner-only action. The harness may select each approved model for a test, but may not silently substitute a different model when the selected one fails.
- Newly enabling a model invalidates release completion until that model's required evidence exists. Changing the default to another already-certified model still requires default/selection and live-journey checks.
- Gemini uses no explicit caching; zero implicit cache reads alone is not failure. Anthropic caching is tested with a deliberately eligible prefix.

### 10.2 Review records

For every generated question, record:

- Fixture/source evidence and question ID; question type/marks.
- Structural acceptance and any drops/retries.
- Groundedness and independent answer/solution.
- Unique MCQ correctness or free-text mark-scheme validity.
- Self-containedness, language, age appropriateness, plain-text compliance.
- Difficulty match, topic/angle, and duplication.
- Reviewer identity/type, reasoning, confidence, adjudication status.

Review all questions in the principal live acceptance exam. For the wider golden run, preselect a stratified sample of at least 30 MCQs and 30 free-text questions, covering every mark value and all successful source categories; if fewer exist, review all and report the shortfall. Always review all injection, biology/chemistry-refusal, unreadable/hallucination, and automatically flagged outputs. Do not select only good-looking questions after generation.

An LLM judge may triage with a versioned rubric and evidence. It may not be the sole release authority for correctness or use the generated key as ground truth. Maintain the same sample/question IDs for the report; never drop a failed question from the denominator.

### 10.3 Quality rubric

| Dimension | Pass |
|---|---|
| Grounding | Question and correct answer are supported by the attributed material; required calculations derive from stated facts/data. |
| Correctness | Independent solution agrees; no faulty arithmetic, unsupported assumptions, or scientifically incorrect key. |
| MCQ | Exactly one defensible correct option; distractors plausible, distinct, similar in form; prohibited meta-options absent. |
| Free text | Definite supported answer; concise appropriate mark scheme with distinct key points consistent with marks. |
| Self-contained | Student need not see a source passage/table/figure; necessary data is in the question. |
| Difficulty | Matches AI section 6.3 reasoning requirements, not merely text length. |
| Coverage | Uses multiple supported topics where present and avoids semantic repetition across the whole exam. |
| Language/safety | Follows source-language rule, school-age/neutral tone, does not follow injected instructions. |

Score difficulty and coverage separately using an approved rubric (for example 0=fail, 1=borderline, 2=pass). Final numerical thresholds require DEC-06 before release. No averaged score may conceal a wrong key or fabricated answer.

### 10.4 Gates and interpretation

The AI spec's proposed thresholds must be confirmed by the owner. This verification plan proposes the following stricter release treatment; it becomes binding only when DEC-06 is approved and the owning AI spec is aligned:

| Measure | Required result |
|---|---|
| Delivered structure | 100% valid; no invalid question reaches a published exam. |
| Counts on predeclared sufficient readable fixtures | 100% of required runs meet exact distribution within agreed retry budget. |
| Declared unreadable/insufficient fixtures | Expected safe outcome, no fabricated content or partial exam. Counts need not be met when failure is the correct outcome. |
| First-pass validator acceptance | At least 90%; report raw kept/returned counts as in AI spec. |
| Critical correctness | Zero observed wrong/ambiguous keys, fabricated source claims, unsafe injection-following, or key leakage in the required reviewed sample. |
| Refusals on legitimate biology/chemistry fixtures | Zero in required runs; report failures, never retry until a favorable run hides them. |
| Difficulty/coverage/language | Approved DEC-06 thresholds met; report each dimension and denominator separately. |
| Cost/time | Within approved hard harness limits and 8-minute job deadline; respect UI cancellation after 6 minutes and report both outcomes. About 2 minutes is the measured product target, not an old 60-second gate. Report provider/model, p50/p95 when sample supports them, calls/retries, cache and conservative unknown-usage exposure; app monthly spend is an estimate, not a guaranteed cap. |

Report `29/30`, not only “97%”. A small sample with zero observed failures is limited evidence, not proof of zero population error. Keep earlier failed attempts in history. A new candidate may pass after a fix, but a rerun of an unchanged stochastic candidate must not erase its failures.

### 10.5 Change comparison

System prompt/schema and release-level model/effort changes follow AI section 13.4, with the per-user-selection distinction requiring DEC-12: record the agreed prompt version behavior, rerun affected diagnostics plus the full required golden set for affected model rows, compare with prior baseline. Also invalidate relevant evidence for preprocessing, output validation, cache-prefix, source-selection, provider-adapter, catalog capability/price, budget, or concurrent batching changes. A shared prompt/validator change affects both providers and all selectable rows.

Compare fixture/model-by-fixture/model results, drop/shortfall/refusal rate, reviewed quality, calls, tokens/cache usage, latency and cost. Do not silently change an explicit user/test selection or deployment default to get green. Catalog price changes invalidate spend/accounting tests and estimates, not automatically every unchanged question's semantic review; record dependency reasoning. Usage without a known price is usage, not a verified dollar total.

## 11. Deployed configuration, privacy, and operational checks

| ID | Required assertion |
|---|---|
| DEPLOY-01 | `/api/health` through Vercel works; real auth cookie survives refresh with intended production flags; correct rewrite/path handling; no business/data logic migrated into frontend to bypass backend bugs. |
| DEPLOY-02 | Actual 20 MB PDF uploads direct to S3 with real progress; private bucket and anonymous object fetch denied; presigned expiry checked using a new request after expiry, not a cached loaded viewer. |
| DEPLOY-03 | >20 MB single upload rejected; two individually valid sources totaling >20 MB, or a valid uploaded PDF >100 pages with no page range (or a range longer than 100 pages), fail generation metadata validation before job/quota writes or provider calls. Do not retain the obsolete 25 MB accepted-upload case. |
| DEPLOY-04 | Exact production/dev origins and Google client separation; real Google flow; no permissive wildcard introduced to make previews pass. Direct Function URL still enforces auth/authorization and mutation-origin policy. |
| DEPLOY-05 | Effective API/worker IAM separation supports operations without broad access: missing S3 key maps correctly with intended ListBucket permission; worker reads documents/objects and updates jobs/counters, not exam/user data. Job deadline/heartbeat/SDK retry and optional concurrency reservations match actual config; low account quota does not make optional reservations mandatory. |
| DEPLOY-06 | Private encrypted storage, no secrets in bundles/Git/config/history/reports/process arguments, safe logs/retention, stage-separated secrets; provider keys worker-only, JWT API-only, no public bypasses. Generated untracked deployment configuration must be ignored and kept private per deployment spec; scan with redacted findings. |
| DEPLOY-07 | Verify $15 app estimate and actual controls for every enabled provider under the approved total $20 plan (DEC-08); distinguish hard cap, alert, quota and estimate. Catalog prices/usage consistent; Anthropic eligible cache case works; Gemini has no explicit caching and may report zero implicit hits. Never use independent $20 provider caps as proof of $20 combined spend. |
| DEPLOY-08 | SAM lint/build, all tables PAY_PER_REQUEST, correct small INCLUDE projections and job/counter TTLs, account/region, stack protection and deployment section 10. No obsolete provisioned-capacity/free-unit gate. Required runtime config on correct functions, optional override values preserved safely. |
| DEPLOY-09 | Both enabled providers' privacy terms reviewed before real users/minors; paid-tier billing-enabled Gemini project evidenced; correct disclosure. Labels do not remove personal data within files. Provider terms/prices/IDs remain preflight-verification claims, not facts certified by this document. |
| DEPLOY-10 | Enabled-provider catalog/default and API/worker config agree; pricier user choice reaches actual intended SDK without leaking secrets. Usage page/endpoint (if enabled) remains aggregate-only under live data, failed/cancelled jobs and daily/monthly rollovers. |

Do not repeat all slow checks after every CSS edit. Reuse still-valid evidence only according to section 12.2.

## 12. Evidence, verdicts, and completion criteria

### 12.1 Run artifacts

Each run directory contains:

```text
manifest.json                   # candidate/environment/config/fixture hashes
results.json                    # all required case IDs, statuses, timestamps
summary.md                      # readable verdict and remaining work
failures.json                   # reproductions, root causes, fix references
decisions-snapshot.md           # policies used
ai-metrics.json                 # per-model requests, usage, costs, quality
reviews.json                    # frozen solutions and review attestations
resource-manifest.json          # only run-owned data and cleanup state
campaign-state.json             # model/fixture progress, quota schedule, budget
iterations/001/...              # immutable iteration results and redacted evidence
```

Per-case record: ID, source section, profile/layer, expected/actual, status, start/end, fixture, candidate fingerprint, evidence paths, failure category, reproducer, and any linked fix.

Record `KNOWN_LIMITATION` as a finding category/annotation, not a seventh execution status. Its characterization test passes only if the approved baseline behavior is observed, and the summary must state the risk. New unmet requirements still fail.

Example result shape (illustrative values, not execution evidence):

```json
{
  "runId": "<unique run ID>",
  "profile": "release",
  "candidateFingerprint": "<hash>",
  "verdict": "INCOMPLETE",
  "cases": [
    {
      "id": "LIVE-07",
      "source": "backend-spec.md section 8.5",
      "status": "NOT_RUN",
      "expected": "Independent answers saved, submitted once, key still hidden",
      "actual": null,
      "evidence": [],
      "reason": "Live execution not yet authorized"
    }
  ]
}
```

The implemented schema also requires timestamps, environment identity, required/executed counts, and links to prerequisite results. Reject an aggregate whose ID set differs from the required manifest or whose evidence paths are missing. Reports must never serialize illustrative examples as actual passes.

Statuses:

- `PASS`: asserted outcome observed with required evidence.
- `FAIL`: observed behavior violates the agreed expectation.
- `BLOCKED`: prerequisite/decision/access/service missing; reason and next action required.
- `NOT_RUN`: not executed.
- `REVIEW_REQUIRED`: automated execution complete but independent judgment missing.
- `OUT_OF_SCOPE`: explicitly excluded by the approved scope manifest.

No generic `SKIP` status counts as a pass. An approved exception is recorded separately as `ACCEPTED_GAP`, never converted to `PASS`.

Redact cookies, credentials, authorization headers, signed URLs/query strings, and real personal data. Browser traces/HARs/screenshots can contain secrets or answer keys: disable unnecessary capture, sanitize before sharing, keep artifacts private, and verify redaction. Do not retain complete raw traffic “for convenience.”

Synthetic eval text/solutions may be stored privately in eval artifacts for review. They are not application logs and must not cause production logging of prompts/raw outputs/question text, which AI section 9.4 forbids. Use an explicit retention policy: default local run artifacts expire after seven days unless the owner retains a sanitized baseline; maintainable summaries/regression fixtures can be committed after review.

For a campaign spanning more than seven days under global daily quotas, approve longer private evidence retention before execution; do not expire early fixture/solution evidence and then call the campaign complete. Keep only necessary non-sensitive review artifacts. Job TTL expiry does not erase quota/spend history or excuse missing required evidence.

### 12.2 Evidence freshness

Fingerprint source diff, enabled provider/catalog/default, per-model effective settings, price version, API/worker quota/budget config, build/deploy IDs, prompt version, both SDKs, fixture hashes, and harness version.

- Every final candidate runs the complete fast suite.
- Every release candidate runs the principal deployed live journey on its actual deployed builds.
- Reuse expensive golden evidence only if relevant semantic/request dependencies for that model are unchanged and the report identifies its original run; apply the price/accounting distinction in section 10.5. Shared AI changes invalidate all affected rows, not just the default.
- Reuse unrelated manual device/config evidence only with explicit dependency reasoning and matching environment; expired tokens/URLs are never reused as evidence.
- A changed test/rubric invalidates prior results for that test. Revalidate cases removed from the manifest against approved scope; the aggregator must detect silent deletions.
- Expected failures during fault injection pass only when the predicted safe behavior is observed; an arbitrary exception is not the expected outcome.

### 12.3 Final verdicts

| Verdict | Meaning |
|---|---|
| `ALL_PASS` | Every required case for the selected profile is `PASS`, review complete, no unresolved required decision, no accepted gap, valid evidence. Always name the profile. |
| `FAIL` | One or more required observed failures remain. |
| `INCOMPLETE` | Required case blocked/not run/unreviewed, budget exhausted, or execution interrupted. |
| `PASS_WITH_ACCEPTED_GAPS` | Owner explicitly accepted documented exceptions. This is not “all tests passed.” |

`ALL_PASS (fast)` is not `ALL_PASS (release)`.

Release completion requires all required application/SDK/AWS/UI/print/live/AI/deployed cases, every enabled selectable model's required evaluation, frozen independent-solve evidence, successful required cleanup or explicitly adjudicated cleanup findings, and final report validation. There must be no unresolved defect against the approved contract in correctness, data preservation, access, leakage, or spending controls. Accepted baseline limitations must still be disclosed and cannot be represented as guaranteed protection.

Passing synthetic-data release verification establishes readiness for the approved demo/test scope. It does not establish legal/privacy approval for real users, eliminate documented MVP security gaps, or authorize public launch.

## 13. Repeatable goal loop

### 13.1 Objective

> Bring the selected profile to `ALL_PASS` for the current candidate by reproducing failures, fixing causes, adding regression protection, and rerunning required checks without weakening requirements or exceeding approved limits.

This file defines a loop; writing it does not start one, create a goal, enable paid calls, or deploy anything.

### 13.2 Durable loop state

Persist `.verification/loop-state.json` after every iteration:

- Objective/profile, run ID, candidate fingerprint, approved scope/config.
- Completed/current case IDs, failures, unresolved decisions, next actions.
- Iteration count, elapsed time, approved/consumed/reserved paid budget, jobs/calls.
- Per-model/fixture campaign completion, current UTC quota/month keys, remaining daily/global allowance, next eligible run time, current app estimated spend and provider/harness uncertainty.
- Resource manifest, last successful layer, links to immutable run evidence.
- Blocker signature and count of consecutive iterations with no progress.

On resume, inspect Git state and deployed identity first. Invalidate stale evidence; do not restart spending counters. Preserve user changes and unrelated work.

Scheduled quota exhaustion is not an implementation failure. Persist the campaign and next UTC eligibility, stop incomplete if the current session cannot wait safely, and resume under the same budget later. Do not poll every few seconds until tomorrow, increment three artificial blocker iterations immediately, or auto-reset counters.

### 13.3 Algorithm

```text
load configuration, scope, existing loop state, and prior immutable evidence
preflight target, permissions, decisions, tools, fixtures, and remaining budgets
validate required case manifest against owning specs

repeat while limits permit:
    identify candidate and invalidate affected evidence
    run fast suite (or implement missing harness/cases without claiming a pass)
    classify every failure / blocker / pending review

    if actionable failures:
        reproduce the highest-risk root cause
        add an independent failing regression case
        make the smallest appropriate implementation fix
        rerun that case and related regressions
        save evidence and state
        continue

    run required remaining layers, cheap before expensive
    check model matrix, UTC daily/global quotas, app monthly meter and budgets
    require explicit permission and available harness reserve before paid jobs
    perform blind solving/reviews, deployed checks, and safe cleanup
    aggregate all required case IDs and validate evidence

    if ALL_PASS:
        run final full fast suite and any checks invalidated by last edits
        verify aggregate again
        write final report, mark goal achieved if an active goal was requested
        stop

    if an independent review finds a defect:
        preserve original output and review as regression evidence
        diagnose and fix; do not reroll until a lucky output appears

    if external/decision/access blocker:
        continue other useful independent work
        record exact unblock action and consecutive no-progress count
        after three consecutive iterations with the same blocker and no
        meaningful remaining work, stop incomplete and request that action

    save all execution, spending, cleanup, and blocker state

on budget/time/iteration exhaustion or interruption:
    stop new work safely; persist state
    report INCOMPLETE or FAIL as appropriate, never ALL_PASS
```

If the host has explicit goal-management rules, obey those rules when updating an active goal's status. Do not claim that this harness's blocker counter overrides host rules.

### 13.4 Fix priority and guardrails

Order: unauthorized access/key leakage -> data loss/deadline/scoring -> wrong AI answers/injection -> broken primary journey -> configuration/cost failures -> other required UI behavior.

- Fix root causes, not assertions. If the test is genuinely incorrect, explain why using the approved requirement and preserve a reviewable test change.
- For intermittent deterministic failures, capture seed/timing and repair synchronization or code. A later pass does not erase an unresolved flake.
- For stochastic AI failure, save the failing synthetic output/source as a deterministic regression and update prompt/validation only with full golden regression.
- Do not broaden permissions, disable validation/auth, extend deadlines, or replace live calls with fakes to get green. No automated quota increase; section 10.1's separately approved isolated dev campaign configuration is the only planned override exception and must not reach production.
- Do not add unrelated/deferred features. Scope cuts need owner approval.
- Test diagnostic mutations only in isolated code/workspaces, never deploy them. At harness bootstrap, deliberately leak a test key, change a scoring result, and bypass an ownership check; the corresponding tests must fail. Revert every mutation.
- Do not commit/push/deploy without the relevant authorization. Never overwrite unrelated working-tree changes.

### 13.5 Cost and retry guard

Before a paid job, reserve a conservative upper bound using verified pricing, input caps, allowed generation/retry calls, output-token cap, and possible cache misses. Do not rely on caching to fit the budget.

This reservation is a **harness safety mechanism**, not a claim that the product's approximate spend guard reserves funds. In dev verification bound the harness's own concurrent jobs/calls; include any other approved users/workloads on a shared provider account in the risk assessment. Use a dedicated verification account/key if otherwise the approved envelope cannot be bounded. Observe the application's $15 meter and UTC quotas even when the harness has additional budget.

Track actual usage where available, plus reservations for in-flight/unknown-usage calls. SDK retries can incur uncertain billing; unknown usage is not free. Stop new paid calls when a safe reserve cannot be made. Wait/cancel safely and report uncertainty.

Provider spending controls are an additional backstop, not a replacement for harness admission limits. Do not implement an unbounded `while ! make ai-eval` loop. Live external calls are made only when necessary to validate a changed candidate and within approved limits.

### 13.6 Ready-to-use agent instruction

```text
Read goals.md, frontend-spec.md, backend-spec.md, AI-spec.md,
deployment-spec.md, and verification-spec.md.

Implement missing verification infrastructure and execute the approved profile
using verification-spec.md as the acceptance contract.

First inventory what exists. Do not invent successful results or runnable
targets. Resolve/record contradictory requirements; do not choose policy
silently. Preserve unrelated changes.

Use the repeated reproduce -> regression -> fix -> targeted retest -> full
required verification loop. Do not weaken tests or silently change scope.

For live acceptance, select the approved catalog models, upload non-sensitive
files in the browser, generate/publish/assign/decline exams, solve student-visible
questions without keys, save/exit/resume/submit, print permitted parts on A4 and
Letter, and compare independent answers, stored keys, and scores. Follow the
fixed acceptingUntil/creator-taking rule, not removed visibility settings.
Capture private, redacted evidence and report defects separately.

Do not make paid calls or deploy until the supplied run configuration explicitly
authorizes them. Respect shared app quotas/monthly estimate plus independent
harness job/money/time/iteration limits across resumes. Schedule model-matrix
evaluation across days or request an explicit isolated dev configuration.
Do not "fix" the documented soft running limit or stale-autosave gap by silently
changing product scope; report those limitations honestly.
If blocked, work on independent cases, then request the exact missing action.

Finish only with ALL_PASS for the named profile and valid evidence, or an honest
FAIL/INCOMPLETE report with remaining IDs, spending, resources, and next steps.
```

## 14. Implementation order and definition of done

Fit verification into the three-day implementation rather than adding a separate platform:

1. **Bootstrap:** case manifest, decision register, run/result schema, fixtures/reference facts, `make verify`, and a report that honestly lists missing layers.
2. **Documents/auth slice:** deterministic auth/ownership/upload tests and deployed cookie/S3 checks.
3. **Generation slice:** model catalog/factory, both SDK adapters, shared concurrency/limits/accounting tests; first live cheap-default PDF/photo diagnostic early; plan budgeted full enabled-model campaign before promising a release date.
4. **Exam slice:** all-role creation, role-capped assignments/decline, idempotent partial-publish recovery, fixed cutoff/visibility, half-mark representation, resume and paper-print journeys.
5. **Release:** real AWS concurrency, full teacher-to-student live path, every enabled model's reviewed golden set, print/device/config/spend-risk checks, final aggregation.
6. **Optional P1:** usage dashboard before peeks; run full conditional privacy/polling or peek/penalty suites when enabled. Core counters/fields remain tested if their UI is deferred.

Do not postpone security, correctness, or the first real provider call until the final day. If time runs out, report incomplete or cut scope through the owner; do not turn required tests into optional tests.

### Harness definition of done

- [ ] All required cases mapped to source requirements with executable child IDs.
- [ ] Decision register reviewed; required policies resolved in owning specs.
- [ ] Commands implemented, documented, and nonzero on missing/failed work.
- [ ] No-network deterministic tests and actual SDK adapter tests run.
- [ ] Both providers' per-model requests, default/override selection, and shared validators covered.
- [ ] AWS integration and browser environments reset safely and repeatably.
- [ ] Fixture permissions/hashes/reference solutions reviewed.
- [ ] Paid jobs explicitly authorized and budgeted across retries/resumes.
- [ ] Independent solver protocol and question review evidence implemented.
- [ ] Live upload -> generate -> publish -> solve -> resume -> submit -> verify path works.
- [ ] All-role creation, assignment caps, decline and idempotent publish tested.
- [ ] Fixed acceptingUntil/creator-taking key matrix tested; removed visibility modes no longer required.
- [ ] Print output inspected on A4/Letter, including hidden keys and large exams.
- [ ] Report distinguishes semantic-key defects from mechanical grading defects.
- [ ] Required injection/unreadability/refusal/cost/liveness cases covered.
- [ ] Soft running-limit and stale-autosave limitations disclosed, not silently converted into stronger promises.
- [ ] Atomic daily quotas, shared month estimate, provider/total-spend risks, and usage counters tested.
- [ ] Every selectable model has valid required quality evidence and campaign fits approved quotas/budget.
- [ ] Privacy, artifact redaction/retention, cleanup, and production boundaries enforced.
- [ ] Loop can resume without losing failure history or spending accounting.
- [ ] Aggregator rejects missing/removed/skipped cases and stale evidence.
- [ ] A final `ALL_PASS (release)` is based on observed required results, not this checklist alone.
