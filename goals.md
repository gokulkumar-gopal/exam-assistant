# Exam Assistant: Goals

A responsive full-stack web app where Students, Parents, and Teachers upload study material, generate questions of varying difficulty from it, assemble timed exams, and take them.

Priority: **P0** = MVP, **P1** = fast follow.

## Project Constraint (binding on all tech choices)

**This is a 3-day project built by 1 person, with coding agents doing the implementation.** Every technology, architecture, and scope decision must fit that budget. When two options both work, pick the one that is faster to build and has less to operate.

Rules for coding agents:

1. **Exactly two deployables, no more.** (a) A **frontend**: a Next.js app deployed to **Vercel**. (b) A **backend**: one service deployed to **AWS**. No microservices, no cron jobs, and no queues or workers except the single mechanism question-generation jobs need (rule 8).
2. **Two languages, one per deployable.** The frontend is TypeScript: Next.js (App Router) with Tailwind and shadcn/ui. The backend is one Go service on the standard library (`backend-spec.md`). The frontend holds **no business logic and no database access**; it only renders UI and calls the backend API.
3. **Simplest possible data layer, on AWS.** **DynamoDB** is the database (no other database, and no `Scan` in request paths), and one S3 bucket holds files. **No SQLite and no local disk**: neither persists on Vercel or on serverless hosts. No caching layer, no search engine.
4. **Buy, don't build.** Use managed services instead of writing them. Files go to S3 through presigned URLs (the browser uploads directly, never through the app server). Auth is the backend's own session (a backend-set cookie) with two ways in: **email and password**, and **Sign in with Google** (Google Identity Services in the browser, ID-token verification in the backend). No managed identity service such as Cognito. Use one LLM API, through its official Go SDK, as set out in `AI-spec.md`.
5. **Let the LLM do the heavy lifting.** Question generation is done by an LLM: the backend sends the selected files to a vision-capable LLM, which reads PDFs and images directly and returns the questions. Do not build a separate OCR pipeline unless the LLM approach fails. All AI requirements are in `AI-spec.md`.
6. **Use browser-native features first.** Camera capture via `<input type="file" accept="image/*" capture>`, PDF viewing via PDF.js, and responsive layout via Tailwind with shadcn/ui.
7. **Minimal AWS footprint.** Use the fewest services that work: Lambda (an API function and a worker function from one Go binary, called through a Function URL), DynamoDB, and one S3 bucket per stage. No VPC, no API Gateway, no Kubernetes, no multi-region, no custom CI. The setup is a single SAM template plus `make deploy`, defined in `deployment-spec.md`, because one person has to be able to rebuild it on demand.
8. **Compute instead of schedule, and jobs instead of long requests.** Exam closing and auto-submit are decided on read by comparing the deadline to the current time, not by background jobs. Question generation is the one exception: it runs as a **job** (the API returns a job id immediately and the frontend polls for progress), so it works under any gateway or proxy timeout.
9. **Frontend and backend meet at one contract.** The API names, shapes, and the Vercel `/api/*` rewrite are defined in `frontend-spec.md` (sections 8 and 8.1). The backend implements that contract; changes go through that file first.
10. **Minimal dependencies.** Prefer well-known, well-documented libraries that agents know well. Any choice expected to take more than about 1 hour to set up needs a one-line justification in the PR or commit.
11. **Cut scope before adding complexity.** If a goal threatens the 3-day budget, move it to Deferred rather than building a heavier solution.
12. **Demo-grade is acceptable** for non-functional items (see Deferred), but nothing may leave user data publicly accessible. S3 buckets are private, and files are served only through short-lived presigned URLs.

## Product Goals

- [ ] Give students enough challenging practice material to get good at a subject.
- [ ] Give teachers a way to generate stimulating problems beyond the textbook.
- [ ] Give parents visibility into, and a way to co-work on, a student's preparation.

## Goals by Area

### 1. Accounts and Roles
- [ ] **P0** Sign up and log in with email and password, as Student, Parent, or Teacher.
- [ ] **P0** Sign up and log in with Google. A new Google user chooses their role at first sign-up.
- [ ] **P0** Signing in with Google reuses an existing account that has the same verified email (and replaces its password sign-in with Google).
- [ ] **Deferred** Parent can link one or more student accounts (student must accept).

### 2. Document Management (all roles)
- [ ] **P0** Upload one or many files (PDF, TXT, JPG, PNG; up to 25 MB each), directly to S3 via presigned URLs.
- [ ] **P0** Take a photo with the device camera and save it to the library as a file.
- [ ] **P0** Browse all my documents in one library, with sort and search.
- [ ] **P0** Delete my own documents (with confirmation).
- [ ] **P0** Keep each user's documents private, enforced by owner and role.
- [ ] **P1** Organize documents with folders or tags, and rename them.

### 3. Document Viewer
- [ ] **P0** Open PDFs, scans, and photos inside the app, with no download.
- [ ] **P0** Scroll multi-page documents, and zoom images, within the viewer.
- [ ] **P1** Launch "Generate questions" from the viewer with the open file pre-selected.

### 4. Question Generation
- [ ] **P0** Select one or more files as sources.
- [ ] **P0** Choose multiple choice or free text.
- [ ] **P0** Set a total marks target (for example 30, 40, 50).
- [ ] **P0** Set the number of questions per mark value, from 1 to 5 marks, and block generation if the total doesn't match the target.
- [ ] **P0** Generate questions from the selected files, at varying complexity, each showing its marks and source.
- [ ] **P0** Order questions ascending by marks by default, with an optional shuffle.
- [ ] **P1** Regenerate, edit, or remove individual questions before saving.

### 4a. AI question generation (detailed in `AI-spec.md`)
- [ ] **P0** When the user generates questions, the backend sends the content of the selected files to an LLM and gets back the list of questions. The LLM is the only source of generated questions.
- [ ] **P0** The LLM is called only from the backend. The API key, prompts, and raw model output never reach the browser.
- [ ] **P0** The result has exactly the requested number of questions at each mark value, in the requested type (multiple choice with four options and one correct answer, or free text with a model answer).
- [ ] **P0** Questions use only the supplied files (nothing invented), are readable without the file in front of the student, and each names the file it came from.
- [ ] **P0** Difficulty follows marks: 1 mark is recall, 5 marks is extended reasoning.
- [ ] **P0** Our code checks every model response (structure, counts, marks, options, duplicates) and drops bad questions. If the count cannot be met after one retry, the job fails with a clear message and no partial exam.
- [ ] **P0** Text inside uploaded files is treated as content, never as instructions to the model.
- [ ] **P0** Each job is bounded: file size and page caps, a cap on questions per call, per-user running and daily limits. Token usage is recorded for every job.
- [ ] **P0** The user is told near the Generate button that the selected files are sent to an AI service.
- [ ] **P1** Regenerate or add a single question without repeating the questions already in the exam.
- [ ] **P1** The model and its settings are changeable by environment variable, and a manual evaluation on sample files exists (`AI-spec.md` section 13).

### 5. Create Test/Exam
- [ ] **P0** Configure sources, question type, marks, and order in one create-exam flow.
- [ ] **P0** Set a title and a closing deadline (minutes, hours, or days). After it passes, the exam can't be taken.
- [ ] **P0** Optionally assign the exam to zero, one, or many users.
- [ ] **P0** Create an answer key, manually or auto-drafted and editable, with a visibility rule.
- [ ] **P1** Edit or delete an exam before anyone has started it.

### 6. Exam Lists
- [ ] **P0** Open Exams page: all exams applicable to me, with time remaining.
- [ ] **P0** Closed Exams page: ended exams, with status and score.
- [ ] **P0** Creator sees per-assignee status (not started, in progress, submitted).

### 7. Taking an Exam
- [ ] **P0** Complete the exam in one sitting and submit.
- [ ] **P0** Save progress and resume later where I left off, within the deadline.
- [ ] **P0** View the answer key according to the exam's visibility rule.
- [ ] **P0** Auto-grade multiple choice on submission and show the score.
- [ ] **P1** Creator manually marks free-text answers.
- [ ] **Deferred** Parent sees a linked student's exam status and scores.

### 8. Non-functional
- [ ] Responsive on phone, tablet, and desktop.
- [ ] Secure file storage, with access enforced server-side.
- [ ] Upload retry on poor connections without corrupting the library.
- [ ] Question generation runs as a job with visible progress, finishes in under about 60 seconds, can be cancelled, and fails cleanly.
- [ ] The exam timer and deadline are based on server time, not the device clock.
- [ ] Reads and writes recover from a briefly unreachable backend without losing the user's input.
- [ ] Scans and photos are read by a vision-capable LLM (no separate OCR pipeline). A poor-quality warning is deferred.

## Delivery Phases

Re-scoped to the 3-day, 1-person constraint, with a Vercel frontend and an AWS backend.

| Day | Goal | Done when |
|---|---|---|
| 1. Build | Build every P0 goal in vertical slices, in this order: (1) documents: scaffold both deployables, login (email and password, and Google) with a role, upload including camera, library, viewer, delete; (2) question generation: pick files, type, marks breakdown, ordering and shuffle, as a job with polling; (3) exams: create with deadline and answer key, assign by email, Open and Closed lists; (4) taking exams: autosave and resume, deadline enforcement, answer key view, MCQ auto-grade. The frontend can use its mock-data flag until each backend slice is ready | All P0 goals work end to end on the local frontend against the backend. |
| 2. Deploy | Production AWS setup (DynamoDB tables, S3 bucket and its CORS rule, the two Lambda functions, secrets, LLM key), production Google OAuth client and its authorized origins, set `API_URL` and `NEXT_PUBLIC_GOOGLE_CLIENT_ID` on Vercel, fix production-only issues (cookies through the rewrite, presigned URLs, timeouts), smoke test on a phone and a desktop | The deployed app passes the same end-to-end flow as local. |
| 3. Test and fix | Test with real PDFs and camera photos, tune question quality, fix bugs, cut or finish anything left from the Deferred list | A teacher assigns an exam, a student takes it across two sessions, and sees a score, on the deployed app. |

Day 1 checkpoints:
- **First hour:** deploy an empty frontend skeleton to Vercel and a backend health endpoint to AWS, with the `/api/*` rewrite connecting them, and confirm a login round-trip works. Create the dev Google OAuth client at the same time (about 30 to 45 minutes of console work; see `deployment-spec.md` section 8.0). This is where cookie, rewrite, AWS, and Google setup problems show up, so do not postpone it.
- **Midday:** generate questions from one real PDF and one real camera photo. If quality is poor, fix the prompt or approach before building exams on top of it.

If Day 1 runs long, cut from the end of the slice list in the order given under Deferred. Do not extend the schedule. Anything not built by the end of Day 1 moves to Deferred, and Day 3 is for fixing, not building.

## Deferred (outside the 3-day scope)

Cut in this order, first item first:

- Parent-student account linking and parent progress view (parents can still use every other feature as ordinary users).
- Folders, tags, and renaming (B-5).
- Launching "Generate questions" from the viewer.
- Editing, regenerating, or removing individual generated questions.
- Manual marking of free-text answers (the student self-checks against the key).
- Editing or deleting an exam after creation.
- HEIC support and non-essential file formats. JPG, PNG, PDF, and TXT are enough.
- OCR quality warnings and tuned generation time targets.
- Password reset, adding a password to a Google-only account, and other sign-in providers.

Note: `frontend-spec.md` already describes the viewer's Generate button, per-question edit, regenerate, and delete, and the "Add a k-mark question?" notice. Build those last, and drop them first if time runs short. Do not drop upload retry on failure (the spec requires it); only the fancier retry-with-backoff behaviour can be trimmed.

## Assumptions to Confirm

- Answer key: creator chooses visibility. Default is anytime for self-made exams and after close for assigned ones.
- Parents see linked students' exams and scores, but not private documents.
- MVP grading: multiple choice is automatic, and free text is self-checked or manually marked.
- Timer is a fixed deadline. Saving doesn't pause it, and saved answers are auto-submitted at the deadline.
- File limit is 25 MB, in PDF, TXT, JPG, or PNG.
- Both sign-in methods are offered. Google is the lower-friction path; email and password stays because many students are minors or use school accounts that may not allow Google sign-in.
- A new Google user without a chosen role is asked for one before the account is created.
- Roles are self-selected and unverified in the MVP, for both sign-in methods.
- The LLM is Anthropic Claude through the official Go SDK. The default model is `claude-opus-5-5`; the cheaper `claude-sonnet-5-5` is chosen by changing one environment variable if the golden-set evaluation shows it is good enough (`AI-spec.md` sections 3.2 and 17).
- The frontend stays on Vercel. If it moves to AWS, only the deployment section of `frontend-spec.md` (8.1) changes.
- The backend runs on Lambda with a Function URL, as set out in `deployment-spec.md`. That choice was made so the project stays within the free tier and needs no VPC; revisit it only if Lambda proves unworkable (for example, jobs that need more than the worker's 10-minute limit).

## Key Risks

- AWS setup eating Day 2. Mitigate by deploying a health endpoint in the first hour of Day 1 and keeping to the Lambda, DynamoDB, and S3 footprint in `deployment-spec.md`.
- Cross-origin and cookie problems between Vercel and AWS (S3 CORS, presigned URLs, the session cookie through the rewrite).
- Gateway or proxy timeouts on slow LLM calls. Mitigated by the job and polling design.
- OCR quality on camera photos.
- Question quality and difficulty mapping for 1 to 5 marks.
- Free-text grading is unsolved for the MVP.
- LLM cost per generation job (about $0.40 to $0.75 on the cases in `AI-spec.md` section 10.3, to be measured), refusals on legitimate school material, and wrong or ungrounded questions. Mitigated by caching, per-user limits, a provider spend limit, server-side checks, and the golden-set evaluation.
- Privacy and consent rules for minors' data. Google sign-in adds names and emails from Google accounts of possibly minor users.
- Google setup (consent screen, authorized origins, test users) happens outside the code and can block sign-in if left late.
