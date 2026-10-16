package storetest

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// ExamFactory returns a fresh, empty ExamStore.
type ExamFactory func(t *testing.T) store.ExamStore

// AttemptFactory returns a fresh, empty AttemptStore.
type AttemptFactory func(t *testing.T) store.AttemptStore

func exam(id, creator string) store.Exam {
	one := 1
	return store.Exam{ID: id, CreatorID: creator, Title: "Title " + id, Type: "mcq", TotalMarks: 2,
		ClosesAt: T0.Add(48 * time.Hour), MaxPeeks: 3, CreatedAt: T0,
		Questions: []store.Question{
			{ID: "q1", Marks: 1, Text: "t1", Options: []string{"a", "b", "c", "d"}, CorrectIndex: &one, SourceDocID: "d1", SourceDocName: "Doc"},
			{ID: "q2", Marks: 1, Text: "t2", ModelAnswer: "m", SourceDocID: "d1", SourceDocName: "Doc"},
		}}
}

func att(examID, userID string) store.Attempt {
	return store.Attempt{ExamID: examID, UserID: userID, Status: store.AttemptNotStarted, CreatedAt: T0}
}

// RunExamStoreSuite runs every ExamStore contract test.
func RunExamStoreSuite(t *testing.T, f ExamFactory) {
	t.Run("PutExamConflictOnExistingID", func(t *testing.T) { PutExamConflictOnExistingID(t, f) })
	t.Run("GetExamRoundTripAndNotFound", func(t *testing.T) { GetExamRoundTripAndNotFound(t, f) })
	t.Run("GetExamsOmitsMissingAndHandlesMoreThan100", func(t *testing.T) { GetExamsOmitsMissingAndHandlesMoreThan100(t, f) })
	t.Run("CreatorListNewestFirstCap50", func(t *testing.T) { CreatorListNewestFirstCap50(t, f) })
	t.Run("CreatorListSummaryFieldsAndScope", func(t *testing.T) { CreatorListSummaryFieldsAndScope(t, f) })
}

// RunAttemptStoreSuite runs every AttemptStore contract test.
func RunAttemptStoreSuite(t *testing.T, f AttemptFactory) {
	t.Run("PutAttemptIfAbsentIgnoresDuplicate", func(t *testing.T) { PutAttemptIfAbsentIgnoresDuplicate(t, f) })
	t.Run("SaveConditionalOnNotSubmitted", func(t *testing.T) { SaveConditionalOnNotSubmitted(t, f) })
	t.Run("SaveStartsAttemptSetsStartedAtOnce", func(t *testing.T) { SaveStartsAttemptSetsStartedAtOnce(t, f) })
	t.Run("SaveKeepsRevealedAndMissingIsNotFound", func(t *testing.T) { SaveKeepsRevealedAndMissingIsNotFound(t, f) })
	t.Run("DeclineOnlyFromNotStarted", func(t *testing.T) { DeclineOnlyFromNotStarted(t, f) })
	t.Run("SubmitConditionalAndScores", func(t *testing.T) { SubmitConditionalAndScores(t, f) })
	t.Run("ListByUserSummaries", func(t *testing.T) { ListByUserSummaries(t, f) })
	t.Run("ListByExamOrdered", func(t *testing.T) { ListByExamOrdered(t, f) })
	t.Run("RevealChargesOnceAndRespectsMax", func(t *testing.T) { RevealChargesOnceAndRespectsMax(t, f) })
	t.Run("RevealStartsAndGuardsState", func(t *testing.T) { RevealStartsAndGuardsState(t, f) })
	t.Run("RevealRaceNeverExceedsMax", func(t *testing.T) { RevealRaceNeverExceedsMax(t, f) })
}

func PutExamConflictOnExistingID(t *testing.T, f ExamFactory) {
	s := f(t)
	noErr(t, s.PutExam(ctx, exam("e1", "u1")))
	other := exam("e1", "u2")
	other.Title = "different"
	is(t, s.PutExam(ctx, other), store.ErrConflict)
	got, err := s.GetExam(ctx, "e1")
	noErr(t, err)
	if got.CreatorID != "u1" || got.Title != "Title e1" {
		t.Fatalf("conflicting put overwrote the exam: %+v", got)
	}
}

func GetExamRoundTripAndNotFound(t *testing.T, f ExamFactory) {
	s := f(t)
	want := exam("e1", "u1")
	noErr(t, s.PutExam(ctx, want))
	got, err := s.GetExam(ctx, "e1")
	noErr(t, err)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", got, want)
	}
	_, err = s.GetExam(ctx, "nope")
	is(t, err, store.ErrNotFound)
}

func GetExamsOmitsMissingAndHandlesMoreThan100(t *testing.T, f ExamFactory) {
	s := f(t)
	var ids []string
	for i := 0; i < 150; i++ {
		id := fmt.Sprintf("e%03d", i)
		noErr(t, s.PutExam(ctx, exam(id, "u1")))
		ids = append(ids, id)
	}
	got, err := s.GetExams(ctx, append(ids, "missing", "e001"))
	noErr(t, err)
	if len(got) != 150 || got["e149"].ID != "e149" || got["e000"].Title != "Title e000" {
		t.Fatalf("got %d exams", len(got))
	}
	if _, ok := got["missing"]; ok {
		t.Fatal("missing id must be omitted")
	}
	empty, err := s.GetExams(ctx, nil)
	noErr(t, err)
	if len(empty) != 0 {
		t.Fatalf("empty = %v", empty)
	}
}

func CreatorListNewestFirstCap50(t *testing.T, f ExamFactory) {
	s := f(t)
	for i := 0; i < 60; i++ {
		noErr(t, s.PutExam(ctx, exam(fmt.Sprintf("e%03d", i), "u1")))
	}
	noErr(t, s.PutExam(ctx, exam("zzz", "u2")))
	for _, limit := range []int{0, 100, 51, 50} {
		got, err := s.ListByCreator(ctx, "u1", limit)
		noErr(t, err)
		if len(got) != 50 || got[0].ID != "e059" || got[49].ID != "e010" {
			t.Fatalf("limit %d: len %d first %s", limit, len(got), got[0].ID)
		}
	}
	got, err := s.ListByCreator(ctx, "u1", 3)
	noErr(t, err)
	if len(got) != 3 || got[0].ID != "e059" || got[2].ID != "e057" {
		t.Fatalf("limit 3: %+v", got)
	}
	none, err := s.ListByCreator(ctx, "nobody", 10)
	noErr(t, err)
	if len(none) != 0 {
		t.Fatalf("none = %v", none)
	}
}

func CreatorListSummaryFieldsAndScope(t *testing.T, f ExamFactory) {
	s := f(t)
	e := exam("e1", "u1")
	noErr(t, s.PutExam(ctx, e))
	got, err := s.ListByCreator(ctx, "u1", 10)
	noErr(t, err)
	want := store.ExamSummary{ID: "e1", CreatorID: "u1", Title: e.Title, Type: "mcq", TotalMarks: 2, ClosesAt: e.ClosesAt, CreatedAt: e.CreatedAt}
	if len(got) != 1 || !got[0].ClosesAt.Equal(want.ClosesAt) || !got[0].CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("got %+v", got)
	}
	got[0].ClosesAt, got[0].CreatedAt = want.ClosesAt, want.CreatedAt
	if got[0] != want {
		t.Fatalf("summary:\n got %+v\nwant %+v", got[0], want)
	}
}

func PutAttemptIfAbsentIgnoresDuplicate(t *testing.T, f AttemptFactory) {
	s := f(t)
	created, err := s.PutAttemptIfAbsent(ctx, att("e1", "u1"))
	noErr(t, err)
	if !created {
		t.Fatal("first put must create")
	}
	noErr(t, s.Save(ctx, "e1", "u1", map[string]string{"q1": "a"}, 1, T0.Add(time.Minute)))
	created, err = s.PutAttemptIfAbsent(ctx, att("e1", "u1"))
	noErr(t, err)
	if created {
		t.Fatal("duplicate put must report created=false")
	}
	got, err := s.GetAttempt(ctx, "e1", "u1")
	noErr(t, err)
	if got.Status != store.AttemptInProgress || got.Answers["q1"] != "a" {
		t.Fatalf("duplicate put overwrote the row: %+v", got)
	}
	_, err = s.GetAttempt(ctx, "e1", "other")
	is(t, err, store.ErrNotFound)
}

func SaveConditionalOnNotSubmitted(t *testing.T, f AttemptFactory) {
	s := f(t)
	_, _ = s.PutAttemptIfAbsent(ctx, att("e1", "u1"))
	noErr(t, s.Save(ctx, "e1", "u1", map[string]string{"q1": "a"}, 0, T0.Add(time.Minute)))
	final := att("e1", "u1")
	final.Status = store.AttemptSubmitted
	final.Answers = map[string]string{"q1": "b"}
	submitted := T0.Add(2 * time.Minute)
	final.SubmittedAt = &submitted
	noErr(t, s.Submit(ctx, final))
	is(t, s.Save(ctx, "e1", "u1", map[string]string{"q1": "late"}, 5, T0.Add(3*time.Minute)), store.ErrPrecondition)
	got, err := s.GetAttempt(ctx, "e1", "u1")
	noErr(t, err)
	if got.Answers["q1"] != "b" || got.LastQuestionIndex != 0 {
		t.Fatalf("late save changed the submitted attempt: %+v", got)
	}
}

func SaveStartsAttemptSetsStartedAtOnce(t *testing.T, f AttemptFactory) {
	s := f(t)
	_, _ = s.PutAttemptIfAbsent(ctx, att("e1", "u1"))
	got, _ := s.GetAttempt(ctx, "e1", "u1")
	if got.StartedAt != nil || got.Status != store.AttemptNotStarted || got.Answers == nil || got.Revealed == nil {
		t.Fatalf("fresh attempt: %+v", got)
	}
	t1, t2 := T0.Add(time.Minute), T0.Add(5*time.Minute)
	noErr(t, s.Save(ctx, "e1", "u1", map[string]string{"q1": "a"}, 1, t1))
	got, _ = s.GetAttempt(ctx, "e1", "u1")
	if got.Status != store.AttemptInProgress || got.StartedAt == nil || !got.StartedAt.Equal(t1) || !got.SavedAt.Equal(t1) {
		t.Fatalf("after first save: %+v", got)
	}
	noErr(t, s.Save(ctx, "e1", "u1", map[string]string{"q1": "b", "q2": "x"}, 2, t2))
	got, _ = s.GetAttempt(ctx, "e1", "u1")
	if !got.StartedAt.Equal(t1) || !got.SavedAt.Equal(t2) || got.LastQuestionIndex != 2 || got.Answers["q1"] != "b" || got.Answers["q2"] != "x" || got.Status != store.AttemptInProgress {
		t.Fatalf("after second save: %+v", got)
	}
	// An empty answer map replaces, not merges.
	noErr(t, s.Save(ctx, "e1", "u1", map[string]string{}, 0, t2))
	got, _ = s.GetAttempt(ctx, "e1", "u1")
	if len(got.Answers) != 0 {
		t.Fatalf("answers = %v", got.Answers)
	}
}

func SaveKeepsRevealedAndMissingIsNotFound(t *testing.T, f AttemptFactory) {
	s := f(t)
	is(t, s.Save(ctx, "e1", "u1", nil, 0, T0), store.ErrNotFound)
	_, _ = s.PutAttemptIfAbsent(ctx, att("e1", "u1"))
	_, err := s.Reveal(ctx, "e1", "u1", "q1", 3)
	noErr(t, err)
	noErr(t, s.Save(ctx, "e1", "u1", map[string]string{"q1": "a"}, 1, T0.Add(time.Minute)))
	got, _ := s.GetAttempt(ctx, "e1", "u1")
	if !reflect.DeepEqual(got.Revealed, []string{"q1"}) {
		t.Fatalf("revealed = %v", got.Revealed)
	}
}

func DeclineOnlyFromNotStarted(t *testing.T, f AttemptFactory) {
	s := f(t)
	is(t, s.Decline(ctx, "e1", "u1"), store.ErrNotFound)
	_, _ = s.PutAttemptIfAbsent(ctx, att("e1", "u1"))
	noErr(t, s.Decline(ctx, "e1", "u1"))
	noErr(t, s.Decline(ctx, "e1", "u1")) // idempotent
	got, _ := s.GetAttempt(ctx, "e1", "u1")
	if got.Status != store.AttemptDeclined {
		t.Fatalf("status = %s", got.Status)
	}
	is(t, s.Save(ctx, "e1", "u1", nil, 0, T0), store.ErrPrecondition)

	_, _ = s.PutAttemptIfAbsent(ctx, att("e2", "u1"))
	noErr(t, s.Save(ctx, "e2", "u1", map[string]string{}, 0, T0))
	is(t, s.Decline(ctx, "e2", "u1"), store.ErrPrecondition)
	got, _ = s.GetAttempt(ctx, "e2", "u1")
	if got.Status != store.AttemptInProgress {
		t.Fatalf("decline changed an in-progress attempt: %s", got.Status)
	}

	_, _ = s.PutAttemptIfAbsent(ctx, att("e3", "u1"))
	sub := att("e3", "u1")
	sub.Status = store.AttemptSubmitted
	noErr(t, s.Submit(ctx, sub))
	is(t, s.Decline(ctx, "e3", "u1"), store.ErrPrecondition)
}

func SubmitConditionalAndScores(t *testing.T, f AttemptFactory) {
	s := f(t)
	is(t, s.Submit(ctx, att("e1", "u1")), store.ErrNotFound)
	_, _ = s.PutAttemptIfAbsent(ctx, att("e1", "u1"))
	_, _ = s.Reveal(ctx, "e1", "u1", "q2", 3)
	two, three := 3, 4
	sat := T0.Add(time.Hour)
	a := att("e1", "u1")
	a.Status = store.AttemptSubmitted
	a.Answers = map[string]string{"q1": "b"}
	a.LastQuestionIndex = 1
	a.ScoreHalves, a.ScoreBeforePeeksHalves = &two, &three
	a.SubmittedAt, a.SavedAt = &sat, &sat
	a.AutoSubmitted = true
	a.Revealed = []string{"ignored"}
	noErr(t, s.Submit(ctx, a))
	got, err := s.GetAttempt(ctx, "e1", "u1")
	noErr(t, err)
	if got.Status != store.AttemptSubmitted || *got.ScoreHalves != 3 || *got.ScoreBeforePeeksHalves != 4 || !got.SubmittedAt.Equal(sat) ||
		!got.AutoSubmitted || got.Answers["q1"] != "b" || got.LastQuestionIndex != 1 || !reflect.DeepEqual(got.Revealed, []string{"q2"}) {
		t.Fatalf("submitted: %+v", got)
	}
	is(t, s.Submit(ctx, a), store.ErrPrecondition)
	_, err = s.Reveal(ctx, "e1", "u1", "q1", 3)
	is(t, err, store.ErrPrecondition)
}

func ListByUserSummaries(t *testing.T, f AttemptFactory) {
	s := f(t)
	_, _ = s.PutAttemptIfAbsent(ctx, att("e2", "u1"))
	_, _ = s.PutAttemptIfAbsent(ctx, att("e1", "u1"))
	_, _ = s.PutAttemptIfAbsent(ctx, att("e1", "u2"))
	score, before := 5, 6
	sat := T0.Add(time.Hour)
	done := att("e1", "u1")
	done.Status = store.AttemptSubmitted
	done.ScoreHalves, done.ScoreBeforePeeksHalves, done.SubmittedAt = &score, &before, &sat
	done.Answers = map[string]string{"q1": "a"}
	noErr(t, s.Submit(ctx, done))
	got, err := s.ListByUser(ctx, "u1")
	noErr(t, err)
	if len(got) != 2 || got[0].ExamID != "e1" || got[1].ExamID != "e2" {
		t.Fatalf("got %+v", got)
	}
	g := got[0]
	if g.UserID != "u1" || g.Status != store.AttemptSubmitted || g.ScoreHalves == nil || *g.ScoreHalves != 5 ||
		g.ScoreBeforePeeksHalves == nil || *g.ScoreBeforePeeksHalves != 6 || g.SubmittedAt == nil || !g.SubmittedAt.Equal(sat) {
		t.Fatalf("submitted summary: %+v", g)
	}
	if got[1].Status != store.AttemptNotStarted || got[1].ScoreHalves != nil || got[1].SubmittedAt != nil {
		t.Fatalf("pending summary: %+v", got[1])
	}
	none, err := s.ListByUser(ctx, "nobody")
	noErr(t, err)
	if len(none) != 0 {
		t.Fatalf("none = %v", none)
	}
}

func ListByExamOrdered(t *testing.T, f AttemptFactory) {
	s := f(t)
	for _, u := range []string{"u3", "u1", "u2"} {
		_, _ = s.PutAttemptIfAbsent(ctx, att("e1", u))
	}
	_, _ = s.PutAttemptIfAbsent(ctx, att("e2", "u1"))
	noErr(t, s.Save(ctx, "e1", "u2", map[string]string{"q1": "a"}, 1, T0))
	got, err := s.ListByExam(ctx, "e1")
	noErr(t, err)
	if len(got) != 3 || got[0].UserID != "u1" || got[1].UserID != "u2" || got[2].UserID != "u3" || got[1].Answers["q1"] != "a" {
		t.Fatalf("got %+v", got)
	}
}

func RevealChargesOnceAndRespectsMax(t *testing.T, f AttemptFactory) {
	s := f(t)
	_, _ = s.PutAttemptIfAbsent(ctx, att("e1", "u1"))
	r, err := s.Reveal(ctx, "e1", "u1", "q1", 2)
	noErr(t, err)
	if !r.Charged || !reflect.DeepEqual(r.Revealed, []string{"q1"}) {
		t.Fatalf("first: %+v", r)
	}
	r, err = s.Reveal(ctx, "e1", "u1", "q1", 2)
	noErr(t, err)
	if r.Charged || !reflect.DeepEqual(r.Revealed, []string{"q1"}) {
		t.Fatalf("repeat: %+v", r)
	}
	r, err = s.Reveal(ctx, "e1", "u1", "q2", 2)
	noErr(t, err)
	if !r.Charged || !reflect.DeepEqual(r.Revealed, []string{"q1", "q2"}) {
		t.Fatalf("second: %+v", r)
	}
	_, err = s.Reveal(ctx, "e1", "u1", "q3", 2)
	is(t, err, store.ErrConflict)
	r, err = s.Reveal(ctx, "e1", "u1", "q2", 2) // already revealed is free even at the cap
	noErr(t, err)
	if r.Charged || len(r.Revealed) != 2 {
		t.Fatalf("repeat at cap: %+v", r)
	}
	_, err = s.Reveal(ctx, "e1", "u1", "q1", 0)
	noErr(t, err) // already revealed beats a zero max: the store enforces only the cap on new reveals
}

func RevealStartsAndGuardsState(t *testing.T, f AttemptFactory) {
	s := f(t)
	_, err := s.Reveal(ctx, "e1", "u1", "q1", 3)
	is(t, err, store.ErrNotFound)
	_, _ = s.PutAttemptIfAbsent(ctx, att("e1", "u1"))
	_, err = s.Reveal(ctx, "e1", "u1", "q1", 0)
	is(t, err, store.ErrConflict)
	if got, _ := s.GetAttempt(ctx, "e1", "u1"); got.Status != store.AttemptNotStarted {
		t.Fatalf("refused reveal started the attempt: %+v", got)
	}
	_, err = s.Reveal(ctx, "e1", "u1", "q1", 3)
	noErr(t, err)
	got, _ := s.GetAttempt(ctx, "e1", "u1")
	if got.Status != store.AttemptInProgress || got.StartedAt == nil {
		t.Fatalf("reveal must start the attempt: %+v", got)
	}
	_, _ = s.PutAttemptIfAbsent(ctx, att("e2", "u1"))
	noErr(t, s.Decline(ctx, "e2", "u1"))
	_, err = s.Reveal(ctx, "e2", "u1", "q1", 3)
	is(t, err, store.ErrPrecondition)
}

func RevealRaceNeverExceedsMax(t *testing.T, f AttemptFactory) {
	s := f(t)
	_, _ = s.PutAttemptIfAbsent(ctx, att("e1", "u1"))
	var wg sync.WaitGroup
	var charged, conflicts atomic.Int32
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := s.Reveal(ctx, "e1", "u1", fmt.Sprintf("q%d", i), 3)
			switch {
			case err == nil && r.Charged:
				charged.Add(1)
			case errors.Is(err, store.ErrConflict):
				conflicts.Add(1)
			default:
				t.Errorf("reveal %d: %+v %v", i, r, err)
			}
		}(i)
	}
	wg.Wait()
	got, _ := s.GetAttempt(ctx, "e1", "u1")
	if charged.Load() != 3 || conflicts.Load() != 17 || len(got.Revealed) != 3 {
		t.Fatalf("charged %d conflicts %d revealed %v", charged.Load(), conflicts.Load(), got.Revealed)
	}
}
