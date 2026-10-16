package exams

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// List limits.
const (
	// GraceWindow is added to closesAt to get acceptingUntil (backend-spec 9.2).
	GraceWindow = 60 * time.Second
	// MaxListConcurrency bounds per-exam counts reads and lazy finalizes (backend-spec 9.2).
	MaxListConcurrency = 10
	// MaxListAttempts bounds the exams one open/closed list loads. ListByUser reads the whole
	// user-index partition (one Query, paged by the store; about 1 RCU per 8 KB of projected
	// rows, a few hundred bytes each), so a user with thousands of attempts still pays that
	// read; this cap bounds the BatchGet and finalize work after it. Exam IDs are ULIDs for
	// server-minted keys, so the highest IDs are normally the newest exams.
	MaxListAttempts = 500
)

// Scopes of GET /exams.
const (
	ScopeOpen    = "open"
	ScopeClosed  = "closed"
	ScopeCreated = "created"
)

// Classification results of Classify.
const (
	ClassOpen   = "open"
	ClassClosed = "closed"
)

// AttemptMissed is the derived status of an unfinished attempt after the exam is over; never stored.
const AttemptMissed = "missed"

// Finalizer is the lazy-finalize hook (backend-spec 9.2). List calls Finalize for an attempt
// that is in_progress at or after acceptingUntil; it returns the attempt as it is after
// finalizing (submitted, graded, AutoSubmitted) and must be idempotent and safe under
// concurrent calls. A nil Finalizer on the Service means no finalizing: such an attempt is
// listed as closed and derived "missed". B-4.4 supplies the real implementation.
type Finalizer interface {
	Finalize(ctx context.Context, exam store.Exam, a store.Attempt) (store.Attempt, error)
}

// SetFinalizer wires the lazy-finalize hook. Call it before the Service serves requests.
func (s *Service) SetFinalizer(f Finalizer) { s.fin = f }

// UserRef is the public identity of a user in a list item: never an email.
type UserRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Counts are the creator's per-status attempt counts (declined included).
type Counts struct {
	Submitted  int `json:"submitted"`
	InProgress int `json:"inProgress"`
	NotStarted int `json:"notStarted"`
	Declined   int `json:"declined"`
}

// ListItem is one row of GET /exams (backend-spec 8.5). Attempt fields are set only in the open
// and closed scopes; Counts only in created.
type ListItem struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	CreatedBy      UserRef   `json:"createdBy"`
	ClosesAt       time.Time `json:"closesAt"`
	AcceptingUntil time.Time `json:"acceptingUntil"`
	TotalMarks     int       `json:"totalMarks"`
	Type           string    `json:"type"`
	AttemptStatus  string    `json:"attemptStatus,omitempty"`
	Score          *float64  `json:"score,omitempty"`
	MaxScore       *int      `json:"maxScore,omitempty"`
	Counts         *Counts   `json:"counts,omitempty"`
}

// AcceptingUntil is the instant the exam stops accepting work.
func AcceptingUntil(exam store.Exam) time.Time { return exam.ClosesAt.Add(GraceWindow) }

func over(exam store.Exam, now time.Time) bool { return !now.Before(AcceptingUntil(exam)) }

// Classify returns "open" or "closed" for the attempt's list, or "" for a declined attempt,
// which is in neither. Submitted is closed immediately; otherwise closed from acceptingUntil
// (now >= closesAt+60s).
func Classify(exam store.Exam, a store.Attempt, now time.Time) string {
	switch a.Status {
	case store.AttemptDeclined:
		return ""
	case store.AttemptSubmitted:
		return ClassClosed
	}
	if over(exam, now) {
		return ClassClosed
	}
	return ClassOpen
}

// DerivedStatus is the attempt status the API shows: an unsubmitted attempt at or after
// acceptingUntil is "missed" (derived, never stored).
func DerivedStatus(exam store.Exam, a store.Attempt, now time.Time) string {
	if (a.Status == store.AttemptNotStarted || a.Status == store.AttemptInProgress) && over(exam, now) {
		return AttemptMissed
	}
	return a.Status
}

// List returns the caller's exams for scope open, closed or created. The server clock decides
// every boundary. It reads the user-index (or creator-index) and then BatchGets the exams;
// it never scans.
func (s *Service) List(ctx context.Context, user store.User, scope string) ([]ListItem, error) {
	switch scope {
	case ScopeOpen, ScopeClosed:
		return s.listAttempts(ctx, user, scope)
	case ScopeCreated:
		return s.listCreated(ctx, user)
	}
	return nil, apperr.Validation("Some fields need attention.", map[string]string{
		"scope": "scope must be open, closed or created.",
	})
}

// forEach runs fn(i) for i in [0,n), at most MaxListConcurrency at a time, and returns the first error.
func forEach(n int, fn func(i int) error) error {
	sem := make(chan struct{}, MaxListConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var first error
	for i := 0; i < n; i++ {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			if err := fn(i); err != nil {
				mu.Lock()
				if first == nil {
					first = err
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return first
}

type row struct {
	exam store.Exam
	a    store.Attempt
}

func (s *Service) listAttempts(ctx context.Context, user store.User, scope string) ([]ListItem, error) {
	now := s.clk.Now()
	sums, err := s.attempts.ListByUser(ctx, user.ID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	cand := make([]store.AttemptSummary, 0, len(sums))
	for _, a := range sums {
		if a.Status != store.AttemptDeclined {
			cand = append(cand, a)
		}
	}
	if len(cand) > MaxListAttempts {
		sort.Slice(cand, func(i, j int) bool { return cand[i].ExamID > cand[j].ExamID })
		cand = cand[:MaxListAttempts]
	}
	if len(cand) == 0 {
		return []ListItem{}, nil
	}
	examIDs := make([]string, len(cand))
	for i, a := range cand {
		examIDs[i] = a.ExamID
	}
	found, err := s.exams.GetExams(ctx, examIDs)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	rows := make([]row, 0, len(cand))
	for _, c := range cand {
		x, ok := found[c.ExamID]
		if !ok {
			continue // exam row gone: nothing to show
		}
		rows = append(rows, row{exam: x, a: store.Attempt{
			ExamID: c.ExamID, UserID: c.UserID, Status: c.Status, ScoreHalves: c.ScoreHalves,
			ScoreBeforePeeksHalves: c.ScoreBeforePeeksHalves, SubmittedAt: c.SubmittedAt,
		}})
	}
	if s.fin != nil {
		var todo []int
		for i, r := range rows {
			if r.a.Status == store.AttemptInProgress && over(r.exam, now) {
				todo = append(todo, i)
			}
		}
		err := forEach(len(todo), func(k int) error {
			r := &rows[todo[k]]
			full, err := s.attempts.GetAttempt(ctx, r.exam.ID, user.ID)
			if err != nil {
				return err
			}
			fin, err := s.fin.Finalize(ctx, r.exam, full)
			if err != nil {
				return err
			}
			r.a = fin
			return nil
		})
		if err != nil {
			return nil, apperr.Internal(err)
		}
	}
	var creatorIDs []string
	seen := map[string]bool{}
	for _, r := range rows {
		if !seen[r.exam.CreatorID] {
			seen[r.exam.CreatorID] = true
			creatorIDs = append(creatorIDs, r.exam.CreatorID)
		}
	}
	creators := map[string]store.User{}
	if len(creatorIDs) > 0 {
		if creators, err = s.users.GetManyByID(ctx, creatorIDs); err != nil {
			return nil, apperr.Internal(err)
		}
	}
	out := []ListItem{}
	var picked []row
	for _, r := range rows {
		if Classify(r.exam, r.a, now) == scope {
			picked = append(picked, r)
		}
	}
	// open: soonest deadline first; closed: most recent deadline first; ties by ID.
	sort.Slice(picked, func(i, j int) bool {
		a, b := picked[i].exam, picked[j].exam
		if !a.ClosesAt.Equal(b.ClosesAt) {
			if scope == ScopeOpen {
				return a.ClosesAt.Before(b.ClosesAt)
			}
			return a.ClosesAt.After(b.ClosesAt)
		}
		return a.ID < b.ID
	})
	for _, r := range picked {
		it := baseItem(r.exam, UserRef{ID: r.exam.CreatorID, Name: creators[r.exam.CreatorID].Name})
		it.AttemptStatus = DerivedStatus(r.exam, r.a, now)
		mx := r.exam.TotalMarks
		it.MaxScore = &mx
		if r.a.Status == store.AttemptSubmitted && r.exam.Type == "mcq" && r.a.ScoreHalves != nil {
			sc := float64(*r.a.ScoreHalves) / 2
			it.Score = &sc
		}
		out = append(out, it)
	}
	return out, nil
}

func baseItem(x store.Exam, by UserRef) ListItem {
	return ListItem{ID: x.ID, Title: x.Title, CreatedBy: by, ClosesAt: x.ClosesAt.UTC(),
		AcceptingUntil: AcceptingUntil(x).UTC(), TotalMarks: x.TotalMarks, Type: x.Type}
}

func (s *Service) listCreated(ctx context.Context, user store.User) ([]ListItem, error) {
	now := s.clk.Now()
	sums, err := s.exams.ListByCreator(ctx, user.ID, store.MaxListByCreator)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	out := make([]ListItem, len(sums))
	by := UserRef{ID: user.ID, Name: user.Name}
	err = forEach(len(sums), func(i int) error {
		sm := sums[i]
		rows, err := s.attempts.ListByExam(ctx, sm.ID)
		if err != nil {
			return err
		}
		x := store.Exam{ID: sm.ID, CreatorID: sm.CreatorID, Title: sm.Title, Type: sm.Type,
			TotalMarks: sm.TotalMarks, ClosesAt: sm.ClosesAt, CreatedAt: sm.CreatedAt}
		var c Counts
		for _, a := range rows {
			if s.fin != nil && a.Status == store.AttemptInProgress && over(x, now) {
				// The finalizer needs the questions to grade; load the exam only when needed.
				full, err := s.exams.GetExam(ctx, sm.ID)
				if err != nil {
					return err
				}
				if a, err = s.fin.Finalize(ctx, full, a); err != nil {
					return err
				}
			}
			switch a.Status {
			case store.AttemptSubmitted:
				c.Submitted++
			case store.AttemptInProgress:
				c.InProgress++
			case store.AttemptNotStarted:
				c.NotStarted++
			case store.AttemptDeclined:
				c.Declined++
			}
		}
		it := baseItem(x, by)
		it.Counts = &c
		out[i] = it
		return nil
	})
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return out, nil
}
