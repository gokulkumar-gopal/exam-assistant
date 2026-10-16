package memory

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

type userDay struct{ generate, regen int }

// JobStore is an in-memory store.JobStore. One mutex makes CreateJobWithQuota atomic.
// Jobs and questions whose TTL has passed (per the injected clock) behave as deleted.
// Quota counters are keyed by day and month, so they need no expiry here.
type JobStore struct {
	mu        sync.Mutex
	clk       clock.Clock
	jobs      map[string]store.Job
	questions map[string]jobQuestions
	userDays  map[string]userDay      // "<user>#<yyyymmdd>"
	global    map[string]int          // yyyymmdd -> jobs
	spend     map[string]int64        // yyyymm -> micro-dollars
	usage     map[string]usageCounter // "<yyyymm|yyyymmdd>#<model>"
}

type jobQuestions struct {
	qs  []store.Question
	ttl int64
}

var _ store.JobStore = (*JobStore)(nil)

// NewJobStore returns an empty JobStore reading time from clk.
func NewJobStore(clk clock.Clock) *JobStore {
	return &JobStore{clk: clk, jobs: map[string]store.Job{}, questions: map[string]jobQuestions{},
		userDays: map[string]userDay{}, global: map[string]int{}, spend: map[string]int64{}, usage: map[string]usageCounter{}}
}

// live returns the job if present and not expired. Caller holds the lock.
func (s *JobStore) live(id string) (store.Job, bool) {
	j, ok := s.jobs[id]
	if !ok || (j.TTL != 0 && s.clk.Now().Unix() >= j.TTL) {
		return store.Job{}, false
	}
	return j, true
}

func cloneJob(j store.Job) store.Job {
	j.FailedSources = slices.Clone(j.FailedSources)
	j.Request.SourceIDs = slices.Clone(j.Request.SourceIDs)
	j.Request.Avoid = slices.Clone(j.Request.Avoid)
	if j.Request.Counts != nil {
		c := make(map[string]int, len(j.Request.Counts))
		for k, v := range j.Request.Counts {
			c[k] = v
		}
		j.Request.Counts = c
	}
	return j
}

func (s *JobStore) CreateJobWithQuota(_ context.Context, j store.Job, q store.QuotaCheck) error {
	if j.ID == "" || j.OwnerID == "" || strings.Contains(j.ID, "#") {
		return errors.New("memory: job needs an id without '#' and an owner")
	}
	if err := store.ValidateQuotaCheck(q); err != nil {
		return err
	}
	if j.Day != q.Day {
		return fmt.Errorf("memory: job day %q differs from quota day %q", j.Day, q.Day)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.live(j.ID); ok {
		return fmt.Errorf("%w: job %s exists", store.ErrConflict, j.ID)
	}
	ud := s.userDays[q.UserID+"#"+q.Day]
	switch {
	case q.Regen && (q.MaxRegen <= 0 || ud.regen >= q.MaxRegen):
		return &store.QuotaError{Kind: store.QuotaRegen}
	case !q.Regen && (q.MaxUser <= 0 || ud.generate >= q.MaxUser):
		return &store.QuotaError{Kind: store.QuotaUser}
	case q.MaxGlobal <= 0 || s.global[q.Day] >= q.MaxGlobal:
		return &store.QuotaError{Kind: store.QuotaGlobal}
	case q.BudgetMicros <= 0 || s.spend[q.Month] >= q.BudgetMicros:
		return &store.QuotaError{Kind: store.QuotaBudget}
	}
	if q.Regen {
		ud.regen++
	} else {
		ud.generate++
	}
	s.userDays[q.UserID+"#"+q.Day] = ud
	s.global[q.Day]++
	s.jobs[j.ID] = cloneJob(j)
	return nil
}

func (s *JobStore) GetJob(_ context.Context, id string) (store.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.live(id)
	if !ok {
		return store.Job{}, store.ErrNotFound
	}
	return cloneJob(j), nil
}

func (s *JobStore) ClaimJob(_ context.Context, id string, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.live(id)
	if !ok || j.Status != store.JobQueued {
		return false, nil
	}
	j.Status, j.UpdatedAt = store.JobRunning, now
	s.jobs[id] = j
	return true, nil
}

func (s *JobStore) Heartbeat(_ context.Context, id string, now time.Time, p store.Progress) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.live(id)
	if !ok {
		return store.ErrNotFound
	}
	if j.Status != store.JobRunning {
		return fmt.Errorf("%w: job is %s", store.ErrPrecondition, j.Status)
	}
	j.UpdatedAt, j.Done, j.Total, j.Usage = now, p.Done, p.Total, p.Usage
	s.jobs[id] = j
	return nil
}

func (s *JobStore) FinishJob(_ context.Context, id string, to string, upd store.JobFinish) (bool, error) {
	if to != store.JobDone && to != store.JobFailed && to != store.JobCancelled {
		return false, fmt.Errorf("memory: cannot finish a job as %q", to)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.live(id)
	if !ok || j.Status != store.JobRunning || (to == store.JobDone && j.CancelRequested) {
		return false, nil
	}
	now := upd.Now
	if now.IsZero() {
		now = s.clk.Now()
	}
	j.Status, j.UpdatedAt, j.Done, j.Error = to, now, upd.Done, upd.Error
	j.FailedSources, j.Usage = slices.Clone(upd.FailedSources), upd.Usage
	s.jobs[id] = j
	return true, nil
}

func (s *JobStore) RequestCancel(_ context.Context, ownerID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.live(id)
	if !ok || j.OwnerID != ownerID {
		return store.ErrNotFound
	}
	j.CancelRequested = true
	s.jobs[id] = j
	return nil
}

func (s *JobStore) MarkFailedIfQueuedOrRunning(_ context.Context, id, msg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.live(id)
	if !ok {
		return store.ErrNotFound
	}
	if j.Status == store.JobQueued || j.Status == store.JobRunning {
		j.Status, j.Error, j.UpdatedAt = store.JobFailed, msg, s.clk.Now()
		s.jobs[id] = j
	}
	return nil
}

func (s *JobStore) PutQuestions(_ context.Context, jobID string, qs []store.Question) error {
	if jobID == "" || strings.Contains(jobID, "#") {
		return errors.New("memory: invalid job id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.questions[jobID] = jobQuestions{qs: slices.Clone(qs), ttl: s.clk.Now().Add(store.JobItemTTL).Unix()}
	return nil
}

func (s *JobStore) GetQuestions(_ context.Context, jobID string) ([]store.Question, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, ok := s.questions[jobID]
	if !ok || s.clk.Now().Unix() >= q.ttl {
		return nil, store.ErrNotFound
	}
	return slices.Clone(q.qs), nil
}

func (s *JobStore) ListOwnerJobs(_ context.Context, ownerID string) ([]store.JobSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []store.JobSummary
	for id := range s.jobs {
		if j, ok := s.live(id); ok && j.OwnerID == ownerID {
			out = append(out, store.JobSummary{ID: j.ID, Status: j.Status, Kind: j.Kind, CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt})
		}
	}
	slices.SortFunc(out, func(a, b store.JobSummary) int { return strings.Compare(b.ID, a.ID) })
	return out, nil
}

func (s *JobStore) AddSpend(_ context.Context, month string, micros int64) (int64, error) {
	if micros < 0 || len(month) != 6 {
		return 0, errors.New("memory: spend must be non-negative and the month yyyymm")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spend[month] += micros
	return s.spend[month], nil
}

func (s *JobStore) SpendThisMonth(_ context.Context, month string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.spend[month], nil
}
