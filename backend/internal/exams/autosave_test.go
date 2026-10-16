package exams_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/exams"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// asEnv seeds an mcq exam e1 (questions q3, q1, q2) by t1 with s1 assigned (not_started).
func asEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	e.seedFull(t, "e1", "t1", "mcq")
	e.seedAttempt(t, "e1", "s1", store.AttemptNotStarted, nil)
	return e
}

func (e *env) autosave(u store.User, answers map[string]string, idx int) (time.Time, error) {
	return e.svc.Autosave(context.Background(), u, "e1", exams.AutosaveInput{Answers: answers, LastQuestionIndex: idx})
}

func (e *env) row(t *testing.T) store.Attempt {
	t.Helper()
	a, err := e.attempts.GetAttempt(context.Background(), "e1", "s1")
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAutosaveFirstSaveStartsAttempt(t *testing.T) {
	e := asEnv(t)
	e.clk.Set(t0.Add(time.Minute))
	at, err := e.autosave(e.student, map[string]string{"q1": "o1"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !at.Equal(t0.Add(time.Minute)) {
		t.Fatalf("savedAt %v", at)
	}
	a := e.row(t)
	if a.Status != store.AttemptInProgress || a.StartedAt == nil || !a.StartedAt.Equal(at) ||
		a.Answers["q1"] != "o1" || a.LastQuestionIndex != 2 || a.SavedAt == nil || !a.SavedAt.Equal(at) {
		t.Fatalf("%+v", a)
	}
	e.clk.Set(t0.Add(2 * time.Minute))
	if _, err := e.autosave(e.student, map[string]string{"q1": "o2"}, 0); err != nil {
		t.Fatal(err)
	}
	if b := e.row(t); !b.StartedAt.Equal(at) {
		t.Fatalf("StartedAt moved: %v", b.StartedAt)
	}
}

func TestAutosaveReplacesWholeMap(t *testing.T) {
	e := asEnv(t)
	if _, err := e.autosave(e.student, map[string]string{"q1": "a", "q2": "b"}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := e.autosave(e.student, map[string]string{"q3": "c"}, 0); err != nil {
		t.Fatal(err)
	}
	first := e.row(t)
	if len(first.Answers) != 1 || first.Answers["q3"] != "c" {
		t.Fatalf("not replaced: %v", first.Answers)
	}
	if _, err := e.autosave(e.student, map[string]string{"q3": "c"}, 0); err != nil { // same body twice
		t.Fatal(err)
	}
	second := e.row(t)
	if len(second.Answers) != 1 || second.Answers["q3"] != "c" || second.LastQuestionIndex != 0 || second.Status != store.AttemptInProgress {
		t.Fatalf("not idempotent: %+v", second)
	}
	if _, err := e.autosave(e.student, map[string]string{}, 0); err != nil { // empty map clears
		t.Fatal(err)
	}
	if len(e.row(t).Answers) != 0 {
		t.Fatal("empty map did not clear")
	}
}

func TestAutosaveLeavesRevealedAlone(t *testing.T) {
	e := asEnv(t)
	if _, err := e.attempts.Reveal(context.Background(), "e1", "s1", "q1", 3, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := e.autosave(e.student, map[string]string{"q2": "x"}, 1); err != nil {
		t.Fatal(err)
	}
	if r := e.row(t).Revealed; len(r) != 1 || r[0] != "q1" {
		t.Fatalf("revealed touched: %v", r)
	}
}

func TestAutosaveRejectsUnknownQuestionIDs(t *testing.T) {
	e := asEnv(t)
	_, err := e.autosave(e.student, map[string]string{"q1": "a", "nope": "b"}, 0)
	wantField(t, err, "answers")
	if e.row(t).Status != store.AttemptNotStarted {
		t.Fatal("rejected save changed the attempt")
	}
}

func TestAutosaveRejectsBadShapes(t *testing.T) {
	e := asEnv(t)
	_, err := e.autosave(e.student, nil, 0)
	wantField(t, err, "answers")
	for _, idx := range []int{-1, 3, 1000} {
		_, err = e.autosave(e.student, map[string]string{}, idx)
		wantField(t, err, "lastQuestionIndex")
	}
	for _, idx := range []int{0, 2} {
		if _, err = e.autosave(e.student, map[string]string{}, idx); err != nil {
			t.Fatalf("idx %d: %v", idx, err)
		}
	}
}

func TestAutosaveValueOver5000Chars422(t *testing.T) {
	e := asEnv(t)
	if _, err := e.autosave(e.student, map[string]string{"q1": strings.Repeat("a", 5000)}, 0); err != nil {
		t.Fatalf("5000 must pass: %v", err)
	}
	// 5000 multi-byte characters are 5000 characters, not 15000 bytes.
	if _, err := e.autosave(e.student, map[string]string{"q1": strings.Repeat("é", 5000)}, 0); err != nil {
		t.Fatalf("5000 runes must pass: %v", err)
	}
	_, err := e.autosave(e.student, map[string]string{"q1": strings.Repeat("a", 5001)}, 0)
	wantField(t, err, "answers")
	_, err = e.autosave(e.student, map[string]string{"q1": strings.Repeat("é", 5001)}, 0)
	wantField(t, err, "answers")
	if got := e.row(t).Answers["q1"]; len([]rune(got)) != 5000 {
		t.Fatalf("rejected value was stored")
	}
}

func TestAutosaveMCQValueNotCheckedAgainstOptions(t *testing.T) {
	e := asEnv(t)
	if _, err := e.autosave(e.student, map[string]string{"q1": "half-typed free text"}, 0); err != nil {
		t.Fatal(err)
	}
}

func TestAutosaveInsideGraceWindowAccepted(t *testing.T) {
	e := asEnv(t)
	e.clk.Set(closes.Add(30 * time.Second)) // after closesAt, inside the 60 s grace
	if _, err := e.autosave(e.student, map[string]string{"q1": "late"}, 0); err != nil {
		t.Fatal(err)
	}
	if e.row(t).Answers["q1"] != "late" {
		t.Fatal("not stored")
	}
}

func TestAutosaveJustBeforeAcceptingUntilAccepted(t *testing.T) {
	e := asEnv(t)
	f := &fakeFin{}
	e.svc.SetFinalizer(f)
	e.seedAttempt(t, "e1", "s2", store.AttemptNotStarted, nil)
	e.clk.Set(closes.Add(exams.GraceWindow - time.Nanosecond))
	if _, err := e.autosave(e.student, map[string]string{"q1": "x"}, 0); err != nil {
		t.Fatal(err)
	}
	if f.calls != 0 {
		t.Fatal("finalize ran inside the grace window")
	}
}

func TestAutosaveAtAcceptingUntilIs409ExamClosed(t *testing.T) {
	e := asEnv(t)
	f := &fakeFin{}
	e.svc.SetFinalizer(f)
	if _, err := e.autosave(e.student, map[string]string{"q1": "x"}, 0); err != nil { // in_progress
		t.Fatal(err)
	}
	e.clk.Set(closes.Add(exams.GraceWindow))
	_, err := e.autosave(e.student, map[string]string{"q1": "y"}, 0)
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Status != http.StatusConflict || ae.Code != "exam_closed" {
		t.Fatalf("want 409 exam_closed, got %v", err)
	}
	if f.calls != 1 {
		t.Fatalf("lazy finalize calls = %d, want 1", f.calls)
	}
	if e.row(t).Answers["q1"] != "x" {
		t.Fatal("a closed autosave wrote")
	}
}

func TestAutosaveClosedFinalizeFailureIs500(t *testing.T) {
	e := asEnv(t)
	e.svc.SetFinalizer(&fakeFin{err: errors.New("boom")})
	if _, err := e.autosave(e.student, map[string]string{}, 0); err != nil {
		t.Fatal(err)
	}
	e.clk.Set(closes.Add(time.Hour))
	_, err := e.autosave(e.student, map[string]string{}, 0)
	wantStatus(t, err, http.StatusInternalServerError)
}

func TestAutosaveAfterSubmit409Conflict(t *testing.T) {
	e := asEnv(t)
	e.seedAttempt(t, "e1", "s3", store.AttemptSubmitted, nil)
	s3 := e.addUser(t, "s3", "s3@x.com", "student")
	_, err := e.autosave(s3, map[string]string{"q1": "x"}, 0)
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Status != http.StatusConflict || ae.Code != "conflict" {
		t.Fatalf("want 409 conflict, got %v", err)
	}
}

func TestAutosaveOnDeclinedOrNoAttempt404(t *testing.T) {
	e := asEnv(t)
	if err := e.attempts.Decline(context.Background(), "e1", "s1"); err != nil {
		t.Fatal(err)
	}
	_, declined := e.autosave(e.student, map[string]string{}, 0)
	stranger := e.addUser(t, "x1", "x1@x.com", "student")
	_, noRow := e.autosave(stranger, map[string]string{}, 0)
	_, missing := e.svc.Autosave(context.Background(), e.student, "nope", exams.AutosaveInput{Answers: map[string]string{}})
	for _, err := range []error{declined, noRow, missing} {
		wantStatus(t, err, http.StatusNotFound)
	}
	if declined.Error() != noRow.Error() || noRow.Error() != missing.Error() {
		t.Fatalf("404s differ: %v | %v | %v", declined, noRow, missing)
	}
}

func TestAutosaveCreatorWithoutAttempt404(t *testing.T) {
	e := asEnv(t)
	_, err := e.autosave(e.teacher, map[string]string{}, 0)
	wantStatus(t, err, http.StatusNotFound)
	_, miss := e.svc.Autosave(context.Background(), e.teacher, "nope", exams.AutosaveInput{Answers: map[string]string{}})
	if err.Error() != miss.Error() {
		t.Fatalf("differs from missing: %v | %v", err, miss)
	}
}

// TestKnownGapDocumented pins the accepted gap of backend-spec 8.5: the answers map carries no
// sequence number, so a delayed older PUT that arrives after a newer one overwrites it. If a
// sequence check is ever added, this test must change deliberately.
func TestKnownGapDocumented(t *testing.T) {
	e := asEnv(t)
	if _, err := e.autosave(e.student, map[string]string{"q1": "newer"}, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := e.autosave(e.student, map[string]string{"q1": "older"}, 0); err != nil {
		t.Fatal(err)
	}
	if a := e.row(t); a.Answers["q1"] != "older" || a.LastQuestionIndex != 0 {
		t.Fatalf("gap behaviour changed: %+v", a)
	}
}
