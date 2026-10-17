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

// StatusAttempt is one line of a user's attempt history. Never carries answers.
type StatusAttempt struct {
	AttemptNo     int        `json:"attemptNo"`
	Status        string     `json:"status"`
	Score         *float64   `json:"score,omitempty"`
	SubmittedAt   *time.Time `json:"submittedAt,omitempty"`
	AutoSubmitted bool       `json:"autoSubmitted"`
}

// StatusRow is one user's row: status, score and submittedAt are those of the user as a whole
// (submitted if any attempt is, with the best attempt's score and time). Never carries answers.
type StatusRow struct {
	UserID       string          `json:"userId"`
	Name         string          `json:"name"`
	Email        string          `json:"email"`
	Status       string          `json:"status"`
	Score        *float64        `json:"score,omitempty"`
	SubmittedAt  *time.Time      `json:"submittedAt,omitempty"`
	AttemptCount int             `json:"attemptCount"`
	History      []StatusAttempt `json:"history"`
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
// One row per user: status is submitted if any attempt is, otherwise the latest attempt's stored
// status (a never-started attempt after the exam is over stays not_started; the frontend may
// label it "Missed", only the lists derive "missed"). Score and submittedAt are the best
// attempt's; history lists every attempt, oldest first, with marks and times. Counts are per
// user. An in_progress latest attempt at or after acceptingUntil is passed to the Finalizer
// first, like List does. Rows are bounded by the assignee cap (times the attempt limit), so the
// view is one page; answers are never copied.
func (s *Service) Status(ctx context.Context, user store.User, examID string) (StatusView, error) {
	exam, err := s.exams.GetExam(ctx, examID)
	if errors.Is(err, store.ErrNotFound) {
		return StatusView{}, apperr.NotFound()
	}
	if err != nil {
		return StatusView{}, apperr.Internal(err)
	}
	if exam.CreatorID != user.ID {
		_, err := s.attempts.GetLatestAttempt(ctx, examID, user.ID)
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
	runs := groupByUser(rows)
	if s.fin != nil {
		type pos struct{ run, at int }
		var todo []pos
		for i, r := range runs {
			if l := len(r) - 1; r[l].Status == store.AttemptInProgress && over(exam, now) {
				todo = append(todo, pos{i, l}) // only the latest attempt of a user can be unfinished
			}
		}
		err := forEach(len(todo), func(k int) error {
			p := todo[k]
			fin, err := s.fin.FinalizeIfExpired(ctx, runs[p.run][p.at], exam, now)
			if err != nil {
				return err
			}
			runs[p.run][p.at] = fin
			return nil
		})
		if err != nil {
			return StatusView{}, apperr.Internal(err)
		}
	}
	ids := make([]string, 0, len(runs))
	for _, r := range runs {
		ids = append(ids, r[0].UserID)
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
		Attempts: make([]StatusRow, 0, len(runs)),
	}
	score := func(a store.Attempt) *float64 {
		if exam.Type == "mcq" && a.Status == store.AttemptSubmitted {
			return halvesToScore(a.ScoreHalves)
		}
		return nil
	}
	utc := func(t *time.Time) *time.Time {
		if t == nil {
			return nil
		}
		u := t.UTC()
		return &u
	}
	for _, run := range runs {
		uid := run[0].UserID
		u := users[uid] // a deleted or unknown user keeps its row with an empty name and email
		r := StatusRow{UserID: uid, Name: u.Name, Email: u.Email, Status: run.status(),
			AttemptCount: len(run), History: make([]StatusAttempt, 0, len(run))}
		for _, a := range run {
			r.History = append(r.History, StatusAttempt{AttemptNo: a.AttemptNo, Status: a.Status, Score: score(a),
				SubmittedAt: utc(a.SubmittedAt), AutoSubmitted: a.AutoSubmitted})
		}
		if best, ok := run.best(); ok {
			r.Score, r.SubmittedAt = score(best), utc(best.SubmittedAt)
		}
		switch r.Status {
		case store.AttemptSubmitted:
			v.Counts.Submitted++
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
