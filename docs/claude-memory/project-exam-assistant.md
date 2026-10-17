---
name: project-exam-assistant
description: Exam Assistant personal project status, repo location, and what blocks deployment
metadata:
  type: project
---

Exam Assistant is a personal full-stack project (Go backend on AWS Lambda/DynamoDB/S3, Next.js frontend on Vercel). Repo: github.com/gokulkumar-gopal/exam-assistant (private). Local copy: ~/aidev/leadership/exam-assistant.

As of 2026-10-17 all application code through the production config checks is merged on `main` and tests pass (Go tests, 442 frontend tests). The frontend has only run against the mock backend. See HANDOFF.md in the repo root for the task-by-task gap list.

**Why:** the deployment track stopped at D-2.1 because it needs the user's own AWS, Google Cloud, Vercel and LLM provider accounts, which the agent cannot create.

**How to apply:** read HANDOFF.md and plans/README.md first. Do not start deploy tasks until the user confirms the accounts exist and `aws sts get-caller-identity` works. Never commit `backend/.env.deploy.*`. The user's personal GitHub account for this repo is `gokulkumar-gopal` (with a hyphen); they confirmed it over the similarly named `gokulkumargopal`.
