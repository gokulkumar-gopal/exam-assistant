# Exam Assistant: Goals

A responsive full-stack web app where Students, Parents, and Teachers upload study material, generate questions of varying difficulty from it, assemble timed exams, and take them.

Priority: **P0** = MVP, **P1** = fast follow.

## Project Constraint (binding on all tech choices)

**This is a 3-day project built by 1 person, with coding agents doing the implementation.** Every technology, architecture, and scope decision must fit that budget. When two options both work, pick the one that is faster to build and has less to operate.

Rules for coding agents:

1. **Exactly two deployables, no more.** (a) A **frontend**: a Next.js app deployed to **Vercel**. (b) A **backend**: one service deployed to **AWS**. No microservices, no cron jobs, and no queues or workers except the single mechanism question-generation jobs need (rule 8).
2. **Two languages, one per deployable.** The frontend is TypeScript: Next.js (App Router) with Tailwind and shadcn/ui. The backend is one Go service on the standard library (`backend-spec.md`). The frontend holds **no business logic and no database access**; it only renders UI and calls the backend API.
3. **Simplest possible data layer, on AWS.** **DynamoDB** (on-demand) is the database (no other database, and no `Scan` in request paths), and one S3 bucket holds files. **No SQLite and no local disk**: neither persists on Vercel or on serverless hosts. No caching layer, no search engine.
4. **Buy, don't build.** Use managed services instead of writing them. Files go to S3 through presigned URLs (the browser uploads directly, never through the app server). Auth is the backend's own session (a backend-set cookie) with two ways in: **email and password**, and **Sign in with Google** (Google Identity Services in the browser, ID-token verification in the backend). No managed identity service such as Cognito. Use LLM APIs only through their official Go SDKs (Google Gemini and Anthropic Claude), as set out in `AI-spec.md`.
5. **Let the LLM do the heavy lifting.** Question generation is done by an LLM: the backend sends the selected files to a vision-capable LLM, which reads PDFs and images directly and returns the questions. Do not build a separate OCR pipeline unless the LLM approach fails. All AI requirements are in `AI-spec.md`.
6. **Use browser-native features first.** Camera capture via `<input type="file" accept="image/*" capture>`, PDF viewing via PDF.js, and responsive layout via Tailwind with shadcn/ui.
7. **Minimal AWS footprint.** Use the fewest services that work: Lambda (an API function and a worker function from one Go binary, called through a Function URL), DynamoDB, and one S3 bucket per stage. No VPC, no API Gateway, no Kubernetes, no multi-region, no custom CI. The setup is a single SAM template plus `make deploy`, defined in `deployment-spec.md`, because one person has to be able to rebuild it on demand.
8. **Compute instead of schedule, and jobs instead of long requests.** Exam closing and auto-submit are decided on read by comparing the deadline to the current time, not by background jobs. Question generation is the one exception: it runs as a **job** (the API returns a job id immediately and the frontend polls for progress), so it works under any gateway or proxy timeout.
9. **Frontend and backend meet at one contract.** The API names, shapes, and error behaviour are defined in `backend-spec.md` section 8; `frontend-spec.md` lists the calls the frontend makes and defines the Vercel `/api/*` rewrite (its section 8.1). Change the backend spec first, then the frontend spec.
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
- [ ] **P0** Upload one or many files (PDF, TXT, JPG, PNG; up to 20 MB each), directly to S3 via presigned URLs.
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
- [ ] **P0** Any signed-in user can choose which model generates their questions, from a fixed catalog. The default is the cheapest available model (Gemini 3.1 Flash-Lite when Gemini is enabled, otherwise Claude Haiku 4.5). There is no admin role.
- [ ] **P0** The LLM is called only from the backend. The API key, prompts, and raw model output never reach the browser.
- [ ] **P0** The result has exactly the requested number of questions at each mark value, in the requested type (multiple choice with four options and one correct answer, or free text with a model answer).
- [ ] **P0** Questions use only the supplied files (nothing invented), are readable without the file in front of the student, and each names the file it came from.
- [ ] **P0** Difficulty follows marks: 1 mark is recall, 5 marks is extended reasoning.
- [ ] **P0** Our code checks every model response (structure, counts, marks, options, duplicates) and drops bad questions. If the count cannot be met after one retry, the job fails with a clear message and no partial exam.
- [ ] **P0** Text inside uploaded files is treated as content, never as instructions to the model.
- [ ] **P0** Each job is bounded: file size and page caps (checked before the job starts), a cap on questions per call, hard daily limits per user and for the whole app, and a soft limit on jobs running at once. Token usage is recorded for every job.
- [ ] **P0** The user is told near the Generate button that the selected files are sent to an AI service.
- [ ] **P0** Monthly LLM spend is estimated by the app and new jobs are refused once the estimate reaches the monthly budget ($15 by default, under the $20 limit at the provider), with a clear message to the user.
- [ ] **P1** Anyone signed in can see live AI usage (monthly spend against the budget, today's jobs and tokens, usage per model, jobs running now, and the latest jobs), without any user names or content, refreshed every few seconds.
- [ ] **P1** Regenerate or add a single question without repeating the questions already in the exam.
- [ ] **P1** The model and its settings are changeable by environment variable, and a manual evaluation on sample files exists (`AI-spec.md` section 13).

### 5. Create Test/Exam
- [ ] **P0** Configure sources, question type, marks, and order in one create-exam flow.
- [ ] **P0** Set a title and a closing deadline (minutes, hours, or days). After it passes, the exam can't be taken.
- [ ] **P0** Any role, students included, can create an exam. An exam with no assignees is a personal practice exam.
- [ ] **P0** Optionally assign the exam to zero, one, or many users. Teachers and parents can assign to anyone (up to 50); students can assign only to other students (up to 10).
- [ ] **P0** An assignee sees who created the exam and can decline it; a declined exam disappears from their lists and shows as declined to the creator.
- [ ] **P0** Create an answer key, manually or auto-drafted and editable. Visibility is one fixed rule, not a setting (see Taking an Exam).
- [ ] **P1** Let the creator set how many answers a taker may peek at (0 to 10; default 3 for a practice exam, 0 when assigning).
- [ ] **P1** Edit or delete an exam before anyone has started it.

### 6. Exam Lists
- [ ] **P0** Open Exams page: all exams applicable to me, with time remaining.
- [ ] **P0** Closed Exams page: ended exams, with status and score.
- [ ] **P0** Creator sees per-assignee status (not started, in progress, submitted).

### 7. Taking an Exam
- [ ] **P0** Complete the exam in one sitting and submit.
- [ ] **P0** Save progress and resume later where I left off, within the deadline.
- [ ] **P0** Retake a submitted exam any number of times until it closes. Every attempt keeps its own answers and marks, and all of them stay visible to the person who took it. The creator sees each student's best score, attempt count and per-attempt marks, but never a student's answers. Each retake gets a fresh peek budget; a technical safety limit of 50 attempts per person per exam applies.
- [ ] **P0** The answer key is visible to the exam's creator, and to everyone else once the exam is over (the deadline plus a 60-second grace window). A creator who is taking their own exam does not see the key until they submit.
- [ ] **P0** The deadline has a 60-second grace window so the final auto-submit at zero is never lost.
- [ ] **P1** Peek at the answer to a single question during the exam for a score penalty: a revealed question earns at most half its marks. The result shows the score with and without the penalty.
- [ ] **P1** Light gamification only: a "peeks left" counter during the exam, a "no peeks" badge on the result, and the score with and without the penalty.
- [ ] **P0** Auto-grade multiple choice on submission and show the score.
- [ ] **P1** Creator manually marks free-text answers.
- [ ] **Deferred** Parent sees a linked student's exam status and scores.

### 7a. Printing and paper exams
- [ ] **P0** Print an exam so it can be taken on paper, from the browser's print dialog (which also gives Save as PDF). Available to anyone who can open the exam.
- [ ] **P0** The printout is formatted for paper: a header with title, total marks and name/date/score lines; numbered questions with marks in brackets; multiple-choice options A to D; ruled answer lines for free text in proportion to the marks; no question split across pages; works on A4 and Letter; black on white.
- [ ] **P0** Choose what to print: question paper, answer sheet (multiple choice), answer key.
- [ ] **P0** The answer key can be printed only by someone to whom the key is visible under the same rule as on screen. Printing never bypasses it.
- [ ] **Deferred** Recording the result of a paper exam in the app, a true offline mode that works without a connection, and server-generated PDFs.

### 8. Non-functional
- [ ] Responsive on phone, tablet, and desktop.
- [ ] Secure file storage, with access enforced server-side.
- [ ] Upload retry on poor connections without corrupting the library.
- [ ] Question generation runs as a job with visible progress, usually finishes within about 2 minutes (hard limit 8 minutes), can be cancelled, and fails cleanly.
- [ ] The exam timer and deadline are based on server time, not the device clock.
- [ ] Reads and writes recover from a briefly unreachable backend without losing the user's input.
- [ ] Scans and photos are read by a vision-capable LLM (no separate OCR pipeline). A poor-quality warning is deferred.

## Delivery Phases

Re-scoped to the 3-day, 1-person constraint, with a Vercel frontend and an AWS backend.

| Day | Goal | Done when |
|---|---|---|
| 1. Build | Build every P0 goal in vertical slices, in this order: (1) documents: scaffold both deployables, login (email and password, and Google) with a role, upload including camera, library, viewer, delete; (2) question generation: pick files, type, marks breakdown, ordering and shuffle, as a job with polling; (3) exams: create with deadline and answer key, assign by email, Open and Closed lists; (4) taking exams: autosave and resume, deadline enforcement, answer key view, MCQ auto-grade, decline, and printing (browser print, no backend work); then, last, (5) the peek-for-a-penalty feature (P1). The frontend can use its mock-data flag until each backend slice is ready | All P0 goals work end to end on the local frontend against the backend. |
| 2. Deploy | Production AWS setup (DynamoDB tables, S3 bucket and its CORS rule, the two Lambda functions, secrets, LLM key), production Google OAuth client and its authorized origins, set `API_URL` and `NEXT_PUBLIC_GOOGLE_CLIENT_ID` on Vercel, fix production-only issues (cookies through the rewrite, presigned URLs, timeouts), smoke test on a phone and a desktop | The deployed app passes the same end-to-end flow as local. |
| 3. Test and fix | Test with real PDFs and camera photos, tune question quality, fix bugs, cut or finish anything left from the Deferred list | A teacher assigns an exam, a student takes it across two sessions, and sees a score, on the deployed app. |

Day 1 checkpoints:
- **First hour:** deploy an empty frontend skeleton to Vercel and a backend health endpoint to AWS, with the `/api/*` rewrite connecting them, and confirm a login round-trip works. Create the dev Google OAuth client at the same time (about 30 to 45 minutes of console work; see `deployment-spec.md` section 8.0). This is where cookie, rewrite, AWS, and Google setup problems show up, so do not postpone it.
- **Midday:** generate questions from one real PDF and one real camera photo. If quality is poor, fix the prompt or approach before building exams on top of it.

If Day 1 runs long, cut from the end of the slice list in the order given under Deferred. Do not extend the schedule. Anything not built by the end of Day 1 moves to Deferred, and Day 3 is for fixing, not building.

## Deferred (outside the 3-day scope)

Cut in this order, first item first:

- Peeking at answers for a score penalty, and its light gamification (the data fields stay in the model so it can be added later without a migration).
- Parent-student account linking and parent progress view (parents can still use every other feature as ordinary users).
- Folders, tags, and renaming (B-5).
- Launching "Generate questions" from the viewer.
- Editing, regenerating, or removing individual generated questions.
- Manual marking of free-text answers (the student self-checks against the key).
- Editing or deleting an exam after creation.
- HEIC support and non-essential file formats. JPG, PNG, PDF, and TXT are enough.
- OCR quality warnings and tuned generation time targets.
- Password reset, adding a password to a Google-only account, and other sign-in providers.
- Points, streaks, levels, leaderboards, and undoing a decline.
- Recording paper-exam results in the app, an offline-capable app (PWA), and server-generated PDFs. Printing from the browser covers taking an exam offline on paper.

Note: `frontend-spec.md` already describes the viewer's Generate button, per-question edit, regenerate, and delete, and the "Add a k-mark question?" notice. Build those last, and drop them first if time runs short. Do not drop upload retry on failure (the spec requires it); only the fancier retry-with-backoff behaviour can be trimmed.

## Assumptions to Confirm

- Answer key: one fixed rule, no per-exam setting. The creator sees it; everyone else sees it once the exam is over. A peek (P1) is the only way to see a single answer earlier.
- Printing: "taken offline" means taken on paper. The print view is browser printing only, so it needs no backend change. A paper exam has no timer, and its result is marked by hand and not entered in the app.
- Students are allowed to create and assign exams. Because roles are self-selected, "students only" assignment is a courtesy, not a security boundary.
- Parents see linked students' exams and scores, but not private documents.
- MVP grading: multiple choice is automatic, and free text is self-checked or manually marked.
- Timer is a fixed deadline. Saving doesn't pause it, and saved answers are auto-submitted at the deadline.
- Retakes: unlimited until the exam closes (50-attempt safety limit). The answer key rule does not change: per-question correctness stays hidden from non-creators until the exam is over, while every attempt's total marks are visible straight after submitting.
- File limit is 20 MB per file, in PDF, TXT, JPG, or PNG. One generation job can use up to 10 files, 20 MB and 100 PDF pages in total. For a long PDF the user can choose a page range (From and To) and only those pages count.
- **LLM budget: about $20 a month.** The app keeps its own estimate of LLM spend and refuses new generation jobs once it reaches $15 for the month (`LLM_MONTHLY_BUDGET_USD`); the LLM key has a $20 monthly limit at the provider, set before the production deploy. Other limits: 3 generate jobs and 10 regenerates per user a day, 10 jobs a day for the whole app. On the default cheap model that is a few hundred jobs a month; on Sonnet or Opus it is a few dozen.
- Both sign-in methods are offered. Google is the lower-friction path; email and password stays because many students are minors or use school accounts that may not allow Google sign-in.
- A new Google user without a chosen role is asked for one before the account is created.
- Roles are self-selected and unverified in the MVP, for both sign-in methods.
- The LLM is Anthropic Claude through the official Go SDK. The default is the cheapest enabled model (Gemini 3.1 Flash-Lite, else Claude Haiku 4.5), chosen to fit the $20 a month budget. Anyone signed in may pick a pricier model (Sonnet or Opus) for a generation if the cheap one's questions are not good enough; the shared monthly budget guard limits the total. Gemini must be used on a paid-tier key, because the free tier uses submitted content to improve Google's products (`AI-spec.md` sections 3.2, 3.5 and 17).
- The frontend stays on Vercel. If it moves to AWS, only the deployment section of `frontend-spec.md` (8.1) changes.
- The backend runs on Lambda with a Function URL, as set out in `deployment-spec.md`. That choice keeps the project inexpensive and free of a VPC. DynamoDB is on-demand: it is not in the always-free tier but costs cents at demo volume and is covered by AWS credits; revisit it only if Lambda proves unworkable (for example, jobs that need more than the worker's 10-minute limit).

## Key Risks

- AWS setup eating Day 2. Mitigate by deploying a health endpoint in the first hour of Day 1 and keeping to the Lambda, DynamoDB, and S3 footprint in `deployment-spec.md`.
- Cross-origin and cookie problems between Vercel and AWS (S3 CORS, presigned URLs, the session cookie through the rewrite).
- Gateway or proxy timeouts on slow LLM calls. Mitigated by the job and polling design.
- OCR quality on camera photos.
- Question quality and difficulty mapping for 1 to 5 marks.
- Free-text grading is unsolved for the MVP.
- Students assigning exams to other students can send unwanted exams. Mitigated by the cap of 10, the student-only rule, showing the creator's name, and the decline option.
- LLM cost against a $20 a month budget (about $0.03 to $0.10 per job on the default Gemini model and $0.08 to $0.80 on the Claude models, `AI-spec.md` section 10.3, to be measured), refusals on legitimate school material, and wrong or ungrounded questions. Anyone can pick a pricier model, which uses the shared budget faster. Mitigated by caching, the cost notice on the Create screen, the app-side monthly budget guard, per-user and global daily limits, the provider spend limit, server-side checks, and the golden-set evaluation.
- Privacy and consent rules for minors' data. Google sign-in adds names and emails from Google accounts of possibly minor users.
- Google setup (consent screen, authorized origins, test users) happens outside the code and can block sign-in if left late.
