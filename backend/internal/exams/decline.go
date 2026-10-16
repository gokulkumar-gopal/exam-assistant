package exams

import (
	"context"
	"errors"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// Decline moves the caller's not_started attempt to declined (backend-spec 8.5). It is idempotent.
// Whoever has no attempt row, whether the exam is missing or someone else's, gets the same 404.
// In-progress and submitted attempts are refused with 409. There is no undo.
func (s *Service) Decline(ctx context.Context, user store.User, examID string) error {
	err := s.attempts.Decline(ctx, examID, user.ID)
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
