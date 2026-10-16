package exams_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/exams"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
	"github.com/gokulkumar/exam-assistant/backend/internal/store/memory"
)

// revEnv is subEnv: mcq exam e1 (q3 3 marks "o0", q1 2 marks "o1", q2 1 mark "o2"), maxPeeks 3,
// not_started s1, real finalizer.
func revEnv(t *testing.T) *env { return subEnv(t) }

func (e *env) reveal(u store.User, q string) (exams.RevealView, error) {
	return e.svc.Reveal(context.Background(), u, "e1", q)
}

func (e *env) mustReveal(t *testing.T, q string) exams.RevealView {
	t.Helper()
	v, err := e.reveal(e.student, q)
	if err != nil {
		t.Fatalf("reveal %s: %v", q, err)
	}
	return v
}

// mutateExam swaps e1 for a modified copy (the memory store has no replace) and rewires the service.
func (e *env) mutateExam(t *testing.T, f func(*store.Exam)) {
	t.Helper()
	x := e.exam(t, "e1")
	f(&x)
	e.exams = memory.NewExamStore()
	if err := e.exams.PutExam(context.Background(), x); err != nil {
		t.Fatal(err)
	}
	e.svc = exams.NewService(e.users, e.exams, e.attempts, e.clk)
	e.svc.SetFinalizer(exams.NewFinalizer(e.attempts))
}

func (e *env) setMaxPeeks(t *testing.T, n int) {
	t.Helper()
	e.mutateExam(t, func(x *store.Exam) { x.MaxPeeks = n })
}

func wantAppErr(t *testing.T, err error, status int, code, msg string) {
	t.Helper()
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Status != status || ae.Code != code || ae.Message != msg {
		t.Fatalf("want %d %s %q, got %v", status, code, msg, err)
	}
}

func TestRevealChargesOncePerQuestionIdempotent(t *testing.T) {
	e := revEnv(t)
	for i := 0; i < 3; i++ {
		v := e.mustReveal(t, "q1")
		if v.QuestionID != "q1" || v.PeeksLeft != 2 {
			t.Fatalf("call %d: %+v", i, v)
		}
	}
	if r := e.row(t).Revealed; len(r) != 1 || r[0] != "q1" {
		t.Fatalf("revealed %v", r)
	}
	// A repeat at the cap is free and never the 'No peeks left.' 409.
	e.mustReveal(t, "q2")
	if v := e.mustReveal(t, "q3"); v.PeeksLeft != 0 {
		t.Fatalf("%+v", v)
	}
	for _, q := range []string{"q1", "q2", "q3"} {
		if v := e.mustReveal(t, q); v.PeeksLeft != 0 || v.QuestionID != q {
			t.Fatalf("repeat at cap %s: %+v", q, v)
		}
	}
	if r := e.row(t).Revealed; len(r) != 3 {
		t.Fatalf("revealed %v", r)
	}
}

func TestRevealReturnsCorrectIndexForMCQAndModelAnswerForFree(t *testing.T) {
	e := revEnv(t)
	v := e.mustReveal(t, "q1")
	if v.CorrectIndex == nil || *v.CorrectIndex != 1 || v.ModelAnswer != "" {
		t.Fatalf("mcq: %+v", v)
	}
	f := newEnv(t)
	f.seedFull(t, "e1", "t1", "free")
	f.seedAttempt(t, "e1", "s1", store.AttemptNotStarted, nil)
	v = f.mustReveal(t, "q2")
	if v.CorrectIndex != nil || v.ModelAnswer != secretModel+"-q2" || v.PeeksLeft != 2 {
		t.Fatalf("free: %+v", v)
	}
	if got := marshal(t, v); strings.Contains(got, "correctIndex") || !strings.Contains(got, `"peeksLeft":2`) || !strings.Contains(got, `"questionId":"q2"`) {
		t.Fatalf("wire %s", got)
	}
}

func TestRevealMaxPeeks0Is403(t *testing.T) {
	e := revEnv(t)
	e.setMaxPeeks(t, 0)
	_, err := e.reveal(e.student, "q1")
	wantAppErr(t, err, http.StatusForbidden, "forbidden", "Peeking is turned off for this exam.")
	if a := e.row(t); a.Status != store.AttemptNotStarted || len(a.Revealed) != 0 {
		t.Fatalf("refused reveal changed the row: %+v", a)
	}
}

func TestRevealNoPeeksLeft409(t *testing.T) {
	e := revEnv(t)
	e.setMaxPeeks(t, 2)
	e.mustReveal(t, "q1")
	e.mustReveal(t, "q2")
	_, err := e.reveal(e.student, "q3")
	wantAppErr(t, err, http.StatusConflict, "conflict", "No peeks left.")
	if !regexp.MustCompile(`(?i)no peeks`).MatchString(err.Error()) {
		t.Fatal("frontend regex /no peeks/i must match")
	}
	if r := e.row(t).Revealed; len(r) != 2 {
		t.Fatalf("revealed %v", r)
	}
}

func TestRevealKeyAlreadyVisible409(t *testing.T) {
	e := revEnv(t)
	// The creator took their own exam and submitted: the key is theirs.
	e.seedAttempt(t, "e1", "t1", store.AttemptSubmitted, nil)
	_, err := e.reveal(e.teacher, "q1")
	wantAppErr(t, err, http.StatusConflict, "conflict", "The answer key is already visible.")
	// Not the same message as running out of peeks.
	if strings.Contains(err.Error(), "No peeks") {
		t.Fatal("messages must be distinct")
	}
	if len(e.rowOf(t, "t1").Revealed) != 0 {
		t.Fatal("charged")
	}
}

func (e *env) rowOf(t *testing.T, uid string) store.Attempt {
	t.Helper()
	a, err := e.attempts.GetAttempt(context.Background(), "e1", uid)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestRevealAfterAcceptingUntil409ExamClosed(t *testing.T) {
	e := revEnv(t)
	e.mustReveal(t, "q1")
	e.save(t, "s1", map[string]string{"q3": "o0"}, 0)
	e.clk.Set(closes.Add(59 * time.Second)) // grace window: still open
	e.mustReveal(t, "q2")
	e.clk.Set(closes.Add(60 * time.Second))
	_, err := e.reveal(e.student, "q3")
	wantClosed(t, err)
	a := e.row(t)
	if len(a.Revealed) != 2 || a.Status != store.AttemptSubmitted || !a.AutoSubmitted {
		t.Fatalf("closed reveal must finalize and not charge: %+v", a)
	}
	// A repeat of an already revealed question after close is closed too (the key is public).
	_, err = e.reveal(e.student, "q1")
	wantClosed(t, err)
}

func TestRevealStartsNotStartedAttempt(t *testing.T) {
	e := revEnv(t)
	e.clk.Set(t0.Add(5 * time.Minute))
	e.mustReveal(t, "q1")
	a := e.row(t)
	if a.Status != store.AttemptInProgress || a.StartedAt == nil || !a.StartedAt.Equal(t0.Add(5*time.Minute)) || a.SavedAt != nil {
		t.Fatalf("%+v", a)
	}
}

func TestRevealWithoutAttemptOrDeclined404(t *testing.T) {
	e := revEnv(t)
	e.seedAttempt(t, "e1", "p1", store.AttemptDeclined, nil)
	ctx := context.Background()
	type call struct {
		name string
		u    store.User
		exam string
	}
	var first *apperr.Error
	for _, c := range []call{{"stranger", e.addUser(t, "s9", "s9@x.com", "student"), "e1"}, {"creator no row", e.teacher, "e1"},
		{"declined", e.parent, "e1"}, {"missing exam", e.student, "nope"}} {
		_, err := e.svc.Reveal(ctx, c.u, c.exam, "q1")
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Status != http.StatusNotFound {
			t.Fatalf("%s: %v", c.name, err)
		}
		if first == nil {
			first = ae
		} else if fmt.Sprintf("%+v", ae) != fmt.Sprintf("%+v", first) {
			t.Fatalf("%s differs: %+v vs %+v", c.name, ae, first)
		}
	}
}

func TestRevealUnknownQuestion404or422(t *testing.T) {
	e := revEnv(t)
	for _, q := range []string{"zzz", "", "Q1"} {
		_, err := e.reveal(e.student, q)
		wantField(t, err, "questionId")
	}
	if a := e.row(t); a.Status != store.AttemptNotStarted || len(a.Revealed) != 0 {
		t.Fatalf("%+v", a)
	}
}

func TestRevealConditionalUpdateRaceNeverExceedsMax(t *testing.T) {
	e := revEnv(t)
	e.mutateExam(t, func(x *store.Exam) {
		x.Questions = nil
		for i := 0; i < 20; i++ {
			ci := 0
			x.Questions = append(x.Questions, store.Question{ID: fmt.Sprintf("r%02d", i), Marks: 1, Text: "t", Options: []string{"a", "b"}, CorrectIndex: &ci, SourceDocID: "d", SourceDocName: "B.pdf"})
		}
		x.TotalMarks = 20
	})
	var wg sync.WaitGroup
	var ok, conflict atomic.Int32
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := e.reveal(e.student, fmt.Sprintf("r%02d", i))
			var ae *apperr.Error
			switch {
			case err == nil:
				ok.Add(1)
			case errors.As(err, &ae) && ae.Status == 409 && ae.Message == "No peeks left.":
				conflict.Add(1)
			default:
				t.Errorf("%d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	if ok.Load() != 3 || conflict.Load() != 17 || len(e.row(t).Revealed) != 3 {
		t.Fatalf("ok %d conflict %d revealed %v", ok.Load(), conflict.Load(), e.row(t).Revealed)
	}
}

func TestRevealReturnsOnlyTheAskedKey(t *testing.T) {
	e := newEnv(t)
	e.seedFull(t, "e1", "t1", "free")
	e.seedAttempt(t, "e1", "s1", store.AttemptNotStarted, nil)
	v := e.mustReveal(t, "q1")
	got := marshal(t, v)
	if !strings.Contains(got, secretModel+"-q1") || strings.Contains(got, secretModel+"-q2") || strings.Contains(got, secretModel+"-q3") {
		t.Fatalf("%s", got)
	}
	m := newEnv(t)
	m.seedFull(t, "e1", "t1", "mcq")
	m.seedAttempt(t, "e1", "s1", store.AttemptNotStarted, nil)
	if got := marshal(t, m.mustReveal(t, "q3")); strings.Contains(got, "options") || strings.Count(got, "correctIndex") != 1 {
		t.Fatalf("%s", got)
	}
}

func TestRevealOnSubmittedAttemptIsRefusedWithoutMutation(t *testing.T) {
	e := revEnv(t)
	e.mustReveal(t, "q1")
	if _, err := e.submit(e.student, body(map[string]string{"q3": "o0", "q1": "o1"}, 0)); err != nil {
		t.Fatal(err)
	}
	before := e.row(t)
	_, err := e.reveal(e.student, "q2")
	wantAppErr(t, err, http.StatusConflict, "conflict", "This exam has already been submitted.")
	_, err = e.reveal(e.student, "q1") // even an already revealed one: the attempt is over
	wantAppErr(t, err, http.StatusConflict, "conflict", "This exam has already been submitted.")
	after := e.row(t)
	if len(after.Revealed) != 1 || *after.ScoreHalves != *before.ScoreHalves || *after.ScoreBeforePeeksHalves != *before.ScoreBeforePeeksHalves {
		t.Fatalf("before %+v after %+v", before, after)
	}
}

// raceAttempts runs hook once, just before the first Submit reaches the store: a reveal that
// lands between Submit's read and its conditional write.
type raceAttempts struct {
	store.AttemptStore
	hook func()
	once sync.Once
}

func (r *raceAttempts) Submit(ctx context.Context, a store.Attempt) error {
	r.once.Do(r.hook)
	return r.AttemptStore.Submit(ctx, a)
}

func TestRevealLandingBeforeSubmitWriteIsGradedNotLost(t *testing.T) {
	e := revEnv(t)
	race := &raceAttempts{AttemptStore: e.attempts}
	race.hook = func() {
		if _, err := e.attempts.Reveal(context.Background(), "e1", "s1", "q3", 3, t0); err != nil {
			t.Error(err)
		}
	}
	svc := exams.NewService(e.users, e.exams, race, e.clk)
	svc.SetFinalizer(exams.NewFinalizer(race))
	res, err := svc.Submit(context.Background(), e.student, "e1", body(map[string]string{"q3": "o0", "q1": "o1", "q2": "o2"}, 0))
	if err != nil {
		t.Fatal(err)
	}
	// q3 (3 marks) was peeked at before the write landed: 1.5 + 2 + 1, not 6.
	if res.Score == nil || *res.Score != 4.5 || *res.ScoreBeforePeeks != 6 {
		t.Fatalf("%+v", res)
	}
	a := e.row(t)
	if *a.ScoreHalves != 9 || len(a.Revealed) != 1 {
		t.Fatalf("%+v", a)
	}
}

func TestRevealedAnswersReturnedOnResume(t *testing.T) {
	e := newEnv(t)
	e.seedFull(t, "e1", "t1", "free")
	e.seedAttempt(t, "e1", "s1", store.AttemptNotStarted, nil)
	e.mustReveal(t, "q1")
	e.mustReveal(t, "q3")
	e.mustReveal(t, "q1")
	v := e.get(t, e.student, "e1")
	if v.KeyVisible || v.Attempt == nil || v.Attempt.PeeksLeft != 1 || len(v.Attempt.Revealed) != 2 {
		t.Fatalf("%+v", v.Attempt)
	}
	got := marshal(t, v)
	if !strings.Contains(got, secretModel+"-q1") || !strings.Contains(got, secretModel+"-q3") || strings.Contains(got, secretModel+"-q2") {
		t.Fatalf("only revealed keys may appear: %s", got)
	}
}

func TestRevealedCorrectScoresHalfAndBeforePeeksFull(t *testing.T) {
	e := revEnv(t)
	e.mustReveal(t, "q3") // 3 marks
	e.mustReveal(t, "q2") // 1 mark: a half mark is 1 half
	res, err := e.submit(e.student, body(map[string]string{"q3": "o0", "q1": "o1", "q2": "o2"}, 0))
	if err != nil {
		t.Fatal(err)
	}
	if *res.Score != 1.5+2+0.5 || *res.ScoreBeforePeeks != 6 || res.MaxScore != 6 {
		t.Fatalf("%+v %v %v", res, *res.Score, *res.ScoreBeforePeeks)
	}
	if a := e.row(t); *a.ScoreHalves != 8 || *a.ScoreBeforePeeksHalves != 12 {
		t.Fatalf("%+v", a)
	}
}

func TestRevealedWrongScoresZero(t *testing.T) {
	e := revEnv(t)
	e.mustReveal(t, "q3")
	e.mustReveal(t, "q1")
	res, err := e.submit(e.student, body(map[string]string{"q3": "o3", "q2": "o2"}, 0)) // q3 wrong, q1 unanswered
	if err != nil {
		t.Fatal(err)
	}
	if *res.Score != 1 || *res.ScoreBeforePeeks != 1 {
		t.Fatalf("%v %v", *res.Score, *res.ScoreBeforePeeks)
	}
}

func TestResultPeeksUsedAndRevealedFlag(t *testing.T) {
	e := revEnv(t)
	e.mustReveal(t, "q3")
	e.mustReveal(t, "q1")
	if _, err := e.submit(e.student, body(map[string]string{"q3": "o0", "q1": "o1", "q2": "o2"}, 0)); err != nil {
		t.Fatal(err)
	}
	v, m := e.result(t, e.student, "e1")
	if v.PeeksUsed != 2 || *v.Score != 1.5+1+1 || *v.ScoreBeforePeeks != 6 {
		t.Fatalf("%+v", v)
	}
	flags := map[string]bool{}
	for _, q := range resQs(t, m) {
		flags[q["id"].(string)] = q["revealed"].(bool)
	}
	if !flags["q3"] || !flags["q1"] || flags["q2"] {
		t.Fatalf("%v", flags)
	}
}
