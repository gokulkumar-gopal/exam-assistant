package exams_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/exams"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// subEnv seeds mcq exam e1 (q3: 3 marks, "o0"; q1: 2 marks, "o1"; q2: 1 mark, "o2") by t1, with a
// not_started s1, and wires the real finalizer. closes is t0+48h.
func subEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	e.seedFull(t, "e1", "t1", "mcq")
	e.seedAttempt(t, "e1", "s1", store.AttemptNotStarted, nil)
	e.svc.SetFinalizer(exams.NewFinalizer(e.attempts))
	return e
}

func (e *env) save(t *testing.T, user string, answers map[string]string, idx int) {
	t.Helper()
	if err := e.attempts.Save(context.Background(), "e1", user, 1, answers, idx, t0); err != nil {
		t.Fatal(err)
	}
}

func (e *env) submit(u store.User, in *exams.SubmitInput) (exams.SubmitResult, error) {
	return e.svc.Submit(context.Background(), u, "e1", in)
}

func body(answers map[string]string, idx int) *exams.SubmitInput {
	return &exams.SubmitInput{AutosaveInput: exams.AutosaveInput{Answers: answers, LastQuestionIndex: &idx}}
}

func f64(v float64) *float64 { return &v }

func wantClosed(t *testing.T, err error) {
	t.Helper()
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Status != http.StatusConflict || ae.Code != "exam_closed" {
		t.Fatalf("want 409 exam_closed, got %v", err)
	}
}

func TestSubmitStoresBodyAnswersThenGrades(t *testing.T) {
	e := subEnv(t)
	e.save(t, "s1", map[string]string{"q3": "o0"}, 0)
	e.clk.Set(t0.Add(time.Hour))
	res, err := e.submit(e.student, body(map[string]string{"q1": "o1", "q2": "o2"}, 2))
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "submitted" || res.Score == nil || *res.Score != 3 || res.ScoreBeforePeeks == nil || *res.ScoreBeforePeeks != 3 ||
		res.MaxScore != 6 || res.AutoSubmitted {
		t.Fatalf("%+v", res)
	}
	a := e.row(t)
	want := map[string]string{"q1": "o1", "q2": "o2"}
	if a.Status != store.AttemptSubmitted || !reflect.DeepEqual(a.Answers, want) || a.LastQuestionIndex != 2 ||
		a.ScoreHalves == nil || *a.ScoreHalves != 6 || a.SavedAt == nil || !a.SavedAt.Equal(e.clk.Now()) {
		t.Fatalf("stored %+v", a)
	}
}

func TestSubmitAutoSubmitRaceBodyAnswersOnlyInBody(t *testing.T) {
	// the client's deadline submit sends answers it never autosaved; they must be graded
	e := subEnv(t)
	e.save(t, "s1", map[string]string{}, 0)
	e.clk.Set(closes.Add(45 * time.Second))
	res, err := e.submit(e.student, body(map[string]string{"q3": "o0", "q1": "o1", "q2": "o2"}, 0))
	if err != nil || res.Score == nil || *res.Score != 6 {
		t.Fatalf("%+v %v", res, err)
	}
	if got := e.row(t).Answers; len(got) != 3 {
		t.Fatalf("answers not stored: %v", got)
	}
}

func TestSubmitSubmittedAtIsMinOfNowAndClosesAt(t *testing.T) {
	e := subEnv(t)
	e.save(t, "s1", map[string]string{"q3": "o0"}, 0)
	before := closes.Add(-time.Hour)
	e.clk.Set(before)
	res, err := e.submit(e.student, nil)
	if err != nil || !res.SubmittedAt.Equal(before) {
		t.Fatalf("before closesAt: %+v %v", res, err)
	}
	e2 := subEnv(t)
	e2.save(t, "s1", map[string]string{"q3": "o0"}, 0)
	e2.clk.Set(closes.Add(30 * time.Second))
	res, err = e2.submit(e2.student, nil)
	if err != nil || !res.SubmittedAt.Equal(closes) {
		t.Fatalf("at closesAt+30s: %+v %v", res, err)
	}
	if a := e2.row(t); a.SubmittedAt == nil || !a.SubmittedAt.Equal(closes) || a.AutoSubmitted {
		t.Fatalf("%+v", a)
	}
	e3 := subEnv(t)
	e3.clk.Set(closes) // exactly closesAt: min is closesAt either way
	if res, err = e3.submit(e3.student, nil); err != nil || !res.SubmittedAt.Equal(closes) {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestSubmitInGraceWindowAccepted(t *testing.T) {
	e := subEnv(t)
	e.save(t, "s1", map[string]string{"q2": "o2"}, 0)
	e.clk.Set(closes.Add(exams.GraceWindow - time.Nanosecond))
	res, err := e.submit(e.student, body(map[string]string{"q2": "o2"}, 0))
	if err != nil || res.Score == nil || *res.Score != 1 || res.AutoSubmitted {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestSubmitAtAcceptingUntilExamClosed409(t *testing.T) {
	e := subEnv(t)
	e.save(t, "s1", map[string]string{"q1": "o1"}, 0)
	e.clk.Set(closes.Add(exams.GraceWindow))
	_, err := e.submit(e.student, body(map[string]string{"q3": "o0", "q1": "o1", "q2": "o2"}, 0))
	wantClosed(t, err)
	a := e.row(t) // finalize ran for this attempt: saved answers graded, body ignored
	if a.Status != store.AttemptSubmitted || !a.AutoSubmitted || a.SubmittedAt == nil || !a.SubmittedAt.Equal(closes) ||
		a.ScoreHalves == nil || *a.ScoreHalves != 4 || len(a.Answers) != 1 {
		t.Fatalf("%+v", a)
	}
}

func TestSubmitIdempotentReturnsExisting(t *testing.T) {
	e := subEnv(t)
	e.clk.Set(t0.Add(time.Hour))
	first, err := e.submit(e.student, body(map[string]string{"q1": "o1"}, 1))
	if err != nil {
		t.Fatal(err)
	}
	e.clk.Set(t0.Add(2 * time.Hour))
	again, err := e.submit(e.student, body(map[string]string{"q1": "WRONG", "q2": "o2"}, 0))
	if err != nil || !reflect.DeepEqual(first, again) {
		t.Fatalf("%+v vs %+v (%v)", first, again, err)
	}
	if a := e.row(t); a.Answers["q1"] != "o1" || len(a.Answers) != 1 {
		t.Fatalf("a resubmit changed the stored answers: %+v", a)
	}
	e.clk.Set(closes.Add(time.Hour)) // still idempotent after the exam is over
	if again, err = e.submit(e.student, nil); err != nil || !reflect.DeepEqual(first, again) {
		t.Fatalf("after close: %+v (%v)", again, err)
	}
}

func TestSubmitNotStartedWithBodyStartsAndSubmits(t *testing.T) {
	e := subEnv(t)
	e.clk.Set(t0.Add(time.Hour))
	res, err := e.submit(e.student, body(map[string]string{"q3": "o0"}, 0))
	if err != nil || res.Score == nil || *res.Score != 3 {
		t.Fatalf("%+v %v", res, err)
	}
	a := e.row(t)
	if a.Status != store.AttemptSubmitted || a.StartedAt == nil || !a.StartedAt.Equal(e.clk.Now()) {
		t.Fatalf("%+v", a)
	}
}

func TestSubmitWithoutAnswersUsesSaved(t *testing.T) {
	for name, in := range map[string]*exams.SubmitInput{"nil body": nil, "empty object": {}} {
		t.Run(name, func(t *testing.T) {
			e := subEnv(t)
			e.save(t, "s1", map[string]string{"q1": "o1", "q2": "nope"}, 1)
			e.clk.Set(t0.Add(time.Hour))
			res, err := e.submit(e.student, in)
			if err != nil || res.Score == nil || *res.Score != 2 {
				t.Fatalf("%+v %v", res, err)
			}
			if a := e.row(t); a.Answers["q1"] != "o1" || a.LastQuestionIndex != 1 {
				t.Fatalf("saved answers lost: %+v", a)
			}
		})
	}
}

func TestSubmitPeekScoring(t *testing.T) {
	e := subEnv(t)
	if _, err := e.attempts.Reveal(context.Background(), "e1", "s1", 1, "q1", 3, t0); err != nil {
		t.Fatal(err)
	}
	e.clk.Set(t0.Add(time.Hour))
	res, err := e.submit(e.student, body(map[string]string{"q3": "o0", "q1": "o1", "q2": "o2"}, 0))
	if err != nil || res.Score == nil || *res.Score != 5 || res.ScoreBeforePeeks == nil || *res.ScoreBeforePeeks != 6 {
		t.Fatalf("%+v %v", res, err)
	}
	a := e.row(t)
	if a.ScoreHalves == nil || *a.ScoreHalves != 10 || a.ScoreBeforePeeksHalves == nil || *a.ScoreBeforePeeksHalves != 12 ||
		len(a.Revealed) != 1 {
		t.Fatalf("%+v", a)
	}
}

func TestSubmitResponseFreeTextNullScores(t *testing.T) {
	e := newEnv(t)
	e.seedFull(t, "e1", "t1", "free")
	e.seedAttempt(t, "e1", "s1", store.AttemptNotStarted, nil)
	e.svc.SetFinalizer(exams.NewFinalizer(e.attempts))
	e.clk.Set(t0.Add(time.Hour))
	res, err := e.submit(e.student, body(map[string]string{"q1": "my essay"}, 0))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if v, ok := m["score"]; !ok || v != nil {
		t.Fatalf("score must be present and null: %s", b)
	}
	if v, ok := m["scoreBeforePeeks"]; !ok || v != nil {
		t.Fatalf("scoreBeforePeeks must be present and null: %s", b)
	}
	if m["maxScore"] != float64(6) || m["status"] != "submitted" || len(m) != 6 {
		t.Fatalf("%s", b)
	}
	if a := e.row(t); a.ScoreHalves != nil || a.ScoreBeforePeeksHalves != nil || a.Answers["q1"] != "my essay" {
		t.Fatalf("%+v", a)
	}
}

func TestSubmitResponseNeverCarriesKeyMaterial(t *testing.T) {
	e := subEnv(t)
	res, err := e.submit(e.student, body(map[string]string{"q1": "o1"}, 0))
	if err != nil || res.Status != "submitted" {
		t.Fatalf("submit must succeed first: %+v %v", res, err)
	}
	b := marshal(t, res)
	keys := []string{}
	var m map[string]any
	_ = json.Unmarshal([]byte(b), &m)
	keyNames(m, &keys)
	for _, k := range keys {
		switch k {
		case "status", "score", "scorebeforepeeks", "maxscore", "submittedat", "autosubmitted":
		default:
			t.Fatalf("unexpected key %q in %s", k, b)
		}
	}
}

func TestSubmitNotFoundShapesAreIdentical(t *testing.T) {
	e := subEnv(t)
	other := e.addUser(t, "x1", "x1@x.com", "student")
	decl := e.addUser(t, "d1", "d1@x.com", "student")
	e.seedAttempt(t, "e1", "d1", store.AttemptDeclined, nil)
	_, missing := e.svc.Submit(context.Background(), e.student, "nope", body(map[string]string{}, 0))
	for name, err := range map[string]error{
		"creator without row": second(e.submit(e.teacher, body(map[string]string{}, 0))),
		"stranger":            second(e.submit(other, body(map[string]string{}, 0))),
		"declined":            second(e.submit(decl, body(map[string]string{}, 0))),
		"missing exam":        missing,
	} {
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Status != http.StatusNotFound || ae.Code != "not_found" {
			t.Fatalf("%s: %v", name, err)
		}
		var me *apperr.Error
		errors.As(missing, &me)
		if ae.Message != me.Message {
			t.Fatalf("%s: message differs", name)
		}
	}
	if a, err := e.attempts.GetAttempt(context.Background(), "e1", "s1", 1); err != nil || a.Status != store.AttemptNotStarted {
		t.Fatal("a 404 submit changed another attempt")
	}
	// declined after the exam is over is still the 404, never exam_closed (which would show a row exists)
	e.clk.Set(closes.Add(time.Hour))
	_, err := e.submit(decl, nil)
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Status != http.StatusNotFound {
		t.Fatalf("declined after close: %v", err)
	}
}

func second(_ exams.SubmitResult, err error) error { return err }

func TestSubmitCreatorWithOwnRowCanSubmit(t *testing.T) {
	e := subEnv(t)
	e.seedAttempt(t, "e1", "t1", store.AttemptNotStarted, nil)
	e.clk.Set(t0.Add(time.Hour))
	if res, err := e.submit(e.teacher, body(map[string]string{"q3": "o0"}, 0)); err != nil || res.Score == nil || *res.Score != 3 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestSubmitValidation(t *testing.T) {
	e := subEnv(t)
	idx := func(i int) *int { return &i }
	for name, c := range map[string]struct {
		in    *exams.SubmitInput
		field string
	}{
		"unknown question": {body(map[string]string{"nope": "x"}, 0), "answers"},
		"index too big":    {body(map[string]string{}, 3), "lastQuestionIndex"},
		"index negative":   {body(map[string]string{}, -1), "lastQuestionIndex"},
		"index alone bad":  {&exams.SubmitInput{AutosaveInput: exams.AutosaveInput{LastQuestionIndex: idx(99)}}, "lastQuestionIndex"},
		"too long answer": {body(map[string]string{"q1": string(make([]rune, exams.MaxAutosaveRunes+1))}, 0),
			"answers[q1]"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := e.submit(e.student, c.in)
			wantField(t, err, c.field)
			if e.row(t).Status != store.AttemptNotStarted {
				t.Fatal("a rejected submit changed the attempt")
			}
		})
	}
	var in exams.SubmitInput
	if err := json.Unmarshal([]byte(`{"answers":{"q1":null},"lastQuestionIndex":0}`), &in); err != nil {
		t.Fatal(err)
	}
	_, err := e.submit(e.student, &in)
	wantField(t, err, "answers")
	if err := json.Unmarshal([]byte(`{"answers":{},"bogus":1}`), &in); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestSubmitStoreErrorIs500AndNoFinalizerStillCloses(t *testing.T) {
	e := newEnv(t)
	e.seedFull(t, "e1", "t1", "mcq")
	e.seedAttempt(t, "e1", "s1", store.AttemptInProgress, nil)
	e.clk.Set(closes.Add(time.Minute))
	_, err := e.submit(e.student, nil) // no finalizer wired: still 409, nothing panics
	wantClosed(t, err)
	e.svc.SetFinalizer(&fakeFin{err: errors.New("boom")})
	_, err = e.submit(e.student, nil)
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Status != http.StatusInternalServerError {
		t.Fatalf("%v", err)
	}
}

// ---- finalize ----

func TestFinalizeAtAcceptingUntilGradesSavedAnswers(t *testing.T) {
	e := subEnv(t)
	e.save(t, "s1", map[string]string{"q3": "o0"}, 0)
	stale := e.row(t) // the caller's copy predates the last save
	e.save(t, "s1", map[string]string{"q3": "o0", "q1": "bad", "q2": "o2"}, 2)
	e.clk.Set(closes.Add(exams.GraceWindow))
	fin := exams.NewFinalizer(e.attempts)
	x, _ := e.exams.GetExam(context.Background(), "e1")
	got, err := fin.FinalizeIfExpired(context.Background(), stale, x, e.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	stored := e.row(t)
	for _, a := range []store.Attempt{got, stored} {
		if a.Status != store.AttemptSubmitted || !a.AutoSubmitted || a.SubmittedAt == nil || !a.SubmittedAt.Equal(closes) ||
			a.ScoreHalves == nil || *a.ScoreHalves != 8 || a.ScoreBeforePeeksHalves == nil || *a.ScoreBeforePeeksHalves != 8 ||
			len(a.Answers) != 3 || a.LastQuestionIndex != 2 {
			t.Fatalf("%+v", a)
		}
	}
	if !reflect.DeepEqual(got, stored) {
		t.Fatalf("returned row differs from stored:\n%+v\n%+v", got, stored)
	}
}

func TestFinalizeNotBeforeAcceptingUntil(t *testing.T) {
	e := subEnv(t)
	e.save(t, "s1", map[string]string{"q3": "o0"}, 0)
	e.clk.Set(closes.Add(59 * time.Second))
	v := e.get(t, e.student, "e1") // a read at closesAt+59 s
	if v.Attempt.Status != store.AttemptInProgress || e.row(t).Status != store.AttemptInProgress {
		t.Fatalf("finalized inside the grace window: %+v", v.Attempt)
	}
	fin := exams.NewFinalizer(e.attempts)
	x, _ := e.exams.GetExam(context.Background(), "e1")
	got, err := fin.FinalizeIfExpired(context.Background(), e.row(t), x, closes.Add(exams.GraceWindow-time.Nanosecond))
	if err != nil || got.Status != store.AttemptInProgress || e.row(t).Status != store.AttemptInProgress {
		t.Fatalf("%+v %v", got, err)
	}
	e.clk.Set(closes.Add(exams.GraceWindow))
	if v := e.get(t, e.student, "e1"); v.Attempt.Status != store.AttemptSubmitted {
		t.Fatalf("a read at acceptingUntil must finalize: %+v", v.Attempt)
	}
}

func TestFinalizeLeavesNotStartedAndDeclined(t *testing.T) {
	e := subEnv(t)
	e.seedAttempt(t, "e1", "p1", store.AttemptDeclined, nil)
	fin := exams.NewFinalizer(e.attempts)
	x, _ := e.exams.GetExam(context.Background(), "e1")
	now := closes.Add(time.Hour)
	for _, uid := range []string{"s1", "p1"} {
		before, _ := e.attempts.GetAttempt(context.Background(), "e1", uid, 1)
		got, err := fin.FinalizeIfExpired(context.Background(), before, x, now)
		after, _ := e.attempts.GetAttempt(context.Background(), "e1", uid, 1)
		if err != nil || !reflect.DeepEqual(got, before) || !reflect.DeepEqual(after, before) {
			t.Fatalf("%s: %+v %v", uid, got, err)
		}
	}
}

func TestFinalizeFreeTextHasNoScores(t *testing.T) {
	e := newEnv(t)
	e.seedFull(t, "e1", "t1", "free")
	e.seedAttempt(t, "e1", "s1", store.AttemptNotStarted, nil)
	e.save(t, "s1", map[string]string{"q1": "essay"}, 0)
	x, _ := e.exams.GetExam(context.Background(), "e1")
	got, err := exams.NewFinalizer(e.attempts).FinalizeIfExpired(context.Background(), e.row(t), x, closes.Add(time.Minute))
	if err != nil || got.Status != store.AttemptSubmitted || got.ScoreHalves != nil || got.ScoreBeforePeeksHalves != nil || !got.AutoSubmitted {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestFinalizePeekRules(t *testing.T) {
	e := subEnv(t)
	if _, err := e.attempts.Reveal(context.Background(), "e1", "s1", 1, "q2", 3, t0); err != nil {
		t.Fatal(err)
	}
	e.save(t, "s1", map[string]string{"q3": "o0", "q2": "o2"}, 0)
	x, _ := e.exams.GetExam(context.Background(), "e1")
	got, err := exams.NewFinalizer(e.attempts).FinalizeIfExpired(context.Background(), e.row(t), x, closes.Add(time.Minute))
	// q3 (3 marks): 6 halves; q2 (1 mark) revealed: 1 half, before 2
	if err != nil || got.ScoreHalves == nil || got.ScoreBeforePeeksHalves == nil || *got.ScoreHalves != 7 || *got.ScoreBeforePeeksHalves != 8 {
		t.Fatalf("%+v %v", got, err)
	}
}

// countingStore counts successful conditional Submit writes and the peak of concurrent Submits.
type countingStore struct {
	store.AttemptStore
	wins     int32
	inflight int32
	peak     int32
	staleGet *store.Attempt // returned once by GetAttempt, to force a lost race
	mu       sync.Mutex
}

func (c *countingStore) GetAttempt(ctx context.Context, examID, userID string, attemptNo int) (store.Attempt, error) {
	c.mu.Lock()
	s := c.staleGet
	c.staleGet = nil
	c.mu.Unlock()
	if s != nil {
		return *s, nil
	}
	return c.AttemptStore.GetAttempt(ctx, examID, userID, attemptNo)
}

func (c *countingStore) Submit(ctx context.Context, a store.Attempt) error {
	n := atomic.AddInt32(&c.inflight, 1)
	for {
		p := atomic.LoadInt32(&c.peak)
		if n <= p || atomic.CompareAndSwapInt32(&c.peak, p, n) {
			break
		}
	}
	time.Sleep(2 * time.Millisecond)
	err := c.AttemptStore.Submit(ctx, a)
	atomic.AddInt32(&c.inflight, -1)
	if err == nil {
		atomic.AddInt32(&c.wins, 1)
	}
	return err
}

func TestFinalizeConcurrentCallsSafe(t *testing.T) {
	e := subEnv(t)
	e.save(t, "s1", map[string]string{"q3": "o0", "q1": "o1"}, 1)
	cs := &countingStore{AttemptStore: e.attempts}
	fin := exams.NewFinalizer(cs)
	x, _ := e.exams.GetExam(context.Background(), "e1")
	stale := e.row(t) // every goroutine starts from the same in_progress snapshot
	now := closes.Add(time.Hour)
	const n = 10
	results := make([]store.Attempt, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i], errs[i] = fin.FinalizeIfExpired(context.Background(), stale, x, now)
		}()
	}
	close(start)
	wg.Wait()
	if w := atomic.LoadInt32(&cs.wins); w != 1 {
		t.Fatalf("%d writes won, want exactly 1", w)
	}
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("call %d: %v", i, errs[i])
		}
		if !reflect.DeepEqual(results[i], results[0]) || results[i].Status != store.AttemptSubmitted || results[i].ScoreHalves == nil || *results[i].ScoreHalves != 10 {
			t.Fatalf("call %d differs: %+v vs %+v", i, results[i], results[0])
		}
	}
}

func TestFinalizeLosingRaceReturnsWinnersRow(t *testing.T) {
	// The finalizer reads a stale in_progress row, then its conditional write loses: it must
	// re-read and return the winner's row, never an error and never its own grading.
	e := subEnv(t)
	e.save(t, "s1", map[string]string{"q3": "o0"}, 0)
	stale := e.row(t)
	win := 99
	sub := closes.Add(-time.Minute)
	if err := e.attempts.Submit(context.Background(), store.Attempt{AttemptNo: 1, ExamID: "e1", UserID: "s1", Answers: map[string]string{"q1": "o1"},
		ScoreHalves: &win, ScoreBeforePeeksHalves: &win, SubmittedAt: &sub}); err != nil {
		t.Fatal(err)
	}
	cs := &countingStore{AttemptStore: e.attempts, staleGet: &stale}
	x, _ := e.exams.GetExam(context.Background(), "e1")
	got, err := exams.NewFinalizer(cs).FinalizeIfExpired(context.Background(), stale, x, closes.Add(time.Hour))
	if err != nil || got.Status != store.AttemptSubmitted || got.ScoreHalves == nil || *got.ScoreHalves != 99 || got.AutoSubmitted {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestFinalizeStoreErrorsPropagate(t *testing.T) {
	e := subEnv(t)
	e.save(t, "s1", map[string]string{"q3": "o0"}, 0)
	x, _ := e.exams.GetExam(context.Background(), "e1")
	missing := e.row(t)
	missing.UserID = "ghost"
	if _, err := exams.NewFinalizer(e.attempts).FinalizeIfExpired(context.Background(), missing, x, closes.Add(time.Hour)); err == nil {
		t.Fatal("a missing row must be an error")
	}
}

func TestListAndStatusFinalizeExpired(t *testing.T) {
	e := newEnv(t)
	e.seedFull(t, "e1", "t1", "mcq")
	cs := &countingStore{AttemptStore: e.attempts}
	e.svc = exams.NewService(e.users, e.exams, cs, e.clk)
	e.svc.SetFinalizer(exams.NewFinalizer(cs))
	const n = 25
	for i := 0; i < n; i++ {
		uid := "u" + string(rune('a'+i))
		e.seedAttempt(t, "e1", uid, store.AttemptNotStarted, nil)
		if err := e.attempts.Save(context.Background(), "e1", uid, 1, map[string]string{"q2": "o2"}, 0, t0); err != nil {
			t.Fatal(err)
		}
	}
	e.seedAttempt(t, "e1", "s1", store.AttemptInProgress, nil)
	if err := e.attempts.Save(context.Background(), "e1", "s1", 1, map[string]string{"q1": "o1"}, 0, t0); err != nil {
		t.Fatal(err)
	}
	e.seedAttempt(t, "e1", "nn", store.AttemptNotStarted, nil)
	e.clk.Set(closes.Add(exams.GraceWindow))
	st, err := e.svc.Status(context.Background(), e.teacher, "e1")
	if err != nil {
		t.Fatal(err)
	}
	if st.Counts.Submitted != n+1 || st.Counts.InProgress != 0 || st.Counts.NotStarted != 1 {
		t.Fatalf("%+v", st.Counts)
	}
	for _, r := range st.Attempts {
		switch {
		case r.UserID == "nn":
			if r.Status != store.AttemptNotStarted || r.Score != nil {
				t.Fatalf("%+v", r)
			}
		case r.Status != store.AttemptSubmitted || r.Score == nil || r.SubmittedAt == nil || !r.SubmittedAt.Equal(closes):
			t.Fatalf("%+v", r)
		case r.UserID == "s1" && *r.Score != 2, r.UserID != "s1" && *r.Score != 1:
			t.Fatalf("score %+v", r)
		}
	}
	if w := atomic.LoadInt32(&cs.wins); w != n+1 {
		t.Fatalf("%d finalize writes, want %d", w, n+1)
	}
	if p := atomic.LoadInt32(&cs.peak); p > exams.MaxListConcurrency || p < 2 {
		t.Fatalf("peak concurrent finalizes %d, want 2..%d", p, exams.MaxListConcurrency)
	}
	// List: the student's own expired attempt is finalized when it is listed (fresh env row)
	e2 := subEnv(t)
	e2.save(t, "s1", map[string]string{"q1": "o1"}, 0)
	e2.clk.Set(closes.Add(exams.GraceWindow))
	items := e2.list(t, e2.student, "closed")
	if len(items) != 1 || items[0].AttemptStatus != store.AttemptSubmitted || items[0].Score == nil || *items[0].Score != 2 {
		t.Fatalf("%+v", items)
	}
	if a := e2.row(t); a.Status != store.AttemptSubmitted || !a.AutoSubmitted {
		t.Fatalf("%+v", a)
	}
}

// hookStore runs hook once, inside Submit and before the write, to land a competing write
// between the service's read and its conditional write.
type hookStore struct {
	store.AttemptStore
	mu        sync.Mutex
	hook      func()
	submitErr error // when set, Submit fails with it
}

func (h *hookStore) Submit(ctx context.Context, a store.Attempt) error {
	h.mu.Lock()
	f := h.hook
	h.hook = nil
	h.mu.Unlock()
	if f != nil {
		f()
	}
	if h.submitErr != nil {
		return h.submitErr
	}
	return h.AttemptStore.Submit(ctx, a)
}

func raceEnv(t *testing.T) (*env, *hookStore) {
	t.Helper()
	e := subEnv(t)
	h := &hookStore{AttemptStore: e.attempts}
	e.svc = exams.NewService(e.users, e.exams, h, e.clk)
	e.svc.SetFinalizer(exams.NewFinalizer(h))
	return e, h
}

func TestSubmitAfterServerAutoFinalizeIsExamClosed(t *testing.T) {
	e := subEnv(t)
	e.save(t, "s1", map[string]string{"q3": "o0"}, 0)
	e.clk.Set(closes.Add(exams.GraceWindow))
	e.get(t, e.student, "e1") // a read auto-finalizes from the saved answers
	before := e.row(t)
	if before.Status != store.AttemptSubmitted || !before.AutoSubmitted {
		t.Fatalf("%+v", before)
	}
	_, err := e.submit(e.student, body(map[string]string{"q3": "o0", "q1": "o1", "q2": "o2"}, 0)) // the delayed deadline submit
	wantClosed(t, err)
	if after := e.row(t); !reflect.DeepEqual(after, before) || *after.ScoreHalves != 6 || len(after.Answers) != 1 {
		t.Fatalf("row changed: %+v", after)
	}
}

func TestSubmitLosingToFinalizerIsExamClosed(t *testing.T) {
	e, h := raceEnv(t)
	e.save(t, "s1", map[string]string{"q3": "o0"}, 0)
	e.clk.Set(closes.Add(exams.GraceWindow - time.Nanosecond)) // Submit reads the row while the exam is still open
	x, _ := e.exams.GetExam(context.Background(), "e1")
	h.hook = func() { // a finalizer runs at acceptingUntil, inside Submit's read-to-write window
		if _, err := exams.NewFinalizer(e.attempts).FinalizeIfExpired(context.Background(), e.row(t), x, closes.Add(exams.GraceWindow)); err != nil {
			t.Error(err)
		}
	}
	_, err := e.submit(e.student, body(map[string]string{"q3": "o0", "q1": "o1", "q2": "o2"}, 0))
	wantClosed(t, err)
	if a := e.row(t); !a.AutoSubmitted || *a.ScoreHalves != 6 || len(a.Answers) != 1 {
		t.Fatalf("%+v", a)
	}
}

func TestSubmitLosingToOtherTabSubmitReturnsWinnersResult(t *testing.T) {
	e, h := raceEnv(t)
	e.clk.Set(t0.Add(time.Hour))
	var first exams.SubmitResult
	h.hook = func() {
		var err error
		if first, err = e.submit(e.student, body(map[string]string{"q3": "o0"}, 0)); err != nil {
			t.Error(err)
		}
	}
	res, err := e.submit(e.student, body(map[string]string{"q1": "o1", "q2": "o2"}, 1))
	if err != nil || !reflect.DeepEqual(res, first) || res.Score == nil || *res.Score != 3 || res.AutoSubmitted {
		t.Fatalf("%+v vs %+v (%v)", res, first, err)
	}
	if a := e.row(t); len(a.Answers) != 1 || a.Answers["q3"] != "o0" {
		t.Fatalf("loser overwrote the winner: %+v", a)
	}
}

func TestSubmitLosingToDeclineIs404(t *testing.T) {
	e, h := raceEnv(t)
	e.clk.Set(t0.Add(time.Hour))
	h.hook = func() {
		if err := e.attempts.Decline(context.Background(), "e1", "s1", 1); err != nil {
			t.Error(err)
		}
	}
	_, err := e.submit(e.student, body(map[string]string{"q3": "o0"}, 0))
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Status != http.StatusNotFound {
		t.Fatalf("want 404, got %v", err)
	}
	if a := e.row(t); a.Status != store.AttemptDeclined {
		t.Fatalf("%+v", a)
	}
}

func TestSubmitUnexpectedStoreErrorIs500(t *testing.T) {
	e, h := raceEnv(t)
	h.submitErr = errors.New("boom")
	e.clk.Set(t0.Add(time.Hour))
	_, err := e.submit(e.student, nil)
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Status != http.StatusInternalServerError {
		t.Fatalf("%v", err)
	}
}

func TestFinalizeStoreSubmitErrorPropagates(t *testing.T) {
	e := subEnv(t)
	e.save(t, "s1", map[string]string{"q3": "o0"}, 0)
	h := &hookStore{AttemptStore: e.attempts, submitErr: errors.New("boom")}
	x, _ := e.exams.GetExam(context.Background(), "e1")
	if _, err := exams.NewFinalizer(h).FinalizeIfExpired(context.Background(), e.row(t), x, closes.Add(time.Hour)); err == nil {
		t.Fatal("a store write error must not be swallowed")
	}
}
