# Handoff: where development stands

Written 2026-10-17 so work can resume on another machine. Source of truth for scope and order is `plans/README.md`; for the API contract, `backend-spec.md` section 8.

## State at handoff

- All application code for milestones M0 to M5b and the production config checks is merged on `main` (commit `ca1a97f`, 197 commits).
- Backend: `cd backend && go test ./...` passes in every package that has tests.
- Frontend: `cd frontend && npx vitest run` passes (35 files, 442 tests).
- The frontend has only been exercised against the mock backend (`MOCK_API=true npm run dev`). Nothing has run against real AWS or real LLM providers.

## Plan tasks with no commit message naming them

Some of these may be done under a different message. Check the code before redoing.

| Track | Not found in git history by ID |
|---|---|
| Backend | B-7.1, B-7.2 (FIX phase: golden-set run, cost check, bug list) |
| Frontend | F-0.1, F-0.4, F-0.5, F-1.1 to F-1.4, F-5b.1 to F-5b.3, F-6.1, F-7.1. Peeks UI (F-5b) and the F-7 resilience pass were merged, so some of these are probably done. F-6.1 is likely the switch off the mock flag for production. |
| Deployment | Everything except D-1.1, D-1.2, D-2.1. D-0.x, D-1.3, D-2.2 onward are mostly console and account steps. |

## What needs a human (cannot be done by the agent)

1. AWS: install the AWS CLI and SAM CLI, sign in with a non-root identity with MFA, pick a region, create a roughly $5 budget alert (task D-0.1).
2. Google Cloud: project, consent screen, web client `exam-assistant-dev` with origin `http://localhost:3000` (D-0.6).
3. Vercel: import the repo, root directory `frontend`, set `API_URL` and `NEXT_PUBLIC_GOOGLE_CLIENT_ID` (D-0.6).
4. LLM keys: an Anthropic dev key with a low monthly limit, and a Gemini key on a billing-enabled paid-tier project, never free tier (D-2.2).
5. Put secrets only in the gitignored `backend/.env.deploy.dev`. Never commit them.
6. Before any prod deploy: a $20 monthly limit on each provider key (D-6.1).

## Suggested next steps

1. Run both test suites on the new machine to confirm a clean checkout.
2. Verify the unconfirmed frontend tasks above against the code.
3. After the human setup, resume the deployment track at D-0.5 (dev stack health round trip).
4. Turn the frontend mock flag off screen by screen as each backend milestone passes on the dev stack.
5. Run the FIX phase: golden set on the cheap models, compare the cost with the provider usage page, rehearse rollback on dev.

## Known risks (from the PRD)

OCR quality on camera photos, question quality and difficulty mapping, free-text grading, and minors' data privacy (COPPA and GDPR-K).

## Known unvalidated items (from plans/README.md)

The SAM template and `write-samconfig.sh` have never been run (use `sam validate --lint` first). Verify model IDs and prices before relying on the catalog. Verify the Gemini Go SDK types against its docs. `GEN_MAX_GLOBAL_PER_DAY` is an assumption.

## Resuming with Claude Code

Open Claude Code in the repo root. Copy `docs/claude-memory/project-exam-assistant.md` into your Claude memory directory for this project if you want the same background. The plans were written for `superpowers:subagent-driven-development`.
