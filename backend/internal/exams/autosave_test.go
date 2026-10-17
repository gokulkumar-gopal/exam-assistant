package exams_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	return e.svc.Autosave(context.Background(), u, "e1", exams.AutosaveInput{Answers: answers, LastQuestionIndex: &idx})
}

func (e *env) row(t *testing.T) store.Attempt {
	t.Helper()
	a, err := e.attempts.GetAttempt(context.Background(), "e1", "s1", 1)
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
	if _, err := e.attempts.Reveal(context.Background(), "e1", "s1", 1, "q1", 3, t0); err != nil {
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
	wantField(t, err, "answers[q1]") // names the offending question
	_, err = e.autosave(e.student, map[string]string{"q1": strings.Repeat("é", 5001)}, 0)
	wantField(t, err, "answers[q1]")
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
	_, err := e.autosave(e.student, map[string]string{"q1": "late"}, 0)
	wantStatus(t, err, http.StatusInternalServerError) // logged, not retried into a write
	if len(e.row(t).Answers) != 0 {
		t.Fatal("a failed finalize still wrote answers")
	}
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
	if err := e.attempts.Decline(context.Background(), "e1", "s1", 1); err != nil {
		t.Fatal(err)
	}
	_, declined := e.autosave(e.student, map[string]string{}, 0)
	stranger := e.addUser(t, "x1", "x1@x.com", "student")
	_, noRow := e.autosave(stranger, map[string]string{}, 0)
	_, missing := e.svc.Autosave(context.Background(), e.student, "nope", exams.AutosaveInput{Answers: map[string]string{}, LastQuestionIndex: ip(0)})
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
	_, miss := e.svc.Autosave(context.Background(), e.teacher, "nope", exams.AutosaveInput{Answers: map[string]string{}, LastQuestionIndex: ip(0)})
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

func TestAutosaveRequiresLastQuestionIndex(t *testing.T) {
	e := asEnv(t)
	_, err := e.svc.Autosave(context.Background(), e.student, "e1", exams.AutosaveInput{Answers: map[string]string{}})
	wantField(t, err, "lastQuestionIndex")
}

func TestAutosaveInputJSONIsStrict(t *testing.T) {
	var in exams.AutosaveInput
	if err := json.Unmarshal([]byte(`{"answers":{"q1":"a"},"lastQuestionIndex":1}`), &in); err != nil ||
		in.Answers["q1"] != "a" || in.LastQuestionIndex == nil || *in.LastQuestionIndex != 1 {
		t.Fatalf("%v %+v", err, in)
	}
	if err := json.Unmarshal([]byte(`{"answers":{},"lastQuestionIndex":0,"x":1}`), &in); err == nil {
		t.Fatal("unknown field accepted")
	}
	e := asEnv(t)
	for _, body := range []string{`{"answers":{"q1":null},"lastQuestionIndex":0}`, `{"answers":{"q1":3},"lastQuestionIndex":0}`, `{"answers":{"q1":["a"]},"lastQuestionIndex":0}`} {
		var bad exams.AutosaveInput
		if err := json.Unmarshal([]byte(body), &bad); err != nil {
			t.Fatal(err)
		}
		_, err := e.svc.Autosave(context.Background(), e.student, "e1", bad)
		wantField(t, err, "answers")
		if e.row(t).Status != store.AttemptNotStarted {
			t.Fatal("rejected save changed the attempt")
		}
	}
}

// bigExam stores a 60-question exam "e60" (ids k0..k59) with s1 assigned.
func (e *env) bigExam(t *testing.T) {
	t.Helper()
	var qs []store.Question
	for i := 0; i < 60; i++ {
		qs = append(qs, store.Question{ID: fmt.Sprintf("k%d", i), Marks: 1, Text: "t", SourceDocID: "d", SourceDocName: "B.pdf", ModelAnswer: "m"})
	}
	x := store.Exam{ID: "e60", CreatorID: "t1", Title: "T", Type: "free_text", TotalMarks: 60, ClosesAt: closes, Questions: qs, CreatedAt: t0}
	if err := e.exams.PutExam(context.Background(), x); err != nil {
		t.Fatal(err)
	}
	if _, err := e.attempts.PutAttemptIfAbsent(context.Background(), store.Attempt{AttemptNo: 1, ExamID: "e60", UserID: "s1",
		Status: store.AttemptNotStarted, Answers: map[string]string{}, Revealed: []string{}, CreatedAt: t0}); err != nil {
		t.Fatal(err)
	}
}

// answersOfBytes builds answers summing exactly n bytes: 10000-byte answers (5000 x "é", the
// per-value maximum) then one remainder answer.
func answersOfBytes(n int) map[string]string {
	m := map[string]string{}
	i := 0
	for ; n >= 10000; i, n = i+1, n-10000 {
		m[fmt.Sprintf("k%d", i)] = strings.Repeat("é", 5000)
	}
	if n/2 > 0 {
		m[fmt.Sprintf("k%d", i)] = strings.Repeat("é", n/2)
		i++
	}
	if n%2 == 1 {
		m[fmt.Sprintf("k%d", i)] = "a"
	}
	return m
}

func TestAutosaveTotalAnswerBytesBoundary(t *testing.T) {
	e := asEnv(t)
	e.bigExam(t)
	save := func(a map[string]string) error {
		_, err := e.svc.Autosave(context.Background(), e.student, "e60", exams.AutosaveInput{Answers: a, LastQuestionIndex: ip(0)})
		return err
	}
	at := answersOfBytes(exams.MaxAutosaveBytes)
	sum := 0
	for _, v := range at {
		sum += len(v)
	}
	if sum != 350<<10 || exams.MaxAutosaveBytes != 358400 {
		t.Fatalf("setup sums to %d", sum)
	}
	if err := save(at); err != nil {
		t.Fatalf("exactly 350 KB must pass: %v", err)
	}
	over := answersOfBytes(exams.MaxAutosaveBytes + 1)
	wantField(t, save(over), "answers")
	a, _ := e.attempts.GetAttempt(context.Background(), "e60", "s1", 1)
	if len(a.Answers) != len(at) {
		t.Fatal("the rejected save replaced the stored answers")
	}
}

// recFin records what the service hands the finalizer.
type recFin struct {
	statuses []string
	nows     []time.Time
	err      error
}

func (f *recFin) FinalizeIfExpired(_ context.Context, a store.Attempt, _ store.Exam, now time.Time) (store.Attempt, error) {
	f.statuses = append(f.statuses, a.Status)
	f.nows = append(f.nows, now)
	return a, f.err
}

func TestAutosaveFinalizerOnlyForInProgressWithServiceNow(t *testing.T) {
	e := asEnv(t) // s1 not_started
	f := &recFin{}
	e.svc.SetFinalizer(f)
	s3 := e.addUser(t, "s3", "s3@x.com", "student")
	e.seedAttempt(t, "e1", "s3", store.AttemptSubmitted, nil)
	s4 := e.addUser(t, "s4", "s4@x.com", "student")
	e.seedAttempt(t, "e1", "s4", store.AttemptInProgress, nil)
	now := closes.Add(exams.GraceWindow + 7*time.Second)
	e.clk.Set(now)
	for _, u := range []store.User{e.student, s3, s4} {
		_, err := e.autosave(u, map[string]string{}, 0)
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != "exam_closed" {
			t.Fatalf("%s: %v", u.ID, err)
		}
	}
	if len(f.statuses) != 1 || f.statuses[0] != store.AttemptInProgress {
		t.Fatalf("finalizer saw %v", f.statuses)
	}
	if !f.nows[0].Equal(now) {
		t.Fatalf("finalizer now %v, want the service clock %v", f.nows[0], now)
	}
}

func TestAutosaveDeclinedAfterDeadlineIs404NotClosed(t *testing.T) {
	e := asEnv(t)
	f := &recFin{}
	e.svc.SetFinalizer(f)
	if err := e.attempts.Decline(context.Background(), "e1", "s1", 1); err != nil {
		t.Fatal(err)
	}
	e.clk.Set(closes.Add(time.Hour))
	_, declined := e.autosave(e.student, map[string]string{}, 0)
	_, missing := e.svc.Autosave(context.Background(), e.student, "nope", exams.AutosaveInput{Answers: map[string]string{}, LastQuestionIndex: ip(0)})
	wantStatus(t, declined, http.StatusNotFound) // 409 would tell a declined user that the exam exists
	if declined.Error() != missing.Error() || len(f.statuses) != 0 {
		t.Fatalf("%v | %v | finalizer %v", declined, missing, f.statuses)
	}
}
