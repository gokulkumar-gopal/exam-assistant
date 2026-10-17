package exams

import (
	"context"
	"errors"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// RevealView is the response of POST /exams/{id}/reveal, minus serverTime: the key of the one
// question asked about (correctIndex for multiple choice, modelAnswer for free text) and the
// peeks the caller has left.
type RevealView struct {
	QuestionID   string `json:"questionId"`
	CorrectIndex *int   `json:"correctIndex,omitempty"`
	ModelAnswer  string `json:"modelAnswer,omitempty"`
	PeeksLeft    int    `json:"peeksLeft"`
}

// Reveal records a peek at one question's answer (backend-spec 8.5, 9.6) and returns that
// question's key, and only that one, on the caller's latest attempt (every attempt has its own
// peek budget). The first reveal of a question is charged; a repeat returns
// the key again for free, even when no peeks are left.
//
// Only a non-declined assignee with an attempt row may peek: a creator without a row, a declined
// user, a stranger and a missing exam are the same 404. From acceptingUntil the answer is 409
// exam_closed, after the caller's in_progress attempt has gone through the lazy-finalize hook.
// A not_started attempt is started by the first reveal. The store's single conditional update
// enforces the peek cap and that the attempt is still open, so concurrent reveals cannot exceed
// maxPeeks and a reveal can never land on an attempt that has been submitted.
func (s *Service) Reveal(ctx context.Context, user store.User, examID, questionID string) (RevealView, error) {
	a, err := s.attempts.GetLatestAttempt(ctx, examID, user.ID)
	if errors.Is(err, store.ErrNotFound) {
		return RevealView{}, apperr.NotFound()
	}
	if err != nil {
		return RevealView{}, apperr.Internal(err)
	}
	if a.Status == store.AttemptDeclined {
		return RevealView{}, apperr.NotFound()
	}
	exam, err := s.exams.GetExam(ctx, examID)
	if errors.Is(err, store.ErrNotFound) {
		return RevealView{}, apperr.NotFound()
	}
	if err != nil {
		return RevealView{}, apperr.Internal(err)
	}
	now := s.clk.Now()
	if IsOver(exam, now) {
		if s.fin != nil && a.Status == store.AttemptInProgress {
			if _, err := s.fin.FinalizeIfExpired(ctx, a, exam, now); err != nil {
				s.log.Error("reveal: lazy finalize failed", "exam_id", examID, "err", err)
				return RevealView{}, apperr.Internal(err)
			}
		}
		return RevealView{}, apperr.ExamClosed()
	}
	if exam.MaxPeeks <= 0 {
		return RevealView{}, apperr.Forbidden("Peeking is turned off for this exam.")
	}
	if KeyVisible(exam, &a, user.ID, now) {
		return RevealView{}, apperr.Conflict("The answer key is already visible.")
	}
	var q *store.Question
	for i := range exam.Questions {
		if exam.Questions[i].ID == questionID {
			q = &exam.Questions[i]
			break
		}
	}
	if q == nil {
		return RevealView{}, apperr.Validation("That question is not in this exam.", map[string]string{"questionId": "Unknown question"})
	}
	res, err := s.attempts.Reveal(ctx, examID, user.ID, a.AttemptNo, questionID, exam.MaxPeeks, now)
	switch {
	case err == nil:
	case errors.Is(err, store.ErrSubmitted):
		return RevealView{}, apperr.Conflict("This exam has already been submitted.")
	case errors.Is(err, store.ErrDeclined), errors.Is(err, store.ErrNotFound):
		return RevealView{}, apperr.NotFound()
	case errors.Is(err, store.ErrConflict):
		return RevealView{}, apperr.Conflict("No peeks left.")
	default:
		return RevealView{}, apperr.Internal(err)
	}
	k := revealedKey(*q)
	v := RevealView{QuestionID: q.ID, CorrectIndex: k.CorrectIndex, PeeksLeft: max(0, exam.MaxPeeks-len(res.Revealed))}
	if k.ModelAnswer != nil {
		v.ModelAnswer = *k.ModelAnswer
	}
	return v, nil
}
