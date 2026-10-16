# Exam Assistant: Deployment Spec

Audience: whoever (person or coding agent) sets up and ships the app. Read this whole file before provisioning anything.

**Related files**
- `goals.md`: the 3-day plan and constraints. Binding. Section 9 below maps this file onto its days.
- `frontend-spec.md`: section 8.1 defines the `/api/*` rewrite and the Vercel-facing contract.
- `backend-spec.md`: section 12 lists what the backend **requires** from deployment. This file says **how** to provide it. If the two disagree, that is a bug in one of them: decide which is right, fix it, and report the mismatch. Neither file silently overrides the other.

---

## 1. Decisions

| Concern | Decision |
|---|---|
| Frontend host | **Vercel**, deployed from Git. This is fixed. |
| Backend host | **AWS**: two Lambda functions (API and worker) built from one Go binary, called through a Lambda **Function URL**. No API Gateway. |
| Database | **DynamoDB**, five tables, **on-demand** billing (no capacity to plan). |
| Files | **S3**, one private bucket per stage. |
| Sign-in | Backend-issued session cookie. Two ways in: email and password, and **Sign in with Google** (Google Identity Services in the browser, ID-token verification in the backend). Needs one Google OAuth client per stage (section 8.0). |
| Infrastructure as code | **AWS SAM** (`backend/template.yaml`). Nothing is created by hand-clicking except the one-time account setup. |
| Stages | `dev` (for local development and testing) and `prod`. Both use the same template. |
| CI/CD | None beyond Vercel's Git integration. Backend deploys are a manual `make deploy`. (Rule 7 of `goals.md`: no custom CI.) |
| Region | One AWS region for everything. Default `us-east-1`; pick one close to your users and keep it the same for all resources. |

Items marked **(verify)** are facts about AWS or Vercel that change over time. Check them on the provider's own pages before depending on them.

## 2. Overview

```
Browser ──► Vercel (Next.js frontend)
              │  rewrite: /api/:path*  ──►  <FunctionUrl>/:path*
              ▼
        Lambda Function URL ──► exam-api (Go) ──► DynamoDB (5 tables)
                                    │              S3 (private bucket)
                                    └─ async invoke ──► exam-worker (Go) ──► S3, LLM API, DynamoDB
Browser ──► S3 (presigned PUT and GET, direct)
```

| Resource (per stage) | Name pattern |
|---|---|
| CloudFormation stack | `exam-assistant-<stage>` |
| API function | `exam-assistant-<stage>-api` |
| Worker function | `exam-assistant-<stage>-worker` |
| DynamoDB tables | `<stage>-users`, `<stage>-documents`, `<stage>-exams`, `<stage>-attempts`, `<stage>-jobs` |
| Bucket | `exam-assistant-<stage>-<account-id>` (bucket names are global, so the account ID keeps it unique) |
| Vercel project | `exam-assistant` |

## 3. Accounts and prerequisites

**Accounts you need**
- An AWS account. Never deploy with the root user. Create an admin-capable identity for yourself (IAM Identity Center user or an IAM user with access keys) and turn on MFA for it and for the root user.
- A Vercel account (the Hobby plan is for personal, non-commercial use; check its terms **(verify)**).
- A GitHub repository connected to Vercel.
- An account with each LLM provider you enable, and an API key for each: an **Anthropic** key (`LLM_API_KEY`) and, for the cheapest default, a **Gemini** key (`GEMINI_API_KEY`). **Set a monthly spend limit of $20 on every enabled provider's key before anything is deployed to prod.** This is a hard gate (section 8.4), not a nicety. Anthropic's console enforces a limit. Google's tools (a Google AI Studio or Cloud Billing budget, and project quotas) may only **alert** or throttle rather than stop spending **(verify)**, so keep the Gemini key on its own **billing-enabled, paid-tier** project with a small quota, and rely on the app's own monthly budget (`LLM_MONTHLY_BUDGET_USD`) as the cap that actually refuses work. Never use a free-tier Gemini key: free-tier content is used to improve Google's products, and this app handles students' study material.
- A Google account for the Google Cloud Console, to create the OAuth clients for sign-in (free for basic profile sign-in **(verify)**).

**Tools on the machine that deploys**
- Go 1.24 or newer
- AWS CLI v2, configured (`aws configure sso` or `aws configure`) and working (`aws sts get-caller-identity`)
- AWS SAM CLI
- Node.js (LTS) and npm, for the frontend
- `make`, `curl`, `jq`, `openssl`

**Check before starting:** run `aws sts get-caller-identity` and confirm it prints the account you intend to use.

## 4. AWS free tier: what to know

Details **(verify)** on AWS's own pages before relying on them.

- Accounts created after July 15, 2025 are on a **Free plan**: up to $200 in credits (the first $100 at sign-up, up to $100 more for completing tasks), and **the account closes 6 months after it opens, or when credits run out, whichever comes first** unless you upgrade to a Paid plan. Older accounts may still be on the previous 12-month program. Find out which one you have (Billing console) before planning anything long-lived.
- **Lambda** has a monthly always-free allowance that does not use up credits. **DynamoDB on-demand** is *not* covered by the always-free allowance (that covers provisioned capacity only **(verify)**), but at this project's volume it costs cents a month and is paid from the credits. That trade was made on purpose: provisioned units this small throttle under real use with items this size.
- **S3** and **CloudWatch Logs** usage at this project's volume is small. Treat both as credit-funded.
- **The LLM is not an AWS cost and is not free.** It is the main running cost, and the budget is about **$20 a month**. The default model is the **cheapest enabled** one: Gemini 3.1 Flash-Lite when `gemini` is in `LLM_PROVIDERS`, otherwise Claude Haiku 4.5. Illustrative cost per generation job, small / heavy set (`AI-spec.md` section 10.3): Gemini 3.1 Flash-Lite about $0.03 / $0.10, Haiku 4.5 about $0.08 / $0.20, Sonnet 5.5 about $0.17 / $0.40, Opus 5.5 about $0.33 / $0.80. On the $15 app-side budget that is roughly 450 / 140 jobs on Gemini Flash-Lite, 190 / 75 on Haiku, 85 / 35 on Sonnet and 45 / 19 on Opus. Anyone can pick a pricier model per job, which uses up the shared budget faster. The guards, in order: the provider-side monthly limits of $20 (hard gate; see the Google caveat in section 3), the app's own monthly budget estimate (`LLM_MONTHLY_BUDGET_USD`, default $15, refuses new jobs when reached), the global limit of 10 jobs a day, and the per-user limits of 3 generate and 10 regenerate a day (`backend-spec.md` section 7.5). Prices and model IDs change; re-check them before launch **(verify)**, in particular Google's scheduled price changes.
- **Google sign-in** (basic profile and email only) has no AWS cost. Verifying tokens means the backend fetches Google's public keys over the internet, which is one more reason not to put the Lambdas in a VPC.
- **Things that cause surprise bills, and that this design avoids on purpose:** NAT gateways (about $30 a month), unattached Elastic IPs, putting Lambdas in a VPC, RDS, extra regions, high-retention logs. Do not add any of these.
- **Budget alerts lag** (hours, sometimes a day). Set one anyway (section 11).

**DynamoDB billing.** Every table and index uses `BillingMode: PAY_PER_REQUEST`. There are no capacity units to plan, no dev/prod split of units, and no throttling from under-provisioning. The `dev` stack costs next to nothing when idle. Cost scales with reads and writes (billed by item size), which is why the template projects only the attributes each index needs (section 6.2).

## 5. Repository layout

One repository:

```
exam-assistant/
  goals.md, frontend-spec.md, backend-spec.md, deployment-spec.md, ...
  frontend/            # Next.js app, deployed to Vercel (Vercel "Root Directory" = frontend)
  backend/             # Go module and SAM template
    template.yaml
    samconfig.toml     # non-secret deploy settings only
    scripts/write-samconfig.sh   # writes .samconfig.<stage>.toml (with secrets, gitignored) for `make deploy`
    Makefile           # build, test, run, smoke, deploy, destroy
    ...
  .gitignore
```

`.gitignore` must include: `.env*` (all environment files), `backend/.aws-sam/`, `frontend/.next/`, `node_modules/`, `backend/.env.deploy.*`, `backend/.samconfig.*.toml` (generated at deploy time and contains secrets).

**Secrets never go in Git.** They live in two places only: per-stage files on the deploying machine (`backend/.env.deploy.dev`, `backend/.env.deploy.prod`, both gitignored, plus the generated `backend/.samconfig.<stage>.toml`, also gitignored) and, once deployed, the Lambda configuration. Frontend has no secrets (`API_URL` is not secret but is server-side only).

## 6. AWS stack (SAM)

### 6.1 Build

The template builds with SAM's `makefile` build method so the build is explicit. `backend/Makefile` must provide these targets (SAM calls them by function name):

```make
build-ApiFunction build-WorkerFunction:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -tags lambda.norpc -o $(ARTIFACTS_DIR)/bootstrap ./cmd/lambda
```

Runtime is `provided.al2023`, architecture `arm64`, handler `bootstrap`. Both functions run the same binary; `MODE` picks the behaviour.

### 6.2 Reference template

A starting point, not gospel. Run `sam validate --lint` and fix whatever it reports. If it disagrees with `backend-spec.md` section 12, treat that as a bug in one of the two and fix it (see the note at the top).

```yaml
AWSTemplateFormatVersion: '2010-09-09'
Transform: AWS::Serverless-2016-10-31
Description: Exam Assistant backend

Parameters:
  Stage:           { Type: String, AllowedValues: [dev, prod] }
  JwtSecret:       { Type: String, NoEcho: true, MinLength: 32 }
  LlmProviders:    { Type: String, Default: anthropic, Description: "Enabled providers, comma-separated: anthropic, gemini (for example gemini,anthropic)" }
  LlmApiKey:       { Type: String, NoEcho: true, Default: "", Description: "Anthropic API key (LLM_API_KEY). Required when anthropic is enabled" }
  GeminiApiKey:    { Type: String, NoEcho: true, Default: "", Description: "Gemini API key (GEMINI_API_KEY), paid tier only. Required when gemini is enabled" }
  LlmModel:        { Type: String, Default: "", Description: "Default model ID. Empty means the cheapest enabled model in the backend's catalog" }
  GoogleClientId:  { Type: String, Description: "Google OAuth client ID that sign-in tokens must be issued for" }
  AllowedOrigins:  { Type: String, Description: "Comma-separated, e.g. https://app.example.com,http://localhost:3000" }
  FrontendOrigins: { Type: CommaDelimitedList, Description: "Same origins, for S3 CORS" }
  ApiReservedConcurrency:    { Type: Number, Default: 0, Description: "0 = no reservation. Suggested 20, only if the account concurrency quota allows it" }
  WorkerReservedConcurrency: { Type: Number, Default: 0, Description: "0 = no reservation. Suggested 5, only if the account concurrency quota allows it" }

Conditions:
  ReserveApi:    !Not [!Equals [!Ref ApiReservedConcurrency, 0]]
  ReserveWorker: !Not [!Equals [!Ref WorkerReservedConcurrency, 0]]

Globals:
  Function:
    Runtime: provided.al2023
    Architectures: [arm64]
    Handler: bootstrap
    MemorySize: 1024
    Environment:
      Variables:                       # shared, non-secret
        ENV: production
        STAGE: !Ref Stage
        S3_BUCKET: !Ref UploadsBucket
        LLM_PROVIDERS: !Ref LlmProviders  # not secret; the API lists and validates models with it, the worker uses it to pick a provider
        LLM_MODEL: !Ref LlmModel          # not secret; empty means the cheapest enabled model

Resources:
  ApiFunction:
    Type: AWS::Serverless::Function
    Metadata: { BuildMethod: makefile }
    Properties:
      FunctionName: !Sub "exam-assistant-${Stage}-api"
      CodeUri: .
      Timeout: 29
      ReservedConcurrentExecutions: !If [ReserveApi, !Ref ApiReservedConcurrency, !Ref "AWS::NoValue"]
      FunctionUrlConfig: { AuthType: NONE }
      LoggingConfig: { LogGroup: !Ref ApiLogs }   # makes the function depend on the group, so Lambda never creates it first
      Environment:
        Variables:                     # the API never calls the LLM, so it gets no provider keys
          MODE: api
          JWT_SECRET: !Ref JwtSecret
          GOOGLE_CLIENT_ID: !Ref GoogleClientId
          ALLOWED_ORIGINS: !Ref AllowedOrigins
          WORKER_FUNCTION_NAME: !Sub "exam-assistant-${Stage}-worker"
      # Least privilege, derived from what internal/store/dynamo and internal/docs call (TestIAMPoliciesMatchDerivation, TestStoreCodeIAMDerivation).
      # No Scan, no BatchWriteItem, no DeleteItem outside documents. Transactions (users, jobs) need Put/Update on each item's table.
      Policies:
        - Statement:
            - Effect: Allow             # users: get, batch get, signup and Google link transactions (Put + Update)
              Action: [dynamodb:GetItem, dynamodb:BatchGetItem, dynamodb:PutItem, dynamodb:UpdateItem]
              Resource: !GetAtt UsersTable.Arn
            - Effect: Allow             # documents: the only table with DeleteItem; ListReady queries the base table
              Action: [dynamodb:GetItem, dynamodb:BatchGetItem, dynamodb:PutItem, dynamodb:UpdateItem, dynamodb:DeleteItem, dynamodb:Query]
              Resource: !GetAtt DocumentsTable.Arn
            - Effect: Allow             # exams: immutable once created (no UpdateItem)
              Action: [dynamodb:GetItem, dynamodb:BatchGetItem, dynamodb:PutItem]
              Resource: !GetAtt ExamsTable.Arn
            - Effect: Allow
              Action: dynamodb:Query
              Resource: !Sub "${ExamsTable.Arn}/index/creator-index"
            - Effect: Allow             # attempts: ListByExam queries the base table
              Action: [dynamodb:GetItem, dynamodb:PutItem, dynamodb:UpdateItem, dynamodb:Query]
              Resource: !GetAtt AttemptsTable.Arn
            - Effect: Allow
              Action: dynamodb:Query
              Resource: !Sub "${AttemptsTable.Arn}/index/user-index"
            - Effect: Allow             # jobs: create-with-quota transaction (Put + Update), cancel, get questions
              Action: [dynamodb:GetItem, dynamodb:PutItem, dynamodb:UpdateItem]
              Resource: !GetAtt JobsTable.Arn
            - Effect: Allow             # owner-index today; day-index is for the usage page
              Action: dynamodb:Query
              Resource: !Sub "${JobsTable.Arn}/index/*"
            - Effect: Allow
              Action: [s3:PutObject, s3:GetObject, s3:DeleteObject]
              Resource: !Sub "${UploadsBucket.Arn}/*"
            - Effect: Allow
              Action: s3:ListBucket        # without it, HeadObject on a missing key returns 403, not 404
              Resource: !GetAtt UploadsBucket.Arn
            - Effect: Allow
              Action: lambda:InvokeFunction
              Resource: !GetAtt WorkerFunction.Arn

  WorkerFunction:
    Type: AWS::Serverless::Function
    Metadata: { BuildMethod: makefile }
    Properties:
      FunctionName: !Sub "exam-assistant-${Stage}-worker"
      CodeUri: .
      Timeout: 600                     # platform ceiling; the job's own deadline is 8 minutes (backend-spec 7.2)
      ReservedConcurrentExecutions: !If [ReserveWorker, !Ref WorkerReservedConcurrency, !Ref "AWS::NoValue"]
      LoggingConfig: { LogGroup: !Ref WorkerLogs }
      Environment:
        Variables:                     # the worker needs the provider keys, not the session secret or Google
          MODE: worker
          LLM_API_KEY: !Ref LlmApiKey        # Anthropic
          GEMINI_API_KEY: !Ref GeminiApiKey  # Gemini (paid tier)
      EventInvokeConfig:
        MaximumRetryAttempts: 0          # a retried job would run the LLM calls twice
        MaximumEventAgeInSeconds: 3600
      Policies:
        - Statement:
            - Effect: Allow
              Action: dynamodb:BatchGetItem   # GetDocs reads the source document rows; nothing else on documents
              Resource: !GetAtt DocumentsTable.Arn
            - Effect: Allow
              Action: [dynamodb:GetItem, dynamodb:PutItem, dynamodb:UpdateItem]   # claim, heartbeat, finish, questions item, spend counter; no Query, no index
              Resource: !GetAtt JobsTable.Arn
            - Effect: Allow
              Action: s3:GetObject
              Resource: !Sub "${UploadsBucket.Arn}/*"

  ApiLogs:
    Type: AWS::Logs::LogGroup
    Properties: { LogGroupName: !Sub "/aws/lambda/exam-assistant-${Stage}-api", RetentionInDays: 14 }
  WorkerLogs:
    Type: AWS::Logs::LogGroup
    Properties: { LogGroupName: !Sub "/aws/lambda/exam-assistant-${Stage}-worker", RetentionInDays: 14 }

  UploadsBucket:
    Type: AWS::S3::Bucket
    Properties:
      BucketName: !Sub "exam-assistant-${Stage}-${AWS::AccountId}"
      PublicAccessBlockConfiguration:
        BlockPublicAcls: true
        BlockPublicPolicy: true
        IgnorePublicAcls: true
        RestrictPublicBuckets: true
      BucketEncryption:
        ServerSideEncryptionConfiguration:
          - ServerSideEncryptionByDefault: { SSEAlgorithm: AES256 }
      CorsConfiguration:
        CorsRules:
          - AllowedOrigins: !Ref FrontendOrigins
            AllowedMethods: [GET, PUT, HEAD]
            AllowedHeaders: ["*"]
            ExposedHeaders: [ETag]
            MaxAge: 3600

  UsersTable:
    Type: AWS::DynamoDB::Table
    Properties:
      TableName: !Sub "${Stage}-users"
      BillingMode: PAY_PER_REQUEST
      AttributeDefinitions: [{ AttributeName: id, AttributeType: S }]
      KeySchema: [{ AttributeName: id, KeyType: HASH }]

  DocumentsTable:
    Type: AWS::DynamoDB::Table
    Properties:
      TableName: !Sub "${Stage}-documents"
      BillingMode: PAY_PER_REQUEST
      AttributeDefinitions: [{ AttributeName: ownerId, AttributeType: S }, { AttributeName: id, AttributeType: S }]
      KeySchema: [{ AttributeName: ownerId, KeyType: HASH }, { AttributeName: id, KeyType: RANGE }]
      TimeToLiveSpecification: { AttributeName: ttl, Enabled: true }

  ExamsTable:
    Type: AWS::DynamoDB::Table
    Properties:
      TableName: !Sub "${Stage}-exams"
      BillingMode: PAY_PER_REQUEST
      AttributeDefinitions: [{ AttributeName: id, AttributeType: S }, { AttributeName: creatorId, AttributeType: S }]
      KeySchema: [{ AttributeName: id, KeyType: HASH }]
      GlobalSecondaryIndexes:
        - IndexName: creator-index
          KeySchema: [{ AttributeName: creatorId, KeyType: HASH }, { AttributeName: id, KeyType: RANGE }]
          Projection: { ProjectionType: INCLUDE, NonKeyAttributes: [title, type, totalMarks, closesAt, createdAt] }   # never the questions

  AttemptsTable:
    Type: AWS::DynamoDB::Table
    Properties:
      TableName: !Sub "${Stage}-attempts"
      BillingMode: PAY_PER_REQUEST
      AttributeDefinitions:
        - { AttributeName: examId, AttributeType: S }
        - { AttributeName: userId, AttributeType: S }
      KeySchema: [{ AttributeName: examId, KeyType: HASH }, { AttributeName: userId, KeyType: RANGE }]
      GlobalSecondaryIndexes:
        - IndexName: user-index
          KeySchema: [{ AttributeName: userId, KeyType: HASH }, { AttributeName: examId, KeyType: RANGE }]
          Projection: { ProjectionType: INCLUDE, NonKeyAttributes: [status, scoreHalves, scoreBeforePeeksHalves, submittedAt] }   # never the answers

  JobsTable:                           # also holds quota items (quota#...), usage counters (usage#...) and question items (<jobId>#questions)
    Type: AWS::DynamoDB::Table
    Properties:
      TableName: !Sub "${Stage}-jobs"
      BillingMode: PAY_PER_REQUEST
      AttributeDefinitions:
        - { AttributeName: id, AttributeType: S }
        - { AttributeName: ownerId, AttributeType: S }
        - { AttributeName: day, AttributeType: S }         # yyyymmdd (UTC), set on job items only
        - { AttributeName: createdAt, AttributeType: S }   # ISO 8601 UTC string, sorts by time
      KeySchema: [{ AttributeName: id, KeyType: HASH }]
      TimeToLiveSpecification: { AttributeName: ttl, Enabled: true }
      GlobalSecondaryIndexes:
        - IndexName: owner-index
          KeySchema: [{ AttributeName: ownerId, KeyType: HASH }, { AttributeName: id, KeyType: RANGE }]
          Projection: { ProjectionType: INCLUDE, NonKeyAttributes: [status, createdAt, updatedAt, kind] }
        - IndexName: day-index                              # the AI usage page lists today's jobs without a Scan
          KeySchema: [{ AttributeName: day, KeyType: HASH }, { AttributeName: createdAt, KeyType: RANGE }]
          Projection: { ProjectionType: INCLUDE, NonKeyAttributes: [status, updatedAt, model, kind, costMicros, calls, inTok, outTok, cacheReadTok, cacheWriteTok] }

Outputs:
  FunctionUrl: { Value: !GetAtt ApiFunctionUrl.FunctionUrl }
  BucketName:  { Value: !Ref UploadsBucket }
  WorkerName:  { Value: !Ref WorkerFunction }
```

**Notes on the template**
- **Log groups are tied to their functions.** `LoggingConfig: { LogGroup: !Ref ApiLogs }` makes CloudFormation create the group before the function; without it Lambda can create the group itself on an early invoke or from a stale stack, and the stack create fails with "already exists" or the group lacks the 14-day retention. `WorkerFunction` has the same `LoggingConfig: { LogGroup: !Ref WorkerLogs }`.
- **Data layer is five tables and four indexes, all on-demand.** `users` has no index (login uses the email lock item, `backend-spec.md` section 6.2). Index projections are `INCLUDE` lists, never `ALL`: an index copies a write only when a projected attribute changes, so autosaving answers or embedding questions never writes to an index. (`day-index` has one partition per UTC day, which is fine at this volume: a handful of jobs a day.) `NonKeyAttributes` must match the attribute names the store implementation writes; the key attributes are projected automatically.
- **The jobs table holds four kinds of item:** job status items (with `day`, `model` and live usage fields; `day-index` reads these), a questions item per job (`<jobId>#questions`, written just before the job turns `done`), quota counters (`quota#<userId>#<yyyymmdd>`, `quota#global#<yyyymmdd>`, `quota#global#<yyyymm>`), and usage counters per model (`usage#<yyyymm>#<modelId>`, `usage#<yyyymmdd>#<modelId>`). All carry `ttl` (jobs 24 hours, daily quota and usage items 3 days, monthly items 40 days).
- **`ENV: production`** is set for both stages so the session cookie always has the `Secure` flag when deployed. Only a locally run server uses `ENV=local`.
- **Worker retries are off on purpose.** The default for asynchronous invocation is to retry twice, which would run a generation job (and pay for its LLM calls) up to three times. Asynchronous delivery is still at-least-once, so the worker claims a job with a conditional `queued -> running` write and exits if it loses (`backend-spec.md` section 7.2).
- **Timeouts line up like this.** Worker function timeout 600 s is the ceiling; the job's own deadline is 8 minutes, and a job with no heartbeat for 6 minutes reads as failed to the client. The API function's 29 s matches the Function URL's practical limit; nothing long runs there.
- **Reserved concurrency is optional and off by default.** When set, it caps how many API or worker executions run at once, which bounds LLM spend and DynamoDB load under abuse. It only works if the account's concurrency quota is high enough: AWS requires at least 100 unreserved executions to remain, and a new account's quota may be lower. Check Service Quotas first **(verify)**; if you cannot reserve, rely on the quota counters and the provider spend limit. A worker invoke throttled by its reservation is dropped (retries are 0), so that job stays `queued` until the 6-minute stale rule fails it and the user retries.
- **Secrets are scoped per function.** `JWT_SECRET` and `GOOGLE_CLIENT_ID` exist only on the API; `LLM_API_KEY` (Anthropic) and `GEMINI_API_KEY` only on the worker. `LLM_PROVIDERS` and `LLM_MODEL` are not secret and are on both: the API needs them to list the enabled models (`GET /ai/models`), mark the default, and validate the `model` a request asks for, without ever holding a key. An empty `LLM_MODEL` means "the cheapest enabled model" and the backend must treat an empty value as unset. If a model is enabled but its key is missing, the worker fails at startup (config loading requires the key in worker mode), logs the missing variable name, and the job stays `queued` until the 6-minute stale rule shows it as failed. `backend-spec.md` section 11 must therefore mark each variable required per `MODE`, so config loading does not fail in a mode that does not need it.
- **IAM is derived from the code, per function and per table.** Each statement lists only the DynamoDB actions the store code calls on that table (`backend/internal/store/dynamo/iam_template_test.go` fails when the code and the template drift): no `Scan`, no `BatchWriteItem`, `DeleteItem` only on `documents`. `TransactWriteItems` has no IAM action of its own; it needs `PutItem`/`UpdateItem` (and `ConditionCheckItem` if a check is used) on each table it touches, which is why `users` and `jobs` carry `PutItem`+`UpdateItem` for the API. `Query` on an index needs the index ARN, so exams and attempts get it on `creator-index` and `user-index` only; the jobs `index/*` grant covers `owner-index` and `day-index`. The worker gets no `Query`, no index access, and no exams, attempts or users access: it reads documents with `BatchGetItem` and reads and writes the jobs table with `GetItem`/`PutItem`/`UpdateItem`. The API also holds read-only `BatchGetItem` on `jobs` for the usage counters (`GET /ai/usage`).
- **No VPC, no API Gateway, no Cognito, no queue.** Do not add them.
- **Public function URL.** `AuthType: NONE` means anyone with the URL can call it; the app authenticates every request itself. Keep the URL out of public docs, and know it is reachable directly, not only through Vercel. If a newly created function URL returns `403`, check its resource policy: AWS has tightened what the permission for public URLs requires **(verify)**.
- **Memory** is 1024 MB for both because PDF page counting happens in the API function and reads the whole file. Lower it after measuring.
- **Termination protection on prod** (section 8.3) is the safeguard against deleting the stack and the data with it. Without it, deleting the stack deletes the tables.
- **Bucket deletion** fails while the bucket has objects, which is a useful guard. `make destroy` must empty the bucket first on purpose (section 11).

### 6.3 Parameters per stage

| Parameter | dev | prod |
|---|---|---|
| `Stage` | `dev` | `prod` |
| `ApiReservedConcurrency`, `WorkerReservedConcurrency` | `0` (off) | `0` (off), or `20` and `5` if the account quota allows (see notes) |
| `AllowedOrigins` | `http://localhost:3000` | the production Vercel domain, for example `https://exam-assistant.vercel.app`, plus any custom domain, and `http://localhost:3000` if you want local frontend against prod (not recommended) |
| `FrontendOrigins` | same list as `AllowedOrigins` | same list as `AllowedOrigins` |
| `JwtSecret` | random, different per stage | random, different per stage |
| `LlmProviders` | `anthropic`, or `gemini,anthropic` once the Gemini key exists | the same list |
| `LlmApiKey` (Anthropic) | a key with a low spend limit; may be empty if `anthropic` is not enabled | a key with a **$20 monthly** spend limit; may be empty if `anthropic` is not enabled |
| `GeminiApiKey` | a **paid-tier** key from a billing-enabled project with a small quota; empty if `gemini` is not enabled | a **paid-tier** key from its own billing-enabled project with a small quota and a budget alert at or below $20; empty if `gemini` is not enabled |
| `LlmModel` | empty (the cheapest enabled model) | empty (the cheapest enabled model), or a model ID from the backend's catalog |
| `GoogleClientId` | the **dev** OAuth client ID (authorized origin: `http://localhost:3000`) | the **prod** OAuth client ID (authorized origin: the production Vercel domain) |

Generate a secret with `openssl rand -hex 32` (hex has no characters that need quoting in the generated deploy config). Never reuse the prod secret in dev.

### 6.4 Optional overrides

These variables have defaults in the code and are **not** set in the template. Add one under the relevant function's `Environment: Variables` only when you need to change it. Names and defaults are owned by `AI-spec.md` section 12 and `backend-spec.md` section 11; the values below are for orientation, so check those files if they ever differ.

| Variable | Code default | Function |
|---|---|---|
| `LLM_EFFORT` | `medium` | worker |
| `LLM_TIMEOUT_SECONDS` | `120` | worker |
| `GEN_MAX_FILES`, `GEN_MAX_TOTAL_BYTES`, `GEN_MAX_PDF_PAGES`, `GEN_MAX_INPUT_TOKENS`, `GEN_MAX_PER_CALL` | see `AI-spec.md` section 12 | API and worker |
| `GEN_MAX_RUNNING` | `2` | API |
| `GEN_MAX_PER_DAY` | `3` (per user, generate) | API |
| `GEN_MAX_REGEN_PER_DAY` | `10` (per user, regenerate) | API |
| `GEN_MAX_GLOBAL_PER_DAY` | `10` (all users, UTC day) | API |
| `LLM_MONTHLY_BUDGET_USD` | `15` (estimated spend; keep below the provider's $20 limit) | API and worker |

## 7. Frontend on Vercel

### 7.1 Project setup (one time)

1. Push the repository to GitHub.
2. In Vercel: **Add New, Project**, import the repository.
3. **Framework Preset:** Next.js. **Root Directory:** `frontend`. Leave build and install commands at their defaults.
4. Under **Environment Variables**, add `API_URL` (section 7.2) for **Production** (and Preview and Development if you want them).
5. Deploy. Every push to the production branch (default `main`) now deploys automatically.

### 7.2 Environment variables

| Name | Value | Scope | Notes |
|---|---|---|---|
| `API_URL` | the stage's `FunctionUrl` output **without a trailing slash**, for example `https://abc123.lambda-url.us-east-1.on.aws` | Production, plus Preview and Development as wanted | Server-side only. No `NEXT_PUBLIC_` prefix. The browser must never call this URL directly (`frontend-spec.md` section 8.1). |
| `NEXT_PUBLIC_GOOGLE_CLIENT_ID` | the stage's Google OAuth client ID (prod client for Production; dev client for Preview and Development) | Production, plus Preview and Development as wanted | Public by design, not a secret. It is inlined at build time, so redeploy after changing it. It must be the **same client ID** the backend stage was given as `GOOGLE_CLIENT_ID`, or sign-in tokens are rejected. |

Production points at the **prod** stack's URL. Development (local) points at the **dev** stack's URL.

After changing an environment variable in Vercel you must **redeploy**; running deployments do not pick it up.

### 7.3 The rewrite

The rewrite is in `frontend/next.config.js`, exactly as in `frontend-spec.md` section 8.1:

```js
async rewrites() {
  return [{ source: "/api/:path*", destination: `${process.env.API_URL}/:path*` }];
}
```

Behaviour to rely on and to verify in the first hour:
- `https://<vercel-domain>/api/health` returns the backend's `{"status":"ok"}`.
- `Set-Cookie` from the backend reaches the browser, and the cookie is stored on the Vercel domain.
- Responses carry `Cache-Control: no-store` from the backend and are not cached by Vercel.
- Vercel's proxying has its own timeout and body-size limits **(verify)**. Nothing in the app depends on long requests (jobs are polled), and no file bodies go through it (uploads go straight to S3).

### 7.4 Domains

- The default `*.vercel.app` domain is enough for the project. If you add a custom domain, add it to `AllowedOrigins` and `FrontendOrigins` and redeploy the stack (the S3 CORS rule and the backend's origin check both use that list).
- HTTPS is automatic on Vercel. The backend cookie is `Secure`, so the frontend must always be served over HTTPS in production.

### 7.5 Preview deployments

Vercel creates a new URL for every branch and pull request. The backend's origin check and the S3 CORS rule use an **exact** list, so a preview URL cannot sign in, upload, or submit. Treat previews as visual checks only. Do real testing on the production domain or on `localhost`. If you decide previews must work, add an origin pattern for them to the backend's origin check and to `FrontendOrigins` (S3 CORS allows a single `*` wildcard in an origin **(verify)**), and accept that every preview then shares the same backend.

### 7.6 Local development

- `frontend/.env.local` (gitignored): `API_URL=<dev FunctionUrl>` to use the dev stack, or `API_URL=http://127.0.0.1:8080` to use a locally running backend (`make run` in `backend/`, which needs AWS credentials and the dev tables), or the mock flag from `frontend-spec.md` section 8.1 to work with no backend at all.
- `ALLOWED_ORIGINS` on the dev stack already includes `http://localhost:3000`.

## 8. Deployment procedure

### 8.0 One-time Google sign-in setup

Do this once per stage, before the first sign-in test. The details of Google's console change, so follow its current screens **(verify)**.

1. In the Google Cloud Console, create a project (for example `exam-assistant`).
2. Configure the **OAuth consent screen** (Google Auth Platform): app name, a support email, audience **External**, and your contact email. Request only the basic scopes `openid`, `email`, and `profile`.
3. While the app is in **Testing** status, only the Google accounts you list as **test users** (a small limit applies) can sign in. Add yourself and anyone who will test. To let anyone sign in, publish the app to **Production**. Basic scopes should not need Google's verification review, but publishing may ask for a privacy policy and a homepage link **(verify)**.
4. Create **two OAuth client IDs** of type **Web application**: `exam-assistant-dev` and `exam-assistant-prod`. Separate clients mean a dev token can never be accepted by prod.
5. Under **Authorized JavaScript origins**, add:
   - dev client: `http://localhost:3000`
   - prod client: the production Vercel origin (for example `https://exam-assistant.vercel.app`) and any custom domain. Origins have no path and no trailing slash, and must match exactly.
   Leave **Authorized redirect URIs** empty; the button-and-credential flow does not use one.
6. Copy each **Client ID**. There is no client secret to store: the backend only verifies tokens, which needs the client ID and Google's public keys.
7. Put each client ID in three places:
   - `GOOGLE_CLIENT_ID` in `backend/.env.deploy.<stage>` (section 8.1)
   - `NEXT_PUBLIC_GOOGLE_CLIENT_ID` in Vercel (section 7.2)
   - `NEXT_PUBLIC_GOOGLE_CLIENT_ID` in `frontend/.env.local` (the dev client)
8. Changes to origins or the consent screen can take from several minutes to a few hours to take effect. If the button reports an origin problem right after a change, wait before changing anything else.

Preview deployments cannot use Google sign-in, for the same reason they cannot call the backend (section 7.5): their URLs are not in the authorized origins, and Google does not allow wildcards.

### 8.1 One-time AWS setup

1. Confirm you are not using the root user: `aws sts get-caller-identity`.
2. Confirm the region: `aws configure get region` (or set `AWS_REGION`). Use the same region for everything.
3. Create the two env files (gitignored):

```
# backend/.env.deploy.dev
JWT_SECRET=<openssl rand -hex 32>
LLM_PROVIDERS=gemini,anthropic      # or just anthropic until the Gemini key exists
LLM_API_KEY=<dev Anthropic key>      # needed when anthropic is enabled
GEMINI_API_KEY=<dev Gemini key>      # needed when gemini is enabled; paid tier only
LLM_MODEL=                           # empty = the cheapest enabled model
ALLOWED_ORIGINS=http://localhost:3000
GOOGLE_CLIENT_ID=<dev OAuth client ID>
```
```
# backend/.env.deploy.prod
JWT_SECRET=<a different random value>
LLM_PROVIDERS=gemini,anthropic
LLM_API_KEY=<prod Anthropic key>
GEMINI_API_KEY=<prod Gemini key>
LLM_MODEL=
ALLOWED_ORIGINS=https://exam-assistant.vercel.app,http://localhost:3000
GOOGLE_CLIENT_ID=<prod OAuth client ID>
# optional, only if the account's concurrency quota allows (section 6.2 notes):
# API_RESERVED_CONCURRENCY=20
# WORKER_RESERVED_CONCURRENCY=5
```

4. Set a budget alert (section 11) before creating anything else.
5. Create the `prod` API key for **each enabled provider** and **set its monthly spend limit now** (Anthropic console; for Gemini, a billing-enabled paid-tier project with a small quota and a budget alert, since Google's controls may only alert). Section 8.4 will not let you deploy prod without it.

### 8.2 `make deploy` (backend)

`backend/Makefile` provides:

```make
STAGE ?= dev
AWS_REGION ?= us-east-1
-include .env.deploy.$(STAGE)
export

check-env:
	@test -f .env.deploy.$(STAGE) || { echo "Missing backend/.env.deploy.$(STAGE). See deployment-spec.md section 8.1."; exit 1; }

deploy: check-env
	sam build
	./scripts/write-samconfig.sh $(STAGE)
	sam deploy --config-file .samconfig.$(STAGE).toml \
	  --stack-name exam-assistant-$(STAGE) --region $(AWS_REGION) \
	  --capabilities CAPABILITY_IAM --resolve-s3 --no-confirm-changeset --no-fail-on-empty-changeset
	@aws cloudformation describe-stacks --stack-name exam-assistant-$(STAGE) --region $(AWS_REGION) \
	  --query "Stacks[0].Outputs" --output table
```

`-include` means `make test`, `make run`, and `make smoke` work on a fresh clone with no env file; only `deploy` needs it, and `check-env` stops it with a clear message.

**How secrets reach CloudFormation.** Passing them with `--parameter-overrides` on the command line would expose them in the process list. Instead `scripts/write-samconfig.sh` reads the exported variables and writes `backend/.samconfig.<stage>.toml` (mode 600, gitignored), and `sam deploy --config-file` reads the overrides from there. The values are `NoEcho` stack parameters, so they are not shown in the console or in the deploy output.

```sh
#!/bin/sh
# usage: write-samconfig.sh <stage>   (variables come from .env.deploy.<stage> via make)
# Writes backend/.samconfig.<stage>.toml (mode 600). Secrets never reach a command line or stdout.
set -eu
umask 077

if [ "$#" -ne 1 ]; then echo "usage: write-samconfig.sh <stage>  (dev or prod)" >&2; exit 2; fi
stage="$1"
case "$stage" in
  dev|prod) ;;
  *) echo "stage must be dev or prod" >&2; exit 2 ;;
esac
dir=$(cd "$(dirname "$0")/.." && pwd)
out="$dir/.samconfig.$stage.toml"

need() { # need VAR: fail with a named message when unset or empty
  eval "v=\${$1:-}"
  [ -n "$v" ] || { echo "$1 is required" >&2; exit 1; }
}
# Values go into a TOML string holding space-separated Key=Value pairs, so a quote,
# backslash or whitespace inside a value would corrupt the file or split a parameter.
safe() {
  eval "v=\${$1:-}"
  case "$v" in
    *[[:space:]\"\\]*) echo "$1 must not contain whitespace, quotes or backslashes" >&2; exit 1 ;;
  esac
}

need JWT_SECRET
need GOOGLE_CLIENT_ID
need ALLOWED_ORIGINS
providers="${LLM_PROVIDERS:-anthropic}"
case ",$providers," in *,anthropic,*) [ -n "${LLM_API_KEY:-}" ] || { echo "LLM_API_KEY is required when anthropic is enabled" >&2; exit 1; } ;; esac
case ",$providers," in *,gemini,*)    [ -n "${GEMINI_API_KEY:-}" ] || { echo "GEMINI_API_KEY is required when gemini is enabled" >&2; exit 1; } ;; esac
for n in JWT_SECRET GOOGLE_CLIENT_ID ALLOWED_ORIGINS LLM_API_KEY GEMINI_API_KEY LLM_MODEL \
         API_RESERVED_CONCURRENCY WORKER_RESERVED_CONCURRENCY; do safe "$n"; done
case "$providers" in *[[:space:]\"\\]*) echo "LLM_PROVIDERS must not contain whitespace, quotes or backslashes" >&2; exit 1 ;; esac

extra=""
if [ -n "${LLM_API_KEY:-}" ]; then extra="$extra LlmApiKey=$LLM_API_KEY"; fi
if [ -n "${GEMINI_API_KEY:-}" ]; then extra="$extra GeminiApiKey=$GEMINI_API_KEY"; fi
if [ -n "${LLM_MODEL:-}" ]; then extra="$extra LlmModel=$LLM_MODEL"; fi
if [ -n "${API_RESERVED_CONCURRENCY:-}" ]; then extra="$extra ApiReservedConcurrency=$API_RESERVED_CONCURRENCY"; fi
if [ -n "${WORKER_RESERVED_CONCURRENCY:-}" ]; then extra="$extra WorkerReservedConcurrency=$WORKER_RESERVED_CONCURRENCY"; fi

# Write to a temp file in the same directory, then rename, so a failure never leaves a partial file.
tmp=$(mktemp "$dir/.samconfig.$stage.XXXXXX")
trap 'rm -f "$tmp"' EXIT
{
  echo 'version = 0.1'
  echo '[default.deploy.parameters]'
  # Values containing commas are wrapped in \" so SAM keeps each as one parameter.
  printf 'parameter_overrides = "Stage=%s JwtSecret=%s LlmProviders=\\"%s\\" GoogleClientId=%s AllowedOrigins=\\"%s\\" FrontendOrigins=\\"%s\\"%s"\n' \
    "$stage" "$JWT_SECRET" "$providers" "$GOOGLE_CLIENT_ID" "$ALLOWED_ORIGINS" "$ALLOWED_ORIGINS" "$extra"
} > "$tmp"
mv "$tmp" "$out"
trap - EXIT
echo "wrote $out"
```

The script stops with a clear message if an enabled provider has no key. Optional values (the two keys, `LlmModel`, the concurrency settings) are passed only when set, so an empty value never reaches CloudFormation as `Key=`. The quoting around the comma-containing values (the origin lists and `LlmProviders`, for example `gemini,anthropic`) is the fiddly part. SAM's own parameter parser (`samcli.cli.types.CfnParameterOverridesType`) was run offline on the generated file and returns each comma list as one value; still confirm once with `sam deploy --debug` on the first real deploy **(verify)**. The script writes through a temp file and renames it, so a failure leaves no partial file; the Gemini key and the Anthropic key contain only letters, digits, `-` and `_`, so they need no quoting.

`samconfig.toml` may be committed but must not contain secrets. Never run `sam deploy --guided` and commit what it writes without checking the file.

### 8.3 First hour (Day 1 checkpoint from `goals.md`)

The aim is to find rewrite, cookie, and AWS problems at the start. Do this with an empty backend that only serves `/health` and a skeleton frontend.

1. `cd backend && make deploy STAGE=dev`. Copy the `FunctionUrl` output.
2. `curl -s <FunctionUrl>/health` returns `{"status":"ok"}`.
3. In Vercel, set `API_URL` to that URL (no trailing slash) and redeploy.
4. `curl -s https://<vercel-domain>/api/health` returns the same JSON. This proves the rewrite.
5. As soon as login exists: sign up in the browser, then in developer tools check that the `session` cookie is stored with `HttpOnly`, `Secure`, `SameSite=Lax`, and no `Domain`, and that it survives a refresh. This proves cookies work through the rewrite.
6. As soon as Google sign-in exists, sign in with a Google test user on `localhost` (dev) and then on the Vercel domain. This proves the OAuth client, the authorized origins, and token verification (the backend must reach Google).
7. Upload a small file as soon as upload exists. This proves the S3 CORS rule and presigned URLs.

### 8.4 Production (Day 2)

**Gate: do not deploy prod until the provider-side monthly spend limit of $20 is set on the key of every provider in `LLM_PROVIDERS`** and a budget alert exists (section 11). Check each provider's console, not your memory. For Gemini that means the key belongs to a billing-enabled paid-tier project with a small quota and a budget alert, and you have noted that Google may only alert rather than stop. Nothing else in the design except the app's own monthly budget caps spend if the app's other limits are bypassed.

1. Create `backend/.env.deploy.prod` with the production Vercel domain in `ALLOWED_ORIGINS`.
2. Confirm the gate above, then `make deploy STAGE=prod`. Copy the `FunctionUrl`.
3. Turn on termination protection for the prod stack: `aws cloudformation update-termination-protection --enable-termination-protection --stack-name exam-assistant-prod --region <region>`.
4. In Vercel, set `API_URL` for **Production** to the prod `FunctionUrl` and redeploy production.
5. Run the smoke test through the whole chain: `make smoke BASE_URL=https://<vercel-domain>/api`.
6. Walk the end-to-end flow on a phone and a desktop (acceptance list in section 10).

### 8.5 Updating

- **Frontend:** push to `main`. Vercel builds and deploys.
- **Backend:** `make deploy STAGE=dev`, test, then `make deploy STAGE=prod`.
- **Changing an origin** (new domain): update `ALLOWED_ORIGINS` in both env files, redeploy the stack, add the origin to the Google client's authorized origins (section 8.0), then redeploy Vercel only if `API_URL` or the client ID changed.
- **Changing a secret:** edit the env file, redeploy the stack. Changing `JWT_SECRET` signs everyone out.

## 9. Mapping to the 3-day plan in `goals.md`

| Day | Deployment work |
|---|---|
| 1, first hour | Section 8.3: dev stack with `/health`, Vercel project, rewrite verified, cookie, Google sign-in (OAuth clients from 8.0), and (once built) upload verified |
| 1, rest of day | Keep deploying `dev` after each backend slice; the frontend builds against the dev stack or the mock flag |
| 2 | Section 8.4: prod stack, termination protection, Vercel production env var, smoke test, device walk-through, fix production-only problems (cookies, presigned URLs, CORS, timeouts) |
| 3 | Redeploy fixes with `make deploy` and Git pushes; run the smoke test after each. In the first test pass, run the AI golden set on the **cheapest enabled model** (`AI-spec.md` section 13). Use a pricier model (Haiku, Sonnet, Opus) only if its quality is not good enough, by setting `LlmModel` for the stage or by picking it per job in the app; each step up roughly doubles or triples the cost per job. Compare the app's monthly spend estimate with each provider's usage page |

## 10. Verification checklist

- [ ] `aws sts get-caller-identity` shows the intended account, not root.
- [ ] `sam validate --lint` passes.
- [ ] `make deploy STAGE=dev` succeeds and prints a `FunctionUrl`.
- [ ] `https://<vercel-domain>/api/health` returns `{"status":"ok"}`.
- [ ] The session cookie is `HttpOnly`, `Secure`, `SameSite=Lax`, no `Domain`, and survives a refresh.
- [ ] Sign in with Google works on the production domain for a listed test user (or any user, if the app is published), and on `localhost` against the dev stack.
- [ ] The prod stack's `GOOGLE_CLIENT_ID` equals the `NEXT_PUBLIC_GOOGLE_CLIENT_ID` on Vercel Production, and differs from the dev client.
- [ ] A 20 MB PDF uploads from the deployed frontend straight to S3 (the request goes to `*.s3.*.amazonaws.com`, not to Vercel or the function), with progress.
- [ ] An uploaded file opens in the viewer, and its URL stops working after about 10 minutes.
- [ ] The bucket shows "Block all public access" on, and objects cannot be fetched without a presigned URL.
- [ ] A generation job started from the deployed app finishes and is not run twice (check the worker's logs for one invocation per job).
- [ ] A job whose worker never starts or stalls reads as failed after about 6 minutes, and the user can retry.
- [ ] Every table and index shows on-demand billing in the DynamoDB console, and no index projects all attributes.
- [ ] The API function has no `LLM_API_KEY`, no `GEMINI_API_KEY` and no `JWT_SECRET` on the worker (check each function's configuration); `LLM_PROVIDERS` and `LLM_MODEL` are on both.
- [ ] `GET /api/ai/models` lists exactly the enabled models, cheapest first, with the cheapest enabled one marked as the default when `LLM_MODEL` is empty.
- [ ] The jobs table has `owner-index` and `day-index` (INCLUDE projections), and the AI usage page loads today's jobs through `day-index` without a Scan.
- [ ] Enabling a provider without its key makes `make deploy` stop with a clear message.
- [ ] If reserved concurrency was set, both functions show it; if not, that was a conscious choice (quota too low).
- [ ] `make smoke BASE_URL=https://<vercel-domain>/api` passes against prod.
- [ ] The prod stack has termination protection on.
- [ ] **Gate:** every enabled provider's prod key has a provider-side monthly spend limit of $20 (the Gemini key is on a billing-enabled paid-tier project with a small quota and a budget alert, and its alert-only caveat is accepted), set before the prod deploy, and an AWS budget alert exists.
- [ ] No secret appears in Git history, in `samconfig.toml`, or in Vercel's `NEXT_PUBLIC_` variables.
- [ ] CloudWatch log groups exist for both functions with 14-day retention.
- [ ] No secret appears in the process list or deploy output (`make deploy` uses the generated `.samconfig.<stage>.toml`, which is gitignored).

## 11. Operations

**Budget alert.** In the AWS Billing console, create a budget with an alert at a small amount (for example $5) to your email. It lags by hours, so it is a backstop, not a limit. The LLM spend limits at the providers are the outer cap (Google's may only alert); the app's monthly budget is what refuses work. Review each provider's usage page after the first real day of use and compare it with the in-app AI usage page and the per-job token usage recorded on jobs (`AI-spec.md` section 11).

**Logs.** Each function writes JSON lines to CloudWatch Logs with 14-day retention. To follow them: `sam logs --stack-name exam-assistant-prod --name ApiFunction --tail` (and `--name WorkerFunction`).

**Rollback.**
- Frontend: in Vercel, promote a previous deployment (instant rollback).
- Backend: check out the previous Git commit and run `make deploy STAGE=prod` again. Lambda versions and aliases are not used.

**Data safety.** There are no backups in the MVP (point-in-time recovery for DynamoDB is a paid option **(verify)** and is out of scope). Termination protection stops an accidental stack delete. Treat prod data as disposable until a backup plan exists.

**Monitoring.** None beyond logs and the budget alert. Check the CloudWatch logs for 5xx responses after each deploy.

**Teardown** (needed if the free-plan account is going to close, or to stop all cost):
1. Vercel: delete the project (or leave it; it has no cost on Hobby **(verify)**).
2. Empty the bucket: `aws s3 rm s3://<bucket> --recursive` (`make destroy STAGE=<stage>` should do this first).
3. Turn off termination protection for prod, then `sam delete --stack-name exam-assistant-<stage> --region <region>`.
4. Confirm in the console that no tables, buckets, or log groups remain in any region you used.
5. Revoke every LLM API key (Anthropic and Gemini).

## 12. Security checklist

- MFA on the AWS root user, your AWS identity, GitHub, and Vercel.
- No access keys in Git, in the repository, or in Vercel. If you use IAM access keys on your machine, rotate them if they leak and prefer SSO.
- Lambda roles are the ones in the template: table-scoped DynamoDB actions (the worker can only read `documents` and read and write `jobs`), `GetObject`, `PutObject`, `DeleteObject` on the bucket objects, `ListBucket` on the bucket for the API only, and invoke permission on the worker only. No `*` resources.
- The bucket blocks all public access. Files are served only through short-lived presigned URLs.
- Google OAuth: no client secret exists or is stored. Client IDs are public. Keep separate dev and prod clients, keep the consent screen to basic scopes, and keep the authorized origins list to exactly the domains in use.
- Whoever owns the Google Cloud project can change the sign-in configuration; protect that Google account with MFA too.
- The function URL is public by design; the app authenticates every request, checks the origin on state-changing requests, and uses `HttpOnly`, `Secure`, `SameSite=Lax` cookies.
- Secrets are injected as stack parameters (`NoEcho`) from a generated, gitignored config file (never on the command line) and stored as Lambda environment variables, scoped per function. They are visible to anyone with read access to the function configuration. Keep that access to yourself. Moving them to a secrets service is out of scope for the MVP.
- Every enabled LLM provider key has a provider-side monthly spend limit (hard gate before prod). The Gemini key must be a paid-tier key: free-tier content is used to improve Google's products. Provider keys exist only on the worker; the API holds none.
- Minors' data: complete the privacy review noted in `goals.md` before any real users.

## 13. Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| Browser console shows a CORS error on upload or file view | S3 CORS rule missing or does not list the page's origin | Add the exact origin (scheme, host, no trailing slash) to `FrontendOrigins` and redeploy the stack. Previews are not allowed by default (section 7.5) |
| Upload `PUT` fails with `SignatureDoesNotMatch` | `Content-Type` or file size differs from what was signed, or the URL expired | Send exactly the headers returned by `upload-url`; do not alter the file between request and upload; get a new URL |
| `https://<domain>/api/health` returns 404 or HTML | rewrite missing, `API_URL` unset, or Vercel not redeployed after setting it | Fix `next.config.js` or `API_URL`; redeploy |
| `/api/...` returns 502 or 504 | wrong `API_URL`, trailing slash, or the function failed or timed out | `curl` the `FunctionUrl` directly; read the API function's logs |
| `curl <FunctionUrl>/health` returns 403 | the public permission on the function URL is missing or incomplete | Check the function's resource policy and the URL's auth type **(verify)** |
| Login works but the next request is unauthenticated | cookie not stored: not `Secure` over HTTPS, a `Domain` attribute set, or the browser blocked it | Inspect `Set-Cookie`; confirm `ENV=production` and no `Domain` |
| Every `POST` returns 403 | origin check rejected the page's origin | Add it to `AllowedOrigins` and redeploy the stack |
| Google button missing, or the console says the origin is not allowed | the page's origin is not in the client's authorized origins, or the change has not propagated, or this is a preview URL | Add the exact origin to the right client (section 8.0) and wait; test on the production domain or `localhost` |
| Google sign-in succeeds in the browser but the backend returns 401 | the backend's `GOOGLE_CLIENT_ID` differs from the client ID the frontend used, or the backend cannot reach Google | Compare the two values per stage; check the stack parameter and that the Lambda has internet access (no VPC) |
| Some people can sign in with Google and others cannot | the consent screen is in Testing and the others are not test users, or their school account blocks it | Add them as test users or publish the app; keep email and password available |
| Generation job stays `queued` | the API cannot invoke the worker, `WORKER_FUNCTION_NAME` is wrong, or the worker's reserved concurrency throttled the invoke (dropped, retries are 0) | Check the API role's `lambda:InvokeFunction` and the env var; check the worker's throttle metric. The 6-minute stale rule fails the job so the user can retry |
| A job runs twice | worker retries are on, or duplicate async delivery without the claim check | Confirm `MaximumRetryAttempts: 0`; confirm the worker's conditional `queued -> running` claim exists (`backend-spec.md` section 7.2) |
| A job fails right away, or a model is missing from `GET /ai/models` | the provider is not in `LLM_PROVIDERS`, or the worker has no key for it | Check `LlmProviders` and the key parameters (section 6.3); the worker logs the missing key |
| `complete` returns an unexpected 500 on a missing upload | the API role lacks `s3:ListBucket`, so `HeadObject` returns 403 instead of 404 | Add `s3:ListBucket` on the bucket ARN (it is in the template) |
| `sam deploy` shows wrong parameter values (for example origins split in two) | quoting in `.samconfig.<stage>.toml` | Run with `--debug`, fix `scripts/write-samconfig.sh`; origin lists need the escaped quotes |
| `sam deploy` fails on the bucket | the bucket name is taken or has objects | Use the account ID in the name (already in the template) and empty it before delete |
| New stack cannot be created: resource already exists | a previous stack's tables or bucket still exist | Delete the leftover resources (section 11) or use a different stage name |

## 14. Open items to decide

- **AWS region.** Default `us-east-1`; choose one near your users.
- **Which free-tier program the account is on,** and therefore how long the account lives.
- **Custom domain or the default `vercel.app` domain.**
- **Whether to keep a `dev` stack at all.** It costs one extra stack (next to nothing on-demand); it lets you develop without touching prod data. If you drop it, develop against `prod` or the mock flag.
- **Preview deployments:** visual-only (default) or supported (section 7.5).
- **Production branch name** if not `main`.
- **Google consent screen:** stay in Testing with a test-user list, or publish to Production (and whether that needs a privacy policy and homepage link).
- **Google client layout:** two clients (dev and prod) as recommended, or one shared client.
- **Reserved concurrency:** whether the account's concurrency quota allows it (check Service Quotas); otherwise rely on the quota counters and the spend limit.
- **Gemini key and project:** create a billing-enabled paid-tier Gemini project for each stage with a small quota and a budget alert, and note that Google's limits may only alert. Decide whether `dev` shares a project with `prod` (not recommended).
- **Model IDs and prices:** verify the exact model IDs and current prices for every catalog model on the providers' own pages before the first deploy, and re-check Google's scheduled price changes **(verify)**.
- **Default model:** empty `LlmModel` means the cheapest enabled model (Gemini 3.1 Flash-Lite when Gemini is enabled, else Haiku 4.5). Confirm its quality on the Day 3 golden set; otherwise raise it per stage or per job.
- **Everything marked (verify)** in this file.
