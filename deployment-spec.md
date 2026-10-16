# Exam Assistant: Deployment Spec

Audience: whoever (person or coding agent) sets up and ships the app. Read this whole file before provisioning anything.

**Related files**
- `goals.md`: the 3-day plan and constraints. Binding. Section 9 below maps this file onto its days.
- `frontend-spec.md`: section 8.1 defines the `/api/*` rewrite and the Vercel-facing contract.
- `backend-spec.md`: section 12 lists what the backend **requires** from deployment. This file says **how** to provide it. If the two disagree, fix `backend-spec.md` section 12 first.

---

## 1. Decisions

| Concern | Decision |
|---|---|
| Frontend host | **Vercel**, deployed from Git. This is fixed. |
| Backend host | **AWS**: two Lambda functions (API and worker) built from one Go binary, called through a Lambda **Function URL**. No API Gateway. |
| Database | **DynamoDB**, five tables, provisioned capacity (small). |
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
- An account with the LLM provider and an API key. Set a monthly spend limit on that key at the provider.
- A Google account for the Google Cloud Console, to create the OAuth clients for sign-in (free for basic profile sign-in **(verify)**).

**Tools on the machine that deploys**
- Go 1.22 or newer
- AWS CLI v2, configured (`aws configure sso` or `aws configure`) and working (`aws sts get-caller-identity`)
- AWS SAM CLI
- Node.js (LTS) and npm, for the frontend
- `make`, `curl`, `jq`, `openssl`

**Check before starting:** run `aws sts get-caller-identity` and confirm it prints the account you intend to use.

## 4. AWS free tier: what to know

Details **(verify)** on AWS's own pages before relying on them.

- Accounts created after July 15, 2025 are on a **Free plan**: up to $200 in credits (the first $100 at sign-up, up to $100 more for completing tasks), and **the account closes 6 months after it opens, or when credits run out, whichever comes first** unless you upgrade to a Paid plan. Older accounts may still be on the previous 12-month program. Find out which one you have (Billing console) before planning anything long-lived.
- About 30 services, including **Lambda** and **DynamoDB**, have monthly always-free allowances that do not use up credits. DynamoDB's allowance is for **provisioned** capacity (reported as 25 read and 25 write capacity units in total, across tables and indexes).
- **S3** and **CloudWatch Logs** usage at this project's volume is small. Treat S3 as credit-funded.
- **The LLM is not an AWS cost and is not free.** It is the main running cost (about $0.40 to $0.75 per generation job; see `AI-spec.md` section 10.3). The default model is `claude-opus-5-5`; set `LlmModel` to `claude-sonnet-5-5` to roughly halve it. The per-user limits in `backend-spec.md` section 7.5 and the provider-side spend limit are the guards.
- **Google sign-in** (basic profile and email only) has no AWS cost. Verifying tokens means the backend fetches Google's public keys over the internet, which is one more reason not to put the Lambdas in a VPC.
- **Things that cause surprise bills, and that this design avoids on purpose:** NAT gateways (about $30 a month), unattached Elastic IPs, putting Lambdas in a VPC, RDS, extra regions, high-retention logs. Do not add any of these.
- **Budget alerts lag** (hours, sometimes a day). Set one anyway (section 11).

**DynamoDB capacity plan** (assuming a total allowance of 25 read and 25 write units; recheck if yours differs):

| Stage | Units per table or index | Objects | Total |
|---|---|---|---|
| prod | 2 read, 2 write | 5 tables + 3 indexes = 8 | 16 |
| dev | 1 read, 1 write | 8 | 8 |
| **Both** | | | **24 (under 25)** |

If you only keep one stage deployed, give it more. Throttling is acceptable at this scale; the backend retries (`backend-spec.md` section 4). If you decide on on-demand mode instead, it costs cents at demo volume, but it may not be covered by the always-free allowance **(verify)**.

## 5. Repository layout

One repository:

```
exam-assistant/
  goals.md, frontend-spec.md, backend-spec.md, deployment-spec.md, ...
  frontend/            # Next.js app, deployed to Vercel (Vercel "Root Directory" = frontend)
  backend/             # Go module and SAM template
    template.yaml
    samconfig.toml     # non-secret deploy settings only
    Makefile           # build, test, run, smoke, deploy, destroy
    ...
  .gitignore
```

`.gitignore` must include: `.env*` (all environment files), `backend/.aws-sam/`, `frontend/.next/`, `node_modules/`, `backend/.env.deploy.*`.

**Secrets never go in Git.** They live in two places only: per-stage files on the deploying machine (`backend/.env.deploy.dev`, `backend/.env.deploy.prod`, both gitignored) and, once deployed, the Lambda configuration. Frontend has no secrets (`API_URL` is not secret but is server-side only).

## 6. AWS stack (SAM)

### 6.1 Build

The template builds with SAM's `makefile` build method so the build is explicit. `backend/Makefile` must provide these targets (SAM calls them by function name):

```make
build-ApiFunction build-WorkerFunction:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -tags lambda.norpc -o $(ARTIFACTS_DIR)/bootstrap ./cmd/lambda
```

Runtime is `provided.al2023`, architecture `arm64`, handler `bootstrap`. Both functions run the same binary; `MODE` picks the behaviour.

### 6.2 Reference template

A starting point, not gospel. Run `sam validate --lint` and fix whatever it reports. Where it differs from `backend-spec.md` section 12, the spec wins.

```yaml
AWSTemplateFormatVersion: '2010-09-09'
Transform: AWS::Serverless-2016-10-31
Description: Exam Assistant backend

Parameters:
  Stage:           { Type: String, AllowedValues: [dev, prod] }
  JwtSecret:       { Type: String, NoEcho: true, MinLength: 32 }
  LlmApiKey:       { Type: String, NoEcho: true }
  LlmModel:        { Type: String, Default: claude-opus-5-5 }
  GoogleClientId:  { Type: String, Description: "Google OAuth client ID that sign-in tokens must be issued for" }
  AllowedOrigins:  { Type: String, Description: "Comma-separated, e.g. https://app.example.com,http://localhost:3000" }
  FrontendOrigins: { Type: CommaDelimitedList, Description: "Same origins, for S3 CORS" }
  TableUnits:      { Type: Number, Default: 2 }

Globals:
  Function:
    Runtime: provided.al2023
    Architectures: [arm64]
    Handler: bootstrap
    MemorySize: 1024
    Environment:
      Variables:
        ENV: production
        STAGE: !Ref Stage
        JWT_SECRET: !Ref JwtSecret
        LLM_API_KEY: !Ref LlmApiKey
        LLM_MODEL: !Ref LlmModel
        GOOGLE_CLIENT_ID: !Ref GoogleClientId
        ALLOWED_ORIGINS: !Ref AllowedOrigins
        S3_BUCKET: !Ref UploadsBucket
        WORKER_FUNCTION_NAME: !Sub "exam-assistant-${Stage}-worker"
        GEN_MAX_RUNNING: "2"
        GEN_MAX_PER_DAY: "20"

Resources:
  ApiFunction:
    Type: AWS::Serverless::Function
    Metadata: { BuildMethod: makefile }
    Properties:
      FunctionName: !Sub "exam-assistant-${Stage}-api"
      CodeUri: .
      Timeout: 29
      FunctionUrlConfig: { AuthType: NONE }
      Environment: { Variables: { MODE: api } }
      Policies:
        - Statement:
            - Effect: Allow
              Action: [dynamodb:GetItem, dynamodb:PutItem, dynamodb:UpdateItem, dynamodb:DeleteItem,
                       dynamodb:Query, dynamodb:BatchGetItem, dynamodb:ConditionCheckItem]
              Resource:
                - !GetAtt UsersTable.Arn
                - !GetAtt DocumentsTable.Arn
                - !GetAtt ExamsTable.Arn
                - !Sub "${ExamsTable.Arn}/index/*"
                - !GetAtt AttemptsTable.Arn
                - !Sub "${AttemptsTable.Arn}/index/*"
                - !GetAtt JobsTable.Arn
                - !Sub "${JobsTable.Arn}/index/*"
            - Effect: Allow
              Action: [s3:PutObject, s3:GetObject, s3:DeleteObject]
              Resource: !Sub "${UploadsBucket.Arn}/*"
            - Effect: Allow
              Action: lambda:InvokeFunction
              Resource: !GetAtt WorkerFunction.Arn

  WorkerFunction:
    Type: AWS::Serverless::Function
    Metadata: { BuildMethod: makefile }
    Properties:
      FunctionName: !Sub "exam-assistant-${Stage}-worker"
      CodeUri: .
      Timeout: 600
      Environment: { Variables: { MODE: worker } }
      EventInvokeConfig:
        MaximumRetryAttempts: 0          # a retried job would run the LLM calls twice
        MaximumEventAgeInSeconds: 3600
      Policies:
        - Statement:
            - Effect: Allow
              Action: [dynamodb:GetItem, dynamodb:PutItem, dynamodb:UpdateItem, dynamodb:Query, dynamodb:BatchGetItem]
              Resource:
                - !GetAtt DocumentsTable.Arn
                - !GetAtt JobsTable.Arn
                - !Sub "${JobsTable.Arn}/index/*"
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
      AttributeDefinitions: [{ AttributeName: id, AttributeType: S }]
      KeySchema: [{ AttributeName: id, KeyType: HASH }]
      ProvisionedThroughput: { ReadCapacityUnits: !Ref TableUnits, WriteCapacityUnits: !Ref TableUnits }

  DocumentsTable:
    Type: AWS::DynamoDB::Table
    Properties:
      TableName: !Sub "${Stage}-documents"
      AttributeDefinitions: [{ AttributeName: ownerId, AttributeType: S }, { AttributeName: id, AttributeType: S }]
      KeySchema: [{ AttributeName: ownerId, KeyType: HASH }, { AttributeName: id, KeyType: RANGE }]
      TimeToLiveSpecification: { AttributeName: ttl, Enabled: true }
      ProvisionedThroughput: { ReadCapacityUnits: !Ref TableUnits, WriteCapacityUnits: !Ref TableUnits }

  ExamsTable:
    Type: AWS::DynamoDB::Table
    Properties:
      TableName: !Sub "${Stage}-exams"
      AttributeDefinitions: [{ AttributeName: id, AttributeType: S }, { AttributeName: creatorId, AttributeType: S }]
      KeySchema: [{ AttributeName: id, KeyType: HASH }]
      ProvisionedThroughput: { ReadCapacityUnits: !Ref TableUnits, WriteCapacityUnits: !Ref TableUnits }
      GlobalSecondaryIndexes:
        - IndexName: creator-index
          KeySchema: [{ AttributeName: creatorId, KeyType: HASH }, { AttributeName: id, KeyType: RANGE }]
          Projection: { ProjectionType: ALL }
          ProvisionedThroughput: { ReadCapacityUnits: !Ref TableUnits, WriteCapacityUnits: !Ref TableUnits }

  AttemptsTable:
    Type: AWS::DynamoDB::Table
    Properties:
      TableName: !Sub "${Stage}-attempts"
      AttributeDefinitions:
        - { AttributeName: examId, AttributeType: S }
        - { AttributeName: userId, AttributeType: S }
      KeySchema: [{ AttributeName: examId, KeyType: HASH }, { AttributeName: userId, KeyType: RANGE }]
      ProvisionedThroughput: { ReadCapacityUnits: !Ref TableUnits, WriteCapacityUnits: !Ref TableUnits }
      GlobalSecondaryIndexes:
        - IndexName: user-index
          KeySchema: [{ AttributeName: userId, KeyType: HASH }, { AttributeName: examId, KeyType: RANGE }]
          Projection: { ProjectionType: ALL }
          ProvisionedThroughput: { ReadCapacityUnits: !Ref TableUnits, WriteCapacityUnits: !Ref TableUnits }

  JobsTable:
    Type: AWS::DynamoDB::Table
    Properties:
      TableName: !Sub "${Stage}-jobs"
      AttributeDefinitions: [{ AttributeName: id, AttributeType: S }, { AttributeName: ownerId, AttributeType: S }]
      KeySchema: [{ AttributeName: id, KeyType: HASH }]
      TimeToLiveSpecification: { AttributeName: ttl, Enabled: true }
      ProvisionedThroughput: { ReadCapacityUnits: !Ref TableUnits, WriteCapacityUnits: !Ref TableUnits }
      GlobalSecondaryIndexes:
        - IndexName: owner-index
          KeySchema: [{ AttributeName: ownerId, KeyType: HASH }, { AttributeName: id, KeyType: RANGE }]
          Projection: { ProjectionType: ALL }
          ProvisionedThroughput: { ReadCapacityUnits: !Ref TableUnits, WriteCapacityUnits: !Ref TableUnits }

Outputs:
  FunctionUrl: { Value: !GetAtt ApiFunctionUrl.FunctionUrl }
  BucketName:  { Value: !Ref UploadsBucket }
  WorkerName:  { Value: !Ref WorkerFunction }
```

**Notes on the template**
- **Data layer is five tables and three indexes.** `users` has no index (login uses the email lock item, `backend-spec.md` section 6.2).
- **`ENV: production`** is set for both stages so the session cookie always has the `Secure` flag when deployed. Only a locally run server uses `ENV=local`.
- **Worker retries are off on purpose.** The default for asynchronous invocation is to retry twice, which would run a generation job (and pay for its LLM calls) up to three times.
- **No VPC, no API Gateway, no Cognito, no queue.** Do not add them.
- **Public function URL.** `AuthType: NONE` means anyone with the URL can call it; the app authenticates every request itself. Keep the URL out of public docs, and know it is reachable directly, not only through Vercel. If a newly created function URL returns `403`, check its resource policy: AWS has tightened what the permission for public URLs requires **(verify)**.
- **Memory** is 1024 MB for both because PDF page counting happens in the API function and reads the whole file. Lower it after measuring.
- **Termination protection on prod** (section 8.3) is the safeguard against deleting the stack and the data with it. Without it, deleting the stack deletes the tables.
- **Bucket deletion** fails while the bucket has objects, which is a useful guard. `make destroy` must empty the bucket first on purpose (section 11).

### 6.3 Parameters per stage

| Parameter | dev | prod |
|---|---|---|
| `Stage` | `dev` | `prod` |
| `TableUnits` | `1` | `2` |
| `AllowedOrigins` | `http://localhost:3000` | the production Vercel domain, for example `https://exam-assistant.vercel.app`, plus any custom domain, and `http://localhost:3000` if you want local frontend against prod (not recommended) |
| `FrontendOrigins` | same list as `AllowedOrigins` | same list as `AllowedOrigins` |
| `JwtSecret` | random, different per stage | random, different per stage |
| `LlmApiKey` | a key with a low spend limit | a key with a spend limit sized to your budget |
| `GoogleClientId` | the **dev** OAuth client ID (authorized origin: `http://localhost:3000`) | the **prod** OAuth client ID (authorized origin: the production Vercel domain) |

Generate a secret with `openssl rand -base64 48`. Never reuse the prod secret in dev.

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

- `frontend/.env.local` (gitignored): `API_URL=<dev FunctionUrl>` to use the dev stack, or `API_URL=http://localhost:8080` to use a locally running backend (`make run` in `backend/`, which needs AWS credentials and the dev tables), or the mock flag from `frontend-spec.md` section 8.1 to work with no backend at all.
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
JWT_SECRET=<openssl rand -base64 48>
LLM_API_KEY=<dev key>
LLM_MODEL=claude-opus-5-5
ALLOWED_ORIGINS=http://localhost:3000
GOOGLE_CLIENT_ID=<dev OAuth client ID>
```
```
# backend/.env.deploy.prod
JWT_SECRET=<a different random value>
LLM_API_KEY=<prod key>
LLM_MODEL=claude-opus-5-5
ALLOWED_ORIGINS=https://exam-assistant.vercel.app,http://localhost:3000
GOOGLE_CLIENT_ID=<prod OAuth client ID>
```

4. Set a budget alert (section 11) before creating anything else.

### 8.2 `make deploy` (backend)

`backend/Makefile` provides:

```make
STAGE ?= dev
AWS_REGION ?= us-east-1
include .env.deploy.$(STAGE)
export

deploy:
	sam build
	sam deploy \
	  --stack-name exam-assistant-$(STAGE) --region $(AWS_REGION) \
	  --capabilities CAPABILITY_IAM --resolve-s3 --no-confirm-changeset --no-fail-on-empty-changeset \
	  --parameter-overrides Stage=$(STAGE) JwtSecret=$$JWT_SECRET LlmApiKey=$$LLM_API_KEY \
	      LlmModel=$$LLM_MODEL GoogleClientId=$$GOOGLE_CLIENT_ID AllowedOrigins=$$ALLOWED_ORIGINS FrontendOrigins=$$ALLOWED_ORIGINS \
	      TableUnits=$(if $(filter prod,$(STAGE)),2,1)
	@aws cloudformation describe-stacks --stack-name exam-assistant-$(STAGE) --region $(AWS_REGION) \
	  --query "Stacks[0].Outputs" --output table
```

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

1. Create `backend/.env.deploy.prod` with the production Vercel domain in `ALLOWED_ORIGINS`.
2. `make deploy STAGE=prod`. Copy the `FunctionUrl`.
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
| 3 | Redeploy fixes with `make deploy` and Git pushes; run the smoke test after each |

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
- [ ] `make smoke BASE_URL=https://<vercel-domain>/api` passes against prod.
- [ ] The prod stack has termination protection on.
- [ ] A budget alert exists and the LLM key has a spend limit.
- [ ] No secret appears in Git history, in `samconfig.toml`, or in Vercel's `NEXT_PUBLIC_` variables.
- [ ] CloudWatch log groups exist for both functions with 14-day retention.
- [ ] Combined DynamoDB provisioned units across all deployed stages are within the free allowance (24 of 25 with the plan in section 4).

## 11. Operations

**Budget alert.** In the AWS Billing console, create a budget with an alert at a small amount (for example $5) to your email. It lags by hours, so it is a backstop, not a limit.

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
5. Revoke the LLM API key.

## 12. Security checklist

- MFA on the AWS root user, your AWS identity, GitHub, and Vercel.
- No access keys in Git, in the repository, or in Vercel. If you use IAM access keys on your machine, rotate them if they leak and prefer SSO.
- Lambda roles are the ones in the template: table-scoped DynamoDB actions, `GetObject`, `PutObject`, `DeleteObject` on the bucket only, and invoke permission on the worker only. No `*` resources.
- The bucket blocks all public access. Files are served only through short-lived presigned URLs.
- Google OAuth: no client secret exists or is stored. Client IDs are public. Keep separate dev and prod clients, keep the consent screen to basic scopes, and keep the authorized origins list to exactly the domains in use.
- Whoever owns the Google Cloud project can change the sign-in configuration; protect that Google account with MFA too.
- The function URL is public by design; the app authenticates every request, checks the origin on state-changing requests, and uses `HttpOnly`, `Secure`, `SameSite=Lax` cookies.
- Secrets are injected as stack parameters (`NoEcho`) and stored as Lambda environment variables, which are visible to anyone with read access to the function configuration. Keep that access to yourself. Moving them to a secrets service is out of scope for the MVP.
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
| Generation job stays `queued` | the API cannot invoke the worker, or `WORKER_FUNCTION_NAME` is wrong | Check the API role's `lambda:InvokeFunction` on the worker and the env var |
| A job runs twice | worker retries are on | Confirm `MaximumRetryAttempts: 0` on the worker |
| Frequent throttling errors | provisioned units too low | Raise `TableUnits` for that stage, keeping the total within the allowance |
| `sam deploy` fails on the bucket | the bucket name is taken or has objects | Use the account ID in the name (already in the template) and empty it before delete |
| New stack cannot be created: resource already exists | a previous stack's tables or bucket still exist | Delete the leftover resources (section 11) or use a different stage name |

## 14. Open items to decide

- **AWS region.** Default `us-east-1`; choose one near your users.
- **Which free-tier program the account is on,** and therefore how long the account lives.
- **Custom domain or the default `vercel.app` domain.**
- **Whether to keep a `dev` stack at all.** It costs one extra stack and 8 capacity units; it lets you develop without touching prod data. If you drop it, give `prod` more capacity and develop against `prod` or the mock flag.
- **Preview deployments:** visual-only (default) or supported (section 7.5).
- **Production branch name** if not `main`.
- **Google consent screen:** stay in Testing with a test-user list, or publish to Production (and whether that needs a privacy policy and homepage link).
- **Google client layout:** two clients (dev and prod) as recommended, or one shared client.
- **Everything marked (verify)** in this file.
