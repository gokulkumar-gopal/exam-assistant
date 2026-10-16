# Deployment Implementation Plan

> For agentic workers: REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Stand up the `dev` and `prod` stacks (AWS SAM) and the Vercel project so the Exam Assistant frontend and Go backend run end to end behind the `/api/*` rewrite, with spend guards in place before prod.

**Architecture:** One SAM template builds two Lambda functions (API with a Function URL, worker with async invoke) from one Go binary, five on-demand DynamoDB tables, and one private S3 bucket per stage. Vercel hosts the Next.js app and proxies `/api/*` to the Function URL. Secrets travel only through gitignored per-stage env files and a generated, gitignored samconfig.

**Tech Stack:** AWS SAM, CloudFormation, Lambda (`provided.al2023`, arm64), DynamoDB, S3, CloudWatch Logs, Vercel, Google Cloud Console (OAuth), Make, sh, curl, jq, openssl.

**Spec:** deployment-spec.md (primary), backend-spec.md sections 5, 11, 12, AI-spec.md section 12, frontend-spec.md section 8.1.

**Pacing:** Organised by milestones, not days. goals.md's "3 days" is guidance only; the owner may finish in 2. Each milestone ends with a check. Deploy `dev` after every backend milestone.

> **Unvalidated artifacts.** The reference SAM template (deployment-spec 6.2) and `scripts/write-samconfig.sh` (8.2) were written without `sam` installed and have never been run. Task D-0.3 must run `sam validate --lint`, and D-0.4 must run `sam deploy --debug` and confirm parameter values arrive intact. Fix the spec and the file together if either fails.

## Global Constraints

- One region for everything; default `us-east-1` (dep 1).
- Stack `exam-assistant-<stage>`; functions `exam-assistant-<stage>-api` / `-worker`; tables `<stage>-users|documents|exams|attempts|jobs`; bucket `exam-assistant-<stage>-<account-id>` (dep 2).
- Stages `dev` and `prod`, same template. No custom CI; backend deploy is manual `make deploy` (dep 1).
- Lambda: `provided.al2023`, `arm64`, handler `bootstrap`, 1024 MB; API timeout 29 s; worker timeout 600 s; worker `MaximumRetryAttempts: 0`, `MaximumEventAgeInSeconds: 3600` (dep 6.2).
- All tables and indexes `PAY_PER_REQUEST`; index projections `INCLUDE`, never `ALL`; indexes: exams `creator-index`, attempts `user-index`, jobs `owner-index` and `day-index` (dep 6.2).
- No VPC, NAT gateway, API Gateway, Cognito, queue, RDS (dep 4, 6.2).
- `ENV=production` on both stages; `ENV=local` only for a locally run server (dep 6.2).
- Secrets scoped per function: API gets `JWT_SECRET`, `GOOGLE_CLIENT_ID`, `ALLOWED_ORIGINS`; worker gets `LLM_API_KEY`, `GEMINI_API_KEY`; `LLM_PROVIDERS` and `LLM_MODEL` (empty = cheapest enabled) on both (dep 6.2).
- Secrets never in Git, never on the sam command line; flow is `.env.deploy.<stage>` -> `write-samconfig.sh` -> `.samconfig.<stage>.toml` (mode 600) (dep 5, 8.2).
- Hard gate: provider-side monthly spend limit of $20 on the key of every provider in `LLM_PROVIDERS` before prod deploy; Gemini key on its own billing-enabled paid-tier project, never free tier (dep 3, 8.4).
- App-side budget `LLM_MONTHLY_BUDGET_USD` default 15; backend defaults: `GEN_MAX_PER_DAY` 3, `GEN_MAX_REGEN_PER_DAY` 10, `GEN_MAX_GLOBAL_PER_DAY` 10, `GEN_MAX_PDF_PAGES` 40, `GEN_MAX_TOTAL_BYTES` 20971520 (backend 11, AI 12). These are code defaults, not set in the template (dep 6.4).
- Presigned URLs last 10 minutes; S3 CORS methods GET, PUT, HEAD, headers `*`, expose `ETag`, origins from `FrontendOrigins` (backend 12).
- Session cookie: `HttpOnly`, `Secure`, `SameSite=Lax`, no `Domain` (dep 8.3).
- Logs: JSON to CloudWatch, 14-day retention (dep 6.2, 10).
- Vercel: Root Directory `frontend`; `API_URL` has no trailing slash and no `NEXT_PUBLIC_` prefix; `NEXT_PUBLIC_GOOGLE_CLIENT_ID` equals the backend stage's `GOOGLE_CLIENT_ID`; redeploy after changing either (dep 7.2).
- Prod stack has termination protection on (dep 8.4).
- Never read, edit, mention or commit `verification-spec.md`.

## Review Focus

Failure modes the specs imply that no single task's checks would otherwise catch. Each has a check in the owning task.

1. **GSI creation order.** CloudFormation allows only one GSI create or delete per update on an existing table. Add all four indexes when the tables are first created, not in later milestones. Check: D-1.1 creates every table and index in one deploy.
2. **Quoting in `write-samconfig.sh`.** Comma-containing values (`gemini,anthropic`, origin lists) can split into extra parameters. Check: D-0.4 deploys with `--debug` and compares the stack's parameters with the env file; includes a two-origin case.
3. **Missing enabled-provider key.** Check: D-2.2 runs `write-samconfig.sh` with `LLM_PROVIDERS=gemini,anthropic` and a missing key and expects a non-zero exit with a named message.
4. **Public Function URL returns 403.** Newer AWS rules may need an extra resource-policy permission. Check: D-0.5 curls the URL and, on 403, inspects the policy (dep 6.2 note, 13).
5. **Duplicate worker runs / dropped invokes.** Check: D-2.1 reads the live `EventInvokeConfig`; D-2.4 confirms one worker invocation per job in the logs.

## File Structure

All paths relative to the repo root `exam-assistant/`. Application code belongs to the other plans; this plan owns these.

- Create `.gitignore`: ignores `.env*`, `backend/.aws-sam/`, `frontend/.next/`, `node_modules/`, `backend/.env.deploy.*`, `backend/.samconfig.*.toml`.
- Create `backend/template.yaml`: the SAM template, built up in slices (M0 to M2).
- Create `backend/samconfig.toml`: non-secret deploy settings only.
- Create `backend/scripts/write-samconfig.sh`: writes `.samconfig.<stage>.toml` from exported variables.
- Create `backend/Makefile`: targets `build-ApiFunction`, `build-WorkerFunction`, `check-env`, `deploy`, `destroy`, plus `test`, `run`, `smoke` (bodies owned by the backend plan, deploy-related parts owned here).
- Create (untracked, local only) `backend/.env.deploy.dev`, `backend/.env.deploy.prod`, `frontend/.env.local`.
- Modify `frontend/next.config.js` only if the rewrite is missing (owned by the frontend plan; verified here).

---

## M0 Skeletons

### Task D-0.1: Prerequisites and budget alert
**Spec:** dep 3, 4, 8.1 (steps 1, 2, 4), 11 (budget alert), 14   **Needs:** none
**Files:** none (local machine and consoles)
**Interfaces:** Produces the account, region, identity and tools every later task relies on.
- [ ] Verify tools: `go version` (>= 1.24), `aws --version` (v2), `sam --version`, `node --version`, `make`, `curl`, `jq`, `openssl` all print a version.
- [ ] Verify identity: `aws sts get-caller-identity` prints the intended account and an ARN that is not `root`. MFA is on for root and your identity.
- [ ] Pick the region and record it; `aws configure get region` matches. Check the Billing console for free-plan vs 12-month program and note the account's end date (dep 4, 14).
- [ ] Create the AWS budget with an email alert at about $5 (dep 11).
- [ ] Check Service Quotas -> Lambda concurrent executions; note the value (decides D-2.3).
- [ ] Create or confirm accounts: Vercel (check Hobby terms), GitHub repo connected to Vercel, Google Cloud, Anthropic console, and a Google AI Studio / Cloud project for Gemini (billing enabled, paid tier).
- [ ] Verify: all of the above ticked; nothing committed.

### Task D-0.2: Repo layout, `.gitignore`, Makefile skeleton
**Spec:** dep 5, 6.1, 8.2   **Needs:** D-0.1
**Files:** Create `.gitignore`, `backend/Makefile`, `backend/samconfig.toml`
**Interfaces:** Produces make targets `build-ApiFunction`, `build-WorkerFunction` (identical bodies: `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -tags lambda.norpc -o $(ARTIFACTS_DIR)/bootstrap ./cmd/lambda`), `check-env`, `deploy`, `destroy`, variables `STAGE ?= dev`, `AWS_REGION ?= us-east-1`, and `-include .env.deploy.$(STAGE)` followed by `export`.
- [ ] Write `.gitignore` with exactly the entries in File Structure.
- [ ] Verify: `git check-ignore backend/.env.deploy.dev backend/.samconfig.dev.toml backend/.aws-sam frontend/.next frontend/.env.local` prints all five paths.
- [ ] Write the Makefile targets from dep 6.1 and 8.2. `destroy` runs, in order: `aws s3 rm s3://exam-assistant-$(STAGE)-<account-id> --recursive`, then `sam delete --stack-name exam-assistant-$(STAGE) --region $(AWS_REGION)`; it refuses `STAGE=prod` unless `CONFIRM=yes` is passed.
- [ ] Verify: `make -n deploy STAGE=dev` with no env file prints the `check-env` message `Missing backend/.env.deploy.dev. See deployment-spec.md section 8.1.` and exits 1; `make -n test` does not fail on a fresh clone (the `-include` works).
- [ ] Write `samconfig.toml` with non-secret settings only. Verify: `grep -iE 'secret|key|password' backend/samconfig.toml` prints nothing.
- [ ] Commit: "deploy: gitignore, Makefile skeleton".

### Task D-0.3: Health-only SAM template
**Spec:** dep 6.2, 8.3 step 1, backend 12 (runtime)   **Needs:** D-0.2; backend B-0 complete (a `cmd/lambda` that answers `GET /health`)
**Files:** Create `backend/template.yaml`
**Interfaces:** Produces parameters `Stage`, `JwtSecret`, `LlmProviders`, `GoogleClientId`, `AllowedOrigins`, `FrontendOrigins`; resources `ApiFunction` (with `FunctionUrlConfig: { AuthType: NONE }`) and `ApiLogs` (14 days); output `FunctionUrl`. No tables, bucket, worker or IAM policies yet.
- [ ] Copy the Parameters, Conditions, Globals, `ApiFunction` (without Policies and without table/bucket references), `ApiLogs` and Outputs blocks from dep 6.2. Remove `S3_BUCKET` from Globals for now; it returns in D-1.2.
- [ ] Run `cd backend && sam validate --lint`. Expected: passes. Fix every reported problem in the template and, if the spec's reference differs, update deployment-spec 6.2 in the same commit.
- [ ] Run `sam build`. Expected: succeeds and `.aws-sam/build/ApiFunction/bootstrap` exists (`file` shows ARM aarch64).
- [ ] Commit: "deploy: health-only SAM template".

### Task D-0.4: `write-samconfig.sh` and `make deploy` dry run
**Spec:** dep 8.1 step 3, 8.2, Review Focus 2   **Needs:** D-0.3
**Files:** Create `backend/scripts/write-samconfig.sh` (mode 755), `backend/.env.deploy.dev` (untracked)
**Interfaces:** Produces `.samconfig.<stage>.toml` (mode 600) with `parameter_overrides` for `Stage`, `JwtSecret`, `LlmProviders`, `GoogleClientId`, `AllowedOrigins`, `FrontendOrigins`, and, only when set, `LlmApiKey`, `GeminiApiKey`, `LlmModel`, `ApiReservedConcurrency`, `WorkerReservedConcurrency`. Exit non-zero with `<VAR> is required when <provider> is enabled` when an enabled provider has no key.
- [ ] Write the script from dep 8.2.
- [ ] Create `backend/.env.deploy.dev` per dep 8.1: fresh `JWT_SECRET=$(openssl rand -hex 32)`, `LLM_PROVIDERS=anthropic`, empty `LLM_MODEL`, `ALLOWED_ORIGINS=http://localhost:3000,http://localhost:3001` (two origins on purpose to test quoting), `GOOGLE_CLIENT_ID=placeholder` until D-0.6.
- [ ] Run `make -C backend check-env STAGE=dev` then `sh scripts/write-samconfig.sh dev` with the env exported. Expected: `.samconfig.dev.toml` exists; `stat -f %Lp` (macOS) prints `600`; `grep -c 'LlmApiKey' .samconfig.dev.toml` is `0` when `LLM_API_KEY` is unset.
- [ ] Run `sam deploy --config-file .samconfig.dev.toml --stack-name exam-assistant-dev --region $AWS_REGION --capabilities CAPABILITY_IAM --resolve-s3 --no-confirm-changeset --debug 2>&1 | grep -i 'parameter'`. Expected: `AllowedOrigins` and `FrontendOrigins` each show both origins as one value, `LlmProviders` shows `anthropic`. If origins split, fix the escaped quotes in the script and re-run.
- [ ] Confirm secrecy: `ps` during deploy and the deploy output do not contain the JWT secret; `git status` does not list the toml or env file.
- [ ] Commit: "deploy: samconfig generator".

### Task D-0.5: Dev stack health round trip
**Spec:** dep 8.2, 8.3 steps 1-2, 13 (403 row)   **Needs:** D-0.4
**Files:** none
**Interfaces:** Produces the dev `FunctionUrl` (call it `$DEV_URL`, no trailing slash).
- [ ] Run `cd backend && make deploy STAGE=dev`. Expected: stack `CREATE_COMPLETE`; the Outputs table prints `FunctionUrl`.
- [ ] Run `curl -s $DEV_URL/health`. Expected: `{"status":"ok"}`. On `403`, inspect the function's resource policy for the public-URL permissions, add what AWS now requires to the template, update dep 6.2/13, redeploy.
- [ ] Run `aws logs tail /aws/lambda/exam-assistant-dev-api --since 5m`. Expected: JSON log lines for the request; group retention shows 14 days (`aws logs describe-log-groups --log-group-name-prefix /aws/lambda/exam-assistant-dev-api --query 'logGroups[0].retentionInDays'` prints `14`).
- [ ] Commit any template fixes: "deploy: dev stack live".

### Task D-0.6: Google dev OAuth client and Vercel project
**Spec:** dep 7.1-7.3, 7.6, 8.0, 8.3 steps 3-4   **Needs:** D-0.5; frontend F-0 complete (skeleton pushed with the `/api` rewrite in `frontend/next.config.js`)
**Files:** Modify (untracked) `backend/.env.deploy.dev`, `frontend/.env.local`
**Interfaces:** Produces the dev Google client ID, the Vercel project `exam-assistant`, and the Vercel production domain (call it `$VERCEL_URL`).
- [ ] Google Cloud: create the project, set the consent screen (External, scopes `openid email profile`, test users = yourself), create Web client `exam-assistant-dev` with authorized JavaScript origin `http://localhost:3000`, no redirect URIs (dep 8.0). Copy the client ID.
- [ ] Put the dev client ID in `backend/.env.deploy.dev` (`GOOGLE_CLIENT_ID`), `frontend/.env.local` (`NEXT_PUBLIC_GOOGLE_CLIENT_ID`, and `API_URL=$DEV_URL`). Redeploy: `make deploy STAGE=dev`. Expected: stack `UPDATE_COMPLETE`, `GoogleClientId` parameter shows the new value.
- [ ] Vercel: import the repo, preset Next.js, Root Directory `frontend`. Set `API_URL=$DEV_URL` (no trailing slash) and `NEXT_PUBLIC_GOOGLE_CLIENT_ID` for Production, Preview and Development. Deploy.
- [ ] Verify: `curl -s https://$VERCEL_URL/api/health` returns `{"status":"ok"}`. If it returns 404 or HTML, check `API_URL`, the rewrite and that Vercel was redeployed (dep 13).
- [ ] Verify headers: `curl -si https://$VERCEL_URL/api/health | grep -i cache-control` shows `no-store`.
- [ ] Add `https://$VERCEL_URL` to `ALLOWED_ORIGINS` and to the dev stack (`.env.deploy.dev`, redeploy) so a browser on the Vercel domain can call the dev backend; add it to the dev client's origins as well (so Google sign-in is testable on the Vercel domain; dep 8.3 step 6).

### Milestone check M0
- [ ] `sam validate --lint` passes; `make deploy STAGE=dev` succeeds.
- [ ] `curl -s $DEV_URL/health` and `curl -s https://$VERCEL_URL/api/health` both return `{"status":"ok"}`.
- [ ] Once login exists (B-1 / F-1): sign up in the browser; devtools shows the `session` cookie with `HttpOnly`, `Secure`, `SameSite=Lax`, no `Domain`, and it survives a refresh (dep 8.3 step 5). Not blocking M0 if login is not built yet; run it at the end of M1.

---

## M1 Documents

### Task D-1.1: Five tables and four indexes
**Spec:** dep 6.2 (data layer), 6.2 notes, 10 (on-demand); backend 12 (DynamoDB), Review Focus 1   **Needs:** D-0.5; backend B-1 store code expects these exact key and attribute names
**Files:** Modify `backend/template.yaml`
**Interfaces:** Produces `UsersTable`, `DocumentsTable` (ttl), `ExamsTable` (`creator-index`), `AttemptsTable` (`user-index`), `JobsTable` (ttl, `owner-index`, `day-index`), all `PAY_PER_REQUEST`.
- [ ] Add the five tables exactly as in dep 6.2, **including all four indexes now** (one-index-per-update limit).
- [ ] Run `sam validate --lint`. Expected: passes.
- [ ] Run `make deploy STAGE=dev`. Expected: `UPDATE_COMPLETE`.
- [ ] Verify: `aws dynamodb list-tables` shows `dev-users|documents|exams|attempts|jobs`; for each, `aws dynamodb describe-table --table-name dev-<t> --query 'Table.BillingModeSummary.BillingMode'` prints `PAY_PER_REQUEST`; `aws dynamodb describe-table --table-name dev-jobs --query 'Table.GlobalSecondaryIndexes[].[IndexName,Projection.ProjectionType]'` lists `owner-index` and `day-index`, both `INCLUDE`; same check on exams and attempts finds no `ALL`.
- [ ] Verify the NonKeyAttributes match what the store writes: compare each INCLUDE list with the attribute names in backend `internal/store` (backend-spec 6.2); fix whichever is wrong.
- [ ] Commit: "deploy: tables and indexes".

### Task D-1.2: Bucket, CORS and API function permissions
**Spec:** dep 6.2 (bucket, ApiFunction Policies), 6.2 notes, 8.3 step 7; backend 12 (S3, permissions)   **Needs:** D-1.1
**Files:** Modify `backend/template.yaml`
**Interfaces:** Produces `UploadsBucket`; `S3_BUCKET` in Globals; API Policies for the five tables and `/index/*`, S3 `PutObject|GetObject|DeleteObject` on `/*`, `s3:ListBucket` on the bucket; Outputs `BucketName`.
- [ ] Add `UploadsBucket` (public access block all four true, AES256, CORS from `FrontendOrigins`: GET, PUT, HEAD, `*`, expose `ETag`, MaxAge 3600), restore `S3_BUCKET: !Ref UploadsBucket` in Globals, and add the API Policies block from dep 6.2 minus the worker invoke statement (that arrives in D-2.1).
- [ ] `sam validate --lint`, then `make deploy STAGE=dev`. Expected: `UPDATE_COMPLETE`.
- [ ] Verify bucket: `aws s3api get-public-access-block --bucket exam-assistant-dev-<account-id>` shows all four `true`; `aws s3api get-bucket-cors --bucket ...` lists both origins and `ETag`.
- [ ] Verify no extra rights: `aws lambda get-policy`/role inspection shows no `*` resource and no `dynamodb:Scan` or `BatchWriteItem`.
- [ ] Commit: "deploy: bucket, CORS, API policies".

### Task D-1.3: Upload, viewer, sign-in checks on the deployed chain
**Spec:** dep 8.3 steps 5-7, 10 (upload, viewer, bucket, Google items), 13   **Needs:** D-1.2; backend B-1 and frontend F-1 complete on dev stack
**Files:** none
- [ ] Deploy the backend to dev (`make deploy STAGE=dev`) and let Vercel build the frontend.
- [ ] Email sign-up on `https://$VERCEL_URL`; refresh stays signed in; cookie flags as in M0 check.
- [ ] Google sign-in on `http://localhost:3000` (against `$DEV_URL`) and on `https://$VERCEL_URL` with a listed test user. Expected: succeeds; a 401 means the backend `GOOGLE_CLIENT_ID` differs from `NEXT_PUBLIC_GOOGLE_CLIENT_ID` (dep 13).
- [ ] Upload a small file, then a ~20 MB PDF. Expected: devtools shows the PUT going to `*.s3.*.amazonaws.com`, progress shown, no CORS error. `SignatureDoesNotMatch` means headers differ from the signed ones (dep 13).
- [ ] Open the file in the viewer; copy its URL; after about 10 minutes it returns an access error.
- [ ] `curl -I https://exam-assistant-dev-<account-id>.s3.<region>.amazonaws.com/users/x` returns 403 (no public read).
- [ ] If any step needed a template or spec correction, commit it: "deploy: fixes from M1 verification".

### Milestone check M1
- [ ] Items in D-1.3 all pass.
- [ ] Section 10 items now true: tables on-demand, no `ALL` projection, bucket private, 20 MB upload direct to S3, viewer URL expires, session cookie flags, Google sign-in on localhost and Vercel domain.

---

## M2 Generation

### Task D-2.1: Worker function, async config, IAM and provider scoping
**Spec:** dep 6.2 (WorkerFunction, EventInvokeConfig, Policies), 6.2 notes (retries, secrets), 6.3; backend 11, 12; AI-spec 12   **Needs:** D-1.2; backend B-2 worker entry point (`MODE=worker`) built
**Files:** Modify `backend/template.yaml`
**Interfaces:** Produces `WorkerFunction` (`MODE=worker`, `LLM_API_KEY`, `GEMINI_API_KEY`, `EventInvokeConfig` retries 0, age 3600, Timeout 600), `WorkerLogs` (14 days), parameters `LlmApiKey`, `GeminiApiKey`, `LlmModel`; API gets `WORKER_FUNCTION_NAME` and `lambda:InvokeFunction` on the worker only.
- [ ] Add parameters `LlmApiKey`, `GeminiApiKey` (both `NoEcho`, default empty), `LlmModel` (default empty), the worker resource, `WorkerLogs`, the API's `WORKER_FUNCTION_NAME` and invoke statement, and `LLM_PROVIDERS`/`LLM_MODEL` in Globals, all as in dep 6.2. Worker policies: `dynamodb:GetItem` on documents; `GetItem|PutItem|UpdateItem|Query` on jobs and `jobs/index/*`; `s3:GetObject` on `/*`.
- [ ] `sam validate --lint`; deploy dev.
- [ ] Verify scoping with `aws lambda get-function-configuration --function-name exam-assistant-dev-api --query 'Environment.Variables|keys(@)'`: contains `JWT_SECRET`, `GOOGLE_CLIENT_ID`, `ALLOWED_ORIGINS`, `WORKER_FUNCTION_NAME`, `LLM_PROVIDERS`, `LLM_MODEL`; does **not** contain `LLM_API_KEY` or `GEMINI_API_KEY`. Same command on `-worker`: contains `LLM_API_KEY`, `GEMINI_API_KEY`, `LLM_PROVIDERS`, `LLM_MODEL`; does **not** contain `JWT_SECRET` or `GOOGLE_CLIENT_ID`.
- [ ] Verify `aws lambda get-function-event-invoke-config --function-name exam-assistant-dev-worker` prints `MaximumRetryAttempts: 0`, `MaximumEventAgeInSeconds: 3600`.
- [ ] Commit: "deploy: worker function and scoping".

### Task D-2.2: Provider keys, spend limits (dev), key-missing guard
**Spec:** dep 3, 6.3, 8.1 steps 3 and 5, 8.2 (script behaviour), 14 (Gemini project), Review Focus 3   **Needs:** D-2.1
**Files:** Modify (untracked) `backend/.env.deploy.dev`
- [ ] Anthropic console: create a dev key with a low monthly spend limit. Gemini: create a **billing-enabled, paid-tier** project and key for dev with a small quota and a budget alert (note that Google may only alert). Do not use a free-tier key.
- [ ] Verify the model IDs and prices for the four catalog models against the providers' own pages (dep 14; AI-spec 3.2) and record any difference as a note for the backend plan's catalog task.
- [ ] Guard test: with `LLM_PROVIDERS=gemini,anthropic` and `GEMINI_API_KEY` unset, run `sh scripts/write-samconfig.sh dev`. Expected: non-zero exit, message names `GEMINI_API_KEY`. Repeat with `LLM_API_KEY` unset. Expected: names `LLM_API_KEY`.
- [ ] Fill the dev env file (`LLM_PROVIDERS=gemini,anthropic`, both keys, `LLM_MODEL=` empty) and deploy dev.
- [ ] Verify (needs backend B-2 `/ai/models`): signed in, `curl` through `https://$VERCEL_URL/api/ai/models` (with the cookie) lists exactly the enabled models, cheapest first, `default: true` on Gemini 3.1 Flash-Lite. Then redeploy with `LLM_PROVIDERS=anthropic` and confirm Gemini disappears.

### Task D-2.3: Optional reserved concurrency
**Spec:** dep 6.2 notes (reserved concurrency), 6.3, 14   **Needs:** D-2.1, D-0.1 (quota value)
**Files:** Modify (untracked) `backend/.env.deploy.dev` and later `.prod`
- [ ] Decide using the quota recorded in D-0.1: if the account concurrency quota leaves at least 100 unreserved after reserving 20 + 5, set `API_RESERVED_CONCURRENCY=20` and `WORKER_RESERVED_CONCURRENCY=5`; otherwise leave unset and note "conscious choice: quota too low".
- [ ] If set: deploy dev, then `aws lambda get-function-concurrency --function-name exam-assistant-dev-api` prints `20` and the worker prints `5`. If unset: the same commands print an empty result. Both are acceptable; record the decision in the milestone notes.

### Task D-2.4: Job runs once; stale job reads failed
**Spec:** dep 10 (generation items), 13 (queued, runs twice rows); backend 7.2   **Needs:** D-2.2; backend B-2 and frontend F-2 complete on dev stack
**Files:** none
- [ ] Start one generation job from the deployed app. Expected: it finishes. `aws logs filter-log-events --log-group-name /aws/lambda/exam-assistant-dev-worker --filter-pattern '"jobId"'` shows exactly one `START` per job id (use `aws logs tail ... --since 15m | grep -c START`).
- [ ] Stall test: temporarily set the worker's reserved concurrency to 0 (`aws lambda put-function-concurrency --function-name exam-assistant-dev-worker --reserved-concurrent-executions 0`), start a job, then wait about 6 minutes. Expected: the poll reports `failed` and the UI offers retry. Restore with `aws lambda delete-function-concurrency ...` (or redeploy).
- [ ] Missing-key behaviour: redeploy dev with `LLM_PROVIDERS=gemini,anthropic` but an empty `LLM_API_KEY` is blocked by the script (D-2.2); instead check the worker log line for a provider with a bad key shows a loud error and the user sees only a generic message.

### Milestone check M2
- [ ] D-2.1 scoping and event-config checks pass.
- [ ] `GET /api/ai/models` lists exactly the enabled models, cheapest first, with the cheapest marked default.
- [ ] One job, one worker invocation; a stalled job fails after about 6 minutes.
- [ ] Reserved concurrency decision recorded.

---

## M3 Exams

### Task D-3.1: Redeploy and smoke against dev
**Spec:** dep 8.5, 10 (`make smoke`); backend 14   **Needs:** B-3 and F-3 complete on dev stack
**Files:** none
- [ ] `make deploy STAGE=dev`; Expected: `UPDATE_COMPLETE`, no 5xx in `aws logs tail /aws/lambda/exam-assistant-dev-api --since 10m --filter-pattern '"status":5'` (adjust to the log field name the backend uses).
- [ ] `make smoke BASE_URL=https://$VERCEL_URL/api` passes. Every POST succeeding proves the origin check lists the Vercel origin; an all-403 result means `ALLOWED_ORIGINS` is missing it (dep 13).
- [ ] Milestone check M3: assign an exam to a second account in the deployed app; it shows under Open for the assignee.

---

## M4 Taking and printing

### Task D-4.1: Redeploy and production-only behaviour check on dev
**Spec:** dep 6.2 notes (`ENV: production`), 7.3, 8.5; backend 9.5   **Needs:** B-4 and F-4 complete on dev stack
**Files:** none
- [ ] `make deploy STAGE=dev`; `make smoke BASE_URL=https://$VERCEL_URL/api` passes.
- [ ] On the Vercel domain, take a timed exam through submit; the cookie survives, no 5xx in the API log group.
- [ ] Open the print view (`/exams/[id]/print`) on the Vercel domain and print to PDF. It needs no backend endpoint and no deployment change; confirm the page loads for the creator and a 404/no-access for a stranger.
- [ ] Milestone check M4: a lazy-finalize case (leave an attempt open past `closesAt + 60 s`, then load the result) returns an auto-submitted result on the deployed stack.

---

## M5 Later priority (cut order: peeks first, then the usage page)

### Task D-5a.1: Usage page path on dev  *(P1, cut after peeks)*
**Spec:** dep 6.2 (`day-index`), 10 (`day-index` item); backend 8.6   **Needs:** B-5a and F-5a complete on dev stack
**Files:** none
- [ ] Redeploy dev. Run one job, then load `/usage` on the Vercel domain. Expected: month and today totals update within 5 s; `recent` lists the job.
- [ ] Confirm `day-index` is used with no Scan: the API role has no `dynamodb:Scan` (`aws iam simulate-principal-policy` or role policy review) and the page still loads.
- [ ] Milestone check M5a: the page's spend figure equals the sum of `costUsd` of the jobs listed.

### Task D-5b.1: Peeks deploy check  *(P1, first to cut)*
**Spec:** backend 9.6   **Needs:** B-5b and F-5b complete on dev stack
**Files:** none
- [ ] Redeploy dev and run `make smoke BASE_URL=https://$VERCEL_URL/api`. No template change is needed (no new tables, indexes or permissions); if one is found to be needed, note it as a spec mismatch.
- [ ] Milestone check M5b: a reveal on the deployed stack returns the key for that question only, and the attempt shows `peeksLeft` decreasing.

---

## PROD Production deploy

### Task D-6.1: Spend-limit gate (blocking)
**Spec:** dep 3, 8.1 step 5, 8.4 gate, 10 (Gate item), 12   **Needs:** M2 complete
**Files:** none
- [ ] For **each** provider in the prod `LLM_PROVIDERS` list, open its console (not memory): Anthropic shows a $20 monthly limit on the prod key. Gemini key is on its own billing-enabled paid-tier project with a small quota and a budget alert at or below $20; write down that Google may only alert and the app's `LLM_MONTHLY_BUDGET_USD` is the real cap.
- [ ] Confirm the AWS budget alert from D-0.1 exists.
- [ ] Gate passes only when every box is ticked. Do not start D-6.3 otherwise.

### Task D-6.2: Prod Google client and prod env file
**Spec:** dep 6.3, 8.0 (steps 3-7), 8.1, 14 (consent screen)   **Needs:** D-6.1; Vercel production domain known
**Files:** Create (untracked) `backend/.env.deploy.prod`
- [ ] Create Web client `exam-assistant-prod` with authorized origin `https://$VERCEL_URL` (and any custom domain); decide Testing with a test-user list vs publish to Production (dep 14).
- [ ] Write `backend/.env.deploy.prod` from dep 8.1: new `JWT_SECRET` (`openssl rand -hex 32`, different from dev), prod keys, `LLM_PROVIDERS`, empty `LLM_MODEL`, `ALLOWED_ORIGINS=https://$VERCEL_URL`, prod `GOOGLE_CLIENT_ID`, optional concurrency values per D-2.3.
- [ ] Verify: `grep -c "$(grep JWT_SECRET backend/.env.deploy.dev | cut -d= -f2)" backend/.env.deploy.prod` prints `0` (secret differs); `git status` shows the file untracked and ignored.

### Task D-6.3: Prod stack
**Spec:** dep 8.4 steps 1-3   **Needs:** D-6.1, D-6.2
**Files:** none
- [ ] `cd backend && make deploy STAGE=prod`. Expected: `CREATE_COMPLETE`; `FunctionUrl` printed (call it `$PROD_URL`).
- [ ] `curl -s $PROD_URL/health` returns `{"status":"ok"}`.
- [ ] `aws cloudformation update-termination-protection --enable-termination-protection --stack-name exam-assistant-prod --region $AWS_REGION`; verify `aws cloudformation describe-stacks --stack-name exam-assistant-prod --query 'Stacks[0].EnableTerminationProtection'` prints `true`.
- [ ] Re-run the D-2.1 scoping checks and D-1.1 on-demand/projection checks against `prod-*` and `exam-assistant-prod-*`.

### Task D-6.4: Vercel production environment
**Spec:** dep 7.2, 8.4 step 4   **Needs:** D-6.3
**Files:** none
- [ ] In Vercel set Production `API_URL=$PROD_URL` (no trailing slash) and `NEXT_PUBLIC_GOOGLE_CLIENT_ID` to the prod client; keep Preview and Development on the dev values.
- [ ] Redeploy production. Verify `curl -s https://$VERCEL_URL/api/health` returns `{"status":"ok"}` from the **prod** function (check that the prod API log group received the request).
- [ ] Verify the prod client ID equals the prod stack's `GOOGLE_CLIENT_ID` and differs from dev's.

### Task D-6.5: Smoke and device walk-through
**Spec:** dep 8.4 steps 5-6, 10   **Needs:** D-6.4; B-4 complete (all P0 milestones deployed to prod)
**Files:** none
- [ ] `make smoke BASE_URL=https://$VERCEL_URL/api` passes against prod.
- [ ] Walk the full flow on a phone and a desktop: sign up, Google sign-in, upload, generate (default cheapest model), build, assign, take, submit, result, print. Note any production-only failure (cookies, presigned URLs, CORS, timeouts) and fix with a redeploy.

### Task D-6.6: Verification checklist
**Spec:** dep 10   **Needs:** D-6.5
**Files:** none
- [ ] Tick every item of deployment-spec section 10 against prod, including: `sam validate --lint`, cookie flags, 20 MB upload direct to S3, viewer URL expiry, bucket public block, job runs once, stalled job fails at about 6 minutes, per-function env scoping, `/ai/models` list, `owner-index` and `day-index` INCLUDE, key-missing deploy stops, reserved concurrency decision, smoke passes, termination protection on, the gate, no secret in Git history (`git log -p | grep -E 'AIza|sk-ant|JWT_SECRET='` prints nothing) or in `NEXT_PUBLIC_` variables, 14-day log retention, no secret in process list.
- [ ] Milestone check PROD: all items ticked; the two spec-section-10 items that depend on unbuilt P1 features (`day-index` usage page, if M5a was cut) are marked "not applicable: cut".

---

## FIX Test and fix

### Task D-7.1: Budget alert and log review
**Spec:** dep 11 (budget alert, logs, monitoring)   **Needs:** PROD
**Files:** none
- [ ] Confirm the AWS budget alert email arrives (send a test notification or lower the threshold briefly).
- [ ] `sam logs --stack-name exam-assistant-prod --name ApiFunction --tail` and `--name WorkerFunction`; search the last day for 5xx and for worker errors. Expected: none unexplained.

### Task D-7.2: Golden-set cost check
**Spec:** dep 9 (day 3), 11; AI-spec 13   **Needs:** PROD and the backend golden-set test pass
**Files:** none
- [ ] After the first golden-set run on the cheapest enabled model, compare each provider's usage page with the in-app AI usage figures (or the per-job usage on job items if M5a was cut). Expected: within the same order of magnitude of the $0.03 / $0.10 per job estimates (Gemini) or $0.08 / $0.20 (Haiku). Note any gap beyond about 2x as a backend price-table bug.
- [ ] If quality on the cheap model is not good enough, raise the default per stage by setting `LLM_MODEL` in the env file and redeploying, or choose the model per job in the app. Record the new estimated monthly job count (about 450 / 140 on Gemini, 190 / 75 Haiku, 85 / 35 Sonnet, 45 / 19 Opus at the $15 budget).

### Task D-7.3: Redeploy loop
**Spec:** dep 8.5   **Needs:** PROD
**Files:** none
- [ ] For each fix: `make deploy STAGE=dev`, smoke, then `make deploy STAGE=prod`, `make smoke BASE_URL=https://$VERCEL_URL/api`. Frontend fixes ship by pushing to `main`. Changing an origin follows dep 8.5 (env files, redeploy, Google client origins, Vercel only if `API_URL` or client ID changed). Changing `JWT_SECRET` signs everyone out.

### Task D-7.4: Rollback and teardown rehearsal
**Spec:** dep 11 (rollback, teardown)   **Needs:** D-7.3
**Files:** none
- [ ] Rollback rehearsal on dev: check out the previous commit, `make deploy STAGE=dev`, confirm `/health`; for the frontend, promote a previous Vercel deployment and promote back.
- [ ] Teardown rehearsal on a throwaway stage name is not possible (`Stage` allows only `dev` and `prod`); instead run `make -n destroy STAGE=dev` and read the plan: empties the bucket first, then `sam delete`. Note the manual steps: turn off termination protection on prod first, revoke all provider keys, confirm no tables, buckets or log groups remain.
- [ ] Milestone check FIX: the golden-set cost comparison is recorded, the budget alert fires, logs are clean, rollback was exercised on dev.

---

## Spec coverage

| Spec section | Tasks |
|---|---|
| dep 1-2 decisions, naming | D-0.2, D-0.3, Global Constraints |
| dep 3 accounts, tools | D-0.1, D-2.2, D-6.1 |
| dep 4 free tier, cost guards | D-0.1, D-2.2, D-6.1, D-7.2 |
| dep 5 repo layout, secrets | D-0.2, D-0.4 |
| dep 6.1 build | D-0.2, D-0.3 |
| dep 6.2 template | D-0.3, D-1.1, D-1.2, D-2.1 |
| dep 6.3 parameters | D-2.2, D-2.3, D-6.2 |
| dep 6.4 optional overrides | not set (code defaults); no task needed |
| dep 7 Vercel | D-0.6, D-6.4 |
| dep 8.0 Google | D-0.6, D-1.3, D-6.2 |
| dep 8.1 AWS setup | D-0.1, D-0.4, D-2.2, D-6.2 |
| dep 8.2 `make deploy`, script | D-0.2, D-0.4, D-0.5 |
| dep 8.3 first hour | D-0.5, D-0.6, D-1.3 |
| dep 8.4 production | D-6.1, D-6.3, D-6.4, D-6.5 |
| dep 8.5 updating | D-3.1, D-7.3 |
| dep 9 day mapping | milestones (replaced by milestones) |
| dep 10 checklist | M-checks, D-6.6 |
| dep 11 operations | D-0.1, D-7.1, D-7.4 |
| dep 12 security | D-1.2, D-2.1, D-6.3, D-6.6 |
| dep 13 troubleshooting | referenced inline in D-0.5, D-0.6, D-1.3, D-3.1 |
| dep 14 open items | Open questions below |
| backend 5, 11, 12 | D-0.3, D-1.1, D-1.2, D-2.1 |
| AI-spec 12 | D-2.1, D-2.2 |

27 tasks in total.

## Open questions

- Region, free-tier program and account end date, custom domain, preview support, production branch name, Google consent-screen mode, one vs two Google clients, whether to keep `dev` (dep 14). The plan assumes `us-east-1`, the default `vercel.app` domain, `main`, two clients, a dev stack kept, previews visual-only.
- **Defaults conflict.** backend-spec 11 and AI-spec 12 say `GEN_MAX_PER_DAY` 3, `GEN_MAX_REGEN_PER_DAY` 10, `GEN_MAX_GLOBAL_PER_DAY` 10, `GEN_MAX_PDF_PAGES` 40; the earlier decision record said 10 / 30 / 200 / 100. The plan follows the specs as written; confirm which is intended.
- deployment-spec 6.4 lists `GEN_MAX_PER_DAY` etc. as "API" only; the worker may also need `GEN_MAX_*` caps and `LLM_MONTHLY_BUDGET_USD` (listed as api and worker). Not set in the template either way; fine while defaults apply, but any override must go on both functions.
- `write-samconfig.sh` writes `LlmProviders` and the origin lists with escaped quotes; the exact TOML escaping is unproven (D-0.4 settles it).
- Gemini cannot hard-cap spend (alert only): the plan relies on the app budget; decide whether that is acceptable for prod.
- Teardown cannot be rehearsed on a separate stack because `Stage` is limited to `dev` and `prod` (D-7.4 dry-reads `make destroy`).
- Model IDs, prices, the Gemini 2.5 shutdown date, and the Google public-URL permission requirement are marked (verify) in the specs and checked in D-2.2 and D-0.5.
