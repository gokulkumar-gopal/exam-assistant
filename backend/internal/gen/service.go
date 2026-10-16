package gen

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// Fixed request caps (backend-spec 7.5).
const (
	maxCountPerMark = 30
	maxQuestions    = 60
	maxTotalMarks   = 200
)

// Messages shown to users.
const (
	TooLargeMessage    = "These files are too large to use together. Select fewer or smaller files."
	StartFailedMessage = "We couldn't start that. Try again."
	StaleMessage       = "That took too long. Try again."
	RunningMessage     = "You already have question sets being generated. Wait for one to finish, then try again."
)

// StaleAfter is how long a queued or running job may go without a write before the read
// endpoint reports it failed (backend-spec 7.2 step 7). Older than this is stale; exactly
// this old is not.
const StaleAfter = 6 * time.Minute

// JobRunner starts a created job in the background (backend-spec 5). Implementations
// arrive with the worker task.
type JobRunner interface {
	Start(ctx context.Context, jobID string) error
}

// StubRunner is the placeholder runner: it never starts anything, so POST /generate
// answers 503 until a real JobRunner is wired in.
type StubRunner struct{}

// Start always fails.
func (StubRunner) Start(context.Context, string) error { return errors.New("generation not available") }

// Limits are the configured generation limits (backend-spec 7.5, 8.4).
type Limits struct {
	MaxSources    int
	MaxTotalBytes int64
	MaxPDFPages   int
	MaxRunning    int
	Quota         QuotaLimits
}

// GenerateRequest is the body of POST /generate.
type GenerateRequest struct {
	SourceIDs []string       `json:"sourceIds"`
	Type      string         `json:"type"`
	Counts    map[string]int `json:"counts"`
	Shuffle   bool           `json:"shuffle"`
	Model     string         `json:"model,omitempty"`
}

// Progress is the poll view's progress object.
type Progress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

// PollView is the response of GET /generate/{jobId}.
type PollView struct {
	Status        string               `json:"status"`
	Model         string               `json:"model"`
	Progress      Progress             `json:"progress"`
	Questions     []store.Question     `json:"questions"`
	FailedSources []store.FailedSource `json:"failedSources"`
	Error         *string              `json:"error"`
	ServerTime    string               `json:"serverTime"`
}

// Service holds the generation use cases that run in the API process.
type Service struct {
	jobs     store.JobStore
	docs     store.DocStore
	runner   JobRunner
	clk      clock.Clock
	models   []Model
	defModel string
	limits   Limits
}

// NewService builds a Service. models are the enabled catalog models and defaultModelID
// is the one used when a request names none.
func NewService(jobs store.JobStore, docs store.DocStore, runner JobRunner, clk clock.Clock, models []Model, defaultModelID string, limits Limits) *Service {
	return &Service{jobs: jobs, docs: docs, runner: runner, clk: clk, models: models, defModel: defaultModelID, limits: limits}
}

func invalid(msg string, field, fieldMsg string) *apperr.Error {
	return apperr.Validation(msg, map[string]string{field: fieldMsg})
}

// Create validates the request (shape, caps, documents, model), refuses it when a limit
// is reached, creates the queued job with its quota counters in one transaction, and
// starts it. It returns the job ID.
func (s *Service) Create(ctx context.Context, user store.User, req GenerateRequest) (string, error) {
	total, err := s.checkShapeAndCaps(req)
	if err != nil {
		return "", err
	}
	if err := s.checkDocuments(ctx, user, req.SourceIDs); err != nil {
		return "", err
	}
	modelID, err := s.resolveModel(req.Model)
	if err != nil {
		return "", err
	}

	now := s.clk.Now().UTC() // read once: job day, quota day/month, TTL and timestamps all derive from it
	if err := s.checkRunning(ctx, user.ID, now); err != nil {
		return "", err
	}
	id, err := ulid.New(ulid.Timestamp(now), rand.Reader)
	if err != nil {
		return "", apperr.Internal(err)
	}
	job := store.Job{
		ID: id.String(), OwnerID: user.ID, Kind: store.KindGenerate, Model: modelID,
		Day: DayKey(now), Status: store.JobQueued,
		Request: store.GenerateRequest{
			SourceIDs: slices.Clone(req.SourceIDs), Type: req.Type, Counts: cloneCounts(req.Counts), Shuffle: req.Shuffle,
		},
		Total: total, CreatedAt: now, UpdatedAt: now, TTL: now.Add(store.JobItemTTL).Unix(),
	}
	if err := s.jobs.CreateJobWithQuota(ctx, job, s.limits.Quota.Check(user.ID, now, false)); err != nil {
		var qe *store.QuotaError
		if errors.As(err, &qe) {
			return "", apperr.RateLimited(QuotaMessage(qe.Kind))
		}
		return "", apperr.Internal(err)
	}

	if err := s.runner.Start(ctx, job.ID); err != nil {
		// A fresh context: the request's may be the very thing that failed.
		mctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if merr := s.jobs.MarkFailedIfQueuedOrRunning(mctx, job.ID, StartFailedMessage); merr != nil {
			err = errors.Join(err, fmt.Errorf("mark job failed: %w", merr))
		}
		return "", &apperr.Error{Status: 503, Code: "upstream_unavailable", Message: StartFailedMessage, Err: err}
	}
	return job.ID, nil
}

func cloneCounts(c map[string]int) map[string]int {
	out := make(map[string]int, len(c))
	for k, v := range c {
		out[k] = v
	}
	return out
}

func (s *Service) checkShapeAndCaps(req GenerateRequest) (int, error) {
	// Shape.
	if req.Type != "mcq" && req.Type != "free" {
		return 0, invalid("The request is not valid.", "type", `Use "mcq" or "free".`)
	}
	if len(req.SourceIDs) == 0 {
		return 0, invalid("Select at least one file.", "sourceIds", "Select at least one file.")
	}
	if len(req.Counts) == 0 {
		return 0, invalid("Choose how many questions you want.", "counts", "Required")
	}
	seen := map[string]bool{}
	for _, id := range req.SourceIDs {
		if id == "" || seen[id] {
			return 0, invalid("The selected files are not valid.", "sourceIds", "Each file may be selected once.")
		}
		seen[id] = true
	}

	// Caps.
	if len(req.SourceIDs) > s.limits.MaxSources {
		return 0, invalid(fmt.Sprintf("Select at most %d files.", s.limits.MaxSources), "sourceIds",
			fmt.Sprintf("Select at most %d files.", s.limits.MaxSources))
	}
	total, marks := 0, 0
	for k, n := range req.Counts {
		m, err := strconv.Atoi(k)
		if err != nil || m < 1 || m > 5 || strconv.Itoa(m) != k {
			return 0, invalid("Marks must be between 1 and 5.", "counts", "Marks must be between 1 and 5.")
		}
		if n < 0 || n > maxCountPerMark {
			return 0, invalid("Each count must be between 0 and 30.", "counts", "Each count must be between 0 and 30.")
		}
		total += n
		marks += m * n
	}
	if total == 0 {
		return 0, invalid("Choose at least one question.", "counts", "Choose at least one question.")
	}
	if total > maxQuestions {
		return 0, invalid("Choose at most 60 questions.", "counts", "Choose at most 60 questions.")
	}
	if marks > maxTotalMarks {
		return 0, invalid("The total marks can be at most 200.", "counts", "The total marks can be at most 200.")
	}
	return total, nil
}

func (s *Service) checkDocuments(ctx context.Context, user store.User, ids []string) error {
	docs, err := s.docs.GetDocs(ctx, user.ID, ids)
	if err != nil {
		return apperr.Internal(err)
	}
	byID := make(map[string]store.Document, len(docs))
	for _, d := range docs {
		byID[d.ID] = d // GetDocs is owner-scoped, so these are all the caller's
	}
	var bytes int64
	var pages int
	ordered := make([]store.Document, 0, len(ids))
	for _, id := range ids {
		d, ok := byID[id]
		if !ok || d.OwnerID != user.ID || d.Status != store.DocReady {
			return invalid("Some of the selected files are not available.", "sourceIds", "Some of the selected files are not available.")
		}
		ordered = append(ordered, d)
	}
	for _, d := range ordered {
		if d.Type == "PDF" && d.Pages > s.limits.MaxPDFPages {
			msg := fmt.Sprintf("%s has too many pages. Use a file with at most %d pages.", d.Name, s.limits.MaxPDFPages)
			return invalid(msg, "sourceIds", msg)
		}
	}
	for _, d := range ordered {
		bytes += d.SizeBytes
		if d.Type == "PDF" {
			pages += d.Pages
		}
	}
	if bytes > s.limits.MaxTotalBytes || pages > s.limits.MaxPDFPages {
		return apperr.Validation(TooLargeMessage, nil)
	}
	return nil
}

func (s *Service) resolveModel(requested string) (string, error) {
	if requested == "" {
		return s.defModel, nil
	}
	if !slices.ContainsFunc(s.models, func(m Model) bool { return m.ID == requested }) {
		return "", invalid("That model is not available.", "model", "Choose one of the available models.")
	}
	return requested, nil
}

func isStale(updatedAt, now time.Time) bool { return now.Sub(updatedAt) > StaleAfter }

// checkRunning enforces the soft running-jobs limit: queued or running jobs that are not
// stale (and so, with a 24 h TTL, not expired either).
func (s *Service) checkRunning(ctx context.Context, userID string, now time.Time) error {
	jobs, err := s.jobs.ListOwnerJobs(ctx, userID)
	if err != nil {
		return apperr.Internal(err)
	}
	running := 0
	for _, j := range jobs {
		if (j.Status == store.JobQueued || j.Status == store.JobRunning) && !isStale(j.UpdatedAt, now) {
			running++
		}
	}
	if running >= s.limits.MaxRunning {
		return apperr.RateLimited(RunningMessage)
	}
	return nil
}

// Poll returns the job's state. Only the owner can read it (404 otherwise). The questions
// item is read only when the job is done. A queued or running job whose UpdatedAt is
// stale is reported failed; nothing is written.
func (s *Service) Poll(ctx context.Context, user store.User, jobID string) (PollView, error) {
	job, err := s.jobs.GetJob(ctx, jobID)
	if errors.Is(err, store.ErrNotFound) {
		return PollView{}, apperr.NotFound()
	}
	if err != nil {
		return PollView{}, apperr.Internal(err)
	}
	if job.OwnerID != user.ID {
		return PollView{}, apperr.NotFound()
	}
	now := s.clk.Now().UTC()
	v := PollView{
		Status: job.Status, Model: job.Model,
		Progress:      Progress{Done: job.Done, Total: job.Total},
		FailedSources: job.FailedSources, ServerTime: now.Format(time.RFC3339),
	}
	if v.FailedSources == nil {
		v.FailedSources = []store.FailedSource{}
	}
	switch {
	case (job.Status == store.JobQueued || job.Status == store.JobRunning) && isStale(job.UpdatedAt, now):
		msg := StaleMessage
		v.Status, v.Error = store.JobFailed, &msg
	case job.Status == store.JobDone:
		qs, err := s.jobs.GetQuestions(ctx, jobID)
		if err != nil {
			return PollView{}, apperr.Internal(err)
		}
		v.Questions = qs
	case job.Error != "":
		msg := job.Error
		v.Error = &msg
	}
	return v, nil
}

// Cancel sets CancelRequested on the caller's job. It is idempotent; a missing job and
// another user's job are both 404.
func (s *Service) Cancel(ctx context.Context, user store.User, jobID string) error {
	err := s.jobs.RequestCancel(ctx, user.ID, jobID)
	if errors.Is(err, store.ErrNotFound) {
		return apperr.NotFound()
	}
	if err != nil {
		return apperr.Internal(err)
	}
	return nil
}
