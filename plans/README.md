# Exam Assistant: Implementation Plans and Sequencing

Three track plans implement the specs. This note says what order to build them in and where the tracks meet.

| Track | Plan | Task IDs | Implements |
|---|---|---|---|
| Backend | [backend-plan.md](backend-plan.md) | `B-<m>.<n>` | backend-spec.md, AI-spec.md |
| Frontend | [frontend-plan.md](frontend-plan.md) | `F-<m>.<n>` | frontend-spec.md |
| Deployment | [deployment-plan.md](deployment-plan.md) | `D-<m>.<n>` | deployment-spec.md |

`<m>` is the milestone number below: 0 to 5, `5a` and `5b` for the two M5 parts, 6 for PROD, 7 for FIX.

**Days are guidance only.** goals.md says three days. You may finish in two. Nothing below is tied to a day: a milestone is done when its check passes, then you start the next one. The only fixed things are the order and the dependencies.

Source of truth: goals.md, backend-spec.md (its section 8 is the API contract), frontend-spec.md, deployment-spec.md, AI-spec.md. `verification-spec.md` is not part of these plans.

## Milestones

| # | Name | Done when |
|---|---|---|
| M0 | Skeletons | A signed-in browser round trip works against the dev stack: login through the Vercel `/api` rewrite sets a cookie, `/health` answers, and the Google dev client is created. Target: the first hour. |
| M1 | Documents | A user can sign up or log in (password and Google), upload a PDF and a camera photo, see them in the library, view them, and delete them. |
| M2 | Generation | A real PDF and a real camera photo generate a question set on the default model. The model select works, the budget guard and quotas are enforced, the user can edit and regenerate questions. Gemini is the default if its key is enabled, Claude is the fallback. |
| M3 | Exams | A user publishes an exam, assigns it (role rules enforced), the assignee sees it under Open and can decline it, the creator sees status. |
| M4 | Taking and printing | An assignee takes a timed exam with autosave and auto-submit, the grace window and answer-key rule hold, the result screen shows, and the print view prints three clean parts. Retakes (B-4.7, F-4.6): a submitted exam can be retaken any number of times until it closes, every attempt keeps its answers and marks, and the creator sees best score and attempt count. |
| M5a | Usage (P1) | Per-model usage counters feed `GET /ai/usage` and the `/usage` page updates live. The monthly spend counter and budget guard are built earlier, in M2, and are not cuttable. |
| M5b | Peeks (P1) | Reveal, half-mark scoring and the header counter work. |
| PROD | Production deploy | Spend-limit gates pass, the prod stack is up, the prod Google client and Vercel production are wired, and the device smoke test passes. |
| FIX | Test and fix | The golden set has run on the cheap models, the cost check matches the provider usage page, and the bug list is empty. |

## Build order

```
M0 -> M1 -> M2 -> M3 -> M4 -> M5a -> M5b -> PROD -> FIX
```

Run the three tracks in parallel inside a milestone, but a milestone only starts when the previous one's checks pass on all three tracks. PROD can start after M4 if M5 is cut.

## Integration points

1. **The contract is backend-spec section 8.** Neither track changes it alone. If a plan task finds the contract wrong, fix backend-spec.md first, then both tracks.
2. **Frontend runs on the mock flag until the matching backend slice is on the dev stack.** A frontend milestone's screens are built against mock data. Its milestone check is then repeated with the flag off against the dev stack. The flag is only switched off for a screen once that backend milestone's check has passed.
3. **Deployment stays about one slice ahead.** The dev stack for milestone N (tables, bucket, worker, secrets, parameters) is in place before backend milestone N needs to be tried on it. Backend work always passes `go test ./...` with no AWS access first; the dev stack is for `make smoke` and the frontend.
4. **Cross-track needs are written at milestone level**, for example "Needs: B-2 complete on dev stack". Task IDs are only referenced inside their own track.

| Frontend milestone needs | From backend | From deployment |
|---|---|---|
| F-0 | B-0 `/health` | D-0 dev stack, Vercel, rewrite |
| F-1 | B-1 auth and documents | D-1 bucket, CORS, tables |
| F-2 | B-2 generation and `/ai/models` | D-2 worker, provider keys, `LlmProviders` |
| F-3 | B-3 exams | none new |
| F-4 | B-4 taking, result | none new |
| F-5a | B-5a `/ai/usage` | none new (`day-index` is created with the other indexes in D-1.1) |
| F-5b | B-5b reveal | none new |

## Cut order

If time runs out, cut in this order (goals.md, Deferred): peeks (M5b) first, then the usage page and `GET /ai/usage` (M5a). Everything else is P0. The monthly spend counter and budget guard stay, because the guard needs them. The model select and the cheapest-model default are P0.

PROD gates are never cut: both provider spend limits ($20 monthly) before the prod deploy.

## Known unvalidated items to check in the first task that touches them

- The SAM template and `write-samconfig.sh` have never been run: validate with `sam validate --lint` and `sam deploy --debug` in the first deployment task that uses them.
- Model IDs and prices: verify against the provider pages before B-2 codes the catalog.
- Gemini Go SDK types (`google.golang.org/genai`): verify against the SDK docs before the Gemini generator task. Do not guess field names.
- The default global daily cap (`GEN_MAX_GLOBAL_PER_DAY`) is an assumption in the spec.

## Executing

Each plan is written for `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans`. Nothing is committed to git until the plans are approved.
