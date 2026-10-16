package store

import (
	"context"
	"fmt"
	"time"
)

// Job statuses (backend-spec 6.1).
const (
	JobQueued    = "queued"
	JobRunning   = "running"
	JobDone      = "done"
	JobFailed    = "failed"
	JobCancelled = "cancelled"
)

// Job kinds.
const (
	KindGenerate   = "generate"
	KindRegenerate = "regenerate"
)

// Quota refusal kinds carried by QuotaError.
const (
	QuotaUser   = "user"   // the per-user daily generate limit
	QuotaRegen  = "regen"  // the per-user daily regenerate limit
	QuotaGlobal = "global" // the all-users daily limit
	QuotaBudget = "budget" // the monthly spend budget
)

// QuotaError is returned by CreateJobWithQuota when a daily limit or the monthly budget
// refuses the job. Nothing was written.
type QuotaError struct{ Kind string }

func (e *QuotaError) Error() string { return fmt.Sprintf("store: quota refused (%s)", e.Kind) }

// QuotaCheck is what CreateJobWithQuota enforces in the same transaction that creates the
// job. Day is yyyymmdd and Month yyyymm, both UTC. Regen selects the regenerate counter
// instead of the generate counter. A limit of zero or less refuses every job.
type QuotaCheck struct {
	UserID, Day, Month           string
	Regen                        bool
	MaxUser, MaxRegen, MaxGlobal int
	BudgetMicros                 int64
}

// GenerateRequest is the validated request a job was created from.
type GenerateRequest struct {
	SourceIDs []string       `json:"sourceIds"`
	Type      string         `json:"type"` // "mcq" | "free"
	Counts    map[string]int `json:"counts,omitempty"`
	Marks     int            `json:"marks,omitempty"` // regenerate only
	Shuffle   bool           `json:"shuffle,omitempty"`
	Avoid     []string       `json:"avoid,omitempty"`
}

// FailedSource is a source file the worker could not read.
type FailedSource struct {
	DocID  string `json:"docId"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// JobUsage is the per-job usage record (AI-spec 11). The token and cost fields are
// stored as top-level attributes because the day-index projects them.
type JobUsage struct {
	PromptVersion, Effort             string
	InputTokens, OutputTokens         int64
	CacheReadTokens, CacheWriteTokens int64
	CostMicros                        int64
	Calls                             int
	Requested, Kept, Dropped          int
	AnySourceFailed                   bool
}

// Job is the logical job entity (backend-spec 6.1). The generated questions live in a
// second item (see PutQuestions).
type Job struct {
	ID, OwnerID          string
	Kind                 string // KindGenerate | KindRegenerate
	Model                string
	Day                  string // yyyymmdd (UTC) of creation
	Status               string
	Request              GenerateRequest
	Done, Total          int
	FailedSources        []FailedSource
	Error                string
	CancelRequested      bool
	Usage                JobUsage
	CreatedAt, UpdatedAt time.Time
	TTL                  int64 // unix seconds (24 h); 0 means none
}

// Progress is what a heartbeat writes besides UpdatedAt.
type Progress struct {
	Done, Total int
	Usage       JobUsage
}

// JobFinish is the final write of a job. Now becomes UpdatedAt.
type JobFinish struct {
	Now           time.Time
	Done          int
	Error         string
	FailedSources []FailedSource
	Usage         JobUsage
}

// JobSummary is the owner-index projection of a job.
type JobSummary struct {
	ID, Status, Kind     string
	CreatedAt, UpdatedAt time.Time
}

// Question is one generated question (backend-spec 6.1).
type Question struct {
	ID            string   `json:"id"`
	Marks         int      `json:"marks"`
	Text          string   `json:"text"`
	Options       []string `json:"options,omitempty"`      // MCQ only, exactly 4
	CorrectIndex  *int     `json:"correctIndex,omitempty"` // MCQ only
	ModelAnswer   string   `json:"modelAnswer,omitempty"`  // free text only
	SourceDocID   string   `json:"sourceDocId"`
	SourceDocName string   `json:"sourceDocName"`
}

// JobStore persists generation jobs, the daily quota counters and the monthly spend counter.
type JobStore interface {
	// CreateJobWithQuota atomically puts the job and increments the user, global and month
	// counters, or does nothing. *QuotaError says which limit refused; ErrConflict means the
	// job id exists. A refused job leaves no counter and no job behind.
	CreateJobWithQuota(ctx context.Context, j Job, q QuotaCheck) error
	// GetJob returns ErrNotFound for a missing or expired job. The read is consistent.
	GetJob(ctx context.Context, id string) (Job, error)
	// ClaimJob moves queued to running. false, nil when the job is in any other state.
	ClaimJob(ctx context.Context, id string, now time.Time) (bool, error)
	// Heartbeat writes UpdatedAt and progress only while the job is running; otherwise it
	// writes nothing and returns ErrPrecondition (ErrNotFound if the job is missing).
	Heartbeat(ctx context.Context, id string, now time.Time, p Progress) error
	// FinishJob moves running to done, failed or cancelled. It never overwrites another
	// state, and "done" also requires CancelRequested to be false. false, nil if not applied.
	FinishJob(ctx context.Context, id string, to string, upd JobFinish) (bool, error)
	// RequestCancel sets CancelRequested on the owner's job (idempotent). ErrNotFound for a
	// missing or foreign job.
	RequestCancel(ctx context.Context, ownerID, id string) error
	// MarkFailedIfQueuedOrRunning sets failed with msg unless the job is already finished.
	// Not applying is not an error; ErrNotFound for a missing job.
	MarkFailedIfQueuedOrRunning(ctx context.Context, id, msg string) error
	// PutQuestions writes the questions item (<jobId>#questions).
	PutQuestions(ctx context.Context, jobID string, qs []Question) error
	// GetQuestions returns ErrNotFound if the questions item is missing or expired.
	GetQuestions(ctx context.Context, jobID string) ([]Question, error)
	// ListOwnerJobs returns the owner's jobs newest first (id descending; ids are ULIDs).
	// The owner-index is eventually consistent.
	ListOwnerJobs(ctx context.Context, ownerID string) ([]JobSummary, error)
	// AddSpend atomically adds micros to the month's spend and returns the new total.
	AddSpend(ctx context.Context, month string, micros int64) (total int64, err error)
	// SpendThisMonth returns the month's spend (0 when nothing was spent).
	SpendThisMonth(ctx context.Context, month string) (int64, error)
}

// Lifetimes of the items the job store writes.
const (
	JobItemTTL    = 24 * time.Hour
	QuotaDayTTL   = 3 * 24 * time.Hour
	QuotaMonthTTL = 40 * 24 * time.Hour
)

// ValidateQuotaCheck reports a malformed QuotaCheck (a programming error, not a refusal).
func ValidateQuotaCheck(q QuotaCheck) error {
	if q.UserID == "" || len(q.Day) != 8 || len(q.Month) != 6 || q.Day[:6] != q.Month {
		return fmt.Errorf("store: quota check needs a user, a yyyymmdd day and the matching yyyymm month")
	}
	return nil
}
