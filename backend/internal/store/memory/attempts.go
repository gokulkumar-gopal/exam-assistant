package memory

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

type attemptKey struct {
	exam, user string
	no         int
}

// AttemptStore is an in-memory store.AttemptStore. One mutex makes each conditional write atomic.
type AttemptStore struct {
	mu       sync.Mutex
	attempts map[attemptKey]store.Attempt
}

var _ store.AttemptStore = (*AttemptStore)(nil)

// NewAttemptStore returns an empty AttemptStore.
func NewAttemptStore() *AttemptStore { return &AttemptStore{attempts: map[attemptKey]store.Attempt{}} }

func copyTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	c := *t
	return &c
}

func copyInt(i *int) *int {
	if i == nil {
		return nil
	}
	c := *i
	return &c
}

// normalize deep-copies a and gives Answers and Revealed their never-nil form.
func normalize(a store.Attempt) store.Attempt {
	a.Answers = maps.Clone(a.Answers)
	if a.Answers == nil {
		a.Answers = map[string]string{}
	}
	a.Revealed = slices.Clone(a.Revealed)
	if a.Revealed == nil {
		a.Revealed = []string{}
	}
	a.ScoreHalves, a.ScoreBeforePeeksHalves = copyInt(a.ScoreHalves), copyInt(a.ScoreBeforePeeksHalves)
	a.StartedAt, a.SavedAt, a.SubmittedAt = copyTime(a.StartedAt), copyTime(a.SavedAt), copyTime(a.SubmittedAt)
	return a
}

func (s *AttemptStore) PutAttemptIfAbsent(_ context.Context, a store.Attempt) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.AttemptNo == 0 {
		a.AttemptNo = 1
	}
	k := attemptKey{a.ExamID, a.UserID, a.AttemptNo}
	if _, ok := s.attempts[k]; ok {
		return false, nil
	}
	if a.Status == "" {
		a.Status = store.AttemptNotStarted
	}
	s.attempts[k] = normalize(a)
	return true, nil
}

func (s *AttemptStore) PutRetake(ctx context.Context, examID, userID string, attemptNo int, now time.Time) (bool, error) {
	if attemptNo < 1 || attemptNo > store.MaxAttemptsPerExam {
		return false, fmt.Errorf("%w: attempt number %d is outside 1..%d", store.ErrConflict, attemptNo, store.MaxAttemptsPerExam)
	}
	return s.PutAttemptIfAbsent(ctx, store.Attempt{ExamID: examID, UserID: userID, AttemptNo: attemptNo,
		Status: store.AttemptNotStarted, CreatedAt: now})
}

func (s *AttemptStore) GetAttempt(_ context.Context, examID, userID string, attemptNo int) (store.Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.attempts[attemptKey{examID, userID, attemptNo}]
	if !ok {
		return store.Attempt{}, store.ErrNotFound
	}
	return normalize(a), nil
}

// userAttempts returns the user's attempts on the exam, ordered by number. Caller holds the lock.
func (s *AttemptStore) userAttempts(examID, userID string) []store.Attempt {
	var out []store.Attempt
	for k, a := range s.attempts {
		if k.exam == examID && k.user == userID {
			out = append(out, normalize(a))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AttemptNo < out[j].AttemptNo })
	return out
}

func (s *AttemptStore) GetLatestAttempt(_ context.Context, examID, userID string) (store.Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all := s.userAttempts(examID, userID)
	if len(all) == 0 {
		return store.Attempt{}, store.ErrNotFound
	}
	return all[len(all)-1], nil
}

func (s *AttemptStore) ListAttempts(_ context.Context, examID, userID string) ([]store.Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.userAttempts(examID, userID), nil
}

func (s *AttemptStore) ListByUser(_ context.Context, userID string) ([]store.AttemptSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []store.AttemptSummary
	for k, a := range s.attempts {
		if k.user == userID {
			out = append(out, store.AttemptSummary{ExamID: a.ExamID, UserID: a.UserID, AttemptNo: a.AttemptNo, Status: a.Status,
				ScoreHalves: copyInt(a.ScoreHalves), ScoreBeforePeeksHalves: copyInt(a.ScoreBeforePeeksHalves), SubmittedAt: copyTime(a.SubmittedAt)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ExamID != out[j].ExamID {
			return out[i].ExamID < out[j].ExamID
		}
		return out[i].AttemptNo < out[j].AttemptNo
	})
	return out, nil
}

func (s *AttemptStore) ListByExam(_ context.Context, examID string) ([]store.Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []store.Attempt
	for k, a := range s.attempts {
		if k.exam == examID {
			out = append(out, normalize(a))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UserID != out[j].UserID {
			return out[i].UserID < out[j].UserID
		}
		return out[i].AttemptNo < out[j].AttemptNo
	})
	return out, nil
}

// writable returns the attempt if it exists and may still take answers (not submitted, not
// declined). Caller holds the lock.
func (s *AttemptStore) writable(examID, userID string, no int) (store.Attempt, error) {
	a, ok := s.attempts[attemptKey{examID, userID, no}]
	if !ok {
		return a, store.ErrNotFound
	}
	switch a.Status {
	case store.AttemptSubmitted:
		return a, store.ErrSubmitted
	case store.AttemptDeclined:
		return a, store.ErrDeclined
	}
	return a, nil
}

// start moves a not_started attempt to in_progress.
func start(a *store.Attempt, now time.Time) {
	if a.Status == store.AttemptNotStarted {
		a.Status = store.AttemptInProgress
	}
	if a.StartedAt == nil {
		a.StartedAt = copyTime(&now)
	}
}

func (s *AttemptStore) Save(_ context.Context, examID, userID string, no int, answers map[string]string, lastIdx int, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.writable(examID, userID, no)
	if err != nil {
		return err
	}
	start(&a, now)
	a.Answers = maps.Clone(answers)
	a.LastQuestionIndex = lastIdx
	a.SavedAt = copyTime(&now)
	s.attempts[attemptKey{examID, userID, no}] = normalize(a)
	return nil
}

func (s *AttemptStore) Submit(_ context.Context, in store.Attempt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.writable(in.ExamID, in.UserID, in.AttemptNo)
	if err != nil {
		return err
	}
	if in.Revealed != nil && len(in.Revealed) != len(a.Revealed) {
		return store.ErrConflict // a peek landed after the score was computed
	}
	a.Status = store.AttemptSubmitted
	a.Answers = maps.Clone(in.Answers)
	a.LastQuestionIndex = in.LastQuestionIndex
	a.ScoreHalves, a.ScoreBeforePeeksHalves = copyInt(in.ScoreHalves), copyInt(in.ScoreBeforePeeksHalves)
	a.SubmittedAt, a.SavedAt = copyTime(in.SubmittedAt), copyTime(in.SavedAt)
	a.AutoSubmitted = in.AutoSubmitted
	if a.StartedAt == nil {
		a.StartedAt = copyTime(in.StartedAt)
	}
	s.attempts[attemptKey{in.ExamID, in.UserID, in.AttemptNo}] = normalize(a)
	return nil
}

func (s *AttemptStore) Decline(_ context.Context, examID, userID string, no int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := attemptKey{examID, userID, no}
	a, ok := s.attempts[k]
	if !ok {
		return store.ErrNotFound
	}
	switch a.Status {
	case store.AttemptDeclined:
		return nil
	case store.AttemptNotStarted:
		a.Status = store.AttemptDeclined
		s.attempts[k] = a
		return nil
	}
	if a.Status == store.AttemptSubmitted {
		return store.ErrSubmitted
	}
	return store.ErrPrecondition
}

func (s *AttemptStore) Reveal(_ context.Context, examID, userID string, no int, questionID string, max int, now time.Time) (store.RevealResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.writable(examID, userID, no)
	if err != nil {
		return store.RevealResult{}, err
	}
	if slices.Contains(a.Revealed, questionID) {
		return store.RevealResult{Revealed: slices.Clone(a.Revealed)}, nil
	}
	if len(a.Revealed) >= max {
		return store.RevealResult{}, store.ErrConflict
	}
	start(&a, now)
	a.Revealed = append(slices.Clone(a.Revealed), questionID)
	s.attempts[attemptKey{examID, userID, no}] = a
	return store.RevealResult{Revealed: slices.Clone(a.Revealed), Charged: true}, nil
}
