package exams_test

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/exams"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// Answers on subEnv's exam e1 (q3 3 marks "o0", q1 2 marks "o1", q2 1 mark "o2"; 6 marks in all).
var (
	allRight  = map[string]string{"q3": "o0", "q1": "o1", "q2": "o2"}           // 6 marks
	someRight = map[string]string{"q1": "o1", "q2": "o2", "q3": "wrong-secret"} // 3 marks
)

func (e *env) retake(u store.User) (exams.RetakeView, error) {
	return e.svc.Retake(context.Background(), u, "e1")
}

func (e *env) mustRetake(t *testing.T, u store.User) int {
	t.Helper()
	v, err := e.retake(u)
	if err != nil {
		t.Fatalf("Retake: %v", err)
	}
	return v.AttemptNo
}

func (e *env) mustSubmit(t *testing.T, u store.User, answers map[string]string) exams.SubmitResult {
	t.Helper()
	r, err := e.submit(u, body(answers, 0))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	return r
}

func (e *env) resultNo(u store.User, no int) (exams.ResultView, error) {
	return e.svc.Result(context.Background(), u, "e1", no)
}

func (e *env) attemptRows(t *testing.T, uid string) []store.Attempt {
	t.Helper()
	rows, err := e.attempts.ListAttempts(context.Background(), "e1", uid)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestRetakeAfterSubmitStartsNextAttemptAndKeepsTheFirst(t *testing.T) {
	e := subEnv(t)
	e.clk.Set(t0.Add(time.Hour))
	first := e.mustSubmit(t, e.student, someRight)
	if no := e.mustRetake(t, e.student); no != 2 {
		t.Fatalf("attemptNo = %d, want 2", no)
	}
	v := e.get(t, e.student, "e1")
	if v.AttemptCount != 2 || v.Attempt == nil || v.Attempt.AttemptNo != 2 || v.Attempt.Status != store.AttemptNotStarted ||
		len(v.Attempt.Answers) != 0 {
		t.Fatalf("get after retake: %+v count=%d", v.Attempt, v.AttemptCount)
	}
	// Autosave and submit act on attempt 2 and leave attempt 1 as it was.
	e.clk.Set(t0.Add(2 * time.Hour))
	if _, err := e.autosave(e.student, map[string]string{"q3": "o0"}, 0); err != nil {
		t.Fatal(err)
	}
	rows := e.attemptRows(t, "s1")
	if len(rows) != 2 || rows[1].Status != store.AttemptInProgress || rows[1].Answers["q3"] != "o0" ||
		rows[0].Status != store.AttemptSubmitted || len(rows[0].Answers) != 3 || rows[0].Answers["q3"] != "wrong-secret" {
		t.Fatalf("rows after autosave: %+v", rows)
	}
	second := e.mustSubmit(t, e.student, allRight)
	rows = e.attemptRows(t, "s1")
	if second.Score == nil || *second.Score != 6 || first.Score == nil || *first.Score != 3 {
		t.Fatalf("scores: %+v %+v", first, second)
	}
	if *rows[0].ScoreHalves != 6 || *rows[1].ScoreHalves != 12 || rows[0].SubmittedAt.Equal(*rows[1].SubmittedAt) {
		t.Fatalf("per-attempt marks and times: %+v", rows)
	}
	// Submitting again returns attempt 2's result (idempotent), and a late autosave is a conflict.
	again := e.mustSubmit(t, e.student, someRight)
	if again.Score == nil || *again.Score != 6 {
		t.Fatalf("repeat submit: %+v", again)
	}
	_, err := e.autosave(e.student, map[string]string{}, 0)
	wantStatus(t, err, http.StatusConflict)
}

func TestRetakeIsIdempotentWhileTheLatestAttemptIsUnfinished(t *testing.T) {
	e := subEnv(t)
	// Not started attempt 1: nothing is created, the caller is told attempt 1.
	if no := e.mustRetake(t, e.student); no != 1 {
		t.Fatalf("retake of an unfinished first attempt = %d, want 1", no)
	}
	e.mustSubmit(t, e.student, someRight)
	if no := e.mustRetake(t, e.student); no != 2 {
		t.Fatal(no)
	}
	if no := e.mustRetake(t, e.student); no != 2 {
		t.Fatalf("second retake = %d, want the same attempt 2", no)
	}
	if _, err := e.autosave(e.student, map[string]string{"q1": "o1"}, 0); err != nil {
		t.Fatal(err)
	}
	if no := e.mustRetake(t, e.student); no != 2 { // in_progress
		t.Fatalf("retake while in progress = %d, want 2", no)
	}
	if n := len(e.attemptRows(t, "s1")); n != 2 {
		t.Fatalf("%d rows, want 2", n)
	}
}

func TestRetakeConcurrentCallsCreateOneAttempt(t *testing.T) {
	e := subEnv(t)
	e.mustSubmit(t, e.student, someRight)
	var wg sync.WaitGroup
	nos := make([]int, 8)
	for i := range nos {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if v, err := e.retake(e.student); err == nil {
				nos[i] = v.AttemptNo
			}
		}()
	}
	wg.Wait()
	for i, n := range nos {
		if n != 2 {
			t.Fatalf("call %d returned %d, want 2", i, n)
		}
	}
	if n := len(e.attemptRows(t, "s1")); n != 2 {
		t.Fatalf("%d rows, want 2", n)
	}
}

func TestRetakeClosedAfterAcceptingUntil(t *testing.T) {
	e := subEnv(t)
	e.clk.Set(t0.Add(time.Hour))
	e.mustSubmit(t, e.student, someRight)
	e.clk.Set(closes.Add(time.Minute)) // acceptingUntil exactly
	_, err := e.retake(e.student)
	wantClosed(t, err)
	if n := len(e.attemptRows(t, "s1")); n != 1 {
		t.Fatalf("%d rows, want 1", n)
	}
	// One second earlier it still works (the grace window is open).
	e.clk.Set(closes.Add(time.Minute - time.Second))
	if no := e.mustRetake(t, e.student); no != 2 {
		t.Fatal(no)
	}
	// An unfinished retake that expires is finalized by the closed answer.
	if _, err := e.autosave(e.student, map[string]string{"q3": "o0"}, 0); err != nil {
		t.Fatal(err)
	}
	e.clk.Set(closes.Add(time.Hour))
	_, err = e.retake(e.student)
	wantClosed(t, err)
	if rows := e.attemptRows(t, "s1"); rows[1].Status != store.AttemptSubmitted || !rows[1].AutoSubmitted {
		t.Fatalf("expired retake not finalized: %+v", rows[1])
	}
}

func TestRetakeIs404ForDeclinerNonHolderAndMissingExam(t *testing.T) {
	e := subEnv(t)
	e.seedAttempt(t, "e1", "p1", store.AttemptNotStarted, nil)
	if err := e.svc.Decline(context.Background(), e.parent, "e1"); err != nil {
		t.Fatal(err)
	}
	other := e.addUser(t, "s9", "s9@x.com", "student")
	for name, u := range map[string]store.User{"decliner": e.parent, "non-holder": other, "creator without a row": e.teacher} {
		_, err := e.retake(u)
		if err == nil {
			t.Fatalf("%s: want 404", name)
		}
		wantStatus(t, err, http.StatusNotFound)
	}
	_, err := e.svc.Retake(context.Background(), e.student, "nope-nope-1")
	wantStatus(t, err, http.StatusNotFound)
}

func TestRetakeLimitIs50Attempts(t *testing.T) {
	e := subEnv(t)
	ctx := context.Background()
	sub := t0
	for n := 2; n <= store.MaxAttemptsPerExam-1; n++ { // attempts 1..49 submitted
		if _, err := e.attempts.PutAttemptIfAbsent(ctx, store.Attempt{ExamID: "e1", UserID: "s1", AttemptNo: n,
			Status: store.AttemptSubmitted, SubmittedAt: &sub, CreatedAt: t0}); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.attempts.Submit(ctx, store.Attempt{ExamID: "e1", UserID: "s1", AttemptNo: 1, Status: store.AttemptSubmitted, SubmittedAt: &sub}); err != nil {
		t.Fatal(err)
	}
	if no := e.mustRetake(t, e.student); no != store.MaxAttemptsPerExam {
		t.Fatalf("49th attempt submitted: retake = %d, want 50", no)
	}
	e.mustSubmit(t, e.student, someRight)
	_, err := e.retake(e.student)
	wantAppErr(t, err, http.StatusConflict, "conflict", "You have reached the limit of 50 attempts for this exam.")
	if n := len(e.attemptRows(t, "s1")); n != 50 {
		t.Fatalf("%d rows", n)
	}
	items := e.list(t, e.student, exams.ScopeClosed)
	if len(items) != 1 || items[0].CanRetake == nil || *items[0].CanRetake || items[0].AttemptCount != 50 {
		t.Fatalf("list at the limit: %+v", items)
	}
}

func TestRetakeHasAFreshPeekBudget(t *testing.T) {
	e := revEnv(t)
	for _, q := range []string{"q3", "q1", "q2"} {
		e.mustReveal(t, q)
	}
	if _, err := e.reveal(e.student, "q3"); err != nil { // a repeat is free
		t.Fatal(err)
	}
	e.mustSubmit(t, e.student, someRight)
	e.mustRetake(t, e.student)
	v := e.get(t, e.student, "e1")
	if v.Attempt.PeeksLeft != 3 || len(v.Attempt.Revealed) != 0 {
		t.Fatalf("fresh attempt: peeksLeft %d revealed %d", v.Attempt.PeeksLeft, len(v.Attempt.Revealed))
	}
	for _, q := range v.Exam.Questions {
		if q.CorrectIndex != nil {
			t.Fatalf("the earlier attempt's peeks must not leak the key into the retake: %+v", q)
		}
	}
	if r := e.mustReveal(t, "q1"); r.PeeksLeft != 2 {
		t.Fatalf("peeksLeft = %d, want 2", r.PeeksLeft)
	}
	rows := e.attemptRows(t, "s1")
	if len(rows[0].Revealed) != 3 || len(rows[1].Revealed) != 1 {
		t.Fatalf("peeks per attempt: %v %v", rows[0].Revealed, rows[1].Revealed)
	}
	res, err := e.resultNo(e.student, 1)
	if err != nil || res.PeeksUsed != 3 {
		t.Fatalf("attempt 1 peeks used: %+v %v", res.PeeksUsed, err)
	}
}

func TestDeclineIsNotAllowedOnARetake(t *testing.T) {
	e := subEnv(t)
	e.mustSubmit(t, e.student, someRight)
	e.mustRetake(t, e.student)
	wantStatus(t, e.svc.Decline(context.Background(), e.student, "e1"), http.StatusConflict)
	if rows := e.attemptRows(t, "s1"); rows[1].Status != store.AttemptNotStarted {
		t.Fatalf("%+v", rows[1])
	}
}

func TestResultDefaultsToLatestSubmittedAndTakesAttemptNumber(t *testing.T) {
	e := subEnv(t)
	e.clk.Set(t0.Add(time.Hour))
	e.mustSubmit(t, e.student, someRight) // attempt 1: 3 marks
	e.mustRetake(t, e.student)

	// A retake that has not been submitted never turns the default result into a 409.
	v, err := e.resultNo(e.student, 0)
	if err != nil || v.AttemptNo != 1 || v.Status != "submitted" || v.Score == nil || *v.Score != 3 {
		t.Fatalf("default during a retake: %+v %v", v, err)
	}
	if len(v.Attempts) != 2 || v.Attempts[1].AttemptNo != 2 || v.Attempts[1].Status != store.AttemptNotStarted ||
		v.Attempts[1].Score != nil || v.Attempts[1].SubmittedAt != nil {
		t.Fatalf("attempts[]: %+v", v.Attempts)
	}
	if v.BestAttemptNo == nil || *v.BestAttemptNo != 1 {
		t.Fatalf("bestAttemptNo: %v", v.BestAttemptNo)
	}
	// Asking for the unfinished attempt is the old 409; one that does not exist is 404.
	_, err = e.resultNo(e.student, 2)
	wantStatus(t, err, http.StatusConflict)
	_, err = e.resultNo(e.student, 3)
	wantStatus(t, err, http.StatusNotFound)

	e.clk.Set(t0.Add(2 * time.Hour))
	e.mustSubmit(t, e.student, allRight) // attempt 2: 6 marks
	d, err := e.resultNo(e.student, 0)
	if err != nil || d.AttemptNo != 2 || *d.Score != 6 || *d.BestAttemptNo != 2 || len(d.Attempts) != 2 {
		t.Fatalf("default after the second submit: %+v %v", d, err)
	}
	one, err := e.resultNo(e.student, 1)
	if err != nil || one.AttemptNo != 1 || *one.Score != 3 || *one.BestAttemptNo != 2 {
		t.Fatalf("?attempt=1: %+v %v", one, err)
	}
	// Each attempt returns its own answers; marks for every attempt are in attempts[].
	var a1, a2 string
	for _, q := range one.Questions {
		if q.ID == "q3" {
			a1 = q.YourAnswer
		}
	}
	for _, q := range d.Questions {
		if q.ID == "q3" {
			a2 = q.YourAnswer
		}
	}
	if a1 != "wrong-secret" || a2 != "o0" {
		t.Fatalf("answers per attempt: %q %q", a1, a2)
	}
	if d.Attempts[0].Score == nil || *d.Attempts[0].Score != 3 || d.Attempts[1].Score == nil || *d.Attempts[1].Score != 6 ||
		d.Attempts[0].MaxScore != 6 || d.Attempts[0].SubmittedAt == nil {
		t.Fatalf("attempts[]: %+v", d.Attempts)
	}
	// A key hidden from the student stays hidden for every attempt.
	for _, r := range []exams.ResultView{d, one} {
		if r.KeyVisible {
			t.Fatal("key must be hidden for a non-creator before the exam is over")
		}
		for _, q := range r.Questions {
			if q.Correct != nil || q.CorrectAnswer != nil {
				t.Fatalf("per-question correctness leaked: %+v", q)
			}
		}
	}
	// Once the exam is over the key opens for every attempt.
	e.clk.Set(closes.Add(time.Hour))
	late, err := e.resultNo(e.student, 1)
	if err != nil || !late.KeyVisible || late.Questions[0].Correct == nil {
		t.Fatalf("after close: %+v %v", late, err)
	}
}

func TestResultBestAttemptIsEarliestOnATie(t *testing.T) {
	e := subEnv(t)
	e.mustSubmit(t, e.student, allRight)
	e.mustRetake(t, e.student)
	e.mustSubmit(t, e.student, allRight)
	v, err := e.resultNo(e.student, 0)
	if err != nil || v.AttemptNo != 2 || v.BestAttemptNo == nil || *v.BestAttemptNo != 1 {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestResultIsMissedOnlyWithoutASubmittedAttempt(t *testing.T) {
	e := subEnv(t)
	e.mustSubmit(t, e.student, someRight)
	e.mustRetake(t, e.student) // never started
	e.clk.Set(closes.Add(time.Hour))
	v, err := e.resultNo(e.student, 0)
	if err != nil || v.Status != "submitted" || v.AttemptNo != 1 {
		t.Fatalf("submitted then never retaken: %+v %v", v, err)
	}
	v, err = e.resultNo(e.student, 2) // the never-started retake itself
	if err != nil || v.Status != exams.AttemptMissed || v.Score != nil || v.AttemptNo != 2 {
		t.Fatalf("attempt 2: %+v %v", v, err)
	}
	if v.Attempts[1].Status != exams.AttemptMissed {
		t.Fatalf("attempts[]: %+v", v.Attempts)
	}
}

func TestListShowsOneItemPerExamWithBestScoreAndCanRetake(t *testing.T) {
	e := subEnv(t)
	e.clk.Set(t0.Add(time.Hour))
	e.mustSubmit(t, e.student, allRight) // 6
	e.mustRetake(t, e.student)
	e.mustSubmit(t, e.student, someRight) // 3: the best stays 6
	e.mustRetake(t, e.student)            // attempt 3, unfinished

	// Mid retake: the exam is open, with the best score so far, and not in closed as well.
	open := e.list(t, e.student, exams.ScopeOpen)
	if len(open) != 1 || open[0].AttemptCount != 3 || open[0].Score == nil || *open[0].Score != 6 ||
		open[0].AttemptStatus != store.AttemptSubmitted || open[0].CanRetake == nil || *open[0].CanRetake {
		t.Fatalf("open: %+v", open)
	}
	if c := e.list(t, e.student, exams.ScopeClosed); len(c) != 0 {
		t.Fatalf("a retake in progress must not also be closed: %+v", c)
	}

	e.mustSubmit(t, e.student, map[string]string{})
	if o := e.list(t, e.student, exams.ScopeOpen); len(o) != 0 {
		t.Fatalf("open after submit: %+v", o)
	}
	closed := e.list(t, e.student, exams.ScopeClosed)
	if len(closed) != 1 {
		t.Fatalf("one item per exam: %+v", closed)
	}
	it := closed[0]
	if it.AttemptCount != 3 || it.Score == nil || *it.Score != 6 || it.AttemptStatus != store.AttemptSubmitted ||
		it.CanRetake == nil || !*it.CanRetake || it.MaxScore == nil || *it.MaxScore != 6 {
		t.Fatalf("closed: %+v", it)
	}
	// After the deadline: no retake, and an exam with a submitted attempt is never "missed", even
	// when a later retake was never started.
	e.mustRetake(t, e.student) // attempt 4, never started
	e.clk.Set(closes.Add(time.Hour))
	it = e.list(t, e.student, exams.ScopeClosed)[0]
	if it.CanRetake == nil || *it.CanRetake || it.AttemptStatus != store.AttemptSubmitted || it.AttemptCount != 4 || *it.Score != 6 {
		t.Fatalf("after the deadline: %+v", it)
	}
}

func TestListFinalizedRetakeCountsAsSubmitted(t *testing.T) {
	e := subEnv(t)
	e.mustSubmit(t, e.student, someRight) // 3
	e.mustRetake(t, e.student)
	if _, err := e.autosave(e.student, allRight, 0); err != nil { // saved, never submitted
		t.Fatal(err)
	}
	e.clk.Set(closes.Add(time.Hour))
	it := e.list(t, e.student, exams.ScopeClosed)[0]
	if it.AttemptStatus != store.AttemptSubmitted || it.Score == nil || *it.Score != 6 || it.AttemptCount != 2 {
		t.Fatalf("auto-submitted retake: %+v", it)
	}
}

func TestListNeverStartedStillMissedAndDeclinedStillHidden(t *testing.T) {
	e := subEnv(t)
	e.seedAttempt(t, "e1", "p1", store.AttemptDeclined, nil)
	e.clk.Set(closes.Add(time.Hour))
	it := e.list(t, e.student, exams.ScopeClosed)
	if len(it) != 1 || it[0].AttemptStatus != exams.AttemptMissed || it[0].AttemptCount != 1 || it[0].CanRetake == nil || *it[0].CanRetake {
		t.Fatalf("%+v", it)
	}
	if len(e.list(t, e.parent, exams.ScopeClosed))+len(e.list(t, e.parent, exams.ScopeOpen)) != 0 {
		t.Fatal("a declined exam is in neither list")
	}
}

func TestStatusGroupsByUserWithHistoryAndNoAnswers(t *testing.T) {
	e := subEnv(t)
	e.addUser(t, "s2", "s2@x.com", "student")
	e.seedAttempt(t, "e1", "s2", store.AttemptNotStarted, nil)
	e.seedAttempt(t, "e1", "p1", store.AttemptDeclined, nil)
	e.clk.Set(t0.Add(time.Hour))
	e.mustSubmit(t, e.student, someRight) // 3
	e.mustRetake(t, e.student)
	e.clk.Set(t0.Add(2 * time.Hour))
	e.mustSubmit(t, e.student, allRight) // 6
	e.mustRetake(t, e.student)           // attempt 3 unfinished

	v := e.status3(t, e.teacher, "e1")
	if len(v.Attempts) != 3 {
		t.Fatalf("one row per user: %+v", v.Attempts)
	}
	if v.Counts != (exams.Counts{Submitted: 1, NotStarted: 1, Declined: 1}) {
		t.Fatalf("counts are per user: %+v", v.Counts)
	}
	r := rowFor(t, v, "s1")
	if r.Status != store.AttemptSubmitted || r.AttemptCount != 3 || r.Score == nil || *r.Score != 6 ||
		r.SubmittedAt == nil || !r.SubmittedAt.Equal(t0.Add(2*time.Hour)) {
		t.Fatalf("s1 row: %+v", r)
	}
	if len(r.History) != 3 || r.History[0].AttemptNo != 1 || *r.History[0].Score != 3 || r.History[1].AttemptNo != 2 ||
		*r.History[1].Score != 6 || r.History[2].AttemptNo != 3 || r.History[2].Status != store.AttemptNotStarted ||
		r.History[2].Score != nil || r.History[0].SubmittedAt == nil {
		t.Fatalf("history: %+v", r.History)
	}
	if s2 := rowFor(t, v, "s2"); s2.Status != store.AttemptNotStarted || s2.AttemptCount != 1 || len(s2.History) != 1 || s2.Score != nil {
		t.Fatalf("s2 row: %+v", s2)
	}
	if js := marshal(t, v); strings.Contains(js, "wrong-secret") || strings.Contains(strings.ToLower(js), "answers") {
		t.Fatalf("status must carry no answers: %s", js)
	}
	// The status view finalizes an expired retake and counts it as submitted.
	if _, err := e.autosave(e.student, map[string]string{"q3": "o0"}, 0); err != nil {
		t.Fatal(err)
	}
	e.clk.Set(closes.Add(time.Hour))
	r = rowFor(t, e.status3(t, e.teacher, "e1"), "s1")
	if r.History[2].Status != store.AttemptSubmitted || !r.History[2].AutoSubmitted || *r.Score != 6 {
		t.Fatalf("finalized retake: %+v", r)
	}
}

func TestStatusAndAssignCountDistinctUsersNotRows(t *testing.T) {
	// A student creator may assign at most 10 people: ten holders with extra attempts still fit
	// once, and an eleventh distinct user does not.
	e := newEnv(t)
	e.seedFull(t, "e1", "s1", "mcq")
	var ids []string
	for i := 0; i < 10; i++ {
		id := "st" + string(rune('a'+i))
		e.addUser(t, id, id+"@x.com", "student")
		e.seedAttempt(t, "e1", id, store.AttemptNotStarted, nil)
		ids = append(ids, id)
	}
	ctx := context.Background()
	sub := t0
	for n := 2; n <= 4; n++ { // three extra attempts for the first holder
		if _, err := e.attempts.PutAttemptIfAbsent(ctx, store.Attempt{ExamID: "e1", UserID: ids[0], AttemptNo: n,
			Status: store.AttemptSubmitted, SubmittedAt: &sub, CreatedAt: t0}); err != nil {
			t.Fatal(err)
		}
	}
	e.addUser(t, "st-extra", "extra@x.com", "student")
	_, err := e.svc.Assign(ctx, e.student, "e1", []string{"extra@x.com"})
	wantField(t, err, "emails")
	// Re-assigning an existing holder is reported as already assigned, however many attempts.
	res, err := e.svc.Assign(ctx, e.student, "e1", []string{ids[0] + "@x.com"})
	if err != nil || len(res.AlreadyAssigned) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestCreatorTakingTheirOwnExamCanRetakeAndKeyHidesDuringIt(t *testing.T) {
	e := subEnv(t)
	e.seedAttempt(t, "e1", "t1", store.AttemptNotStarted, nil) // the creator's practice row
	if v := e.get(t, e.teacher, "e1"); v.KeyVisible {
		t.Fatal("key hidden while the creator has not finished")
	}
	e.mustSubmit(t, e.teacher, someRight)
	if v := e.get(t, e.teacher, "e1"); !v.KeyVisible {
		t.Fatal("key visible once the creator has submitted")
	}
	if no := e.mustRetake(t, e.teacher); no != 2 {
		t.Fatal(no)
	}
	v := e.get(t, e.teacher, "e1")
	if v.KeyVisible || v.Attempt.AttemptNo != 2 || v.AttemptCount != 2 {
		t.Fatalf("key must hide again during the retake: %+v", v)
	}
	for _, q := range v.Exam.Questions {
		if q.CorrectIndex != nil {
			t.Fatalf("key leaked during a retake: %+v", q)
		}
	}
	// Peeking works on the retake (the key is hidden), and the default result is attempt 1.
	if _, err := e.svc.Reveal(context.Background(), e.teacher, "e1", "q1"); err != nil {
		t.Fatal(err)
	}
	r, err := e.resultNo(e.teacher, 0)
	if err != nil || r.AttemptNo != 1 || r.KeyVisible {
		t.Fatalf("result during the creator's retake: %+v %v", r, err)
	}
	e.mustSubmit(t, e.teacher, allRight)
	if v := e.get(t, e.teacher, "e1"); !v.KeyVisible {
		t.Fatal("key visible again after the retake is submitted")
	}
	if r, err := e.resultNo(e.teacher, 1); err != nil || !r.KeyVisible || r.Questions[0].Correct == nil {
		t.Fatalf("creator result after submitting: %+v %v", r, err)
	}
}

func TestSubmittedThenNeverRetakenIsNotMissedInGetOrList(t *testing.T) {
	e := subEnv(t)
	e.mustSubmit(t, e.student, someRight)
	e.clk.Set(closes.Add(time.Hour))
	v := e.get(t, e.student, "e1")
	if v.Attempt.Status != store.AttemptSubmitted || v.AttemptCount != 1 {
		t.Fatalf("%+v", v.Attempt)
	}
	if it := e.list(t, e.student, exams.ScopeClosed); len(it) != 1 || it[0].AttemptStatus != store.AttemptSubmitted {
		t.Fatalf("%+v", it)
	}
}
