package exams

import (
	"context"
	"errors"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// Decline moves the caller's not_started attempt to declined (backend-spec 8.5). It is idempotent.
// Whoever has no attempt row, whether the exam is missing or someone else's, gets the same 404.
// In-progress and submitted attempts are refused with 409 conflict, and any not-yet-declined
// attempt after acceptingUntil with 409 exam_closed. There is no undo.
func (s *Service) Decline(ctx context.Context, user store.User, examID string) error {
	// The row comes first: the exam is only read once the caller has a row, so a missing exam and
	// someone else's exam are the same 404.
	a, err := s.attempts.GetLatestAttempt(ctx, examID, user.ID)
	if errors.Is(err, store.ErrNotFound) {
		return apperr.NotFound()
	}
	if err != nil {
		return apperr.Internal(err)
	}
	if a.Status == store.AttemptDeclined {
		return nil // idempotent, also after the deadline
	}
	if a.AttemptNo > 1 {
		// A retake is not an assignment: declining it would hide the attempts already submitted.
		return apperr.Conflict("You can only decline an exam you have not started.")
	}
	// Declining is a write: after the deadline it would turn a missed attempt into a declined one.
	exam, err := s.exams.GetExam(ctx, examID)
	if errors.Is(err, store.ErrNotFound) {
		return apperr.NotFound()
	}
	if err != nil {
		return apperr.Internal(err)
	}
	if over(exam, s.clk.Now()) {
		return apperr.ExamClosed()
	}
	err = s.attempts.Decline(ctx, examID, user.ID, a.AttemptNo)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return apperr.NotFound()
	case errors.Is(err, store.ErrPrecondition):
		return apperr.Conflict("You can only decline an exam you have not started.")
	}
	return apperr.Internal(err)
}
