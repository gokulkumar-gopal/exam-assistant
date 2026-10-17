package gen

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"

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

// PageRange is a 1-based inclusive page range of one PDF source.
type PageRange = store.PageRange

// GenerateRequest is the body of POST /generate. PageRanges is optional and keyed by
// document id (backend-spec 8.4).
type GenerateRequest struct {
	SourceIDs  []string             `json:"sourceIds"`
	Type       string               `json:"type"`
	Counts     map[string]int       `json:"counts"`
	Shuffle    bool                 `json:"shuffle"`
	Model      string               `json:"model,omitempty"`
	PageRanges map[string]PageRange `json:"pageRanges,omitempty"`
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
	if err := s.checkDocuments(ctx, user, req.SourceIDs, req.PageRanges); err != nil {
		return "", err
	}
	modelID, err := s.resolveModel(req.Model)
	if err != nil {
		return "", err
	}

	job := store.Job{
		Kind: store.KindGenerate, Model: modelID,
		Request: store.GenerateRequest{
			SourceIDs: slices.Clone(req.SourceIDs), Type: req.Type, Counts: cloneCounts(req.Counts), Shuffle: req.Shuffle,
			PageRanges: maps.Clone(req.PageRanges),
		},
		Total: total,
	}
	return s.enqueue(ctx, user, job, false)
}

// enqueue completes the queued job (ID, owner, day, timestamps, TTL), refuses it when the
// running limit is reached, creates it with its quota counters in one transaction (regen
// selects the regenerate counter) and starts it. It returns the job ID.
func (s *Service) enqueue(ctx context.Context, user store.User, job store.Job, regen bool) (string, error) {
	now := s.clk.Now().UTC() // read once: job day, quota day/month, TTL and timestamps all derive from it
	if err := s.checkRunning(ctx, user.ID, now); err != nil {
		return "", err
	}
	id, err := ulid.New(ulid.Timestamp(now), rand.Reader)
	if err != nil {
		return "", apperr.Internal(err)
	}
	job.ID, job.OwnerID, job.Day, job.Status = id.String(), user.ID, DayKey(now), store.JobQueued
	job.CreatedAt, job.UpdatedAt, job.TTL = now, now, now.Add(store.JobItemTTL).Unix()
	if err := s.jobs.CreateJobWithQuota(ctx, job, s.limits.Quota.Check(user.ID, now, regen)); err != nil {
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

// Avoid limits of a regenerate request (backend-spec 8.4): strings, and characters in each.
const (
	maxAvoidItems = 60
	maxAvoidChars = 600
)

// Regenerate validates the request like Create (sources, caps, model) plus marks and the
// avoid list, then creates a regenerate job with Total 1 that counts against the regenerate
// quota, and starts it (backend-spec 7.6, 8.4). It returns the job ID.
func (s *Service) Regenerate(ctx context.Context, user store.User, req RegenerateRequest) (string, error) {
	if req.Marks < 1 || req.Marks > 5 {
		return "", invalid("Marks must be between 1 and 5.", "marks", "Marks must be between 1 and 5.")
	}
	if req.Type != "mcq" && req.Type != "free" {
		return "", invalid("The request is not valid.", "type", `Use "mcq" or "free".`)
	}
	if err := s.checkSourceIDs(req.SourceIDs); err != nil {
		return "", err
	}
	if len(req.Avoid) > maxAvoidItems {
		return "", invalid("The request is not valid.", "avoid", fmt.Sprintf("At most %d items.", maxAvoidItems))
	}
	for _, a := range req.Avoid {
		if utf8.RuneCountInString(a) > maxAvoidChars {
			return "", invalid("The request is not valid.", "avoid", fmt.Sprintf("Each item may have at most %d characters.", maxAvoidChars))
		}
	}
	if err := s.checkDocuments(ctx, user, req.SourceIDs, req.PageRanges); err != nil {
		return "", err
	}
	modelID, err := s.resolveModel(req.Model)
	if err != nil {
		return "", err
	}
	job := store.Job{
		Kind: store.KindRegenerate, Model: modelID,
		Request: store.GenerateRequest{
			SourceIDs: slices.Clone(req.SourceIDs), Type: req.Type, Marks: req.Marks, Avoid: slices.Clone(req.Avoid),
			PageRanges: maps.Clone(req.PageRanges),
		},
		Total: 1,
	}
	return s.enqueue(ctx, user, job, true)
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
	if err := s.checkSourceIDs(req.SourceIDs); err != nil {
		return 0, err
	}
	if len(req.Counts) == 0 {
		return 0, invalid("Choose how many questions you want.", "counts", "Required")
	}
	// Caps.
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

// checkSourceIDs is the shape and count check of a source list.
func (s *Service) checkSourceIDs(ids []string) error {
	if len(ids) == 0 {
		return invalid("Select at least one file.", "sourceIds", "Select at least one file.")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			return invalid("The selected files are not valid.", "sourceIds", "Each file may be selected once.")
		}
		seen[id] = true
	}
	if len(ids) > s.limits.MaxSources {
		msg := fmt.Sprintf("Select at most %d files.", s.limits.MaxSources)
		return invalid(msg, "sourceIds", msg)
	}
	return nil
}

func (s *Service) checkDocuments(ctx context.Context, user store.User, ids []string, ranges map[string]PageRange) error {
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
	if err := checkPageRanges(ids, byID, ranges); err != nil {
		return err
	}
	// effective is the pages a PDF contributes: the range length when it has a range.
	effective := func(d store.Document) int {
		if r, ok := ranges[d.ID]; ok {
			return r.To - r.From + 1
		}
		return d.Pages
	}
	for _, d := range ordered {
		if d.Type == "PDF" && effective(d) > s.limits.MaxPDFPages {
			msg := fmt.Sprintf("%s has too many pages. Choose at most %d pages.", d.Name, s.limits.MaxPDFPages)
			return invalid(msg, "sourceIds", msg)
		}
	}
	for _, d := range ordered {
		if _, ranged := ranges[d.ID]; !ranged {
			bytes += d.SizeBytes // a ranged PDF is measured after trimming, in the worker
		}
		if d.Type == "PDF" {
			pages += effective(d)
		}
	}
	if bytes > s.limits.MaxTotalBytes || pages > s.limits.MaxPDFPages {
		return apperr.Validation(TooLargeMessage, nil)
	}
	return nil
}

// checkPageRanges validates the optional per-PDF ranges (backend-spec 8.4): each key is a
// selected document, the document is a PDF, and 1 <= from <= to <= its pages.
func checkPageRanges(ids []string, byID map[string]store.Document, ranges map[string]PageRange) error {
	for _, id := range slices.Sorted(maps.Keys(ranges)) {
		r := ranges[id]
		d, ok := byID[id]
		if !slices.Contains(ids, id) || !ok {
			return invalid("The page range is not valid.", "pageRanges", "Page ranges are only for the selected files.")
		}
		var msg string
		switch {
		case d.Type != "PDF":
			msg = fmt.Sprintf("%s is not a PDF, so you can't choose pages.", d.Name)
		case r.From < 1 || r.To < r.From || r.To > d.Pages:
			msg = fmt.Sprintf("Choose pages 1 to %d for %s.", d.Pages, d.Name)
		}
		if msg != "" {
			return invalid(msg, "pageRanges", msg)
		}
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

// RegenerateRequest is the body of POST /generate/regenerate.
type RegenerateRequest struct {
	Marks     int      `json:"marks"`
	SourceIDs []string `json:"sourceIds"`
	Type      string   `json:"type"`
	Avoid     []string `json:"avoid"`
	Model     string   `json:"model,omitempty"`
	// PageRanges are the ranges of the original request, so the new question is drawn from
	// the same pages.
	PageRanges map[string]PageRange `json:"pageRanges,omitempty"`
}
