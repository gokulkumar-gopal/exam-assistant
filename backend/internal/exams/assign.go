package exams

import (
	"context"
	"errors"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// AssignResult is the outcome of Assign. No field is ever nil. Emails are as sent, trimmed.
type AssignResult struct {
	Assigned        []string `json:"assigned"`
	AlreadyAssigned []string `json:"alreadyAssigned"`
	Unknown         []string `json:"unknown"`
}

// Assign adds assignees to an exam (backend-spec 8.5). Only the creator may; anyone else gets the
// same 404 as for a missing exam. Existing rows (declined ones too) are never touched and are
// reported as already assigned. A write failure is 503; repeating the request completes it.
func (s *Service) Assign(ctx context.Context, user store.User, examID string, emails []string) (AssignResult, error) {
	exam, err := s.exams.GetExam(ctx, examID)
	if errors.Is(err, store.ErrNotFound) {
		return AssignResult{}, apperr.NotFound()
	}
	if err != nil {
		return AssignResult{}, apperr.Internal(err)
	}
	if exam.CreatorID != user.ID {
		return AssignResult{}, apperr.NotFound()
	}
	if over(exam, s.clk.Now()) {
		return AssignResult{}, apperr.ExamClosed()
	}

	clean, msg := checkEmails(emails, user.Role)
	if msg == "" && len(clean) == 0 {
		msg = "Send at least one email."
	}
	if msg != "" {
		return AssignResult{}, apperr.Validation("Some fields need attention.", map[string]string{"emails": msg})
	}
	ids, unknown, err := s.resolve(ctx, user, clean)
	if err != nil {
		return AssignResult{}, err
	}
	rows, err := s.attempts.ListByExam(ctx, examID)
	if err != nil {
		return AssignResult{}, apperr.Internal(err)
	}
	have := map[string]bool{}
	holders := map[string]bool{}
	for _, a := range rows {
		have[a.UserID] = true
		holders[a.UserID] = true
	}
	for _, uid := range ids {
		holders[uid] = true
	}
	if !contains(ids, user.ID) {
		delete(holders, user.ID) // the creator's practice row is not an assignee
	}
	if n, capN := len(holders), assigneeCap(user.Role); n > capN {
		return AssignResult{}, apperr.Validation("Some fields need attention.", map[string]string{"emails": capMessage(capN)})
	}

	// Map each resolved ID back to the email it was sent as: resolve keeps input order.
	res := AssignResult{Assigned: []string{}, AlreadyAssigned: []string{}, Unknown: unknown}
	unk := map[string]bool{}
	for _, e := range unknown {
		unk[e] = true
	}
	var toWrite []string
	i := 0
	for _, e := range clean {
		if unk[e] {
			continue
		}
		uid := ids[i]
		i++
		if have[uid] {
			res.AlreadyAssigned = append(res.AlreadyAssigned, e)
			continue
		}
		res.Assigned = append(res.Assigned, e)
		toWrite = append(toWrite, uid)
	}
	if err := s.writeRows(ctx, examID, toWrite, s.clk.Now()); err != nil {
		return AssignResult{}, err
	}
	return res, nil
}
