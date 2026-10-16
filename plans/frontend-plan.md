# Frontend Implementation Plan

> For agentic workers: REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Build the Exam Assistant web UI (auth, documents, create, exams, take, result, print, usage, peeks) as one Next.js app that only renders and calls `/api/*`.
**Architecture:** Next.js App Router on Vercel. All API calls are same-origin `/api/*`, rewritten to `${API_URL}/*` (the rewrite strips `/api`); auth is a backend-set cookie. A `MOCK_API=true` layer with JSON fixtures lets every screen be built before its backend milestone exists. Long work is a polled job; uploads go by XHR straight to S3.
**Tech Stack:** Next.js, TypeScript, Tailwind, shadcn/ui (lucide only), react-pdf/PDF.js, native forms, optional SWR, Google Identity Services, Vitest (pure helpers only).
**Spec:** frontend-spec.md (all), backend-spec.md 8 (API contract, wins on any difference), AI-spec.md (generation behaviour), goals.md (priorities), deployment-spec.md (Vercel/env), prototype/index.html (visuals; prototype wins on visuals, spec wins on behaviour).

> Days are guidance only. The owner may finish in 2. Work by milestone: M0 to M4 are P0; **M5a (usage) and M5b (peeks) are P1 and cuttable, peeks first, then the usage page.** Do not start milestone N+1 until N works end to end (frontend-spec 10).

## Global Constraints
- Font: `"Helvetica Neue", Helvetica, Arial, sans-serif` on `body` and Tailwind `fontFamily.sans`; no web fonts, no `next/font/google`, no `font-mono` (timer uses `tabular-nums`) (fs 1.1).
- Mobile first: 375 px with no horizontal page scroll, touch targets >= 40 px; breakpoints 640 px (nav collapses) and 720 px (Create two columns) (fs 1.4, 9).
- Status never by colour alone; pills/badges always contain text; contrast >= 4.5:1; focus ring 2 px `--accent` offset 2 px; `prefers-reduced-motion` disables bar slide and progress transitions (fs 9).
- Tokens exactly as fs 3: `--bg #faf7ff`, `--card #fff`, `--ink #1b1340`, `--muted #6a6490`, `--line #e6defa`, `--tint #f3ecff`, `--accent #6d28d9`, `--accent2 #ff4f8b`, `--grad linear-gradient(135deg,#6d28d9 0%,#d946ef 60%,#ff4f8b 100%)`, ok/warn/bad triples; mark colours 1..5 `#0d9488 #2563eb #7c3aed #ea580c #e11d48`; file tiles PDF `#ff5a5f`, JPG `#0d9488`, PNG `#f59e0b`, TXT `#2563eb`. No new colours; gradient only for primary action, selection, progress. No dark mode.
- No business logic, no DB, no Next route handlers/server actions, no state libs, no animation libs, no icon packs beyond lucide (fs 1.3, 2).
- Env: `API_URL` (server only, rewrite) and `NEXT_PUBLIC_GOOGLE_CLIENT_ID` (public, build-time). No `NEXT_PUBLIC_API_URL` (fs 8.1).
- Questions/answers are plain text: render as text nodes, never HTML/Markdown (fs 6.4).
- Deadline: `acceptingUntil = closesAt + 60 s`; frontend only displays it. Timer/"closes in" use `serverTime - Date.now()` offset computed from API responses, never the device clock alone (fs 8.1).
- Key rule is fixed (no visibility setting anywhere in UI); render only what the backend returns (`keyVisible`). Hidden key = no ✓/✗, no "Correct answer" (fs 6.7).
- Scores may end in .5: show one decimal only when needed; never round (fs 6.7, 8).
- Upload: types PDF/JPG/PNG/TXT, max 20 MB (20971520), max 3 parallel; `type` sent to `upload-url` is the MIME type. Generation caps shown: 10 files, 20 MB, 40 pages (see Open questions).
- Generation: poll every 1.5 s; give up after 3 consecutive poll failures or 6 min (send `DELETE` first); `DELETE` also on Cancel and on leaving the page (fs 6.4, 9).
- Assign caps: student 10 (students only), teacher/parent 50. Peeks 0..10, default 3 when Assign to is empty, else 0.
- Dialogs only for: delete documents, submit with unanswered, decline, (P1) reveal. Toasts bottom centre, ink background, ~2.5 s, one at a time.
- Error table fs 8.1 applies to every screen (401 to `/login` with return path except on `/login`/`/signup`; 403 toast + Home; 404 EmptyState; 5xx/network read retry 1/2/4 s then Retry; autosave retry every 3 s; failed writes keep form state).

## Review Focus
Each is tested in the named task.
1. **Hidden-key leak:** with `keyVisible:false` the result and print views render no ✓/✗, no "Correct answer", no Answer key option, even if the payload carries stray `correctIndex` (F-4.4, F-4.5).
2. **Deadline paths:** auto-submit at zero posts answers in the body with no preceding autosave; `409 exam_closed` on submit or autosave gives the closed toast and goes to result; timer survives refresh and a wrong device clock (F-4.2, F-4.3).
3. **Publish idempotency:** one `Idempotency-Key` per attempt, reused on retry, new key only after success or edited content; failed publish keeps all form state (F-3.1).
4. **Plain-text rendering:** a question/answer/title containing `<b>x</b>` or `<img onerror>` shows the characters in Create, Take, Result, Print and Status (F-2.4, F-4.1, F-4.4, F-4.5).
5. **Credential and half-created state:** Google credential never in storage/URL/logs and resent unchanged after `role_required`; failed upload leaves no row in the list (F-1.2, F-1.4).

## File Structure
```
frontend/
  next.config.js                 rewrite /api/:path* -> ${API_URL}/:path*
  tailwind.config.ts, app/globals.css   tokens, Helvetica, print CSS import
  app/layout.tsx                 body font, Toaster, session provider
  app/(auth)/login/page.tsx, signup/page.tsx
  app/page.tsx                   Home
  app/documents/page.tsx, [id]/page.tsx
  app/create/page.tsx
  app/exams/[id]/take|status|result|print/page.tsx
  app/usage/page.tsx             (P1)
  lib/api.ts                     fetch wrapper, error mapping, retry, serverTime offset
  lib/mock/ (index.ts, fixtures/*.json)   MOCK_API layer
  lib/types.ts                   types from backend-spec 8
  lib/format.ts                  deadlineText, scoreText, bytes, tokensK
  lib/upload.ts                  presign -> XHR PUT -> complete, queue of 3
  lib/generate.ts                job start/poll/cancel/give-up
  lib/exam-order.ts, lib/totals.ts, lib/key.ts (pure helpers)
  components/ AppBar ExamBar Pill MarkBadge FileTile Stepper Meter DotNav Timer
             SelectionBar EmptyState Notice PeeksCounter PrintToolbar PrintSheet
             GoogleButton RoleChips UploadSheet Viewer QuestionCard ...
  styles/print.css               @media print rules
  tests/                         Vitest for lib/* only
```

---

## M0 Skeletons

### Task F-0.1: Scaffold, tokens, Helvetica
**Spec:** fs 2, 3, 1.1 **Needs:** none
**Files:** Create `frontend/` (create-next-app TS, Tailwind, shadcn init with Button, Dialog, AlertDialog, Input, Select, Checkbox, Table, Toast); Modify `tailwind.config.ts`, `app/globals.css`, `app/layout.tsx`
**Interfaces:** Produces CSS variables and Tailwind keys `bg card ink muted line tint accent accent2 ok warn bad`, `bg-grad`, `markColor(1..5)`, `.btn-primary`, `.btn-secondary`, `.card`.
- [ ] Remove `next/font/google`; set the stack once on `body` and `fontFamily.sans`; add main bottom padding 120 px, content max width 880 px.
- [ ] Verify: a throwaway page with button, input, table, timer span shows computed `font-family` = the stack for all four (fs 11 item 1); no network font request.
- [ ] Commit "frontend: scaffold, tokens, Helvetica".

### Task F-0.2: Shared presentational components
**Spec:** fs 7 **Needs:** F-0.1
**Files:** Create `components/{Pill,MarkBadge,FileTile,Stepper,Meter,EmptyState,Notice}.tsx`
**Interfaces:** `Pill({variant:'neutral'|'ok'|'warn'|'bad', children})`; `MarkBadge({marks:1|2|3|4|5})` text "N mark(s)" ("1 mark"); `FileTile({type})`; `Stepper({label, marks?, value, onChange, subtotal})` min 0; `Meter({value,max,tone:'accent'|'ok'|'warn'|'bad'})`; `Notice({children})`.
- [ ] Verify with a dev gallery route (removed before commit): every variant renders text; Stepper cannot go below 0; MarkBadge colours match the table.
- [ ] Commit "frontend: shared components".

### Task F-0.3: API client, mock layer, rewrite
**Spec:** fs 8, 8.1, 9 **Needs:** F-0.1
**Files:** Create `lib/api.ts`, `lib/types.ts`, `lib/mock/*`, `lib/format.ts`; Modify `next.config.js`; Test `tests/format.test.ts`
**Interfaces:** Produces `api<T>(method, path, body?, opts?: {idempotencyKey?: string}): Promise<T>` throwing `ApiError{status, code, message, fields?}`; `serverNow(): number` (device time + stored offset, updated from every `serverTime`); `deadlineText(closesAtIso, nowMs): string` ("closes in 5h", "closes in 2d", "closed Oct 8"; relative under 7 days); `scoreText(n): string` (27.5 stays "27.5", 34 stays "34"); `MOCK_API=true` routes `/api/*` to fixtures.
- [ ] Write tests: `deadlineText` returns "closes in 5h" for +5 h, "closes in 2d" for +2 d, absolute "closes Oct 18" form for +8 d; `scoreText(27.5)==="27.5"`, `scoreText(34)==="34"`.
- [ ] Run: FAIL. Implement; run: PASS.
- [ ] Implement error mapping per fs 8.1 table (401 redirect with return path except on `/login`,`/signup`; read retries 1/2/4 s on 502/503/504/network; write failures never clear state).
- [ ] Add rewrite exactly `/api/:path*` to `${process.env.API_URL}/:path*`; fixtures cover every call in fs 8.
- [ ] Commit "frontend: api client, mock layer, rewrite".

### Task F-0.4: AppBar, session, route guards
**Spec:** fs 4, 5 **Needs:** F-0.2, F-0.3
**Files:** Create `components/AppBar.tsx`, `components/ExamBar.tsx`, `lib/session.tsx`, `app/layout.tsx` guard; stub pages for every route in fs 5
**Interfaces:** `useMe(): {user: User|null, loading}` from `GET /api/me`; `AppBar` shows Home, Documents, Create (all roles), avatar menu (name, nav links under 640 px, **AI usage**, **Log out**); `ExamBar({title, savedAt, peeksLeft?, deadlineIso, onSaveExit})` placeholder.
- [ ] Verify (mock): unauthenticated visit to `/documents` redirects to `/login?next=/documents`; 375 px nav collapses into the avatar menu; active link has 2 px accent underline.
- [ ] Commit "frontend: shell and guards".

### Task F-0.5: Vercel skeleton and login round trip
**Spec:** fs 8.1, 10.1 **Needs:** F-0.4; deployment M0 complete (dev stack /health) ; dev Google client
**Files:** Create `frontend/.env.example`; Modify Vercel project settings (not code)
- [ ] Deploy the skeleton to Vercel with `API_URL` = dev API; set `NEXT_PUBLIC_GOOGLE_CLIENT_ID` (dev client) and redeploy.
- [ ] Verify: `curl https://<app>/api/health` returns the backend health body; after a login (F-1.1 stub is enough: a temporary POST) the cookie is set and `GET /api/me` still works after refresh.
- [ ] Commit "frontend: env example".

### Milestone check M0
`next build` passes; fs 11: Helvetica item, 375 px item on the stub pages; `/api/health` through Vercel returns 200; cookie persists across refresh through the rewrite.

---

## M1 Documents (auth, library, upload, viewer)

### Task F-1.1: Login and signup (email) with role chips
**Spec:** fs 6.8 **Needs:** F-0.4; **Needs:** B-1 auth endpoints on dev stack (or mock)
**Files:** Create `app/(auth)/login/page.tsx`, `signup/page.tsx`, `components/RoleChips.tsx`, `components/AuthCard.tsx`
**Interfaces:** `RoleChips({value,onChange,highlight?})` as `role="radiogroup"` with arrow keys; calls `POST /api/auth/signup {name,email,password,role}`, `POST /api/auth/login {email,password}`.
- [ ] Verify: signup validates name 1..80, valid email, password >= 8 with inline `--bad` errors (`aria-live="polite"`); role required; `409` shows "An account with that email already exists. Log in, or sign in with Google."; every failed login shows both "Email or password is incorrect." and "Signed up with Google? Use the Google button above."; in flight the button reads "Signing in…" and the form is disabled; `401` on these pages never redirects.
- [ ] Commit "frontend: email auth".

### Task F-1.2: Google button and `role_required` retry
**Spec:** fs 6.8 (Google), 11 Google items **Needs:** F-1.1
**Files:** Create `components/GoogleButton.tsx`; Modify both auth pages
**Interfaces:** `GoogleButton({text:'signup_with'|'signin_with', role?, onNeedRole, onError})`; posts `{credential, role}` to `/api/auth/google`; loads `https://accounts.google.com/gsi/client` with `next/script afterInteractive` on these two routes only; no `prompt()`. With `MOCK_API` shows "Continue as demo user".
- [ ] Verify: `201`/`200` go to the return path or Home; `422 role_required` shows role chips highlighted with "Choose who you are to finish creating your account." and picking one resends the **same credential held in a ref** with that role; `401` shows "Google sign-in didn't work. Try again."; `409` shows the backend message.
- [ ] Verify (Review Focus 5): after a full flow, `localStorage`, `sessionStorage`, cookies set by JS, URL and `console` contain no credential string; blocking the script URL hides the button and shows "Google sign-in isn't available here. Use email and password." with the email form working.
- [ ] Commit "frontend: google sign-in".

### Task F-1.3: Library list, search, sort, selection bar, delete
**Spec:** fs 6.2 **Needs:** F-0.2, F-1.1; **Needs:** B-1 documents on dev stack (or mock)
**Files:** Create `app/documents/page.tsx`, `components/SelectionBar.tsx`, `components/DocRow.tsx`
**Interfaces:** `GET /api/documents`, `DELETE /api/documents/:id`; `SelectionBar({count,onDelete,onGenerate})` fixed bottom, gradient `#2a0f6b` to `#6d28d9`, full width on mobile, hidden at 0.
- [ ] Verify: rows show checkbox, FileTile, name link (600), meta "Oct 14 · 12 pages"; search filters by name client-side; sort Newest/Name; empty state "No documents yet. Upload your first file to get started." with Upload button; selecting 2 shows "2 selected"; **Generate questions** navigates to `/create?sources=id,id`; Delete dialog "Delete 2 documents?" / "This can't be undone." removes only those rows.
- [ ] Commit "frontend: documents library".

### Task F-1.4: Upload sheet, validation, direct-to-S3 upload with retry
**Spec:** fs 6.2 upload, 8.1 S3 **Needs:** F-1.3; **Needs:** B-1 upload endpoints and S3 CORS on dev stack
**Files:** Create `lib/upload.ts`, `components/UploadSheet.tsx`, `components/UploadRow.tsx`, `components/DropZone.tsx`; Test `tests/upload.test.ts`
**Interfaces:** `validateFile(f:{name,type,size}): string|null`; `uploadFile(file, onProgress): Promise<Doc>` runs presign (`type` = MIME) then XHR `PUT` with returned `headers` then `/complete`; `UploadQueue` limit 3 parallel.
- [ ] Write tests: `validateFile` rejects `image/gif` and a 20971521-byte PDF with "{name} isn't supported. Use PDF, JPG, PNG, or TXT under 20 MB."; accepts exactly 20971520 bytes; queue never runs more than 3 at once (fake `uploadFile`).
- [ ] Run: FAIL. Implement; run: PASS.
- [ ] Implement UI: Upload popover with **Take photo** (`accept="image/*" capture="environment"`), **Choose file** (`application/pdf,image/jpeg,image/png,text/plain` multiple), **Choose from photos** (`image/*` multiple); drop zone hidden under 640 px with dashed 2 px `#c4a8f5`; progress row from `xhr.upload.onprogress` with "Uploading…".
- [ ] Verify (Review Focus 5): force failure at each of three steps; the row turns `bad` with **Retry** restarting at step 1 and no row remains in the list after leaving the page; success replaces the row with the finished `Doc`; DevTools shows the file body goes only to the S3 host.
- [ ] Commit "frontend: upload".

### Task F-1.5: Document viewer
**Spec:** fs 6.3 **Needs:** F-1.3
**Files:** Create `app/documents/[id]/page.tsx`, `components/Viewer.tsx`
**Interfaces:** `GET /api/documents/:id/file` returns `{url, expiresAt}`; `Viewer({doc})`; zoom 50%..300% in 15% steps; URL never stored.
- [ ] Verify: multi-page PDF scrolls continuously with lazy page render inside the `#e6dcfa` container (max 70 vh, radius 10); image is one page with pinch zoom on touch; TXT wraps in a white page in Helvetica; a forced `403` triggers exactly one refetch then success; loading placeholder and error "We couldn't open this file." with Retry/Back; **Generate questions** goes to `/create?sources=<id>`.
- [ ] Commit "frontend: document viewer".

### Milestone check M1
fs 11: Upload accepts/rejects item, camera item (on a phone), bottom bar count and Generate navigation, delete confirmation, viewer scroll/zoom, 20 MB PDF with real progress and no body to the app server, URL-expiry refetch, Google items (role prompt, existing user, blocked script, credential never stored), failed-login generic error, signup both methods; fs 11 deployed items for `/api/*` and cookie persistence. Needs: B-1 complete on the dev stack.

---

## M2 Generation

### Task F-2.1: Create page setup, totals and sticky footer
**Spec:** fs 6.4 left card, right cards 1-4 (without model and peeks), footer **Needs:** F-1.3
**Files:** Create `app/create/page.tsx`, `lib/totals.ts`, `components/SourcesCard.tsx`, `components/CreateFooter.tsx`; Test `tests/totals.test.ts`
**Interfaces:** `totalMarks(counts:Record<1|2|3|4|5,number>): number`; `footerState(total,target,count,hasSource): {tone:'accent'|'ok'|'bad', text, generateEnabled}`.
- [ ] Write tests: counts {1:2,3:2} totals 8; `footerState(28,30,..)` text "2 to go · N questions", tone accent, disabled; `(30,30)` "Ready to generate · N questions", ok, enabled; `(32,30)` "2 over · N questions", bad, disabled; no source forces disabled even at target.
- [ ] Run: FAIL. Implement; run: PASS.
- [ ] Implement layout: one column under 720 px, 260 px + flexible above; sources pre-checked from `?sources=`; helper "Up to 10 files, 20 MB and 40 pages in total."; type segmented control; chips 30/40/50 default 30; five coloured steppers with "= N marks"; Shuffle checkbox "Shuffle order (default: low to high marks)"; Title; Closes in select (1 hour, 1 day, 2 days, 1 week, Pick date and time revealing a datetime input); Assign to emails with role-based helper ("Student emails only · up to 10 people" / "Up to 50 people" / "Leave empty for a personal practice exam").
- [ ] Commit "frontend: create setup".

### Task F-2.2: Model select (P0)
**Spec:** fs 6.4 card 5, 11 model item **Needs:** F-2.1; **Needs:** B-2 `/ai/models` on dev stack (or mock)
**Files:** Create `components/ModelSelect.tsx`; Modify `app/create/page.tsx`, `lib/types.ts`
**Interfaces:** `GET /api/ai/models` returns `{models:[{id,name,provider,tier,priceInPerMTok,priceOutPerMTok,approxJobCost:{small,heavy},default}], serverTime}`; `ModelSelect({models,value,onChange})`; page state `modelId|undefined`.
- [ ] Verify: default is the entry with `default:true`; each option text is "{name} · {tier} · about $X per set" using `approxJobCost.small`; picking a non-cheapest model shows the Notice "This model costs more per set and uses more of the shared monthly AI budget."; the link **See AI usage** opens `/usage`; when `/ai/models` fails or is empty the card is hidden and generate sends no `model`; choice is held in page state only.
- [ ] Commit "frontend: model select".

### Task F-2.3: Generation job: start, poll, cancel, give up
**Spec:** fs 6.4 generation flow 1-6, 9 **Needs:** F-2.1, F-2.2; **Needs:** B-2 generate endpoints on dev stack
**Files:** Create `lib/generate.ts`, `components/GenerationCard.tsx`; Test `tests/generate.test.ts`
**Interfaces:** `startGeneration(req:{sourceIds,type,counts,shuffle,model?}): Promise<{jobId}>`; `pollJob(jobId, {intervalMs:1500, maxMs:360000, maxFailures:3, now, sleep, send}): Promise<JobResult>` with `JobResult = {status:'done',questions,model,failedSources?}|{status:'failed',error}|{status:'cancelled'}|{status:'gaveUp'}`.
- [ ] Write tests with fake clock/fetch: (a) 3 consecutive poll failures give `gaveUp` and one `DELETE /generate/:jobId` sent first; (b) runtime beyond 360000 ms gives `gaveUp` with one `DELETE`; (c) one failure then success does not give up; (d) `done` stops polling and returns the job's `model`.
- [ ] Run: FAIL. Implement; run: PASS.
- [ ] Implement `GenerationCard`: "Writing question {done} of {total}…", gradient bar, **Cancel** (calls `DELETE`, clears card); `beforeunload`/route-leave sends `DELETE` for a running job; line above Generate "Selected files are sent to an AI service to write questions."
- [ ] Implement error UI: `failedSources` Notice "We couldn't read {name}. Try a clearer photo or a different file."; `failed` shows `error` verbatim with **Try again**; `429` shows the message verbatim with no retry; `422` naming a file shows a `bad` notice verbatim and unchecks nothing; `422` with `fields.model` refetches models, resets to default, shows "That model isn't available. Pick another."
- [ ] Commit "frontend: generation job".

### Task F-2.4: Generated question list (edit, regenerate, delete, shuffle, add)
**Spec:** fs 6.4 list, 9 unsaved-changes **Needs:** F-2.3
**Files:** Create `components/QuestionCard.tsx`, `components/QuestionList.tsx`, `lib/exam-order.ts`; Test `tests/exam-order.test.ts`
**Interfaces:** `orderQuestions(qs, shuffle:boolean, seed:number): Question[]` (ascending marks, stable; shuffle deterministic per seed); `regenerate(req:{marks,sourceIds,type,avoid:string[],model?})` uses `POST /api/generate/regenerate` and the same polling; `gapNotice(target,total): {k:number}|null` with `k=min(gap,5)`.
- [ ] Write tests: ascending stable order for marks [3,1,2,1]; toggling shuffle with a fixed seed reorders the same set (no loss); `gapNotice(30,27)` gives k=3, `gapNotice(30,20)` gives k=5, `gapNotice(30,30)` is null.
- [ ] Run: FAIL. Implement; run: PASS.
- [ ] Implement cards: MarkBadge, "from {file name}", Edit (inline question and options, Save/Cancel), Regenerate (sends `avoid` = text of every question currently in the list and `model` = model returned by the job), Delete; multiple choice shows four options with ✓ "correct"; notice "You deleted {n} marks. Add a {k}-mark question?" with **Add**; **Regenerate all**; heading "Generated questions · {N} marks"; `beforeunload` prompt while generated and unpublished.
- [ ] Verify (Review Focus 4): a generated question `<b>x</b>` renders the literal characters; regenerate request body contains `marks`, `sourceIds`, `type`, `avoid`, `model`.
- [ ] Commit "frontend: question list".

### Milestone check M2
fs 11: Model select item (including `/ai/models` failure hides it and generate still works), footer under/at/over colours, ascending order and shuffle reorder, delete then Add restores total, generation returns job immediately with advancing progress text, Cancel clears the card, AI notice present, leaving the page and 6-minute give-up send `DELETE`, `422` naming a file and `429` verbatim, regenerate body fields, plain-text item. Needs: B-2 complete on the dev stack.

---

## M3 Exams

### Task F-3.1: Publish with idempotency and answer-key review
**Spec:** fs 6.4 publish, answer key **Needs:** F-2.4; **Needs:** B-3 `POST /exams` on dev stack
**Files:** Create `components/PublishBar.tsx`, `components/AnswerKeyReview.tsx`, `lib/publish.ts`; Test `tests/publish.test.ts`
**Interfaces:** `makePublisher(): {publish(body): Promise<PublishResult>}` holding one UUID `Idempotency-Key` per attempt; `PublishResult = {id, unknownEmails:string[], serverTime}`; body `{title, closesInSeconds|closesAt, assigneeEmails, maxPeeks, questions:[{marks,text,options?,correctIndex?,modelAnswer?,sourceDocId,sourceDocName}]}` (no key-visibility field).
- [ ] Write tests (Review Focus 3): first failing call then retry sends the identical header value; after success a new `makePublisher` call uses a different value; editing a question after a failure creates a new key.
- [ ] Run: FAIL. Implement; run: PASS.
- [ ] Implement: **Publish exam** disabled unless remaining total equals target and title non-empty; the deadline is sent as `closesInSeconds` (or `closesAt` from the date picker), never computed from the device clock; "Review answer key" opens an editable list (one answer per question); on success toast "Exam published" then Home, but first show a Notice "We couldn't find an account for: {emails}. They didn't get the exam." with **Done** when `unknownEmails` is non-empty; failure toast "Couldn't reach the server. Try again." with all form and question state intact.
- [ ] Commit "frontend: publish exam".

### Task F-3.2: Home lists
**Spec:** fs 6.1 **Needs:** F-0.2, F-1.1; **Needs:** B-3 `GET /exams?scope=` on dev stack
**Files:** Create `app/page.tsx`, `components/ExamRow.tsx`, `lib/exam-row.ts`; Test `tests/exam-row.test.ts`
**Interfaces:** `rowView(exam, nowMs): {pill:{variant,text}|null, action:'Start'|'Resume'|'View result'}` per the fs 6.1 table; sections Open, Closed, Created by you (`counts` line "12 submitted, 3 in progress, 5 not started, 1 declined", declined only above 0, **View status** button).
- [ ] Write tests: not started open gives no pill label "Not started" and **Start**; in progress gives warn "In progress" and **Resume**; submitted gives ok "Submitted · 34/40" and **View result**; missed gives bad "Missed" and **View result**; past `closesAt` but before `acceptingUntil` gives warn "Closing…" and **Resume**.
- [ ] Run: FAIL. Implement; run: PASS.
- [ ] Implement rows: title 600, meta "Assigned by {name} · closes in 2d" ("You" when self-created), exactly one action button, quiet **Print** opening `/exams/[id]/print` in a new tab; skeleton of three rows per list; empty states "No open exams. Exams you create or are assigned appear here." and "Nothing closed yet."; header with **Create exam** for every role.
- [ ] Commit "frontend: home".

### Task F-3.3: Decline
**Spec:** fs 6.1 Decline **Needs:** F-3.2
**Files:** Modify `components/ExamRow.tsx`; Create `components/DeclineDialog.tsx`
**Interfaces:** `POST /api/exams/:id/decline`.
- [ ] Verify: **Decline** shows only on open, not-started, assigned-by-someone-else rows; the AlertDialog "Decline this exam?" with body "It will be removed from your list. {creator} will see that you declined." removes the row on confirm; Cancel changes nothing; self-created rows never show Decline.
- [ ] Commit "frontend: decline exam".

### Task F-3.4: Teacher status and Assign
**Spec:** fs 6.6, 6.4 assign caps **Needs:** F-3.2; **Needs:** B-3 status and assign endpoints
**Files:** Create `app/exams/[id]/status/page.tsx`, `components/AssignDialog.tsx`
**Interfaces:** `GET /api/exams/:id/status`; `POST /api/exams/:id/assign {emails}` returns `{assigned, alreadyAssigned, unknown}`.
- [ ] Verify: subtitle "2 submitted, 1 in progress, 1 not started" plus ", 1 declined" when above 0; table Student, Status (pill, `bad`-outline "Declined"), Score ("—" until graded), Submitted; header **Print**; empty "Not assigned." with **Assign**; dialog shows the cap text for the role and a Notice for `unknown`; at 375 px the table scrolls inside its card only.
- [ ] Commit "frontend: status and assign".

### Milestone check M3
fs 11: publish appears in Open (assignee, and own Open when no assignees) with correct deadline text; retry reuses `Idempotency-Key`; unknown emails Notice after publish and Assign; decline leaves both lists and status shows "Declined"; no answer-key visibility control on Create; student sees Create and student-only cap 10, teacher/parent 50; status table counts. Needs: B-3 complete on the dev stack.

---

## M4 Taking and printing

### Task F-4.1: Take screen and DotNav
**Spec:** fs 6.5 layout, resume **Needs:** F-0.4; **Needs:** B-4 `GET /exams/:id` on dev stack
**Files:** Create `app/exams/[id]/take/page.tsx`, `components/DotNav.tsx`, `components/OptionCard.tsx`; Test `tests/dots.test.ts`
**Interfaces:** `GET /api/exams/:id` returns `{exam, attempt:{answers, lastQuestionIndex, revealed?, peeksLeft?}, keyVisible, acceptingUntil, serverTime}`; `dotState(i, answers, current): 'answered'|'current'|'blank'`.
- [ ] Write tests: answered index with current also gives `current` outline flag while keeping answered fill; unanswered gives `blank`.
- [ ] Run: FAIL. Implement; run: PASS.
- [ ] Implement: 30 px dots (gradient answered, 2 px `--accent2` outline offset 2 px current), meta "Question 2 of 12 · 3 marks", question as h1 (20 px), option cards ("A." + text; selected border `--accent`, `--tint`, 4 px `--accent2` left edge, 600), textarea min 120 px placeholder "Type your answer", Back/Next/**Review** footer, "Closes Oct 18, 6:00 PM" and **Print this exam** link (new tab); keys 1-4 and arrows optional; resume restores answers and last question; if already closed and never started redirect to result.
- [ ] Verify (Review Focus 4): question and option text `<b>x</b>` render literally.
- [ ] Commit "frontend: take screen".

### Task F-4.2: Timer, autosave, Save & exit
**Spec:** fs 6.5 timer/autosave/save-exit, 8.1 time **Needs:** F-4.1
**Files:** Create `components/Timer.tsx`, `lib/autosave.ts`, finish `components/ExamBar.tsx`; Test `tests/timer.test.ts`, `tests/autosave.test.ts`
**Interfaces:** `timerView(closesAtIso, nowMs): {text:'42:10 left'|'Closing…', low:boolean}` (low under 5 min); `createAutosaver({put:(answers,lastQ)=>Promise<void>, debounceMs:500, retryMs:3000, onState}): {change(), flush()}`; `PUT /api/exams/:id/attempt`.
- [ ] Write tests (Review Focus 2): `timerView` with a skewed device clock (offset applied) shows server-based remaining; text 4:59 left is low, 5:00 is not; at 0 shows "Closing…"; autosaver debounces text to one `put` per 500 ms, retries after failure every 3000 ms with state "Not saved. Retrying…", and `onState` carries last-success time for "Saved 10:42"; a `409` stops retrying and reports `closed`.
- [ ] Run: FAIL. Implement; run: PASS.
- [ ] Implement ExamBar: title, "Saved hh:mm", timer (bold, tabular nums, `--bad` when low), **Save & exit** (flush, toast "Progress saved. Resume any time from Home.", go Home); refresh must not reset the timer (derived from `closesAt` and server offset).
- [ ] Commit "frontend: timer and autosave".

### Task F-4.3: Review page, submit, auto-submit, closed handling
**Spec:** fs 6.5 review/submit/deadline **Needs:** F-4.2
**Files:** Create `components/ReviewPage.tsx`, `lib/submit.ts`; Test `tests/submit.test.ts`
**Interfaces:** `submitExam(id, answers, lastQ): Promise<'ok'|'closed'>` posting `POST /api/exams/:id/submit {answers, lastQuestionIndex}` and mapping `409 exam_closed` to `'closed'`; `unansweredLine(answers, n): string` ("2 unanswered: question 3, 7" / "All questions answered.").
- [ ] Write tests (Review Focus 2): `autoSubmitAtZero` calls submit once with the current answers in the body and makes **no** autosave call before it; `409 exam_closed` yields `'closed'`; `unansweredLine` strings exact; double trigger of zero submits once.
- [ ] Run: FAIL. Implement; run: PASS.
- [ ] Implement: Review page h1 "Review before you submit", dots with jump, **Back to questions**, **Submit exam** (no dialog when complete; AlertDialog "Submit with 2 unanswered questions?" otherwise); at zero toast "Time's up. Your answers were submitted." then result; closed outcome (from submit or from autosave `409`) toast "This exam has closed. Your last saved answers were submitted." then result.
- [ ] Commit "frontend: review and submit".

### Task F-4.4: Result page
**Spec:** fs 6.7 **Needs:** F-4.3; **Needs:** B-4 `GET /exams/:id/result`
**Files:** Create `app/exams/[id]/result/page.tsx`, `components/ResultCard.tsx`, `lib/key.ts`; Test `tests/result.test.ts`
**Interfaces:** `resultCardView(q, keyVisible): {showPill:boolean, showCorrect:boolean}`; self-check **I got it** / **I missed it** for free text updates score locally (revealed capped at half marks).
- [ ] Write tests (Review Focus 1): `keyVisible=false` gives `showPill=false`, `showCorrect=false` even when the question object contains `correctIndex` and `modelAnswer`; `keyVisible=true` gives pill and correct line; `scoreText` on 27.5 shows "27.5 / 40" in the subtitle string builder.
- [ ] Run: FAIL. Implement; run: PASS.
- [ ] Implement: header **← Back**, "{exam title}: result", **Print**; subtitle "Score 34 / 40"; hidden-key line "Answers available after {keyAvailableAt}." with no empty gap; cards (number badge, ok "✓ Correct" / bad "✗ Incorrect", question, "Your answer: …", "Correct answer: …"); free text "Compare with the model answer"; unfinished attempt `409` shows "Submit the exam to see results." with a link back. Peeks-related lines are added in F-5b.3.
- [ ] Commit "frontend: result page".

### Task F-4.5: Print view
**Spec:** fs 6.9, 7 **Needs:** F-0.3; **Needs:** B-4 `GET /exams/:id`
**Files:** Create `app/exams/[id]/print/page.tsx`, `components/PrintToolbar.tsx`, `components/PrintSheet.tsx`, `styles/print.css`; Test `tests/print.test.ts`
**Interfaces:** `PrintToolbar({title, options, keyVisible, keyAvailableAt, onChange})`; `PrintSheet({exam, creatorName, userName, options:{paper:true, answerSheet:boolean, key:boolean, nameLine:boolean}})`; `ruledLines(marks): number` = `marks*3`; `defaultOptions(exam, keyVisible)`.
- [ ] Write tests (Review Focus 1): `ruledLines(1)===3`, `ruledLines(5)===15`; with `keyVisible=false` the key checkbox is disabled with "Available after {date}" and `PrintSheet` never renders "Answer key" even if the option is passed true; answer sheet option exists only for multiple-choice exams; the paper part cannot be turned off; question text `<b>x</b>` prints literally.
- [ ] Run: FAIL. Implement; run: PASS.
- [ ] Implement per fs 6.9: toolbar card (hint "Choose Save as PDF in the print dialog to keep a copy.", checkboxes Question paper, Answer sheet (default on), Answer key (default on only when visible), Name line (default on), primary **Print** calling `window.print()`, hidden when it does not exist), preview sheets max 794 px using the same components; `@media print`: toolbar/app bar/shadows/gradients/pills removed, black on white, 11 pt, `@page { size: auto; margin: 18mm 16mm }`, `print-color-adjust` not forcing backgrounds, `break-inside: avoid` per question and `break-after: avoid` on the number row, 8 mm ruled lines, 6 mm spacing, fixed footer with exam title 9 pt, answer sheet grid with 4 mm circles (two columns above 10 questions), key page "Answer key: {title}" and "For the teacher, parent or marker. Not for students."; no deadline printed; `404` shows the shared not-found EmptyState; no attempt is started.
- [ ] Add Print buttons/links: Home rows (F-3.2), take screen link, result header, status header all open `/exams/[id]/print` in a new tab.
- [ ] Verify (manual): print a 60-question exam on A4 and Letter to PDF; no clipping, no question split, ruled lines proportional to marks.
- [ ] Commit "frontend: print view".

### Milestone check M4
fs 11: taking exam autosave and "Saved hh:mm", Save & exit then Resume restores position, refresh keeps timer, changing the device clock does not change timer or deadline text, review lists unanswered and confirms blanks, deadline passes shows "Closing…" and submits with body answers then opens result, `409 exam_closed` toast, result hidden-key state with no ✓/✗ and the "Answers available after {date}." line, creator taking own exam sees no key until submit, 27.5 / 40 shows half marks, print item (A4 and Letter, 60 questions, key only when visible, no attempt started), unreachable-backend item. Needs: B-4 complete on the dev stack.

---

## M5a AI usage (P1, cut after peeks)

### Task F-5a.1: Usage page
**Spec:** fs 6.10, 4 menu link, 6.4 link **Needs:** F-0.2, F-2.2; **Needs:** B-5a `/ai/usage` on dev stack (or mock)
**Files:** Create `app/usage/page.tsx`, `lib/usePolling.ts`; Test `tests/tokens.test.ts`
**Interfaces:** `GET /api/ai/usage` returns `{serverTime, month:{key,spendUsd,budgetUsd,jobs,calls,tokens}, today, byModel[], running, recent[]}`; `usePolling(fn, 5000)` pauses while `document.hidden` and refreshes once when visible; `tokensK(n): string` (one decimal in thousands); `spendTone(spend,budget): 'accent'|'warn'|'bad'` (>=80% warn, >=100% bad).
- [ ] Write tests: `tokensK(12345)==="12.3K"`; `spendTone(11.9,15)` accent, `(12,15)` warn, `(15,15)` bad.
- [ ] Run: FAIL. Implement; run: PASS.
- [ ] Implement per fs 6.10: h1 "AI usage", "Live. Updates every 5 seconds.", "Updated 10:42:07" from `serverTime`, This month card with Meter and "$3.42 of $15.00" plus the 100% Notice "The monthly AI budget has been used. New question sets are paused until next month.", Today card, "Running now: N", By model table (only models with usage, cheapest first), Latest jobs list with status pills (queued neutral, running warn, done ok, failed bad, cancelled neutral) and "Generate"/"Regenerate"; failed refresh keeps numbers and shows "Couldn't refresh. Retrying…" in `--bad`; empty "No AI jobs yet."; no user, file or question text rendered; tables scroll inside their card at 375 px; AppBar menu link **AI usage** and Create's link exist.
- [ ] Commit "frontend: AI usage page".

### Milestone check M5a
fs 11 AI usage item: reachable from the avatar menu for each role; warn at 80%, bad at 100%; 5 s refresh, pause while hidden, failure text; nothing identifying shown. Needs: B-5a complete on the dev stack.

---

## M5b Peeks (P1, build last, cut first)

### Task F-5b.1: "Peeks allowed" on Create
**Spec:** fs 6.4 card 4 **Needs:** F-3.1
**Files:** Modify `app/create/page.tsx`, `lib/publish.ts`
**Interfaces:** `defaultMaxPeeks(assigneeEmails:string[]): 0|3`; `maxPeeks` already in the publish body.
- [ ] Write test `tests/peeks.test.ts`: `defaultMaxPeeks([])===3`, `defaultMaxPeeks(['a@b.c'])===0`.
- [ ] Run: FAIL. Implement; run: PASS.
- [ ] Implement the stepper 0..10, label "Peeks allowed", helper "Takers can reveal an answer mid-exam for half marks. 0 turns it off."; value follows the default until the user edits it.
- [ ] Commit "frontend: peeks allowed".

### Task F-5b.2: Reveal in the take screen
**Spec:** fs 6.5 Peeks, 7 PeeksCounter **Needs:** F-4.2, F-5b.1; **Needs:** B-5b reveal
**Files:** Create `components/PeeksCounter.tsx`, `components/RevealDialog.tsx`; Modify take page
**Interfaces:** `POST /api/exams/:id/reveal {questionId}` returns `{questionId, correctIndex|modelAnswer, peeksLeft}`.
- [ ] Verify: only when `maxPeeks>0`; header "💡 N left"; **Reveal answer** opens "Reveal this answer?" with "Revealed questions score at most half marks. You have N peeks left."; on success a `warn` Notice "Answer: {text}" labelled "Revealed · half marks at most"; revealing the same question again costs nothing; `409` disables the button; `403` removes the control; revealed answers survive reload from `attempt.revealed`.
- [ ] Commit "frontend: reveal answer".

### Task F-5b.3: Result peek lines
**Spec:** fs 6.7 peeks **Needs:** F-4.4, F-5b.2
**Files:** Modify `components/ResultCard.tsx`, result page
**Interfaces:** result fields `scoreBeforePeeks`, `peeksUsed`, `maxPeeks`, per-question `revealed`.
- [ ] Verify: when `peeksUsed>0` second line "Score without the peek penalty: 36 / 40" and "Peeks used: N of M"; when `maxPeeks>0` and `peeksUsed===0` an ok badge "No peeks"; revealed questions show the neutral "Answer revealed" marker and their revealed answer even when `keyVisible` is false, still without ✓/✗.
- [ ] Commit "frontend: peek results".

### Milestone check M5b
fs 11 (P1) peeks item: counter drops per new reveal, repeat reveal free, reload keeps revealed answers, result shows both scores and "No peeks". Needs: B-5b complete on the dev stack.

---

## PROD Production deploy

### Task F-6.1: Vercel production configuration
**Spec:** fs 8.1 **Needs:** deployment PROD gates passed; prod stack and prod Google client ready
**Files:** Modify Vercel project env (no code)
- [ ] Set production `API_URL` and the prod `NEXT_PUBLIC_GOOGLE_CLIENT_ID`; redeploy (client ID is inlined at build time).
- [ ] Verify: fs 11 deployed items (rewrite reaches AWS, login persists across refresh, 20 MB upload, URL-expiry refetch); test uploads on production or localhost, not a preview URL (CORS).
- [ ] Commit none.

### Milestone check PROD
Device smoke from a phone: sign in with Google, camera upload, generate, publish, take, submit, print to PDF. Needs: D-6 complete.

---

## FIX Test and fix

### Task F-7.1: Full acceptance walk
**Spec:** fs 11 **Needs:** M4 complete (and M5a/M5b if built)
- [ ] Walk every checkbox in fs 11 against the deployed dev or prod stack; file each failure as a fix commit with the checklist item in the message.
- [ ] Verify: 375 px no horizontal scroll on every route in fs 5; Helvetica computed-style check on buttons, inputs, table, timer.

### Task F-7.2: Resilience and accessibility pass
**Spec:** fs 8.1 errors, 9 **Needs:** F-7.1
- [ ] Block the backend (DevTools offline): reads show "Can't reach the server. Retrying…" then Retry; writes keep input and toast "Couldn't reach the server. Try again."; cold-start never shows a blank screen (skeleton or progress everywhere).
- [ ] Verify: Tab reaches every button and row; every icon-only button has `aria-label`; focus ring visible; `prefers-reduced-motion` disables the bar slide and progress transitions.

---

## Spec coverage

| Spec section | Tasks |
|---|---|
| fs 1, 2, 3 rules, stack, tokens | F-0.1, F-0.2, F-7.1 |
| fs 4 shell, role rules | F-0.4, F-4.2 |
| fs 5 routes, guards | F-0.4 |
| fs 6.1 Home, Decline | F-3.2, F-3.3 |
| fs 6.2 library, upload | F-1.3, F-1.4 |
| fs 6.3 viewer | F-1.5 |
| fs 6.4 Create, generation, publish, key | F-2.1 to F-2.4, F-3.1, F-5b.1 |
| fs 6.5 take, peeks | F-4.1 to F-4.3, F-5b.2 |
| fs 6.6 status, assign | F-3.4 |
| fs 6.7 result | F-4.4, F-5b.3 |
| fs 6.8 auth, Google | F-1.1, F-1.2 |
| fs 6.9 print | F-4.5 |
| fs 6.10 usage | F-5a.1 |
| fs 7 components | F-0.2, F-1.3, F-4.1, F-4.2, F-4.5, F-5b.2 |
| fs 8 API calls | F-0.3 and each screen task |
| fs 8.1 rewrite, errors, time, local dev | F-0.3, F-0.5, F-4.2, F-6.1, F-7.2 |
| fs 9 behaviours | F-2.3, F-2.4, F-7.2 |
| fs 10 build order | milestone order |
| fs 11 acceptance | each Milestone check, F-7.1 |
| fs 12 out of scope | not built (no task) |

## Open questions
1. **Page cap (resolved):** 40 pages in total in frontend-spec 6.4, backend-spec 8.4 and AI-spec (`GEN_MAX_PDF_PAGES` 40). The 100 in an older decision note is superseded.
2. **Field names (resolved):** `scoreBeforePeeks`, `maxPeeks`, `keyAvailableAt`, `acceptingUntil` match backend-spec 8.5. Still confirm `peeksUsed`, `revealed[]`, `peeksLeft` and the `/ai/usage` shape when writing `types.ts` in F-5a.1 and F-5b.3.
3. **Generate request body (resolved):** `counts` is a map keyed "1" to "5", per backend-spec 8.4.
4. **Print "Answer key" for a creator who is also taking the exam:** fs 6.9 says default on only when `keyVisible`; the plan follows `keyVisible` from `GET /exams/:id` and does no extra rule.
5. **Print `keyAvailableAt`:** fs 6.9 says the checkbox uses the result's `keyAvailableAt`, but the print view only loads `GET /exams/:id`, which lists `acceptingUntil`. The plan shows "Available after {acceptingUntil}"; confirm that is intended.
6. **Self-check scoring in MVP** is local only (fs 6.7); nothing persists, so a reload loses it. Left as written.
7. **Model IDs and prices** shown in Create come only from `GET /ai/models`; the frontend hard-codes none, so verifying IDs and prices is a backend task.
8. **Vitest** is an addition not named in fs 2; it is used only for pure helpers. Drop it if the owner prefers manual verification only.
