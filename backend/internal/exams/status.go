package exams

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// StatusExam is the exam header of the creator status view.
type StatusExam struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	ClosesAt   time.Time `json:"closesAt"`
	TotalMarks int       `json:"totalMarks"`
}

// StatusRow is one assignee row. Never carries answers.
type StatusRow struct {
	UserID      string     `json:"userId"`
	Name        string     `json:"name"`
	Email       string     `json:"email"`
	Status      string     `json:"status"`
	Score       *float64   `json:"score,omitempty"`
	SubmittedAt *time.Time `json:"submittedAt,omitempty"`
}

// StatusView is the response of GET /exams/{id}/status.
type StatusView struct {
	Exam     StatusExam  `json:"exam"`
	Counts   Counts      `json:"counts"`
	Attempts []StatusRow `json:"attempts"`
}

// usersBatch bounds one GetManyByID call.
const usersBatch = 100

// Status is the creator status view (backend-spec 8.5). Visibility rule: an existing exam and a
// caller who is not the creator but has an attempt row for it (an assignee, declined included)
// gets 403 forbidden, since they legitimately know the exam exists; a caller with no relation
// to the exam and a missing exam get the same 404.
//
// Per-row status is the stored status: a never-started attempt after the exam is over stays
// not_started (the frontend may label it "Missed"; only the lists derive "missed"). An
// in_progress attempt at or after acceptingUntil is passed to the Finalizer first, like List
// does. Rows are bounded by the assignee cap, so the view is one page; answers are never copied.
func (s *Service) Status(ctx context.Context, user store.User, examID string) (StatusView, error) {
	exam, err := s.exams.GetExam(ctx, examID)
	if errors.Is(err, store.ErrNotFound) {
		return StatusView{}, apperr.NotFound()
	}
	if err != nil {
		return StatusView{}, apperr.Internal(err)
	}
	if exam.CreatorID != user.ID {
		_, err := s.attempts.GetAttempt(ctx, examID, user.ID)
		switch {
		case err == nil:
			return StatusView{}, apperr.Forbidden("Only the creator can see this.")
		case errors.Is(err, store.ErrNotFound):
			return StatusView{}, apperr.NotFound()
		}
		return StatusView{}, apperr.Internal(err)
	}
	rows, err := s.attempts.ListByExam(ctx, examID)
	if err != nil {
		return StatusView{}, apperr.Internal(err)
	}
	now := s.clk.Now()
	if s.fin != nil {
		var todo []int
		for i, a := range rows {
			if a.Status == store.AttemptInProgress && over(exam, now) {
				todo = append(todo, i)
			}
		}
		err := forEach(len(todo), func(k int) error {
			fin, err := s.fin.FinalizeIfExpired(ctx, rows[todo[k]], exam, now)
			if err != nil {
				return err
			}
			rows[todo[k]] = fin
			return nil
		})
		if err != nil {
			return StatusView{}, apperr.Internal(err)
		}
	}
	ids := make([]string, 0, len(rows))
	for _, a := range rows {
		ids = append(ids, a.UserID)
	}
	users := map[string]store.User{}
	for lo := 0; lo < len(ids); lo += usersBatch {
		hi := min(lo+usersBatch, len(ids))
		got, err := s.users.GetManyByID(ctx, ids[lo:hi])
		if err != nil {
			return StatusView{}, apperr.Internal(err)
		}
		for k, u := range got {
			users[k] = u
		}
	}
	v := StatusView{
		Exam:     StatusExam{ID: exam.ID, Title: exam.Title, ClosesAt: exam.ClosesAt.UTC(), TotalMarks: exam.TotalMarks},
		Attempts: make([]StatusRow, 0, len(rows)),
	}
	for _, a := range rows {
		u := users[a.UserID] // a deleted or unknown user keeps its row with an empty name and email
		r := StatusRow{UserID: a.UserID, Name: u.Name, Email: u.Email, Status: a.Status}
		switch a.Status {
		case store.AttemptSubmitted:
			v.Counts.Submitted++
			if a.SubmittedAt != nil {
				t := a.SubmittedAt.UTC()
				r.SubmittedAt = &t
			}
			if exam.Type == "mcq" && a.ScoreHalves != nil {
				sc := float64(*a.ScoreHalves) / 2
				r.Score = &sc
			}
		case store.AttemptInProgress:
			v.Counts.InProgress++
		case store.AttemptNotStarted:
			v.Counts.NotStarted++
		case store.AttemptDeclined:
			v.Counts.Declined++
		}
		v.Attempts = append(v.Attempts, r)
	}
	sort.Slice(v.Attempts, func(i, j int) bool {
		a, b := v.Attempts[i], v.Attempts[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.UserID < b.UserID
	})
	return v, nil
}
