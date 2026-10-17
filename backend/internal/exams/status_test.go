package exams_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/exams"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

func (e *env) status3(t *testing.T, u store.User, id string) exams.StatusView {
	t.Helper()
	v, err := e.svc.Status(context.Background(), u, id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	return v
}

func rowFor(t *testing.T, v exams.StatusView, uid string) exams.StatusRow {
	t.Helper()
	for _, r := range v.Attempts {
		if r.UserID == uid {
			return r
		}
	}
	t.Fatalf("no row for %s in %+v", uid, v.Attempts)
	return exams.StatusRow{}
}

// Rule (controller ruling): an existing exam and a caller who is not the creator but has an
// attempt row (an assignee, declined included) gets 403; a caller with no relation to the exam
// and a missing exam get the same 404.
func TestStatusCreatorOnlyElse403(t *testing.T) {
	e := newEnv(t)
	e.seedExam(t, "e1", "t1", "mcq", closes, t0)
	e.seedAttempt(t, "e1", "s1", store.AttemptNotStarted, nil)
	e.seedAttempt(t, "e1", "p1", store.AttemptDeclined, nil)
	ctx := context.Background()
	if _, err := e.svc.Status(ctx, e.student, "e1"); err == nil {
		t.Fatal("assignee: want 403")
	} else {
		wantStatus(t, err, http.StatusForbidden)
	}
	_, err := e.svc.Status(ctx, e.parent, "e1")
	wantStatus(t, err, http.StatusForbidden)
	if v := e.status3(t, e.teacher, "e1"); v.Exam.ID != "e1" || len(v.Attempts) != 2 {
		t.Fatalf("creator: %+v", v)
	}
}

func TestStatusUnrelatedAndUnknownAreIdentical404(t *testing.T) {
	e := newEnv(t)
	e.seedExam(t, "e1", "t1", "mcq", closes, t0)
	e.seedAttempt(t, "e1", "s1", store.AttemptNotStarted, nil)
	other := e.addUser(t, "o1", "o1@x.com", "teacher")
	_, a := e.svc.Status(context.Background(), other, "e1")
	_, b := e.svc.Status(context.Background(), other, "nope-nope-1")
	wantStatus(t, a, http.StatusNotFound)
	wantStatus(t, b, http.StatusNotFound)
	ja, _ := json.Marshal(a.(*apperr.Error))
	jb, _ := json.Marshal(b.(*apperr.Error))
	if string(ja) != string(jb) || a.Error() != b.Error() {
		t.Fatalf("%s vs %s", ja, jb)
	}
}

func TestStatusCountsAndRows(t *testing.T) {
	e := newEnv(t)
	e.addUser(t, "s2", "s2@x.com", "student")
	e.addUser(t, "s3", "s3@x.com", "student")
	e.seedExam(t, "e1", "t1", "mcq", closes, t0)
	e.seedAttempt(t, "e1", "s1", store.AttemptSubmitted, ip(7))
	e.seedAttempt(t, "e1", "s2", store.AttemptInProgress, nil)
	e.seedAttempt(t, "e1", "s3", store.AttemptNotStarted, nil)
	e.seedAttempt(t, "e1", "p1", store.AttemptDeclined, nil)
	e.clk.Set(closes.Add(-time.Hour))
	v := e.status3(t, e.teacher, "e1")
	if v.Counts != (exams.Counts{Submitted: 1, InProgress: 1, NotStarted: 1, Declined: 1}) {
		t.Fatalf("counts %+v", v.Counts)
	}
	if v.Exam.Title != "T-e1" || v.Exam.TotalMarks != 30 || !v.Exam.ClosesAt.Equal(closes) {
		t.Fatalf("exam %+v", v.Exam)
	}
	if len(v.Attempts) != 4 {
		t.Fatalf("rows %+v", v.Attempts)
	}
	r := rowFor(t, v, "s1")
	if r.Name != "s1" || r.Email != "s1@x.com" || r.Status != "submitted" {
		t.Fatalf("row %+v", r)
	}
	// stable order by name then id
	for i := 1; i < len(v.Attempts); i++ {
		a, b := v.Attempts[i-1], v.Attempts[i]
		if a.Name > b.Name || (a.Name == b.Name && a.UserID > b.UserID) {
			t.Fatalf("order %+v", v.Attempts)
		}
	}
}

func TestStatusRowsNeverContainAnswers(t *testing.T) {
	e := newEnv(t)
	e.seedExam(t, "e1", "t1", "mcq", closes, t0)
	a := store.Attempt{AttemptNo: 1, ExamID: "e1", UserID: "s1", Status: store.AttemptSubmitted, CreatedAt: t0, SubmittedAt: &t0,
		Answers: map[string]string{"q1": "SECRET-ANSWER-TEXT"}, LastQuestionIndex: 3, Revealed: []string{"q1"}, ScoreHalves: ip(2)}
	if _, err := e.attempts.PutAttemptIfAbsent(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	v := e.status3(t, e.teacher, "e1")
	b, _ := json.Marshal(v)
	s := string(b)
	for _, bad := range []string{"SECRET-ANSWER-TEXT", "answers", "lastQuestionIndex", "lastIdx", "revealed", "q1"} {
		if strings.Contains(s, bad) {
			t.Fatalf("%q leaked: %s", bad, s)
		}
	}
	if len(v.Attempts) != 1 {
		t.Fatalf("rows: %s", s)
	}
}

func TestStatusNeverStartedAfterOverStaysNotStarted(t *testing.T) {
	e := newEnv(t)
	e.seedExam(t, "e1", "t1", "mcq", closes, t0)
	e.seedAttempt(t, "e1", "s1", store.AttemptNotStarted, nil)
	e.clk.Set(closes.Add(time.Hour))
	v := e.status3(t, e.teacher, "e1")
	if r := rowFor(t, v, "s1"); r.Status != "not_started" {
		t.Fatalf("status %q", r.Status)
	}
	if v.Counts.NotStarted != 1 {
		t.Fatalf("counts %+v", v.Counts)
	}
}

func TestStatusDeclinedRow(t *testing.T) {
	e := newEnv(t)
	e.seedExam(t, "e1", "t1", "mcq", closes, t0)
	e.seedAttempt(t, "e1", "s1", store.AttemptDeclined, nil)
	v := e.status3(t, e.teacher, "e1")
	r := rowFor(t, v, "s1")
	if r.Status != "declined" || r.Score != nil || r.SubmittedAt != nil || v.Counts.Declined != 1 {
		t.Fatalf("%+v %+v", r, v.Counts)
	}
}

func TestStatusScoreForSubmittedMCQ(t *testing.T) {
	e := newEnv(t)
	e.addUser(t, "s2", "s2@x.com", "student")
	e.seedExam(t, "e1", "t1", "mcq", closes, t0)
	e.seedExam(t, "e2", "t1", "free", closes, t0)
	e.seedAttempt(t, "e1", "s1", store.AttemptSubmitted, ip(7)) // 3.5
	e.seedAttempt(t, "e1", "s2", store.AttemptInProgress, nil)
	e.seedAttempt(t, "e2", "s1", store.AttemptSubmitted, ip(7))
	v := e.status3(t, e.teacher, "e1")
	r := rowFor(t, v, "s1")
	if r.Score == nil || *r.Score != 3.5 || r.SubmittedAt == nil {
		t.Fatalf("%+v", r)
	}
	if rowFor(t, v, "s2").Score != nil {
		t.Fatal("in-progress must have no score")
	}
	if rowFor(t, e.status3(t, e.teacher, "e2"), "s1").Score != nil {
		t.Fatal("free-text must have no score")
	}
}

func TestStatusUnknownUserNoError(t *testing.T) {
	e := newEnv(t)
	e.seedExam(t, "e1", "t1", "mcq", closes, t0)
	e.seedAttempt(t, "e1", "ghost", store.AttemptNotStarted, nil)
	v := e.status3(t, e.teacher, "e1")
	if r := rowFor(t, v, "ghost"); r.Name != "" || r.Email != "" || r.Status != "not_started" {
		t.Fatalf("%+v", r)
	}
}

type statusFin struct{ called int }

func (f *statusFin) FinalizeIfExpired(_ context.Context, a store.Attempt, _ store.Exam, now time.Time) (store.Attempt, error) {
	f.called++
	a.Status = store.AttemptSubmitted
	a.ScoreHalves = ip(4)
	a.SubmittedAt = &now
	return a, nil
}

func TestStatusFinalizesExpiredInProgress(t *testing.T) {
	e := newEnv(t)
	f := &statusFin{}
	e.svc.SetFinalizer(f)
	e.seedExam(t, "e1", "t1", "mcq", closes, t0)
	e.seedAttempt(t, "e1", "s1", store.AttemptInProgress, nil)
	e.clk.Set(closes.Add(time.Hour))
	v := e.status3(t, e.teacher, "e1")
	r := rowFor(t, v, "s1")
	if f.called != 1 || r.Status != "submitted" || r.Score == nil || *r.Score != 2 || v.Counts.Submitted != 1 {
		t.Fatalf("called %d row %+v counts %+v", f.called, r, v.Counts)
	}
}
