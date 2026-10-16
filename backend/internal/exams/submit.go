package exams

import (
	"context"
	"errors"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// SubmitInput is the optional body of POST /exams/{id}/submit (backend-spec 8.5). It decodes
// exactly like AutosaveInput, but both fields are optional: Answers nil means "use the saved
// answers", LastQuestionIndex nil means "keep the saved position".
type SubmitInput struct{ AutosaveInput }

// SubmitResult is the Submit response minus serverTime. Scores are in marks (half-marks / 2);
// the score fields are JSON null for free-text exams. It carries no answer-key material.
type SubmitResult struct {
	Status           string    `json:"status"`
	Score            *float64  `json:"score"`
	ScoreBeforePeeks *float64  `json:"scoreBeforePeeks"`
	MaxScore         int       `json:"maxScore"`
	SubmittedAt      time.Time `json:"submittedAt"`
	AutoSubmitted    bool      `json:"autoSubmitted"`
}

// maxSubmitRetries bounds the regrade loop when peeks keep landing during a submit.
const maxSubmitRetries = 3

func validateSubmit(exam store.Exam, in *SubmitInput) error {
	if in.badValue {
		return apperr.Validation("An answer must be text.", map[string]string{"answers": "Must be text"})
	}
	if in.Answers != nil {
		if err := checkAnswers(exam, in.Answers); err != nil {
			return err
		}
	}
	if in.LastQuestionIndex != nil {
		if i := *in.LastQuestionIndex; i < 0 || i >= len(exam.Questions) {
			return apperr.Validation("The question position is out of range.",
				map[string]string{"lastQuestionIndex": "Out of range"})
		}
	}
	return nil
}

// Submit grades and submits the caller's attempt (backend-spec 8.5, 9.2, 9.3).
//
// A creator without a row, a declined user, a stranger and a missing exam are the same 404. An
// attempt that the client itself submitted returns its existing result (idempotent), whatever
// the clock or the body say; one the server auto-finalized (AutoSubmitted) is 409 exam_closed,
// because this request's answers were not graded. Otherwise, from acceptingUntil the in_progress attempt goes through the lazy
// finalizer (saved answers graded, AutoSubmitted) and the call is 409 exam_closed; the body's
// answers are NOT used then. Before that, including the 60 s grace window, the body's answers
// (when present) replace the saved ones, are graded, and SubmittedAt = min(now, closesAt).
// A not_started attempt is started and submitted in one write. If the conditional write loses
// a race the winner's row is returned (409 exam_closed if the winner is the finalizer).
//
// Known gap: with no body, a save landing between the read and the write is overwritten and
// not graded (the frontend always sends a body).
func (s *Service) Submit(ctx context.Context, user store.User, examID string, in *SubmitInput) (SubmitResult, error) {
	a, err := s.attempts.GetAttempt(ctx, examID, user.ID)
	if errors.Is(err, store.ErrNotFound) {
		return SubmitResult{}, apperr.NotFound()
	}
	if err != nil {
		return SubmitResult{}, apperr.Internal(err)
	}
	if a.Status == store.AttemptDeclined {
		return SubmitResult{}, apperr.NotFound()
	}
	exam, err := s.exams.GetExam(ctx, examID)
	if errors.Is(err, store.ErrNotFound) {
		return SubmitResult{}, apperr.NotFound()
	}
	if err != nil {
		return SubmitResult{}, apperr.Internal(err)
	}
	if a.Status == store.AttemptSubmitted {
		if a.AutoSubmitted {
			return SubmitResult{}, apperr.ExamClosed() // the server finalized it; no client submit produced this row
		}
		return submitResult(exam, a, s.clk.Now()), nil
	}
	now := s.clk.Now()
	if IsOver(exam, now) {
		if s.fin != nil && a.Status == store.AttemptInProgress {
			if _, err := s.fin.FinalizeIfExpired(ctx, a, exam, now); err != nil {
				s.log.Error("submit: lazy finalize failed", "exam_id", examID, "err", err)
				return SubmitResult{}, apperr.Internal(err)
			}
		}
		return SubmitResult{}, apperr.ExamClosed()
	}
	if in == nil {
		in = &SubmitInput{}
	}
	if err := validateSubmit(exam, in); err != nil {
		return SubmitResult{}, err
	}
	fin := a
	if in.Answers != nil {
		fin.Answers = in.Answers
		saved := now
		fin.SavedAt = &saved
	}
	if in.LastQuestionIndex != nil {
		fin.LastQuestionIndex = *in.LastQuestionIndex
	}
	if fin.StartedAt == nil {
		started := now
		fin.StartedAt = &started
	}
	fin.Status = store.AttemptSubmitted
	fin.AutoSubmitted = false
	at := now
	if closes := exam.ClosesAt; closes.Before(at) {
		at = closes
	}
	fin.SubmittedAt = &at
	fin.ScoreHalves, fin.ScoreBeforePeeksHalves = Grade(exam, fin.Answers, fin.Revealed)
	err = s.attempts.Submit(ctx, fin)
	// A peek that landed after the row was read makes the write lose with ErrConflict. Regrade
	// against the fresh revealed list and write again (a few times at most).
	for try := 0; errors.Is(err, store.ErrConflict) && try < maxSubmitRetries; try++ {
		cur, gerr := s.attempts.GetAttempt(ctx, examID, user.ID)
		if gerr != nil {
			return SubmitResult{}, apperr.Internal(gerr)
		}
		fin.Revealed = cur.Revealed
		fin.ScoreHalves, fin.ScoreBeforePeeksHalves = Grade(exam, fin.Answers, fin.Revealed)
		err = s.attempts.Submit(ctx, fin)
	}
	switch {
	case err == nil:
		return submitResult(exam, fin, now), nil
	case errors.Is(err, store.ErrSubmitted):
		won, gerr := s.attempts.GetAttempt(ctx, examID, user.ID)
		if gerr != nil {
			return SubmitResult{}, apperr.Internal(gerr)
		}
		if won.AutoSubmitted { // lost to the lazy finalizer: this body was not graded
			return SubmitResult{}, apperr.ExamClosed()
		}
		return submitResult(exam, won, now), nil
	case errors.Is(err, store.ErrDeclined), errors.Is(err, store.ErrNotFound):
		return SubmitResult{}, apperr.NotFound()
	}
	return SubmitResult{}, apperr.Internal(err)
}

// submitResult builds the response from a submitted row. A submitted MCQ row that somehow lacks
// scores is regraded from its stored answers, so the response never reports a missing score for
// a gradable exam.
func submitResult(exam store.Exam, a store.Attempt, now time.Time) SubmitResult {
	r := SubmitResult{Status: store.AttemptSubmitted, MaxScore: exam.TotalMarks, AutoSubmitted: a.AutoSubmitted, SubmittedAt: now.UTC()}
	if a.SubmittedAt != nil {
		r.SubmittedAt = a.SubmittedAt.UTC()
	}
	if exam.Type != "mcq" {
		return r
	}
	score, before := a.ScoreHalves, a.ScoreBeforePeeksHalves
	if score == nil {
		score, before = Grade(exam, a.Answers, a.Revealed)
	}
	if before == nil {
		before = score
	}
	h := func(p *int) *float64 { v := float64(*p) / 2; return &v }
	r.Score, r.ScoreBeforePeeks = h(score), h(before)
	return r
}
