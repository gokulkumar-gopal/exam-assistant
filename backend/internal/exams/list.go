package exams

import (
	"context"
	"log/slog"
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
	// MaxPendingRows is a safety bound on a user's unfinished (not_started or in_progress)
	// attempt rows, whose exams must be loaded to know closesAt. Reaching it is logged at WARN;
	// rows are never dropped silently. The durable fix (parked for the spec owner) is to copy
	// closesAt onto the attempt row and into the user-index projection (backend-spec 6.2), so
	// both lists are decided from the index alone and only the shown exams are loaded.
	MaxPendingRows = 1000
	MaxListItems   = 200
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
// listed as closed and derived "missed". NewFinalizer is the real implementation.
type Finalizer interface {
	FinalizeIfExpired(ctx context.Context, a store.Attempt, e store.Exam, now time.Time) (store.Attempt, error)
}

// SetLogger sets the logger for list warnings (default slog.Default()).
func (s *Service) SetLogger(l *slog.Logger) { s.log = l }

// SetFinalizer wires the lazy-finalize hook. Call it before the Service serves requests.
func (s *Service) SetFinalizer(f Finalizer) { s.fin = f }

// UserRef is the public identity of a user in a list item: never an email.
type UserRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Counts are the creator's per-status counts of users (declined included), not of attempt rows.
type Counts struct {
	Submitted  int `json:"submitted"`
	InProgress int `json:"inProgress"`
	NotStarted int `json:"notStarted"`
	Declined   int `json:"declined"`
}

// ListItem is one row of GET /exams (backend-spec 8.5), one per exam however many attempts the
// caller has. Attempt fields are set only in the open and closed scopes; Counts only in created.
type ListItem struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	CreatedBy      UserRef   `json:"createdBy"`
	ClosesAt       time.Time `json:"closesAt"`
	AcceptingUntil time.Time `json:"acceptingUntil"`
	TotalMarks     int       `json:"totalMarks"`
	Type           string    `json:"type"`
	AttemptStatus  string    `json:"attemptStatus,omitempty"`
	Score          *float64  `json:"score,omitempty"` // the best score over the submitted attempts
	MaxScore       *int      `json:"maxScore,omitempty"`
	AttemptCount   int       `json:"attemptCount,omitempty"`
	CanRetake      *bool     `json:"canRetake,omitempty"` // set (true or false) in the open and closed scopes
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
// acceptingUntil is "missed" (derived, never stored). Call it only AFTER FinalizeIfExpired:
// backend-spec 6.1 derives "missed" for not_started only, and an expired in_progress attempt
// becomes submitted when finalized; the in_progress branch here covers only a missing finalizer.
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

// examRun is the caller's attempts on one exam as the list sees them: the latest attempt (a
// summary, replaced by the finalized row when it expires) and what the earlier attempts add.
type examRun struct {
	a     store.Attempt // the latest attempt
	count int
	best  *int // best ScoreHalves over the submitted attempts so far
	any   bool // some attempt is submitted
}

func (r *examRun) add(a store.Attempt) {
	if a.Status != store.AttemptSubmitted {
		return
	}
	r.any = true
	if a.ScoreHalves != nil && (r.best == nil || *a.ScoreHalves > *r.best) {
		h := *a.ScoreHalves
		r.best = &h
	}
}

type row struct {
	exam store.Exam
	run  examRun
}

func (s *Service) listAttempts(ctx context.Context, user store.User, scope string) ([]ListItem, error) {
	now := s.clk.Now()
	sums, err := s.attempts.ListByUser(ctx, user.ID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	// Group the rows by exam; the latest attempt (highest number) decides the list. Only the
	// latest can be unfinished. A declined latest attempt (only the assignment can be declined)
	// is in neither list. Decide from the projected status before loading any exam: unfinished
	// latest attempts are candidates for open and closed; submitted ones only for closed, newest
	// first by submittedAt (projected) and capped, so only that many exams are loaded.
	runs := map[string]*examRun{}
	var ids []string
	for _, a := range sums {
		r, ok := runs[a.ExamID]
		if !ok {
			r = &examRun{}
			runs[a.ExamID] = r
			ids = append(ids, a.ExamID)
		}
		at := store.Attempt{ExamID: a.ExamID, UserID: a.UserID, AttemptNo: a.AttemptNo, Status: a.Status, ScoreHalves: a.ScoreHalves,
			ScoreBeforePeeksHalves: a.ScoreBeforePeeksHalves, SubmittedAt: a.SubmittedAt}
		r.count++
		r.add(at)
		if at.AttemptNo >= r.a.AttemptNo {
			r.a = at
		}
	}
	var pending, submitted []string
	for _, id := range ids {
		switch runs[id].a.Status {
		case store.AttemptNotStarted, store.AttemptInProgress:
			pending = append(pending, id)
		case store.AttemptSubmitted:
			submitted = append(submitted, id)
		}
	}
	if len(pending) > s.maxPend {
		s.log.Warn("exam list: unfinished attempt rows over the safety bound; some are not listed",
			"user", user.ID, "rows", len(pending), "bound", s.maxPend)
		sort.Strings(pending)
		pending = pending[:s.maxPend]
	}
	cand := pending
	if scope == ScopeClosed {
		sort.Slice(submitted, func(i, j int) bool {
			a, b := runs[submitted[i]].a, runs[submitted[j]].a
			switch {
			case a.SubmittedAt == nil || b.SubmittedAt == nil:
				if (a.SubmittedAt == nil) != (b.SubmittedAt == nil) {
					return b.SubmittedAt == nil
				}
			case !a.SubmittedAt.Equal(*b.SubmittedAt):
				return a.SubmittedAt.After(*b.SubmittedAt)
			}
			return a.ExamID > b.ExamID
		})
		if len(submitted) > MaxListItems {
			submitted = submitted[:MaxListItems]
		}
		cand = append(append([]string{}, pending...), submitted...)
	}
	if len(cand) == 0 {
		return []ListItem{}, nil
	}
	found, err := s.exams.GetExams(ctx, cand)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	rows := make([]row, 0, len(cand))
	for _, id := range cand {
		x, ok := found[id]
		if !ok {
			continue // exam row gone: nothing to show
		}
		rows = append(rows, row{exam: x, run: *runs[id]})
	}
	if s.fin != nil {
		var todo []int
		for i, r := range rows {
			if r.run.a.Status == store.AttemptInProgress && over(r.exam, now) {
				todo = append(todo, i)
			}
		}
		err := forEach(len(todo), func(k int) error {
			r := &rows[todo[k]]
			full, err := s.attempts.GetAttempt(ctx, r.exam.ID, user.ID, r.run.a.AttemptNo)
			if err != nil {
				return err
			}
			fin, err := s.fin.FinalizeIfExpired(ctx, full, r.exam, now)
			if err != nil {
				return err
			}
			r.run.a = fin
			r.run.add(fin) // an auto-submitted attempt counts like any submitted one
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
		if Classify(r.exam, r.run.a, now) == scope {
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
	if len(picked) > MaxListItems {
		picked = picked[:MaxListItems]
	}
	for _, r := range picked {
		it := baseItem(r.exam, UserRef{ID: r.exam.CreatorID, Name: creators[r.exam.CreatorID].Name})
		it.AttemptStatus = DerivedStatus(r.exam, r.run.a, now)
		if r.run.any {
			it.AttemptStatus = store.AttemptSubmitted // an exam with a submitted attempt is never "missed"
		}
		mx := r.exam.TotalMarks
		it.MaxScore = &mx
		if r.run.any && r.exam.Type == "mcq" && r.run.best != nil {
			sc := float64(*r.run.best) / 2
			it.Score = &sc
		}
		it.AttemptCount = r.run.count
		can := r.run.a.Status == store.AttemptSubmitted && !over(r.exam, now) && r.run.count < store.MaxAttemptsPerExam
		it.CanRetake = &can
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
		var full *store.Exam // the finalizer needs the questions; loaded at most once, only if needed
		for _, run := range groupByUser(rows) {
			a := run.latest() // only the latest attempt of a user can be unfinished
			if s.fin != nil && a.Status == store.AttemptInProgress && over(x, now) {
				if full == nil {
					fx, err := s.exams.GetExam(ctx, sm.ID)
					if err != nil {
						return err
					}
					full = &fx
				}
				var err error
				if a, err = s.fin.FinalizeIfExpired(ctx, a, *full, now); err != nil {
					return err
				}
				run[len(run)-1] = a
			}
			switch run.status() {
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
