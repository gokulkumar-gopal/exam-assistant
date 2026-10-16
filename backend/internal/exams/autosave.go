package exams

import (
	"context"
	"errors"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// MaxAutosaveRunes is the longest autosaved answer (backend-spec 8.5: 5,000 characters, counted
// as characters like every other text limit in this package, not bytes).
const MaxAutosaveRunes = 5000

// AutosaveInput is the body of PUT /exams/{id}/attempt.
type AutosaveInput struct {
	Answers           map[string]string `json:"answers"`
	LastQuestionIndex int               `json:"lastQuestionIndex"`
}

// Autosave replaces the caller's whole answers map (backend-spec 8.5, 9.2) and returns the
// server time of the save. Only a non-declined assignee with an attempt row may save: a creator
// without a row, a declined user, a stranger and a missing exam are the same 404.
//
// At or after acceptingUntil the answer is 409 exam_closed, after the caller's in_progress
// attempt has gone through the lazy-finalize hook. Before that, including the 60 s grace window,
// the save is accepted. MCQ answers are not checked against the options (they may be mid-edit
// text); only the question ids, the length and the index are validated.
//
// Known accepted gap (spec 8.5): the map has no sequence number, so a delayed older PUT that
// arrives after a newer one overwrites it. TestKnownGapDocumented pins this on purpose.
func (s *Service) Autosave(ctx context.Context, user store.User, examID string, in AutosaveInput) (time.Time, error) {
	a, err := s.attempts.GetAttempt(ctx, examID, user.ID)
	if errors.Is(err, store.ErrNotFound) {
		return time.Time{}, apperr.NotFound()
	}
	if err != nil {
		return time.Time{}, apperr.Internal(err)
	}
	if a.Status == store.AttemptDeclined {
		return time.Time{}, apperr.NotFound()
	}
	exam, err := s.exams.GetExam(ctx, examID)
	if errors.Is(err, store.ErrNotFound) {
		return time.Time{}, apperr.NotFound()
	}
	if err != nil {
		return time.Time{}, apperr.Internal(err)
	}
	now := s.clk.Now()
	if IsOver(exam, now) {
		if s.fin != nil && a.Status == store.AttemptInProgress {
			if _, err := s.fin.FinalizeIfExpired(ctx, a, exam, now); err != nil {
				return time.Time{}, apperr.Internal(err)
			}
		}
		return time.Time{}, apperr.ExamClosed()
	}
	if err := validateAutosave(exam, in); err != nil {
		return time.Time{}, err
	}
	err = s.attempts.Save(ctx, examID, user.ID, in.Answers, in.LastQuestionIndex, now)
	switch {
	case err == nil:
		return now, nil
	case errors.Is(err, store.ErrSubmitted):
		return time.Time{}, apperr.Conflict("This exam has already been submitted.")
	case errors.Is(err, store.ErrDeclined), errors.Is(err, store.ErrNotFound):
		return time.Time{}, apperr.NotFound()
	}
	return time.Time{}, apperr.Internal(err)
}

func validateAutosave(exam store.Exam, in AutosaveInput) error {
	if in.Answers == nil {
		return apperr.Validation("Answers are required.", map[string]string{"answers": "Required"})
	}
	known := make(map[string]bool, len(exam.Questions))
	for _, q := range exam.Questions {
		known[q.ID] = true
	}
	for id, v := range in.Answers {
		if !known[id] {
			return apperr.Validation("Answers refer to a question that is not in this exam.",
				map[string]string{"answers": "Unknown question"})
		}
		if runes(v) > MaxAutosaveRunes {
			return apperr.Validation("An answer is too long.", map[string]string{"answers": "Too long"})
		}
	}
	if in.LastQuestionIndex < 0 || in.LastQuestionIndex >= len(exam.Questions) {
		return apperr.Validation("The question position is out of range.",
			map[string]string{"lastQuestionIndex": "Out of range"})
	}
	return nil
}
