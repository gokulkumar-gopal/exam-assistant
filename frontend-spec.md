# Exam Assistant: Frontend Spec

Audience: a coding agent building the frontend. Read this whole file before writing code.

**Related files**
- `goals.md`: product goals, the 3-day constraint, deferred list. Binding.
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
- Next to it: nav links **Home**, **Documents**, **Create**. The active link has a 2 px `--accent` underline. **Create** is shown only to Teacher and Parent.
- Right: circular avatar (32 px, gradient, user's initial). Click opens a menu with the user's name, the nav links (mobile only), and **Log out**.
- Under 640 px the inline nav links are hidden and appear inside the avatar menu.
- The "View as" role switch in the prototype is **prototype only**. In the real app, role comes from the logged-in user. Do not build it.

### Exam page header (replaces the top bar while taking an exam)
Sticky, 56 px: exam title (left), "Saved hh:mm" (muted), timer, **Save & exit** button. No nav.

### Role rules

| Role | Sees Create? | Can assign exams? |
|---|---|---|
| Student | No | No |
| Teacher | Yes | Yes, to many |
| Parent | Yes | Yes (any user by email in MVP; linking is deferred) |

All roles can use Documents and take assigned exams.

## 5. Routes

| Route | Screen | Roles |
|---|---|---|
| `/login`, `/signup` | Auth (Google button plus email and password, role picker) | public |
| `/` | Home | all |
| `/documents` | Library | all |
| `/documents/[id]` | Viewer | owner |
| `/create` | Create exam | teacher, parent |
| `/exams/[id]/take` | Exam taking (focused layout) | assignee or creator |
| `/exams/[id]/status` | Teacher status table | creator |
| `/exams/[id]/result` | Result and answer key | assignee or creator |

Unauthenticated users redirect to `/login`. A student opening `/create` sees the notice "Only teachers and parents can create exams." and a link Home.

## 6. Screens

### 6.1 Home (`/`)
- Header row: h1 "Home", subtitle "Your exams, nothing else.", and for Teacher/Parent a primary **Create exam** button (links to `/create`).
- Section **Open**: list of exams that are applicable to the user and not past the deadline.
- Section **Closed**: list of exams past the deadline, or submitted.
- Section **Created by you** (Teacher/Parent only): their created exams with the line "12 submitted, 3 in progress, 5 not started" and a **View status** secondary button.
- Each exam row: title (600 weight), meta "Assigned by {name} · closes in 2d" (use "You" if self-created), a status pill, and exactly one action button.

| State | Pill | Button |
|---|---|---|
| Not started, open | none, label "Not started" | **Start** (primary) |
| In progress, open | `warn`, "In progress" | **Resume** (primary) |
| Submitted | `ok`, "Submitted · 34/40" | **View result** |
| Missed (closed, never submitted) | `bad`, "Missed" | **View result** |

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
- **Drop zone** (desktop only, hidden under 640 px): dashed 2 px `#c4a8f5` border, `--tint` background, text "Drag files here to upload (PDF, JPG, PNG, TXT · up to 25 MB)".
- **Tools row:** a search input (placeholder "Search documents", filters by name, client-side) and a sort select (**Newest**, **Name**). No grid toggle.
- **List:** one row per document: checkbox, file-type tile, name (link to the viewer, 600 weight), meta "Oct 14 · 12 pages".
- **Upload progress:** while uploading, a row at the top of the list shows the filename, a progress bar (gradient fill), and "Uploading…". On failure the row turns `bad` with the message and a **Retry** link.
- **Validation:** reject unsupported types and files over 25 MB before upload with a toast: "{name} isn't supported. Use PDF, JPG, PNG, or TXT under 25 MB."
- **Upload mechanics (direct to S3, never through the app server):**
  1. `POST /api/documents/upload-url` with `{ name, type, sizeBytes }`. Response: `{ docId, uploadUrl, headers }`.
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
Teacher and Parent only. One page, no wizard.

**Layout:** h1 "Create exam", subtitle "Pick sources, set the marks, generate." Under 720 px everything stacks into one column. At 720 px and up it is two columns: left 260 px, right flexible.

**Left card: Sources (N)**
- A checkable list of the user's documents (name only). Pre-checked from `?sources=id,id` if present.
- At least one source is required.

**Right column, in this order:**
1. **Question type** card: segmented control with **Multiple choice** and **Free text**. Selected segment uses the gradient, white text.
2. **Total marks** card: chips **30**, **40**, **50**. Selected chip uses the gradient. Default 30. (A custom total is deferred.)
3. **Questions per mark value** card: five stepper rows, one for each of 1 to 5 marks. Each row: label ("1 mark", "2 marks", coloured per the mark-value colours, bold), a **−** button, the count, a **+** button, and a right-aligned "= N marks" subtotal. Counts cannot go below 0. Below the rows, a checkbox "Shuffle order (default: low to high marks)".
4. **Exam details** card: **Title** (text), **Closes in** (select: 1 hour, 1 day, 2 days, 1 week, plus "Pick date and time" which reveals a datetime input), **Assign to** (optional, comma-separated emails; helper "Leave empty for a personal practice exam"), **Answer key visible** (select: Anytime, After submission, After the exam closes).
5. A results region (empty until generation starts).

**Sticky footer (always visible, white, top border):**
- Left: "{total} / {target} marks" in bold.
- Middle: a meter (max 260 px) filled by total/target.
- Then a status: "{n} to go · {count} questions", "Ready to generate · {count} questions", or "{n} over · {count} questions".
- Right: primary **Generate** button.
- Colours: under target `--accent`, at target `--ok`, over target `--bad`.
- **Generate is disabled unless total equals target and at least one source is selected.**

**Generation flow**
1. Clicking Generate calls `POST /api/generate` with the settings. The response is `{ jobId }` immediately (it does not wait for the questions). Show a card in the results region: "Writing question 3 of 12…", a gradient progress bar, and a **Cancel** button.
2. Poll `GET /api/generate/:jobId` every 1.5 seconds. The response is `{ status: "running" | "done" | "failed" | "cancelled", progress: { done, total }, questions?, failedSources?, error? }`. Update the progress text and bar from `progress`. When `status` is `done`, stop polling and render `questions`. Stop polling when the user leaves the page.
3. **Cancel** calls `DELETE /api/generate/:jobId`, stops polling, and clears the card.
4. Give up with the failure notice if a poll fails 3 times in a row or the job runs longer than 3 minutes.
3. If a source file couldn't be read, show a notice naming the file: "We couldn't read {name}. Try a clearer photo or a different file." Generation continues with the other sources if any remain.
4. On any other failure show a `bad` notice with **Try again**.

**Generated question list**
- Heading "Generated questions · {N} marks".
- One card per question: coloured mark badge ("3 marks"), "from {file name}" (meta), and right-aligned text buttons **Edit**, **Regenerate**, **Delete**. Then the numbered question text. For multiple choice, show the four options and mark the correct one with a ✓ and the word "correct".
- Order: ascending by marks by default. If Shuffle is checked, order is random. Changing Shuffle after generating re-orders the existing list without regenerating.
- **Edit** makes the question text (and options) editable inline with Save and Cancel. **Regenerate** replaces only that question with a new one of the same marks. **Delete** removes it.
- If deletions leave the total below the target, show a notice above the list: "You deleted {n} marks. Add a {k}-mark question?" with an **Add** button that generates one question of `min(gap, 5)` marks.
- Under the list: secondary **Regenerate all** and primary **Publish exam**.
- **Publish exam** is disabled while the total of the remaining questions does not equal the target, or the title is empty. On success: toast "Exam published" and go Home.

**Answer key**
- Created at publish time from the generated model answers. A "Review answer key" secondary button opens an inline editable list of one answer per question. Auto-drafted by default and editable.

### 6.5 Taking an exam (`/exams/[id]/take`)
Focused layout: exam page header only (see section 4). Content centered, max width 640 px.

- **Dots row:** one circle per question (30 px), numbered. Answered = gradient fill, white number. Unanswered = white with a border. Current = a 2 px `--accent2` outline with a 2 px offset. Clicking a dot jumps to that question. Wraps on mobile.
- Meta line: "Question 2 of 12 · 3 marks".
- Question text as the h1 (20 px).
- **Multiple choice:** options as full-width cards with a letter ("A.") and text. Selected: `--accent` border, `--tint` background, 4 px `--accent2` inset left edge, weight 600. Hover `--tint`.
- **Free text:** a textarea, minimum height 120 px, placeholder "Type your answer".
- Footer row: **Back** (disabled on the first question) and **Next** (primary). On the last question the primary button is **Review**.
- Under the footer: meta "Closes Oct 18, 6:00 PM" (the absolute deadline, always shown).
- **Timer** (header): "42:10 left", bold, tabular numbers. At under 5 minutes it turns `--bad`. Driven by the server deadline, not a local countdown start, so refreshing never resets it.
- **Autosave:** every answer change is saved (debounce 500 ms for text). The header shows "Saved 10:42" using the time of the last successful save. If a save fails, show "Not saved. Retrying…" in `--bad` and retry.
- **Save & exit:** saves, shows a toast "Progress saved. Resume any time from Home.", and goes Home. The row then shows Resume.
- **Review page** (after the last question): h1 "Review before you submit", a line "2 unanswered: question 3, 7" (or "All questions answered."), the dots row (click to jump), and **Back to questions** and primary **Submit exam**.
- **Submit exam** shows no extra dialog when everything is answered. If any question is unanswered, show a confirmation AlertDialog "Submit with 2 unanswered questions?" with Cancel and Submit.
- **Deadline passes mid-exam:** the timer reaches 0, the app saves the current answers, submits them, shows a toast "Time's up. Your answers were submitted.", and goes to the result page.
- **Open after deadline:** redirect to the result page (or Home with a toast "This exam is closed") if never started.
- **Resume:** reopening restores all answers and the last question the user was on.

### 6.6 Teacher status (`/exams/[id]/status`)
- Header: **← Back**, h1 exam title.
- Subtitle: counts, for example "2 submitted, 1 in progress, 1 not started".
- Table: **Student**, **Status** (pill), **Score**, **Submitted**. Header cells 12 px uppercase muted on `--bg`. Scores show "—" until graded.
- Under the table: secondary **Assign to more people** (opens a small dialog with an emails input).
- Empty (no assignees): "Not assigned." with the **Assign** button.
- On mobile the table scrolls horizontally inside its card, the page does not.

### 6.7 Result (`/exams/[id]/result`)
- Header: **← Back**, h1 "{exam title}: result".
- Subtitle: "Score 34 / 40" (when graded).
- One card per question: number badge, a correctness pill (`ok` "✓ Correct" or `bad` "✗ Incorrect"), the question, "Your answer: …", "Correct answer: …".
- Multiple choice is auto-graded. Free text shows "Compare with the model answer" and a self-check pair of buttons **I got it** / **I missed it** (updates the score locally in MVP).
- If the answer key is hidden by the visibility rule, replace the "Correct answer" lines with "Answer key available after {date or rule}." There must be no empty gap.

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
| `Meter` | Horizontal bar with colour prop |
| `DotNav` | Numbered question dots with states answered, current, blank |
| `Timer` | Takes a deadline ISO string, ticks every second, tabular numbers, low state under 5 minutes |
| `SelectionBar` | Fixed bottom bar for the library |
| `EmptyState` | One sentence plus an optional primary action |
| `Notice` | Orange inline notice (used for warnings, not errors) |

## 8. Data shapes the frontend expects

Define these in `types.ts`. The backend implements them. Mock them locally first.

```ts
type Role = "student" | "parent" | "teacher";

interface User { id: string; name: string; email: string; role: Role }

interface Doc {
  id: string; name: string; type: "PDF" | "JPG" | "PNG" | "TXT";
  pages: number; sizeBytes: number; createdAt: string;
  // No file URL is stored. Fetch a presigned URL on demand (section 6.3).
}

interface Question {
  id: string; marks: 1 | 2 | 3 | 4 | 5; text: string; sourceDocId: string;
  options?: string[];          // multiple choice only, length 4
  correctIndex?: number;       // multiple choice only
  modelAnswer?: string;        // free text only
}

interface Exam {
  id: string; title: string; createdBy: { id: string; name: string };
  closesAt: string;            // ISO, the single source of truth for the deadline
  totalMarks: number;
  type: "mcq" | "free";
  answerKeyVisibility: "anytime" | "after_submit" | "after_close";
  questions: Question[];       // omitted from list endpoints
}

type AttemptStatus = "not_started" | "in_progress" | "submitted" | "missed";

interface Attempt {
  examId: string; userId: string; status: AttemptStatus;
  answers: Record<string, string>;   // questionId -> option text or free text
  lastQuestionIndex: number;
  score?: number; submittedAt?: string; savedAt?: string;
}
```

**API surface the screens call.** All paths are relative (`/api/...`) and are implemented by the AWS backend (section 8.1). Names are the contract the frontend codes against; the backend must match them or tell the frontend what changed.

| Call | Used by |
|---|---|
| `GET /api/me` | shell, role gating |
| `POST /api/auth/signup` `{name, email, password, role}`, `POST /api/auth/login` `{email, password}`, `POST /api/auth/logout` | auth screens, avatar menu |
| `POST /api/auth/google` `{credential, role?}` returning `{user}` (`200` existing, `201` new); `422` code `role_required` when a new user sent no role | Google button (section 6.8) |
| `GET /api/documents`, `DELETE /api/documents/:id` | library |
| `POST /api/documents/upload-url`, then `PUT` to S3, then `POST /api/documents/:id/complete` | upload (section 6.2) |
| `GET /api/documents/:id/file` returning `{ url }` (presigned, short-lived) | viewer |
| `POST /api/generate` `{sourceIds, type, counts, shuffle}` returning `{ jobId }` | create |
| `GET /api/generate/:jobId` (poll), `DELETE /api/generate/:jobId` (cancel) | create |
| `POST /api/generate/regenerate` `{questionId or marks, sourceIds}` returning `{ jobId }` (same polling) | regenerate and add |
| `POST /api/exams` | publish |
| `GET /api/exams?scope=open|closed|created` | home |
| `GET /api/exams/:id`, `GET /api/exams/:id/status` | take, status |
| `PUT /api/exams/:id/attempt` (autosave), `POST /api/exams/:id/submit` | take |
| `POST /api/exams/:id/assign` `{emails}` | create, status |

Rules that live on the server but affect the UI:
- The deadline (`closesAt`) is checked on every read and write. A submit after the deadline is auto-accepted only if the saved attempt existed before it.
- A student never receives `correctIndex` or `modelAnswer` before the visibility rule allows it.

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
- **Confirmation dialogs:** only for deleting documents and submitting with unanswered questions.
- **Unsaved changes:** the Create page warns with the browser's `beforeunload` prompt if questions are generated but not published.
- **Keyboard:** all buttons and rows are reachable by Tab. Exam options accept 1 to 4 as shortcuts and arrow keys for Back and Next (optional if time allows).
- **Accessibility:** every icon-only button has `aria-label`. Pills and badges include text. Contrast must stay at least 4.5:1 for text. Focus rings are visible (2 px `--accent`, offset 2 px). Respect `prefers-reduced-motion` by turning off the action bar slide and progress transitions.
- **Responsive breakpoints:** 640 px (nav collapses into avatar menu), 720 px (Create page becomes two columns).
- **Dark mode:** not required. Do not build it.

## 10. Build order (matches the Day 1 slices in `goals.md`)

1. **Shell and auth:** tokens, Helvetica, `AppBar`, login and signup, route guards. In the first hour, deploy the empty skeleton to Vercel with the `/api/*` rewrite pointed at the AWS backend's health endpoint, and confirm a login round-trip works. This catches rewrite, cookie, and CORS problems on day one.
2. **Documents:** library, upload with the sheet, camera input, validation, progress, selection bar, delete dialog, viewer.
3. **Create exam:** sources, type, target chips, steppers, sticky footer total, generation with progress, question list with edit, regenerate, delete, shuffle, exam details, publish.
4. **Home and taking:** Open and Closed lists, take screen with dots, timer, autosave, Save & exit, review page, submit, result page, teacher status.

Build against mock data first, then connect real endpoints. Do not start slice N+1 until slice N works end to end.

## 11. Acceptance checklist

A reviewer can confirm each of these by clicking through the app.

- [ ] No font other than Helvetica renders anywhere (inspect computed styles on buttons, inputs, table, timer).
- [ ] 375 px width: no horizontal page scroll on any screen.
- [ ] Student does not see Create in the nav and gets a notice on `/create`.
- [ ] Signup offers both "Sign up with Google" and an email form, and a role must be chosen for either.
- [ ] A new Google user who chose no role is asked for one, and ends signed in with the chosen role (the same credential is resent).
- [ ] An existing Google user signs in directly and sees no role chips.
- [ ] If the Google script is blocked, the email form still works and a clear message replaces the Google button.
- [ ] A failed password login shows the generic error and the "Signed up with Google?" hint, and never says which part was wrong.
- [ ] The Google credential is never written to storage, logs, or a URL.
- [ ] Upload accepts PDF, JPG, PNG, TXT, rejects others and files over 25 MB with the specified message; camera item opens the camera on a phone.
- [ ] Selecting documents shows the bottom bar with the correct count; Generate questions opens Create with those sources checked.
- [ ] Deleting asks for confirmation and removes only the chosen documents.
- [ ] Viewer scrolls multi-page PDFs inside the app and zoom works.
- [ ] Generate is disabled until total marks equals the target; the footer shows under, at, and over states with correct colours and text.
- [ ] Questions appear in ascending marks order by default and reorder when Shuffle is toggled.
- [ ] Deleting a question shows the "Add a k-mark question?" notice and Add restores the total.
- [ ] Publish creates an exam that appears in Open (for assignees) with the right deadline text.
- [ ] Taking an exam: answers autosave, "Saved hh:mm" updates, Save & exit then Resume restores answers and position.
- [ ] Refreshing during an exam does not reset the timer.
- [ ] Review page lists unanswered questions; submitting with blanks asks for confirmation.
- [ ] When the deadline passes, the exam auto-submits and shows the result.
- [ ] Result page shows the right correct, incorrect, and hidden-key states.
- [ ] Teacher status table shows the three statuses with counts in the subtitle.
- [ ] On the deployed Vercel site, `/api/*` requests reach the AWS backend and login persists across refresh (cookie works through the rewrite).
- [ ] A 20 MB PDF uploads successfully, showing real progress, with no request to the app server carrying the file body.
- [ ] An uploaded file opens in the viewer; waiting past the URL expiry and scrolling or reloading still works (URL refetched once).
- [ ] Generation returns a job immediately, progress text advances while polling, and Cancel stops it and clears the card.
- [ ] With the backend temporarily unreachable, reads show "Can't reach the server. Retrying…" and writes keep the user's input on screen.
- [ ] Changing the device clock does not change the exam timer or deadline text.

## 12. Out of scope (do not build)

Password reset, other sign-in providers, Google One Tap, screens for linking or unlinking sign-in methods, parent-student linking and the parent progress view, folders and tags, rename, launching generation from anywhere other than the library and viewer buttons, multi-page scan flow, HEIC, custom marks totals, per-attempt durations, dark mode, notifications, profile pages, analytics. See "Deferred" in `goals.md`.

## 13. Decisions already made

- Helvetica everywhere.
- Option A everywhere from the research: top bar (no sidebar), checkbox list library with a bottom action bar, single-page generation setup with a sticky total, one question per page with dots.
- One photo per upload.
- Library button label is **Generate questions**. The top-bar and Home button label is **Create exam**.
- Vibrant violet and pink palette with per-mark and per-file-type colours (section 3).
- Deadline is a single `closesAt`. Saving does not pause it.
- Answer key visibility defaults: Anytime for self-created practice exams, After the exam closes for assigned exams (pre-selected in the Create form).
- Deployment: frontend on Vercel, backend on AWS, joined by a Vercel `/api/*` rewrite (same-origin, cookie auth, no CORS for API calls).
- Uploads go directly from the browser to S3 with presigned URLs. Files are viewed through short-lived presigned URLs.
- Question generation is a job with polling (not streaming), so it works regardless of gateway or proxy timeouts and does not depend on which AWS compute is chosen.
- Two ways to sign in: Google (Identity Services button, ID token verified by the backend) and email and password. The role is chosen at first sign-up.
- A new Google user with no role is asked for one, and the same token is resent (`role_required`).
- The only public environment variable is `NEXT_PUBLIC_GOOGLE_CLIENT_ID`, which is not a secret.
- Assumption to confirm: the frontend stays on Vercel. If it moves to AWS (for example Amplify), only section 8.1 changes.
