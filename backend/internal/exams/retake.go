package exams

import (
	"context"
	"errors"
	"fmt"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// RetakeView is the response of POST /exams/{id}/retake, minus serverTime.
type RetakeView struct {
	AttemptNo int `json:"attemptNo"`
}

// Retake starts a new attempt for a person who has submitted (backend-spec 8.5, Retake). The
// caller must hold the exam: a non-declined attempt row, which includes a creator taking their
// own exam; a creator with no row, a declined user, a stranger and a missing exam are the same
// 404. From acceptingUntil the answer is 409 exam_closed (an expired in_progress attempt goes
// through the lazy-finalize hook first). It is idempotent: while the latest attempt is
// not_started or in_progress that attempt's number is returned and nothing is created. After a
// submitted latest attempt, attempt N+1 is created as not_started, unless N is already the
// MaxAttemptsPerExam limit (409 conflict). The store's conditional put makes two concurrent
// retakes create N+1 once; the loser returns the same number.
func (s *Service) Retake(ctx context.Context, user store.User, examID string) (RetakeView, error) {
	a, err := s.attempts.GetLatestAttempt(ctx, examID, user.ID)
	if errors.Is(err, store.ErrNotFound) {
		return RetakeView{}, apperr.NotFound()
	}
	if err != nil {
		return RetakeView{}, apperr.Internal(err)
	}
	if a.Status == store.AttemptDeclined {
		return RetakeView{}, apperr.NotFound()
	}
	exam, err := s.exams.GetExam(ctx, examID)
	if errors.Is(err, store.ErrNotFound) {
		return RetakeView{}, apperr.NotFound()
	}
	if err != nil {
		return RetakeView{}, apperr.Internal(err)
	}
	now := s.clk.Now()
	if IsOver(exam, now) {
		if s.fin != nil && a.Status == store.AttemptInProgress {
			if _, err := s.fin.FinalizeIfExpired(ctx, a, exam, now); err != nil {
				s.log.Error("retake: lazy finalize failed", "exam_id", examID, "err", err)
				return RetakeView{}, apperr.Internal(err)
			}
		}
		return RetakeView{}, apperr.ExamClosed()
	}
	if a.Status != store.AttemptSubmitted {
		return RetakeView{AttemptNo: a.AttemptNo}, nil
	}
	if a.AttemptNo >= store.MaxAttemptsPerExam {
		return RetakeView{}, apperr.Conflict(fmt.Sprintf("You have reached the limit of %d attempts for this exam.", store.MaxAttemptsPerExam))
	}
	next := a.AttemptNo + 1
	if _, err := s.attempts.PutRetake(ctx, examID, user.ID, next, now); err != nil {
		return RetakeView{}, apperr.Internal(err)
	}
	return RetakeView{AttemptNo: next}, nil
}
