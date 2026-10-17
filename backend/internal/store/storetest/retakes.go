package storetest

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// runRetakeCases registers the one-row-per-attempt contract (backend-spec 6.1, 8.5 Retake).
func runRetakeCases(t *testing.T, f AttemptFactory) {
	t.Run("AttemptsAreIsolated", func(t *testing.T) { AttemptsAreIsolated(t, f) })
	t.Run("PutRetakeCreatesOnceUnderConcurrency", func(t *testing.T) { PutRetakeCreatesOnceUnderConcurrency(t, f) })
	t.Run("PutRetakeNeverOverwritesAnExistingAttempt", func(t *testing.T) { PutRetakeNeverOverwritesAnExistingAttempt(t, f) })
	t.Run("AttemptLimitIs50", func(t *testing.T) { AttemptLimitIs50(t, f) })
	t.Run("ListAttemptsOrderedAndScopedToTheUser", func(t *testing.T) { ListAttemptsOrderedAndScopedToTheUser(t, f) })
	t.Run("GetLatestAttemptIsTheHighestNumber", func(t *testing.T) { GetLatestAttemptIsTheHighestNumber(t, f) })
	t.Run("ListByExamAndListByUserReturnEveryRow", func(t *testing.T) { ListByExamAndListByUserReturnEveryRow(t, f) })
}

// AttemptsAreIsolated: every write targets one attempt row and leaves the others as they were.
func AttemptsAreIsolated(t *testing.T, f AttemptFactory) {
	s := f(t)
	_, _ = s.PutAttemptIfAbsent(ctx, att("e1", "u1"))
	score, before := 4, 5
	sat := T0.Add(time.Hour)
	first := att("e1", "u1")
	first.Status, first.Answers, first.ScoreHalves, first.ScoreBeforePeeksHalves, first.SubmittedAt = store.AttemptSubmitted,
		map[string]string{"q1": "one"}, &score, &before, &sat
	noErr(t, s.Submit(ctx, first))
	created, err := s.PutRetake(ctx, "e1", "u1", 2, T0.Add(2*time.Hour))
	noErr(t, err)
	if !created {
		t.Fatal("PutRetake(2) must create the row")
	}
	second, err := s.GetAttempt(ctx, "e1", "u1", 2)
	noErr(t, err)
	if second.AttemptNo != 2 || second.Status != store.AttemptNotStarted || len(second.Answers) != 0 || len(second.Revealed) != 0 ||
		second.ScoreHalves != nil || second.SubmittedAt != nil || !second.CreatedAt.Equal(T0.Add(2*time.Hour)) {
		t.Fatalf("fresh attempt: %+v", second)
	}
	// Writes to attempt 2 do not touch attempt 1, which stays submitted and refuses writes.
	noErr(t, s.Save(ctx, "e1", "u1", 2, map[string]string{"q1": "two"}, 1, T0.Add(3*time.Hour)))
	r, err := s.Reveal(ctx, "e1", "u1", 2, "q2", 3, T0.Add(3*time.Hour))
	noErr(t, err)
	if !r.Charged || len(r.Revealed) != 1 {
		t.Fatalf("reveal on attempt 2: %+v", r)
	}
	is(t, s.Save(ctx, "e1", "u1", 1, map[string]string{"q1": "late"}, 0, T0), store.ErrSubmitted)
	_, err = s.Reveal(ctx, "e1", "u1", 1, "q2", 3, T0)
	is(t, err, store.ErrSubmitted)
	a1, err := s.GetAttempt(ctx, "e1", "u1", 1)
	noErr(t, err)
	if a1.Status != store.AttemptSubmitted || a1.Answers["q1"] != "one" || len(a1.Revealed) != 0 || *a1.ScoreHalves != 4 ||
		!a1.SubmittedAt.Equal(sat) || a1.AttemptNo != 1 {
		t.Fatalf("attempt 1 changed: %+v", a1)
	}
	// Submitting attempt 2 gives it its own marks.
	score2, before2 := 9, 10
	sat2 := T0.Add(4 * time.Hour)
	sub2 := second
	sub2.Revealed = []string{"q2"} // the peeks the score was computed from
	sub2.Status, sub2.Answers, sub2.ScoreHalves, sub2.ScoreBeforePeeksHalves, sub2.SubmittedAt = store.AttemptSubmitted,
		map[string]string{"q1": "two"}, &score2, &before2, &sat2
	noErr(t, s.Submit(ctx, sub2))
	a2, _ := s.GetAttempt(ctx, "e1", "u1", 2)
	a1, _ = s.GetAttempt(ctx, "e1", "u1", 1)
	if *a2.ScoreHalves != 9 || *a1.ScoreHalves != 4 || a2.Answers["q1"] != "two" || a1.Answers["q1"] != "one" || len(a2.Revealed) != 1 {
		t.Fatalf("per-attempt marks: %+v %+v", a1, a2)
	}
	// Declining one attempt leaves the other alone; a missing number is not found.
	_, _ = s.PutRetake(ctx, "e1", "u1", 3, T0)
	noErr(t, s.Decline(ctx, "e1", "u1", 3))
	if a2, _ = s.GetAttempt(ctx, "e1", "u1", 2); a2.Status != store.AttemptSubmitted {
		t.Fatalf("%+v", a2)
	}
	_, err = s.GetAttempt(ctx, "e1", "u1", 4)
	is(t, err, store.ErrNotFound)
	is(t, s.Save(ctx, "e1", "u1", 4, nil, 0, T0), store.ErrNotFound)
	is(t, s.Decline(ctx, "e1", "u1", 4), store.ErrNotFound)
}

func PutRetakeCreatesOnceUnderConcurrency(t *testing.T, f AttemptFactory) {
	s := f(t)
	_, _ = s.PutAttemptIfAbsent(ctx, att("e1", "u1"))
	var wg sync.WaitGroup
	var created atomic.Int32
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := s.PutRetake(ctx, "e1", "u1", 2, T0)
			if err != nil {
				t.Errorf("PutRetake: %v", err)
			}
			if ok {
				created.Add(1)
			}
		}()
	}
	wg.Wait()
	if created.Load() != 1 {
		t.Fatalf("created %d times, want once", created.Load())
	}
	rows, err := s.ListAttempts(ctx, "e1", "u1")
	noErr(t, err)
	if len(rows) != 2 {
		t.Fatalf("%d rows, want 2", len(rows))
	}
}

func PutRetakeNeverOverwritesAnExistingAttempt(t *testing.T, f AttemptFactory) {
	s := f(t)
	_, _ = s.PutAttemptIfAbsent(ctx, att("e1", "u1"))
	_, _ = s.PutRetake(ctx, "e1", "u1", 2, T0)
	noErr(t, s.Save(ctx, "e1", "u1", 2, map[string]string{"q1": "kept"}, 1, T0))
	ok, err := s.PutRetake(ctx, "e1", "u1", 2, T0.Add(time.Hour))
	noErr(t, err)
	if ok {
		t.Fatal("PutRetake over an existing attempt must report created=false")
	}
	if a, _ := s.GetAttempt(ctx, "e1", "u1", 2); a.Status != store.AttemptInProgress || a.Answers["q1"] != "kept" {
		t.Fatalf("existing attempt was overwritten: %+v", a)
	}
	// PutAttemptIfAbsent defaults to attempt 1 and does not clobber it either.
	ok, err = s.PutAttemptIfAbsent(ctx, att("e1", "u1"))
	noErr(t, err)
	if ok {
		t.Fatal("attempt 1 already exists")
	}
}

func AttemptLimitIs50(t *testing.T, f AttemptFactory) {
	s := f(t)
	_, _ = s.PutAttemptIfAbsent(ctx, att("e1", "u1"))
	for n := 2; n <= store.MaxAttemptsPerExam; n++ {
		ok, err := s.PutRetake(ctx, "e1", "u1", n, T0)
		noErr(t, err)
		if !ok {
			t.Fatalf("attempt %d not created", n)
		}
	}
	for _, n := range []int{0, -1, store.MaxAttemptsPerExam + 1, 1000} {
		ok, err := s.PutRetake(ctx, "e1", "u1", n, T0)
		is(t, err, store.ErrConflict)
		if ok {
			t.Fatalf("attempt %d created", n)
		}
	}
	rows, err := s.ListAttempts(ctx, "e1", "u1")
	noErr(t, err)
	if len(rows) != store.MaxAttemptsPerExam {
		t.Fatalf("%d rows", len(rows))
	}
}

func ListAttemptsOrderedAndScopedToTheUser(t *testing.T, f AttemptFactory) {
	s := f(t)
	// u1 and u10 share a prefix: only the "u1#" rows belong to u1.
	for _, u := range []string{"u10", "u1", "u2"} {
		_, _ = s.PutAttemptIfAbsent(ctx, att("e1", u))
	}
	_, _ = s.PutAttemptIfAbsent(ctx, att("e2", "u1"))
	for _, n := range []int{10, 3, 2, 9} { // out of order, and 10 sorts after 9 only numerically
		_, err := s.PutRetake(ctx, "e1", "u1", n, T0)
		noErr(t, err)
	}
	_, _ = s.PutRetake(ctx, "e1", "u10", 2, T0)
	noErr(t, s.Save(ctx, "e1", "u1", 3, map[string]string{"q1": "mine"}, 1, T0))
	rows, err := s.ListAttempts(ctx, "e1", "u1")
	noErr(t, err)
	var nos []int
	for _, a := range rows {
		if a.UserID != "u1" || a.ExamID != "e1" {
			t.Fatalf("foreign row: %+v", a)
		}
		nos = append(nos, a.AttemptNo)
	}
	if fmt.Sprint(nos) != "[1 2 3 9 10]" {
		t.Fatalf("order %v", nos)
	}
	if rows[2].Answers["q1"] != "mine" || rows[0].Answers == nil || rows[0].Revealed == nil {
		t.Fatalf("answers must be included and never nil: %+v", rows)
	}
	none, err := s.ListAttempts(ctx, "e1", "nobody")
	noErr(t, err)
	if len(none) != 0 {
		t.Fatalf("%v", none)
	}
}

func GetLatestAttemptIsTheHighestNumber(t *testing.T, f AttemptFactory) {
	s := f(t)
	_, err := s.GetLatestAttempt(ctx, "e1", "u1")
	is(t, err, store.ErrNotFound)
	_, _ = s.PutAttemptIfAbsent(ctx, att("e1", "u1"))
	_, _ = s.PutAttemptIfAbsent(ctx, att("e1", "u10"))
	if a, err := s.GetLatestAttempt(ctx, "e1", "u1"); err != nil || a.AttemptNo != 1 {
		t.Fatalf("%+v %v", a, err)
	}
	for _, n := range []int{2, 9, 10} {
		_, _ = s.PutRetake(ctx, "e1", "u1", n, T0)
	}
	_, _ = s.PutRetake(ctx, "e1", "u10", 2, T0)
	a, err := s.GetLatestAttempt(ctx, "e1", "u1")
	noErr(t, err)
	if a.AttemptNo != 10 || a.UserID != "u1" || a.Answers == nil || a.Revealed == nil {
		t.Fatalf("latest: %+v", a)
	}
	_, err = s.GetLatestAttempt(ctx, "e2", "u1")
	is(t, err, store.ErrNotFound)
}

func ListByExamAndListByUserReturnEveryRow(t *testing.T, f AttemptFactory) {
	s := f(t)
	for _, k := range [][2]string{{"e1", "u2"}, {"e1", "u1"}, {"e2", "u1"}} {
		_, _ = s.PutAttemptIfAbsent(ctx, att(k[0], k[1]))
	}
	_, _ = s.PutRetake(ctx, "e1", "u1", 3, T0)
	_, _ = s.PutRetake(ctx, "e1", "u1", 2, T0)
	_, _ = s.PutRetake(ctx, "e1", "u2", 2, T0)
	_, _ = s.PutRetake(ctx, "e2", "u1", 2, T0)
	score := 3
	sat := T0.Add(time.Hour)
	sub := att("e1", "u1")
	sub.Status, sub.ScoreHalves, sub.SubmittedAt = store.AttemptSubmitted, &score, &sat
	noErr(t, s.Submit(ctx, sub))

	byExam, err := s.ListByExam(ctx, "e1")
	noErr(t, err)
	var got []string
	for _, a := range byExam {
		got = append(got, fmt.Sprintf("%s/%d", a.UserID, a.AttemptNo))
	}
	if fmt.Sprint(got) != "[u1/1 u1/2 u1/3 u2/1 u2/2]" {
		t.Fatalf("ListByExam: %v", got)
	}
	byUser, err := s.ListByUser(ctx, "u1")
	noErr(t, err)
	got = nil
	for _, a := range byUser {
		got = append(got, fmt.Sprintf("%s/%d", a.ExamID, a.AttemptNo))
	}
	if fmt.Sprint(got) != "[e1/1 e1/2 e1/3 e2/1 e2/2]" {
		t.Fatalf("ListByUser: %v", got)
	}
	if byUser[0].Status != store.AttemptSubmitted || *byUser[0].ScoreHalves != 3 || !byUser[0].SubmittedAt.Equal(sat) ||
		byUser[1].Status != store.AttemptNotStarted || byUser[1].ScoreHalves != nil {
		t.Fatalf("summaries: %+v", byUser[:2])
	}
}
