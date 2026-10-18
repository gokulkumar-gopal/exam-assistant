# Exam Assistant: PRD, User Stories, and MVP Plan

## 1. Product Summary

**Problem:** Parents and students lack enough challenging practice material. Teachers are limited to what's in standard textbooks.

**Solution:** A responsive full-stack web app where Students, Parents, and Teachers upload study material, generate questions of varying difficulty from it, assemble timed exams, and take them.

**Roles**

| Role | Primary goal |
|---|---|
| Student | Practice and take exams at their own pace |
| Parent | Co-work with the student, with visibility into progress |
| Teacher | Generate stimulating problems and assign exams to many students |

**Out of scope for now:** payments, native mobile apps, live proctoring, plagiarism detection.

## 2. Assumptions (defaults for the open questions)

Change any of these and the affected stories update.

| # | Question | Default assumed |
|---|---|---|
| A1 | Answer key visibility | The creator chooses per exam: *Hidden until submission*, *Available anytime* (practice mode), or *Hidden until exam closes*. Default is *Available anytime* for self-created exams and *Hidden until closes* for exams assigned by others. |
| A2 | Parent visibility | A parent is linked to one or more student accounts. They can view linked students' assigned exams, status, and scores, and can create and assign exams to them. They cannot see a student's private documents unless the student shares them. |
| A3 | Evaluation | MVP: multiple choice is auto-graded against the key. Free text is self-checked against the key by the student, or manually marked by the creator. AI-assisted grading is post-MVP. |
| A4 | Timer | A single **availability window**: the exam closes at a fixed deadline set by the creator. An optional **per-attempt duration** is post-MVP. Saving and resuming does not pause the deadline. |
| A5 | File limits | 25 MB per file. Formats: PDF, TXT, JPG, PNG, HEIC. |
| A6 | Sharing | Documents are private to their owner. Teachers can attach their own documents as exam sources, but students cannot see source files unless shared. |
| A7 | Answer key creation | Manual entry, plus auto-draft from the source material (editable by the creator). |

## 3. Epics and User Stories

Priority: **P0** = MVP, **P1** = fast follow, **P2** = later.

### Epic A: Accounts and Roles

**A-1 (P0)** As a user, I can sign up and log in as a Student, Parent, or Teacher.
- Role is chosen at sign-up and is shown in the UI.
- Role determines which permissions apply (see A2 and A6).

**A-2 (P0)** As a parent, I can link a student account to mine.
- The student must accept the link request.
- A parent can have multiple linked students.

### Epic B: Document Management (all roles)

**B-1 (P0)** As any user, I can upload one or more files at once.
- Accepted types: PDF, TXT, JPG, PNG, HEIC, up to 25 MB each.
- Unsupported types or oversized files are rejected with a clear message.
- Each file shows upload progress and a success or failure state.

**B-2 (P0)** As any user, I can take a photo with my device camera and upload it.
- On mobile and desktop browsers with camera permission.
- The photo is saved in my library as a normal image file.

**B-3 (P0)** As any user, I can browse all my uploaded documents in one library.
- The list shows name, type, size, and upload date.
- I can sort and search by name.

**B-4 (P0)** As any user, I can delete a document.
- A confirmation is required.
- I can only delete my own documents.
- Deleting a document does not delete exams already generated from it.

**B-5 (P1)** As any user, I can organize documents into folders or tags and rename them.

**B-6 (P0)** As any user, I can see only my own documents (and anything explicitly shared with me).

### Epic C: Document Viewer

**C-1 (P0)** As any user, I can open a PDF, scanned image, or captured photo inside the app.
- It opens without leaving the app or downloading.
- Multi-page PDFs scroll continuously within the viewer.

**C-2 (P0)** As any user, I can scroll, and zoom on images, within the viewer.

**C-3 (P1)** As any user, I can click "Generate questions" from the viewer, with the open file pre-selected as a source.

### Epic D: Question Generation

This is a component of Epic E, and it can also be launched standalone from a file.

**D-1 (P0)** As a user, I can select one or more files from my library as sources.
- Selection is multi-select with a count shown.
- Generation requires at least one source.

**D-2 (P0)** As a user, I can choose multiple choice or free text.
- Multiple choice generates 4 options and one correct answer.
- Free text generates a question and a model answer.

**D-3 (P0)** As a user, I can set a total marks target from 10 to 100 in steps of 10 and a per-mark breakdown.
- Mark values available: 1 to 5.
- I enter a count of questions per mark value, and the app shows the running total.
- The total must equal the target, or the app shows the mismatch and blocks generation.

**D-4 (P0)** As a user, I can generate the questions, and they come from the selected files only.
- Questions have varying complexity.
- Each question shows its marks and the source file it came from.
- If a source can't be read (for example, a blurry scan), the user is told which file failed.

**D-5 (P0)** As a user, I can choose question order.
- Default is ascending by marks.
- A "Shuffle" option randomizes the order.

**D-6 (P1)** As a user, I can regenerate, edit, or remove an individual question before saving.

### Epic E: Create Test/Exam

**E-1 (P0)** As a creator, I can start "Create Test/Exam" and configure sources, question type, marks, and order (Epic D) in one flow.

**E-2 (P0)** As a creator, I can set a title and a closing deadline (minutes, hours, or days from now, or a date and time).
- After the deadline, the exam cannot be started or continued, and it moves to Closed.

**E-3 (P0)** As a creator, I can optionally assign the exam to zero, one, or many users.
- Zero assignees: a personal practice exam.
- Teachers can select multiple students. A parent can select their linked students.
- Assigned users see the exam in their Open Exams list.

**E-4 (P0)** As a creator, I can create an answer key.
- Manual entry per question, or auto-draft from the sources, editable before saving.
- The visibility rule (A1) is selectable.

**E-5 (P1)** As a creator, I can edit or delete an exam before anyone has started it.

### Epic F: Exam Lists

**F-1 (P0)** As a user, I can open **Open Exams** and see all exams applicable to me.
- Includes ones I created and ones assigned to me.
- Each shows title, creator, marks, and time remaining.

**F-2 (P0)** As a user, I can open **Closed Exams** and see exams that have ended, with their status (completed or missed) and score where available.

**F-3 (P0)** As a creator, I can see per-assignee status: not started, in progress, or submitted.

### Epic G: Taking an Exam

**G-1 (P0)** As a student, I can take the exam in one sitting and submit it.
- A countdown to the deadline is visible.
- Submitting asks for confirmation and shows unanswered questions.

**G-2 (P0)** As a student, I can save progress and resume later.
- Answers autosave and I can leave and return.
- Resume restores my answers and position.
- If the deadline passes while I'm away, my saved answers are auto-submitted (default assumption).

**G-3 (P0)** As a student, I can view the answer key according to the exam's visibility rule.

**G-4 (P0)** As a student, multiple-choice answers are auto-graded on submission and I see my score.

**G-5 (P1)** As a creator, I can manually mark free-text answers and record a score.

**G-6 (P1)** As a parent, I can see my linked student's exam status and scores.

**G-7 (P0)** As a student (or anyone taking an exam, including a creator on their own exam), I can retake a submitted exam any number of times until it closes.
- Each retake is a new attempt with its own answers, marks and times. Earlier attempts are never changed.
- I can see the marks and my answers for every attempt.
- The answer key rule is unchanged: per-question correct answers stay hidden until the exam is over (the creator still sees them), while each attempt's total marks are visible right after submitting.
- Each attempt gets a fresh peek budget. A technical safety limit of 50 attempts per person per exam applies; there is no product limit.

**G-8 (P1)** As an exam creator, I can assign an exam to myself with one tick (**Assign to me**) instead of typing my own email, on Create and in the Assign dialog, alone or together with other people.
- The creator sees each student's best score, attempt count and per-attempt marks, but never a student's answers.

### Epic H: Non-functional Requirements

- **H-1** Responsive layout on phone, tablet, and desktop.
- **H-2** Files are stored securely and access is enforced server-side by owner and role.
- **H-3** Uploads over spotty connections can fail and retry without corrupting the library.
- **H-4** Question generation shows a progress state and completes or fails cleanly. Target under 60 seconds for a typical request.
- **H-5** Scanned and photographed documents need OCR before generation. Poor OCR quality produces a clear warning.

## 4. Phased MVP Plan

| Phase | Scope | Exit criteria |
|---|---|---|
| **1. Foundation** | A-1, A-2, B-1, B-3, B-4, B-6, C-1, C-2 | A user can sign up, upload a PDF or image, see it in their library, open and scroll it, and delete it. |
| **2. Capture and OCR** | B-2, H-5, rest of H-2/H-3 | Camera photos and scans are stored and become machine-readable text. |
| **3. Question generation** | D-1 to D-5, H-4 | A user selects files, sets marks 1 to 5 and a total, and gets correctly ordered questions. |
| **4. Exams** | E-1 to E-4, F-1, F-2, G-1 to G-4, G-7 | A user creates a timed exam with a key, takes it with save and resume, and sees open and closed lists. |
| **5. Multi-user** | E-3 assignment, F-3, G-6, parent linking | A teacher assigns an exam to a class and tracks status. A parent sees a child's progress. |
| **6. Polish (P1)** | B-5, C-3, D-6, E-5, G-5 | Folders, edit and regenerate questions, manual marking. |

Phases 1 to 4 deliver a single-user MVP. Phase 5 delivers the teacher and parent value proposition.

## 5. Risks

- **OCR quality** on camera photos limits question quality. Phase 2 should be validated with real photographed textbook pages before Phase 3 depends on it.
- **Question quality and difficulty control** is the core product risk. The mark values (1 to 5) need a defined mapping to difficulty and question length.
- **Free-text grading** is unsolved in the MVP and needs a decision before Phase 4 ships to teachers.
- **Minors' data:** students are likely children, so privacy and consent requirements (for example COPPA or GDPR-K) need review before launch.
