package exams

import (
	"context"
	"errors"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// AttemptFinalizer is the Finalizer (backend-spec 9.2) over an AttemptStore.
type AttemptFinalizer struct{ attempts store.AttemptStore }

var _ Finalizer = (*AttemptFinalizer)(nil)

// NewFinalizer builds the lazy finalizer.
func NewFinalizer(attempts store.AttemptStore) *AttemptFinalizer {
	return &AttemptFinalizer{attempts: attempts}
}

// FinalizeIfExpired auto-submits an in_progress attempt once now >= acceptingUntil: the SAVED
// answers are graded, SubmittedAt = closesAt, AutoSubmitted = true. Anything else (not_started,
// declined, submitted, or not yet expired) is returned unchanged.
//
// It re-reads the row first, because the caller's copy may predate a later save or a competing
// finalize. The write is the store's conditional Submit (status <> submitted): when it loses, the
// row is re-read and the winner's row is returned, so concurrent callers all get the same
// result and none gets an error.
func (f *AttemptFinalizer) FinalizeIfExpired(ctx context.Context, a store.Attempt, e store.Exam, now time.Time) (store.Attempt, error) {
	if a.Status != store.AttemptInProgress || !over(e, now) {
		return a, nil
	}
	for try := 0; try <= maxSubmitRetries; try++ {
		cur, err := f.attempts.GetAttempt(ctx, a.ExamID, a.UserID)
		if err != nil {
			return a, err
		}
		if cur.Status != store.AttemptInProgress {
			return cur, nil
		}
		fin := cur
		fin.Status = store.AttemptSubmitted
		fin.AutoSubmitted = true
		closes := e.ClosesAt
		fin.SubmittedAt = &closes
		fin.ScoreHalves, fin.ScoreBeforePeeksHalves = Grade(e, cur.Answers, cur.Revealed)
		err = f.attempts.Submit(ctx, fin)
		switch {
		case err == nil:
			return fin, nil
		case errors.Is(err, store.ErrSubmitted), errors.Is(err, store.ErrDeclined):
			return f.attempts.GetAttempt(ctx, a.ExamID, a.UserID)
		case errors.Is(err, store.ErrConflict):
			continue // a peek landed after the read; re-read and regrade
		}
		return a, err
	}
	return a, errors.New("exams: finalize: too much contention")
}
