# Exam Assistant: Frontend Spec

Audience: a coding agent building the frontend. Read this whole file before writing code.

**Related files**
- `goals.md`: product goals, the 3-day constraint, deferred list. Binding.
- `backend-spec.md`: **the API contract is `backend-spec.md` section 8.** Section 8 of this file only lists the calls the screens make and the facts they depend on. If the two ever differ, the backend spec wins and this file is corrected.
- `AI-spec.md`: how questions are generated; section 6.4 here follows it.
- `exam-assistant-requirements.md`: user stories and acceptance criteria.
- `prototype/index.html`: the approved visual and interaction reference. Open it in a browser. When this spec and the prototype disagree on visuals, the prototype wins. When they disagree on behavior, this spec wins.

---

## 1. Non-negotiable rules

1. **Font is Helvetica everywhere.** No other font may appear anywhere, including buttons, inputs, tables, and the exam timer. Use exactly this stack: `"Helvetica Neue", Helvetica, Arial, sans-serif`. Do not load web fonts. Do not use `font-mono` for anything (use `font-variant-numeric: tabular-nums` for the timer).
2. **Ease of use, no clutter.** One primary action per screen. Hide anything optional. No sidebars, no tab bars inside pages, no modals except delete confirmation. If you are tempted to add a control that is not in this spec, don't.
3. **3-day, 1-person constraint.** The frontend is one Next.js app in one repo, deployed to **Vercel**. The backend is a separate service on **AWS** (see section 8.1). The frontend contains **no business logic and no database access**; it only renders UI and calls the backend API. No state libraries beyond React state and URL, no animation libraries, no icon packs beyond what shadcn ships (lucide). Prefer plain components over clever abstractions. See `goals.md`.
4. **Mobile first.** Every screen must work at 375 px width with no horizontal scroll and touch targets of at least 40 px.
5. **Status is never shown by colour alone.** Always include the text label.

## 2. Stack

| Concern | Choice |
|---|---|
| Framework | Next.js (App Router), TypeScript |
| Styling | Tailwind CSS, tokens in `tailwind.config` / CSS variables from section 3 |
| Components | shadcn/ui (Button, Dialog/AlertDialog, Input, Select, Checkbox, Table, Toast) |
| PDF viewing | PDF.js (`react-pdf` acceptable) rendered as a vertical page stack |
| Images | plain `<img>` |
| Hosting | Vercel (frontend only) |
| Backend | Separate AWS service, reached through `/api/*` (section 8.1). Do not add Next route handlers or server actions that hold logic or data |
| Data fetching | `fetch` to relative `/api/*` URLs; SWR optional. Uploads use `XMLHttpRequest` (for progress events) straight to S3 |
| Forms | native controlled inputs, no form library |
| Sign in with Google | Google Identity Services script (`https://accounts.google.com/gsi/client`) loaded only on `/login` and `/signup`, using Google's own rendered button |

Set `font-family` once on `body` and in the Tailwind `fontFamily.sans` theme key. Remove any default font imports (for example `next/font/google`).

## 3. Design tokens

Define as CSS variables on `:root` and map in Tailwind. Values come from the approved prototype.

### Colour

| Token | Value | Use |
|---|---|---|
| `--bg` | `#faf7ff` | Page background (plus a lavender wash: `linear-gradient(180deg,#efe4ff 0,#faf7ff 320px)`) |
| `--card` | `#ffffff` | Cards, lists, bars |
| `--ink` | `#1b1340` | Primary text |
| `--muted` | `#6a6490` | Secondary text, meta |
| `--line` | `#e6defa` | Borders, dividers |
| `--tint` | `#f3ecff` | Hover and selected backgrounds |
| `--accent` | `#6d28d9` | Links, section headings, selected borders |
| `--accent2` | `#ff4f8b` | Current-question ring, selected-answer edge |
| `--grad` | `linear-gradient(135deg,#6d28d9 0%,#d946ef 60%,#ff4f8b 100%)` | Primary buttons, selected chips and toggles, answered dots, logo, avatar, progress bars |
| `--ok` | `#047857` (bg `#d1fae5`, border `#6ee7b7`) | Success, Submitted, Correct |
| `--warn` | `#c2410c` (bg `#ffedd5`, border `#fdba74`) | In progress, notices |
| `--bad` | `#e11d48` (bg `#ffe4e6`, border `#fda4af`) | Missed, Incorrect, errors |

**Mark-value colours** (badges and the 1 to 5 stepper labels):

| Marks | Colour |
|---|---|
| 1 | `#0d9488` |
| 2 | `#2563eb` |
| 3 | `#7c3aed` |
| 4 | `#ea580c` |
| 5 | `#e11d48` |

**File-type tile colours** (36 x 44 px tile with the extension in white, bold, 10 px):

| Type | Colour |
|---|---|
| PDF | `#ff5a5f` |
| JPG | `#0d9488` |
| PNG | `#f59e0b` |
| TXT | `#2563eb` |

Colour discipline: use the gradient only for the primary action, selection, and progress. Everything else is white, ink, muted, or a status colour. Do not add new colours.

### Typography

| Use | Size | Weight |
|---|---|---|
| Page title (h1) | 24 px, letter-spacing -0.3 px | 700 |
| Exam question (h1 in exam) | 20 px | 700 |
| Section label (h2) | 13 px, uppercase, letter-spacing 0.6 px, colour `--accent` | 700 |
| Body | 15 px, line-height 1.45 | 400 |
| Meta / helper | 13 px, colour `--muted` | 400 |
| Pills, badges | 12 px | 600 to 700 |

### Shape and spacing

- Card radius 10 px, button radius 8 px, pill radius 99 px, badge radius 6 px.
- Spacing scale: 4 / 8 / 12 / 16 / 20 / 28 px.
- Content max width 880 px, centered, 20 px side padding (16 px under 640 px).
- Bottom padding of `main` is 120 px so fixed bars never cover content.
- Primary button: gradient background, white text, weight 600, shadow `0 4px 14px rgba(109,40,217,.28)`. Secondary button: white, 1 px `--line` border, hover `--tint`.
- Disabled controls: opacity 0.4, `cursor: not-allowed`.

## 4. App shell

### Top bar (all pages except the exam page)
- Sticky, height 56 px, white, bottom border.
- Left: logo text "Exam Assistant" (gradient text, 17 px, bold), links to Home.
- Next to it: nav links **Home**, **Documents**, **Create**. The active link has a 2 px `--accent` underline. **Create** is shown to every role.
- Right: circular avatar (32 px, gradient, user's initial). Click opens a menu with the user's name, the nav links (mobile only), **AI usage** (links to `/usage`, every role), and **Log out**.
- Under 640 px the inline nav links are hidden and appear inside the avatar menu.
- The "View as" role switch in the prototype is **prototype only**. In the real app, role comes from the logged-in user. Do not build it.

### Exam page header (replaces the top bar while taking an exam)
Sticky, 56 px: exam title (left), "Saved hh:mm" (muted), the peeks counter "💡 N left" (P1, only when the exam allows peeks), timer, **Save & exit** button. No nav.

### Role rules

| Role | Sees Create? | Can assign exams? |
|---|---|---|
| Student | Yes (practice exams, and exams for other students) | Yes, **to student accounts only**, up to 10 people per exam |
| Teacher | Yes | Yes, to anyone by email, up to 50 people per exam |
| Parent | Yes | Yes, to anyone by email, up to 50 people per exam (linking is deferred) |

All roles can use Documents, create exams, and take exams assigned to them. An assignee can decline an exam they don't want (section 6.1).

## 5. Routes

| Route | Screen | Roles |
|---|---|---|
| `/login`, `/signup` | Auth (Google button plus email and password, role picker) | public |
| `/` | Home | all |
| `/documents` | Library | all |
| `/documents/[id]` | Viewer | owner |
| `/create` | Create exam | all |
| `/exams/[id]/take` | Exam taking (focused layout) | assignee or creator |
| `/exams/[id]/status` | Teacher status table | creator |
| `/exams/[id]/result` | Result and answer key | assignee or creator |
| `/exams/[id]/print` | Print view (question paper, answer sheet, answer key) | assignee or creator |
| `/usage` | AI usage (live spend, tokens, latest jobs) | all |

Unauthenticated users redirect to `/login`.

## 6. Screens

### 6.1 Home (`/`)
- Header row: h1 "Home", subtitle "Your exams, nothing else.", and a primary **Create exam** button for every role (links to `/create`).
- Section **Open**: list of exams that are applicable to the user and still accepting answers (the backend decides; the 60 seconds after `closesAt` still count as open).
- Section **Closed**: list of exams that have ended, or that the user submitted.
- Declined exams appear in neither list.
- Section **Created by you**: the exams the user created (any role) with the line "12 submitted, 3 in progress, 5 not started, 1 declined" (the declined part only when above 0) and a **View status** secondary button.
- Every exam row also has a quiet text button **Print** (next to the main action, and next to **Decline** on exams that can be declined). It opens `/exams/[id]/print` in a new tab (section 6.9). Declined exams are not listed, so they cannot be printed.
- Each exam row: title (600 weight), meta "Assigned by {name} · closes in 2d" (always show the creator's name; use "You" if self-created), a status pill, and exactly one action button.

| State | Pill | Button |
|---|---|---|
| Not started, open | none, label "Not started" | **Start** (primary) |
| In progress, open | `warn`, "In progress" | **Resume** (primary) |
| Submitted | `ok`, "Best 34/40" (and " · 3 attempts" when more than one) | **View result**; plus a quiet secondary **Retake** while the row's `canRetake` is true |
| Missed (closed, never submitted) | `bad`, "Missed" | **View result** |
| Closing (past `closesAt`, within the 60 s grace window) | `warn`, "Closing…" | **Resume** (primary) |

- **Retake:** on a submitted row with `canRetake`, **Retake** calls `POST /api/exams/:id/retake` and goes to `/exams/[id]/take`, which opens the fresh attempt. A failed call shows a toast and stays on Home (`409 exam_closed`: "This exam has closed."; limit: "You have reached the limit of 50 attempts for this exam."). An exam being retaken (latest attempt not yet submitted) appears under **Open exams** with **Resume** or **Start** and keeps the best score from earlier attempts in its meta line.
- **Decline:** on an open exam that is **Not started** and was assigned by someone else, show a quiet text button **Decline** next to Start. It opens an AlertDialog "Decline this exam?" (body "It will be removed from your list. {creator} will see that you declined.") with Cancel and Decline. It calls `POST /api/exams/:id/decline` and removes the row. There is no undo in the MVP.
- Deadline text: "closes in 5h", "closes in 2d", "closed Oct 8". Relative format under 7 days, otherwise absolute.
- **Empty states:** Open: "No open exams. Exams you create or are assigned appear here." Closed: "Nothing closed yet."
- **Loading:** three skeleton rows in each list.

### 6.2 Documents library (`/documents`)
- Header: h1 "Documents", subtitle "Only you can see these.", right side primary **Upload** button.
- **Upload button** opens a small popover sheet with three items: **Take photo**, **Choose file**, **Choose from photos**.
  - Take photo: `<input type="file" accept="image/*" capture="environment">`.
  - Choose file: `<input type="file" accept="application/pdf,image/jpeg,image/png,text/plain" multiple>`.
  - Choose from photos: `<input type="file" accept="image/*" multiple>`.
  - One photo per upload (decided). No multi-page "keep scanning" flow.
- **Drop zone** (desktop only, hidden under 640 px): dashed 2 px `#c4a8f5` border, `--tint` background, text "Drag files here to upload (PDF, JPG, PNG, TXT · up to 20 MB)".
- **Tools row:** a search input (placeholder "Search documents", filters by name, client-side) and a sort select (**Newest**, **Name**). No grid toggle.
- **List:** one row per document: checkbox, file-type tile, name (link to the viewer, 600 weight), meta "Oct 14 · 12 pages".
- **Upload progress:** while uploading, a row at the top of the list shows the filename, a progress bar (gradient fill), and "Uploading…". On failure the row turns `bad` with the message and a **Retry** link.
- **Validation:** reject unsupported types and files over 20 MB before upload with a toast: "{name} isn't supported. Use PDF, JPG, PNG, or TXT under 20 MB."
- **Upload mechanics (direct to S3, never through the app server):**
  1. `POST /api/documents/upload-url` with `{ name, type, sizeBytes }`, where `type` is the file's **MIME type** (for example `application/pdf`), not the short label. Response: `{ docId, uploadUrl, headers }`.
  2. `PUT` the file to `uploadUrl` with an `XMLHttpRequest`, sending the returned `headers` (the `Content-Type` must match what was signed). Drive the progress bar from `xhr.upload.onprogress`.
  3. On success, `POST /api/documents/:docId/complete`. The response is the finished `Doc`; replace the progress row with the normal row.
  4. If any step fails, the row shows the failure state with **Retry**, which restarts from step 1. Never leave a half-created row in the list after a failed upload.
  - Several files upload in parallel, at most 3 at a time.
- **Selection action bar:** when 1 or more rows are checked, a fixed bottom bar slides in (deep violet gradient `#2a0f6b` to `#6d28d9`, white text): "N selected" (left), **Delete** (secondary, red text), **Generate questions** (primary). On mobile it is full width at the bottom. Hidden when nothing is selected.
  - **Generate questions** goes to `/create` with the selected ids pre-checked as sources.
  - **Delete** opens an AlertDialog: title "Delete N documents?", body "This can't be undone.", buttons Cancel and Delete (destructive). Deleting does not affect exams already created from those documents.
- **Empty state:** "No documents yet. Upload your first file to get started." with a primary Upload button.
- Users only ever see their own documents.

### 6.3 Document viewer (`/documents/[id]`)
- Header row: secondary **← Back** (to `/documents`), file name as h1 (18 px), and on the right: **−** and **+** zoom buttons and a primary **Generate questions** (goes to `/create` with this file pre-checked).
- Body: a lavender (`#e6dcfa`) scroll container, max height 70 vh, rounded 10 px, 20 px padding. Pages stack vertically, 16 px apart, each a white page with a shadow, max width 560 px at 100% zoom.
- Zoom: steps of 15%, range 50% to 300%. Images support pinch zoom on touch devices.
- PDFs render all pages in one continuous scroll (lazy render pages as they scroll into view). Images render as a single page. TXT renders as preformatted-style wrapped text in a white page, still in Helvetica.
- **File source:** `GET /api/documents/:id/file` returns `{ url }`, a short-lived presigned S3 URL (about 10 minutes). Pass that URL to PDF.js or the `<img>`. If loading fails with a 403 (expired), fetch a new URL once and retry before showing the error. Never store the URL.
- **Loading:** a page-shaped grey placeholder. **Error:** "We couldn't open this file." with Retry and Back.

### 6.4 Create exam (`/create`)
Open to every role. One page, no wizard.

**Layout:** h1 "Create exam", subtitle "Pick sources, set the marks, generate." Under 720 px everything stacks into one column. At 720 px and up it is two columns: left 260 px, right flexible.

**Left card: Sources (N)**
- A checkable list of the user's documents (name only). Pre-checked from `?sources=id,id` if present.
- At least one source is required.
- Each checked PDF with more than one page shows "Pages [from] to [to] of N" under its name (two number inputs, labelled "{name} first page" and "{name} last page"), defaulting to the whole file. The running page total uses the range length. A range outside 1 to N, or from greater than to, shows an inline `bad` error. `pageRanges` is sent only for PDFs whose range is not the whole file.
- Under the list, a helper line: "Up to 10 files, 20 MB and 100 pages in total." If `POST /api/generate` answers `422` naming a file (a file alone over the page cap, or a set over the caps), show a `bad` notice with the message as written and uncheck nothing.

**Right column, in this order:**
1. **Question type** card: segmented control with **Multiple choice** and **Free text**. Selected segment uses the gradient, white text.
2. **Total marks** card: ten chips, **10** to **100** in steps of 10 (two rows of five on a phone, one row where it fits). Selected chip uses the gradient. Default 30. (A custom total is deferred.)
3. **Questions per mark value** card: five stepper rows, one for each of 1 to 5 marks. Each row: label ("1 mark", "2 marks", coloured per the mark-value colours, bold), a **−** button, the count, a **+** button, and a right-aligned "= N marks" subtotal. Counts cannot go below 0. Below the rows, a checkbox "Shuffle order (default: low to high marks)".
4. **Exam details** card: **Title** (text), **Closes in** (select: 1 hour, 1 day, 2 days, 1 week, plus "Pick date and time" which reveals a datetime input), **Assign to** (optional, comma-separated emails; below the field a checkbox **Assign to me** that adds the creator themself without typing their email; helper "Leave empty for a personal practice exam"; the checkbox is the creator's own email appended to `assigneeEmails` at publish, never shown in the text field, counts toward the cap, and is ignored if the creator also typed their own email; a student sees "Student emails only · up to 10 people", a teacher or parent sees "Up to 50 people"), and **Peeks allowed** (P1: a stepper from 0 to 10, label "Peeks allowed", helper "Takers can reveal an answer mid-exam for half marks. 0 turns it off."; the default is 3 when nobody other than the creator is assigned (empty, or only **Assign to me** ticked) and 0 when other people's emails are entered; the value follows that default until the user edits it).
5. **Model** card (a select; P0). The options come from `GET /api/ai/models` (cheapest first) and the default is the one marked `default: true`, which is the cheapest enabled model unless the deployment sets another. Each option shows the model name, its tier ("cheapest", "cheap", "balanced", "most capable") and "about $X per set" (the small-case `approxJobCost`). Under the select, a quiet link **See AI usage** (opens `/usage`). When the chosen model is not the cheapest, show a `Notice`: "This model costs more per set and uses more of the shared monthly AI budget." If `GET /api/ai/models` fails or returns nothing, hide the card and omit `model` from requests (the backend then uses its default). The choice is remembered for the page session only.
6. A results region (empty until generation starts).

**Sticky footer (always visible, white, top border):**
- Left: "{total} / {target} marks" in bold.
- Middle: a meter (max 260 px) filled by total/target.
- Then a status: "{n} to go · {count} questions", "Ready to generate · {count} questions", or "{n} over · {count} questions".
- Right: primary **Generate** button.
- Colours: under target `--accent`, at target `--ok`, over target `--bad`.
- **Generate is disabled unless total equals target and at least one source is selected.**

**Generation flow**
1. Above the Generate button, a quiet line: "Selected files are sent to an AI service to write questions." Clicking Generate calls `POST /api/generate` with the settings, including `model` (the selected model's id). The response is `{ jobId }` immediately (it does not wait for the questions). Show a card in the results region: "Writing question 3 of 12…", a gradient progress bar, and a **Cancel** button.
2. Poll `GET /api/generate/:jobId` every 1.5 seconds. The response is `{ status: "queued" | "running" | "done" | "failed" | "cancelled", progress: { done, total }, model, questions?, failedSources?, error? }`; remember `model` as the model used for this exam's questions. Update the progress text and bar from `progress`. When `status` is `done`, stop polling and render `questions`.
3. **Cancel** calls `DELETE /api/generate/:jobId`, stops polling, and clears the card. Also call `DELETE` when the user leaves the page while a job is running, and when the app gives up (step 4), so the job stops spending.
4. Give up with the failure notice if a poll fails 3 times in a row or the job runs longer than **6 minutes** (send the `DELETE` first). Generation usually finishes within about 2 minutes; the backend's hard limit is 8 minutes.
5. If a source file couldn't be read, show a notice naming the file (from `failedSources`): "We couldn't read {name}. Try a clearer photo or a different file." Generation continues with the other sources if any remain.
6. On `failed`, show a `bad` notice with the job's `error` text exactly as written (the backend writes it for end users) and **Try again**. On `429 rate_limited` from `POST /api/generate` (a daily limit, or "The monthly AI budget has been used. Try again next month."), show the message as written and no retry button. On `422` with `fields.model` (the model is unknown or not enabled), refetch `GET /api/ai/models`, reset the select to the default, and show "That model isn't available. Pick another."

Questions and answers are **plain text** (Unicode maths such as x², √, ½ is fine). Render them as text, never as HTML or Markdown.

**Generated question list**
- Heading "Generated questions · {N} marks".
- One card per question: coloured mark badge ("3 marks"), "from {file name}" (meta), and right-aligned text buttons **Edit**, **Regenerate**, **Delete**. Then the numbered question text. For multiple choice, show the four options and mark the correct one with a ✓ and the word "correct".
- Order: ascending by marks by default. If Shuffle is checked, order is random. Changing Shuffle after generating re-orders the existing list without regenerating.
- **Edit** makes the question text (and options) editable inline with Save and Cancel. **Regenerate** replaces only that question with a new one of the same marks. It calls `POST /api/generate/regenerate` with `{ marks, sourceIds, type, avoid, model }`, where `model` is the model used for this exam's generation (so a replacement does not silently use a different one) and `avoid` is the text of every question currently in the list, so the replacement doesn't repeat one. **Delete** removes it.
- If deletions leave the total below the target, show a notice above the list: "You deleted {n} marks. Add a {k}-mark question?" with an **Add** button that generates one question of `min(gap, 5)` marks (the same call as Regenerate, with `avoid` set the same way).
- Under the list: secondary **Regenerate all** and primary **Publish exam**.
- **Publish exam** is disabled while the total of the remaining questions does not equal the target, or the title is empty. It calls `POST /api/exams` with an `Idempotency-Key` header holding a UUID generated once per publish attempt and **reused if the user retries** (so a flaky connection never creates two exams). The body sends `closesInSeconds` (or an absolute `closesAt` for the date picker; never compute the deadline from the device clock), `assigneeEmails`, `maxPeeks`, and the questions (with `sourceDocId` and `sourceDocName` as returned by generation).
- On success: toast "Exam published" and go Home. If the response lists `unknownEmails`, show a `Notice` before leaving: "We couldn't find an account for: {emails}. They didn't get the exam." with **Done**. For a student creator, an email that belongs to a non-student account is reported the same way.

**Answer key**
- Created at publish time from the generated model answers. A "Review answer key" secondary button opens an inline editable list of one answer per question. Auto-drafted by default and editable.
- There is no visibility setting. The rule is fixed and enforced by the backend: **the creator sees the key; everyone else sees it once the exam is over** (after `acceptingUntil`). A creator who is also taking the exam does not see the key during their own unfinished attempt. The screens only render what the backend returns (`keyVisible`).

### 6.5 Taking an exam (`/exams/[id]/take`)
Focused layout: exam page header only (see section 4). Content centered, max width 640 px.

- **Dots row:** one circle per question (30 px), numbered. Answered = gradient fill, white number. Unanswered = white with a border. Current = a 2 px `--accent2` outline with a 2 px offset. Clicking a dot jumps to that question. Wraps on mobile.
- Meta line: "Question 2 of 12 · 3 marks".
- Question text as the h1 (20 px).
- **Multiple choice:** options as full-width cards with a letter ("A.") and text. Selected: `--accent` border, `--tint` background, 4 px `--accent2` inset left edge, weight 600. Hover `--tint`.
- **Free text:** a textarea, minimum height 120 px, placeholder "Type your answer".
- Footer row: **Back** (disabled on the first question) and **Next** (primary). On the last question the primary button is **Review**.
- Under the footer: meta "Closes Oct 18, 6:00 PM" (the absolute deadline, always shown), and a quiet text link **Print this exam** that opens `/exams/[id]/print` in a new tab, so the exam stays open. Printing does not start the attempt or change it in any way.
- **Timer** (header): "42:10 left", bold, tabular numbers. At under 5 minutes it turns `--bad`. Driven by the server deadline, not a local countdown start, so refreshing never resets it. At 0 it reads "Closing…" until the app has submitted (the backend accepts answers for 60 seconds after `closesAt`, until `acceptingUntil`).
- **Autosave:** every answer change is saved (debounce 500 ms for text). The header shows "Saved 10:42" using the time of the last successful save. If a save fails, show "Not saved. Retrying…" in `--bad` and retry.
- **Save & exit:** saves, shows a toast "Progress saved. Resume any time from Home.", and goes Home. The row then shows Resume.
- **Review page** (after the last question): h1 "Review before you submit", a line "2 unanswered: question 3, 7" (or "All questions answered."), the dots row (click to jump), and **Back to questions** and primary **Submit exam**.
- **Submit exam** shows no extra dialog when everything is answered. If any question is unanswered, show a confirmation AlertDialog "Submit with 2 unanswered questions?" with Cancel and Submit.
- **Deadline passes mid-exam:** the timer reaches 0 and the app calls `POST /api/exams/:id/submit` **with the current answers in the body** (it does not autosave first), shows a toast "Time's up. Your answers were submitted.", and goes to the result page. If submit answers `409 exam_closed` (the grace window ended first), show the toast "This exam has closed. Your last saved answers were submitted." and go to the result page. Autosave answering `409` (closed or already submitted) does the same.
- **Retakes:** the take screen always works on the latest attempt. A user who lands on `/take` with a submitted latest attempt is sent to the result page, where **Retake** starts a new one. The header shows "Attempt 2" when `attemptCount` is above 1.
- **Open after deadline:** redirect to the result page (or Home with a toast "This exam is closed") if never started.
- **Resume:** reopening restores all answers, the last question the user was on, and any revealed answers.
- **Peeks (P1; only when the exam's `maxPeeks` is above 0):**
  - The header shows "💡 N left" (from `peeksLeft`).
  - Each question has a quiet text button **Reveal answer**. It opens an AlertDialog "Reveal this answer?" (body "Revealed questions score at most half marks. You have N peeks left.") with Cancel and Reveal. Revealing calls `POST /api/exams/:id/reveal` with `{ questionId }`.
  - On success the question shows the answer in a `warn` Notice ("Answer: {text}" or the model answer) with the label "Revealed · half marks at most". Revealing the same question again is free. The answer stays on screen after a reload (the exam response carries `revealed`).
  - `409` "No peeks left." disables the button; `403` means peeking is off, so the control must not be shown. The user can still answer normally after a peek.

### 6.6 Teacher status (`/exams/[id]/status`)
- Header: **← Back**, h1 exam title.
- Subtitle: counts, for example "2 submitted, 1 in progress, 1 not started" (plus ", 1 declined" when above 0).
- Table: **Student**, **Status** (pill: Not started, In progress, Submitted, or `bad`-outline "Declined"), **Score** (the best attempt's score, with "· 3 attempts" when more than one), **Submitted** (that attempt's time). A student with more than one attempt has an **Attempts** disclosure button (labelled "Show attempts for {name}") that expands a row listing every attempt's number, score and time. The creator never sees a student's answers. Header cells 12 px uppercase muted on `--bg`. Scores show "—" until graded.
- Header action: secondary **Print** (opens `/exams/[id]/print`).
- Under the table: secondary **Assign to more people** (opens a small dialog with an emails input, an **Assign to me** checkbox that is hidden once the creator already has a row on the exam, and the cap text from section 6.4; the response's `unknown` emails are shown in a `Notice`).
- Empty (no assignees): "Not assigned." with the **Assign** button.
- On mobile the table scrolls horizontally inside its card, the page does not.

### 6.7 Result (`/exams/[id]/result`)
- Header: **← Back**, h1 "{exam title}: result", and a secondary **Print** button (opens `/exams/[id]/print`).
- Subtitle: "Score 34 / 40" (when graded). Scores can end in .5 (half marks) and are shown with one decimal only when needed. When peeks were used (P1) add a second line "Score without the peek penalty: 36 / 40" (`scoreBeforePeeks`).
- When `maxPeeks` is above 0 and `peeksUsed` is 0, show an `ok` badge "No peeks". Otherwise, when `peeksUsed` is above 0, show a neutral line "Peeks used: N of M" (P1).
- **If `keyVisible` is true:** one card per question: number badge, a correctness pill (`ok` "✓ Correct" or `bad` "✗ Incorrect"), the question, "Your answer: …", "Correct answer: …". Revealed questions also carry a neutral "Answer revealed" marker (P1).
- **If `keyVisible` is false:** the backend omits all correctness data, so **do not render the ✓ or ✗ pills or any "Correct answer" line**. Each card shows the question and "Your answer: …" only, and a line under the subtitle says "Answers available after {keyAvailableAt}." with no empty gap. Questions the user revealed still show their revealed answer (the backend includes it).
- Multiple choice is auto-graded. Free text shows "Compare with the model answer" and a self-check pair of buttons **I got it** / **I missed it** (updates the score locally in MVP; a revealed question counts for at most half its marks).
- Before `acceptingUntil` the result of an unfinished attempt isn't available (`409`); show "Submit the exam to see results." with a link back.
- **Attempts:** when the response's `attempts` has more than one entry, show an **Attempts** table under the subtitle (columns **Attempt**, **Score**, **Submitted**, with the best attempt marked "Best") where each row's button **View** ("View attempt N") loads `?attempt=N`; the selected attempt is shown with `aria-current`. The page defaults to the latest submitted attempt. Switching attempts clears the local self-check marks. Every attempt's own answers and marks stay viewable. While the exam is still before `acceptingUntil` a primary **Retake** button (shown only while a retake is allowed) calls `POST /api/exams/:id/retake` and goes to `/take`.

### 6.8 Auth

Two ways in: **Sign in with Google**, or **email and password**. `/login` and `/signup` share one centered card (max width 380 px) with the logo above it. The prototype has no auth screen; build this one from the existing tokens.

**Signup (`/signup`), top to bottom**
1. h1 "Create your account", subtitle "Choose who you are, then pick how to sign up."
2. **Role chips**: Student, Parent, Teacher. Same style as the Total marks chips, none pre-selected. A role is required for both methods.
3. **Google button**: Google's own rendered button, text "Sign up with Google".
4. A divider with the word "or".
5. **Email form**: Name, Email, Password (helper "At least 8 characters"), primary **Create account** button.
6. Footer: "Already have an account? Log in".

**Login (`/login`)**: h1 "Welcome back", the Google button ("Sign in with Google"), a divider "or", Email and Password fields, primary **Log in**, footer "New here? Create an account". There are no role chips on this page unless the Google flow asks for a role (below).

**Google button**
- Use Google Identity Services. Load `https://accounts.google.com/gsi/client` with `next/script` (`afterInteractive`) on these two routes only. Call `google.accounts.id.initialize({ client_id, callback })` with `client_id` from `NEXT_PUBLIC_GOOGLE_CLIENT_ID`, then `google.accounts.id.renderButton(container, { type: "standard", theme: "outline", size: "large", text: "signup_with" or "signin_with", width: <container width, at most 340> })`. Do not call the One Tap `prompt()` in the MVP.
- Do not restyle, recolour, or replace Google's button. It is exempt from the palette rules in section 3.
- The callback receives a `credential` (an ID token). Send it to `POST /api/auth/google` as `{ credential, role }`, where `role` is the chosen chip or omitted. Keep the credential in component memory only. Never store it, log it, or put it in a URL.
- If the backend replies `422` with code `role_required` (a new account and no role chosen): show the role chips (on `/login` they appear now), highlight them, and show "Choose who you are to finish creating your account." When the user picks one, resend the **same credential** with that role. If the backend answers `401` (the credential expired, which takes about an hour), show the generic Google error below and ask them to press the Google button again.
- On success (`200` or `201`) go to the return path, or Home.
- If the script fails to load, or Google blocks it (some in-app browsers and school accounts do), hide the Google button and show "Google sign-in isn't available here. Use email and password." The email form keeps working.

**Email form behaviour**
- Validate on submit: name 1 to 80 characters (signup only), a valid email, a password of at least 8 characters (signup only). Show errors inline under the field in `--bad`.
- Signup `409`: "An account with that email already exists. Log in, or sign in with Google."
- Login `401`: "Email or password is incorrect." plus a quieter line "Signed up with Google? Use the Google button above." Show both after any failed password login, so the message never reveals whether an email exists.
- Google error: "Google sign-in didn't work. Try again."
- While a request is in flight, disable the Google area and the form, and show "Signing in…" on the button.
- On these two pages a `401` is shown inline; it does not redirect to `/login`.

**Logout:** **Log out** in the avatar menu calls `POST /api/auth/logout`, then goes to `/login`.

**Accessibility:** inputs have visible labels, the role chips are a radio group (`role="radiogroup"`, arrow-key selectable), and errors use `aria-live="polite"`.

### 6.9 Print view (`/exams/[id]/print`)

Lets anyone who can open the exam print it, so it can be taken on paper, and save a PDF through the browser's print dialog ("Save as PDF"). This is **browser printing only**: the screen uses `window.print()` and a print stylesheet. There is no server-generated PDF and no new backend endpoint.

**Data:** `GET /api/exams/:id` only (questions, `maxPeeks` is ignored, `keyVisible`, and the key fields when `keyVisible` is true). Also `GET /api/me` for the name line. Opening or printing changes nothing on the server: it does not start an attempt, use a peek, or record anything. A `404` shows the same not-found state as the other exam pages.

**Screen layout (not printed):**
- A toolbar card with the exam title, a hint "Choose Save as PDF in the print dialog to keep a copy.", checkboxes, and a primary **Print** button (`window.print()`). Hide the Print button where `window.print` does not exist.
- Checkboxes: **Question paper** (always on and disabled if it is the only part), **Answer sheet** (multiple-choice exams only; default on), **Answer key** (default on only when `keyVisible` is true; when false the checkbox is disabled with the text "Available after {date}", using the result's `keyAvailableAt` rule: after the exam is over; and it never appears in the output), **Name line** (default on).
- Below the toolbar a live preview of the pages in white sheets with a soft shadow, max width 794 px. The preview uses the same components as the printed output.

**Printed output** (`@media print`; the toolbar, app bar, buttons, shadows, gradients, pills and background colours are removed; everything is black on white; Helvetica as everywhere, body 11 pt):
- **Page setup:** `@page { size: auto; margin: 18mm 16mm }`, so the user picks A4 or Letter in the dialog. Do not use fixed page heights; the layout must work on both. Set `print-color-adjust` so no backgrounds are forced.
- **Question paper**
  - Header block on the first page only: exam title (18 pt, 700), a meta line "Set by {creator name} · {N} questions · {total} marks", and, if the name line is on, a row "Name: ______________  Date: ____________  Score: ______ / {total}".
  - One instruction line: multiple choice "Choose one answer for each question. Marks are shown in brackets." Free text "Answer all questions in the space provided. Marks are shown in brackets."
  - Each question is a block that must not split across pages (`break-inside: avoid`): a bold number "1.", the question text (keep line breaks, `white-space: pre-wrap`), and the marks at the right, "[2 marks]" ("[1 mark]" for one). Multiple choice: the four options on separate lines "A.", "B.", "C.", "D.", indented. Free text: **3 ruled answer lines per mark** (1 mark is 3 lines, 5 marks is 15), each 8 mm high, drawn with a bottom border.
  - Spacing: 6 mm between questions. Never leave a question number on one page and its text on the next (`break-after: avoid` on the number row).
  - A footer on every page with the exam title in small grey-less (black, 9 pt) text, using a fixed-position element. Page numbers use `@page` margin boxes only where the browser supports them; do not depend on them.
- **Answer sheet** (multiple choice only; starts on a new page): title "Answer sheet: {exam title}", the name line if on, then a grid "1 ○ A ○ B ○ C ○ D" per question, in two columns when there are more than 10 questions. The circles are outlined 4 mm circles, large enough to shade.
- **Answer key** (only when `keyVisible`; starts on a new page): title "Answer key: {exam title}" and "For the teacher, parent or marker. Not for students." Multiple choice: "1. B: {option text} [2 marks]" per question. Free text: the question number, the question text, then "Model answer:" and the model answer text, with the marks.
- **Order:** question paper, then answer sheet, then answer key, each starting on its own page. Selected parts only.
- Questions and answers are plain text with Unicode maths and print as written. There are no figures, because questions are self-contained.
- A paper exam has no timer, so the printed pages do not show a deadline.

**Edge cases:** an exam with 60 questions prints over several pages without clipping or overlapping; very long question text wraps; an empty selection is impossible (the paper part cannot be turned off); a user who cannot open the exam sees not-found.

**Components:** `PrintToolbar` and `PrintSheet` (pure components that take the exam data and the options, used by both the preview and the print output).

### 6.10 AI usage (`/usage`)
Open to every signed-in user. It shows **aggregate numbers only**: no user names, no file names, no question text. Priority P1 (the counters behind it are part of the core build).

- Header: h1 "AI usage", subtitle "Live. Updates every 5 seconds.", and a muted "Updated 10:42:07" (the time of the last successful refresh, from `serverTime`).
- Card **This month ({month key})**: a `Meter` (spend against budget) with the text "$3.42 of $15.00"; the meter is `--accent` normally, `warn` from 80% and `bad` from 100%. Under it, counts: jobs, calls, and tokens (input, output, cache read, cache write, in thousands with one decimal). At 100% add a `Notice`: "The monthly AI budget has been used. New question sets are paused until next month."
- Card **Today** (UTC day): jobs, calls, spend, tokens.
- Line **Running now: N**.
- Table **By model**: model, jobs, calls, tokens, spend. Only models with usage this month, cheapest first.
- List **Latest jobs** (newest 50 of today): time, kind ("Generate" or "Regenerate"), model, a status pill (`queued`, `running` warn, `done` ok, `failed` bad, `cancelled` neutral), calls, cost.
- **Polling:** `GET /api/ai/usage` every 5 seconds, paused while the tab is hidden and refreshed once when it becomes visible. On a failed refresh show "Couldn't refresh. Retrying…" in `--bad` and keep the last numbers on screen.
- Empty state (no jobs this month): "No AI jobs yet."
- Mobile: cards stack; the tables scroll horizontally inside their card, the page does not.

## 7. Shared components

Build these once and reuse.

| Component | Notes |
|---|---|
| `AppBar` | Top bar with role-aware links and avatar menu |
| `ExamBar` | Exam page header with title, saved text, timer, Save & exit |
| `Pill` | Variants: neutral, ok, warn, bad. Always contains text |
| `MarkBadge` | Takes 1 to 5, uses mark-value colours, text "N mark(s)" |
| `FileTile` | PDF, JPG, PNG, TXT colour tile |
| `Stepper` | − count + with min 0 and subtotal |
| `Meter` | Horizontal bar with colour prop. Also used for the monthly spend on the AI usage page |
| `DotNav` | Numbered question dots with states answered, current, blank |
| `Timer` | Takes a deadline ISO string, ticks every second, tabular numbers, low state under 5 minutes |
| `SelectionBar` | Fixed bottom bar for the library |
| `EmptyState` | One sentence plus an optional primary action |
| `Notice` | Orange inline notice (used for warnings, not errors) |
| `PeeksCounter` | P1. "💡 N left" in the exam header, text always present |
| `PrintToolbar`, `PrintSheet` | Print view (section 6.9). The sheet renders the question paper, answer sheet and answer key from the exam data, with a print stylesheet |

## 8. API calls and data shapes

**The contract is `backend-spec.md` section 8.** This section lists the calls the screens make and the facts they depend on; it does not repeat request and response shapes. Define types in `types.ts` from the backend spec and mock them locally first.

```ts
type Role = "student" | "parent" | "teacher";

interface User { id: string; name: string; email: string; role: Role }

interface Doc {
  id: string; name: string; type: "PDF" | "JPG" | "PNG" | "TXT";
  pages: number; sizeBytes: number; createdAt: string;
  // No file URL is stored. Fetch a presigned URL on demand (section 6.3).
}

interface Question {
  id: string; marks: 1 | 2 | 3 | 4 | 5; text: string;
  sourceDocId: string; sourceDocName: string;   // display only
  options?: string[];          // multiple choice only, length 4
  correctIndex?: number;       // present only when the backend allows it
  modelAnswer?: string;        // free text only; same rule
}

type AttemptStatus = "not_started" | "in_progress" | "submitted" | "missed" | "declined";
// "missed" is derived by the backend; "declined" attempts are never listed.
```

**Calls the screens make** (all relative `/api/...`, implemented by the AWS backend):

| Call | Used by | Facts the UI depends on |
|---|---|---|
| `GET /api/me` | shell, role gating | `{ user, serverTime }` |
| `POST /api/auth/signup`, `/auth/login`, `/auth/logout` | auth | generic `401` text, `409` on duplicate email |
| `POST /api/auth/google` `{credential, role?}` | Google button | `200` existing, `201` new, `422 role_required`, `409` "already linked to another sign-in" |
| `GET /api/documents`, `DELETE /api/documents/:id` | library | only the caller's `ready` documents |
| `POST /api/documents/upload-url` (`type` = MIME), `PUT` to S3, `POST /api/documents/:id/complete` | upload | 20 MB cap; `422` on type or size mismatch |
| `GET /api/documents/:id/file` | viewer | `{ url, expiresAt }`, refetch once on `403` |
| `GET /api/ai/models` | create | `{ models: [{ id, name, provider, tier, priceInPerMTok, priceOutPerMTok, approxJobCost: { small, heavy }, default }], serverTime }`; enabled models only, cheapest first; any signed-in user |
| `GET /api/ai/usage` | usage page | aggregate only: `month`, `today`, `byModel`, `running`, `recent` (newest 50 jobs of today); poll every 5 s; any signed-in user |
| `POST /api/generate` `{sourceIds, type, counts, shuffle, model?}` | create | `202 {jobId}`; `422` naming a file when caps are exceeded, or with `fields.model` for an unknown or disabled model; `429` limits and the monthly budget |
| `GET /api/generate/:jobId`, `DELETE /api/generate/:jobId` | create | poll every 1.5 s; the response includes `model`; `error` text is user-ready |
| `POST /api/generate/regenerate` `{marks, sourceIds, type, avoid[], model?}` | regenerate, add | same polling; separate daily limit; send the model used for the exam |
| `POST /api/exams` (optional `Idempotency-Key` header) | publish | body has `closesInSeconds` or `closesAt`, `assigneeEmails`, `maxPeeks`, `questions`; no key-visibility field; response `{ id, unknownEmails, serverTime }` |
| `GET /api/exams?scope=open\|closed\|created` | home | declined exams are never returned; `created` has `counts` including `declined`; each exam has `createdBy`, `attemptStatus`, `score` (the best score), `attemptCount`, `canRetake` |
| `GET /api/exams/:id` | take | `{ exam, attempt, attemptCount, keyVisible, acceptingUntil, serverTime }`; `attempt` is the latest attempt and carries `attemptNo`, `answers`, `lastQuestionIndex`, and (P1) `revealed` and `peeksLeft` |
| `PUT /api/exams/:id/attempt` (autosave) | take | `409` when closed or submitted |
| `POST /api/exams/:id/submit` `{answers?, lastQuestionIndex?}` | take, auto-submit at zero | answers in the body; accepted until `acceptingUntil`; idempotent |
| `POST /api/exams/:id/reveal` `{questionId}` (P1) | take | `{ questionId, correctIndex or modelAnswer, peeksLeft }`; `403` off, `409` none left or key already visible |
| `POST /api/exams/:id/decline` | home | idempotent; only while not started |
| `POST /api/exams/:id/retake` | home, result | `{ attemptNo, serverTime }`; idempotent while the latest attempt is unfinished; `409 exam_closed` after the deadline, `409` at 50 attempts |
| `GET /api/exams/:id/status` | status | creator only; one row per student with `attemptCount`, best `score` and `history[]` (no answers); rows include `declined` |
| `POST /api/exams/:id/assign` `{emails}` | create, status | `{ assigned, alreadyAssigned, unknown }`; role and cap rules apply |
| `GET /api/exams/:id/result` (optional `?attempt=N`) | result | `attemptNo`, `bestAttemptNo`, `attempts[]`, `keyVisible`, `keyAvailableAt`, `score`, `scoreBeforePeeks`, `peeksUsed`, `maxPeeks`, per-question `revealed`; correctness fields are absent when the key is hidden |

Rules that live on the server but affect the UI:
- One deadline cutoff: `acceptingUntil` = `closesAt` + 60 seconds. The server uses it for autosave, submit, list classification and key visibility; the frontend only displays it.
- The creator sees the answer key; everyone else sees it after the exam is over. A student never receives `correctIndex` or `modelAnswer` outside that rule (the only exception is a question they revealed).
- Scores may end in .5. Never round them.

### 8.1 Backend integration (AWS) and deployment

The frontend runs on Vercel. The backend is a separate AWS service. These rules keep the two decoupled.

**Environment variables (Vercel project settings)**

| Variable | Purpose |
|---|---|
| `API_URL` | Base URL of the AWS backend (for the rewrite). Server-side only |
| `NEXT_PUBLIC_GOOGLE_CLIENT_ID` | Google OAuth client ID for the Google button. Public by design (not a secret). Inlined at build time, so redeploy after changing it |

There is no `NEXT_PUBLIC_API_URL`: the browser never calls AWS directly except for the S3 upload and S3 file view URLs the backend hands out. The only public variable is the Google client ID.

**Proxy all API calls through a Vercel rewrite.** In `next.config.js`:

```js
async rewrites() {
  return [{ source: "/api/:path*", destination: `${process.env.API_URL}/:path*` }];
}
```

Consequences the frontend relies on:
- Browser requests are same-origin, so there is **no CORS setup** for API calls.
- Auth is a **session cookie set by the backend** (`HttpOnly`, `Secure`, `SameSite=Lax`, no explicit domain). It passes through the rewrite and is sent automatically. The frontend never reads or stores tokens. Use `credentials: "same-origin"` (the default).
- Because there is no timeout-sensitive streaming, every long operation is a job with polling (generation, section 6.4).

**S3 (uploads and file viewing)**
- Uploads `PUT` straight to a presigned S3 URL, and the viewer loads files from a presigned S3 URL. Both are cross-origin requests from the Vercel domain, so the bucket needs a CORS rule allowing the app's origin(s) for `GET` and `PUT` with the `Content-Type` header. This is a backend task, but a CORS error in the browser console is the symptom to look for.
- Allow the production domain and, during development, `http://localhost:3000`. Vercel preview URLs change on every deploy, so test uploads on production or localhost, not on previews, unless the bucket allows the preview pattern.

**Time**
- Every API response that includes a deadline also includes `serverTime` (ISO). The frontend computes an offset once (`serverTime - Date.now()`) and uses it for the exam timer and "closes in" text, so a wrong device clock never changes the deadline.

**Error handling for network and gateway failures (all screens)**

| Condition | Behaviour |
|---|---|
| `401` | Redirect to `/login`, preserving the return path (on `/login` and `/signup` themselves, show the inline error instead) |
| `403` | Toast "You don't have access to that." and go Home |
| `404` on an exam or document | Show an `EmptyState` "That item isn't available." with a Home link |
| `502`, `503`, `504`, or network error on a read | Show "Can't reach the server. Retrying…" and retry up to 3 times with backoff (1s, 2s, 4s), then show Retry |
| Same error on autosave | Show "Not saved. Retrying…" (section 6.5) and keep retrying every 3 seconds |
| Same error on a write the user triggered (publish, submit, delete) | Keep the user's data on screen, toast "Couldn't reach the server. Try again." Never clear form state on failure |

**Cold starts:** the first request after idle can be slow on serverless backends. Never show a blank screen: always render the skeleton or progress state from section 6 while a request is in flight.

**Local development:** run Next.js on `localhost:3000` with `API_URL` pointing at either the deployed AWS dev backend or a local backend. Add a tiny mock layer (a `MOCK_API=true` flag with local JSON fixtures) so the frontend can be built before the backend exists. Google sign-in needs `NEXT_PUBLIC_GOOGLE_CLIENT_ID` (the dev client) in `.env.local`, and `localhost:3000` must be an authorized origin on that client. With `MOCK_API=true` the Google button is replaced by a "Continue as demo user" button.

## 9. Behaviour details

- **Time:** show relative times ("closes in 5h") for under 7 days. Use the user's local timezone.
- **Toasts:** bottom center, dark ink background, white text, auto-dismiss after about 2.5 seconds. One at a time.
- **Confirmation dialogs:** only for deleting documents, submitting with unanswered questions, declining an exam, and (P1) revealing an answer.
- **Generation in flight:** leaving the page sends `DELETE /api/generate/:jobId` for a running job.
- **Unsaved changes:** the Create page warns with the browser's `beforeunload` prompt if questions are generated but not published.
- **Keyboard:** all buttons and rows are reachable by Tab. Exam options accept 1 to 4 as shortcuts and arrow keys for Back and Next (optional if time allows).
- **Accessibility:** every icon-only button has `aria-label`. Pills and badges include text. Contrast must stay at least 4.5:1 for text. Focus rings are visible (2 px `--accent`, offset 2 px). Respect `prefers-reduced-motion` by turning off the action bar slide and progress transitions.
- **Responsive breakpoints:** 640 px (nav collapses into avatar menu), 720 px (Create page becomes two columns).
- **Dark mode:** not required. Do not build it.

## 10. Build order (matches the Day 1 slices in `goals.md`)

1. **Shell and auth:** tokens, Helvetica, `AppBar`, login and signup, route guards. In the first hour, deploy the empty skeleton to Vercel with the `/api/*` rewrite pointed at the AWS backend's health endpoint, and confirm a login round-trip works. This catches rewrite, cookie, and CORS problems on day one.
2. **Documents:** library, upload with the sheet, camera input, validation, progress, selection bar, delete dialog, viewer.
3. **Create exam:** sources, type, target chips, steppers, sticky footer total, generation with progress, question list with edit, regenerate, delete, shuffle, exam details, publish.
4. **Home and taking:** Open and Closed lists with Decline, take screen with dots, timer, autosave, Save & exit, review page, submit (auto-submit with answers at zero), result page (hidden-key state), teacher status, and the print view (`/exams/[id]/print`, section 6.9) with its Print buttons.
5. **AI usage page (P1, built before peeks, cut after peeks):** the `/usage` route (section 6.10), the avatar-menu link, the **See AI usage** link on Create. The model select on Create (section 6.4) is part of slice 3 (P0), not this slice.
6. **Peeks (P1, build last, first to cut):** "Peeks allowed" stepper on Create, `PeeksCounter`, Reveal answer with its dialog, revealed state on resume, result score with and without the penalty and the "No peeks" badge. The types and requests carry `maxPeeks`, `revealed` and `peeksLeft` from the start, so adding this later needs no rework.

Build against mock data first, then connect real endpoints. Do not start slice N+1 until slice N works end to end.

## 11. Acceptance checklist

A reviewer can confirm each of these by clicking through the app.

- [ ] No font other than Helvetica renders anywhere (inspect computed styles on buttons, inputs, table, timer).
- [ ] 375 px width: no horizontal page scroll on any screen.
- [ ] Model select: Create shows a Model select whose default is the option marked `default` (the cheapest enabled model); each option shows name, tier and an approximate cost per set; choosing a pricier model shows the cost `Notice`; the chosen id is sent as `model` on generate and on regenerate; if `GET /api/ai/models` fails the select is hidden and generation still works.
- [ ] AI usage: `/usage` is in the avatar menu for every role; it shows month spend against budget (warn at 80%, bad at 100%), today, by-model, running now, and latest jobs; it refreshes every 5 seconds, pauses while the tab is hidden, shows "Couldn't refresh. Retrying…" on failure, and shows no user names, file names, or question text.
- [ ] Print: from Home, the exam page, the result page and the status page, **Print** opens a view where the question paper, the answer sheet (multiple choice only) and the key can be chosen. Printing (or Save as PDF) on A4 and on Letter gives clean black-on-white pages: no toolbar or buttons, no question split across pages, 60 questions flow over several pages, free-text questions have ruled lines in proportion to their marks, and the answer key is offered only when the key is visible to that person. Opening or printing does not start an attempt.
- [ ] A student sees Create, can publish a practice exam, and can assign only to student emails (up to 10); a teacher or parent can assign up to 50 people.
- [ ] Signup offers both "Sign up with Google" and an email form, and a role must be chosen for either.
- [ ] A new Google user who chose no role is asked for one, and ends signed in with the chosen role (the same credential is resent).
- [ ] An existing Google user signs in directly and sees no role chips.
- [ ] If the Google script is blocked, the email form still works and a clear message replaces the Google button.
- [ ] A failed password login shows the generic error and the "Signed up with Google?" hint, and never says which part was wrong.
- [ ] The Google credential is never written to storage, logs, or a URL.
- [ ] Upload accepts PDF, JPG, PNG, TXT, rejects others and files over 20 MB with the specified message; camera item opens the camera on a phone.
- [ ] Selecting documents shows the bottom bar with the correct count; Generate questions opens Create with those sources checked.
- [ ] Deleting asks for confirmation and removes only the chosen documents.
- [ ] Viewer scrolls multi-page PDFs inside the app and zoom works.
- [ ] Generate is disabled until total marks equals the target; the footer shows under, at, and over states with correct colours and text.
- [ ] Questions appear in ascending marks order by default and reorder when Shuffle is toggled.
- [ ] Deleting a question shows the "Add a k-mark question?" notice and Add restores the total.
- [ ] Publish creates an exam that appears in Open (for assignees) with the right deadline text. With no assignees it appears in the creator's own Open list.
- [ ] Retrying a failed publish reuses the same `Idempotency-Key` and never creates a second exam.
- [ ] Unknown assignee emails are listed in a warning after publish and after Assign.
- [ ] **Assign to me** on Create adds the creator as an assignee without typing their email (alone, or together with other people), and the same checkbox in the Assign dialog adds the creator to an existing exam. Alone it keeps the practice-exam peek default of 3.
- [ ] An assignee can decline a not-started exam; it leaves both lists and the creator's status shows "Declined".
- [ ] The Create page has no answer-key visibility control.
- [ ] Taking an exam: answers autosave, "Saved hh:mm" updates, Save & exit then Resume restores answers and position.
- [ ] Refreshing during an exam does not reset the timer.
- [ ] Review page lists unanswered questions; submitting with blanks asks for confirmation.
- [ ] When the deadline passes, the timer shows "Closing…", the app submits with the current answers in the request body (no autosave first), and the result page opens.
- [ ] A `409 exam_closed` on submit or autosave shows the closed toast and goes to the result page.
- [ ] Result page shows the right correct, incorrect, and hidden-key states; with the key hidden there are no ✓ or ✗ pills and the line "Answers available after {date}." appears.
- [ ] A creator who is also taking their own exam sees no answer key until they submit (or the exam is over).
- [ ] A near-the-limit answer shows half marks correctly (for example 27.5 / 40).
- [ ] Teacher status table shows the three statuses with counts in the subtitle.
- [ ] Retakes: after submitting, **Retake** on the Closed row and on the result page starts a new attempt until the exam closes; the result page lists every attempt with its marks and **View** opens that attempt's own answers; the Home row shows the best score and attempt count; after the deadline there is no Retake; the creator's status table shows the best score, attempt count and an expandable per-attempt list with no answers.
- [ ] On the deployed Vercel site, `/api/*` requests reach the AWS backend and login persists across refresh (cookie works through the rewrite).
- [ ] A 20 MB PDF uploads successfully, showing real progress, with no request to the app server carrying the file body.
- [ ] An uploaded file opens in the viewer; waiting past the URL expiry and scrolling or reloading still works (URL refetched once).
- [ ] Generation returns a job immediately, progress text advances while polling, and Cancel stops it and clears the card.
- [ ] The AI notice appears above Generate; leaving the page or giving up at 6 minutes sends `DELETE` for the job.
- [ ] A `422` naming a file, and a `429`, show the backend's message as written.
- [ ] Regenerate sends `marks`, `sourceIds`, `type` and `avoid`, and the replacement doesn't repeat an existing question.
- [ ] Questions render as plain text (a question containing `<b>` shows the characters).
- [ ] (P1) With peeks enabled: the header counter drops on each new reveal, revealing the same question twice costs nothing, revealed answers survive a reload, and the result shows the score with and without the penalty and "No peeks" when none were used.
- [ ] With the backend temporarily unreachable, reads show "Can't reach the server. Retrying…" and writes keep the user's input on screen.
- [ ] Changing the device clock does not change the exam timer or deadline text.

## 12. Out of scope (do not build)

Password reset, other sign-in providers, Google One Tap, screens for linking or unlinking sign-in methods, parent-student linking and the parent progress view, folders and tags, rename, launching generation from anywhere other than the library and viewer buttons, multi-page scan flow, HEIC, custom marks totals, recording results of a paper exam in the app, a true offline mode (PWA), server-generated PDFs, printing an exam before it is published, per-attempt durations, dark mode, notifications, profile pages, analytics, XP, points, streaks, levels, and leaderboards (peeks only have the counter, the badge, and the two scores). See "Deferred" in `goals.md`.

## 13. Decisions already made

- Helvetica everywhere.
- Option A everywhere from the research: top bar (no sidebar), checkbox list library with a bottom action bar, single-page generation setup with a sticky total, one question per page with dots.
- One photo per upload.
- Library button label is **Generate questions**. The top-bar and Home button label is **Create exam**.
- Vibrant violet and pink palette with per-mark and per-file-type colours (section 3).
- Deadline is a single `closesAt`. Saving does not pause it.
- No per-exam answer-key setting. Fixed rule: the creator sees the key; others see it once the exam is over (after the 60-second grace window). A creator taking their own exam sees it only after submitting.
- One cutoff, `acceptingUntil` = `closesAt` + 60 s. At zero the app submits with the answers in the body.
- Students can create exams and assign only to students (cap 10). Teachers and parents assign to anyone (cap 50). Assignees can decline.
- Peeking mid-exam for half marks is a P1 feature with minimal gamification (counter, "No peeks" badge, score with and without penalty). Creators set the number of peeks (default 3 for self-practice, 0 when assigning).
- Generation gives up at 6 minutes and sends `DELETE`. Upload cap is 20 MB.
- Model choice is open to every signed-in user: a Model select on Create, defaulting to the cheapest enabled model, with a cost hint and a notice for pricier models. There is no admin role or admin page.
- An AI usage page (`/usage`, every role) shows live aggregate spend and usage, polled every 5 seconds. It is P1; the model select is P0. Cut order: peeks first, then the usage page.
- Exams can be printed so they can be taken on paper: browser printing only (a print route and a print stylesheet), parts chosen by checkbox (question paper, answer sheet for multiple choice, answer key when visible). Paper results are not recorded in the app.
- The API contract lives in `backend-spec.md` section 8.
- Deployment: frontend on Vercel, backend on AWS, joined by a Vercel `/api/*` rewrite (same-origin, cookie auth, no CORS for API calls).
- Uploads go directly from the browser to S3 with presigned URLs. Files are viewed through short-lived presigned URLs.
- Question generation is a job with polling (not streaming), so it works regardless of gateway or proxy timeouts and does not depend on which AWS compute is chosen.
- Two ways to sign in: Google (Identity Services button, ID token verified by the backend) and email and password. The role is chosen at first sign-up.
- A new Google user with no role is asked for one, and the same token is resent (`role_required`).
- The only public environment variable is `NEXT_PUBLIC_GOOGLE_CLIENT_ID`, which is not a secret.
- Assumption to confirm: the frontend stays on Vercel. If it moves to AWS (for example Amplify), only section 8.1 changes.
