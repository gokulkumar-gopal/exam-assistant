package gen

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime/debug"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/docs"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// Messages the worker writes to a job.
const (
	TooLongMessage        = "That took too long. Try again."
	GenerateFailedMessage = "We couldn't generate questions. Try again."
	NoSourcesMessage      = "We couldn't read any of the selected files."
	ShortfallMessage      = "We couldn't generate enough questions from these files."
	SaveFailedMessage     = "We couldn't save your questions. Try again."
	BusyMessage           = "The AI service is busy. Try again in a minute."
	RefusalMessage        = "We couldn't generate questions from these files."

	unreadableFileReason = "We couldn't read this file."
	modelSkippedReason   = "The model couldn't use this file."
	missingFileReason    = "This file is no longer available."
)

// Defaults for the injectable worker timings (backend-spec 7.2 step 3).
const (
	DefaultJobDeadline       = 8 * time.Minute
	DefaultHeartbeatInterval = 20 * time.Second
	defaultMaxPerCall        = 15 // AI-spec 5.3
	maxConcurrentCalls       = 3  // backend-spec 7.2 step 4
	defaultCallTimeout       = 120 * time.Second
	defaultMaxTotalBytes     = 20 << 20
	defaultMaxPDFPages       = 40
	defaultMaxInputTokens    = 80_000
	// finishTimeout bounds the writes that end a job. They run on a context detached from
	// the job's, which may already be past its deadline.
	finishTimeout = 15 * time.Second
)

// WorkerConfig is the part of the process configuration the worker needs.
type WorkerConfig struct {
	MaxPerCall    int           // questions per Generate call (AI-spec 5.3); 0 means 15
	Effort        string        // recorded on the job's usage
	PromptVersion string        // recorded on the job's usage (prompt.PromptVersion; gen cannot import prompt)
	CallTimeout   time.Duration // per Generate call (LLM_TIMEOUT_SECONDS); 0 means 120 s
	MaxTotalBytes int64         // raw bytes of the sources (GEN_MAX_TOTAL_BYTES); 0 means 20 MiB
	MaxPDFPages   int           // pages of the sources (GEN_MAX_PDF_PAGES); 0 means 40
	// MaxInputTokens caps the counted input of the shared prefix (GEN_MAX_INPUT_TOKENS, AI-spec
	// 10.2); 0 means 80,000. It applies only to generators that implement TokenCounter.
	MaxInputTokens int
	// BudgetMicros is the monthly budget (LLM_MONTHLY_BUDGET_USD) in micro-dollars. Unlike the
	// other limits zero does not mean a default: a budget of zero or less refuses every call,
	// as it refuses every job at creation (store.QuotaCheck), so a worker wired without one
	// cannot spend.
	BudgetMicros int64
}

// Worker runs generation jobs: claim, heartbeat, deadline, calls and finish
// (backend-spec 7.2 steps 2 to 7). Sources are prepared with PrepareSources (AI-spec 5.1) and
// every Generator output goes through Validate, ShuffleOptions and OrderQuestions (AI-spec 7).
type Worker struct {
	Jobs  store.JobStore
	Docs  store.DocStore
	Blob  docs.Blob
	Gens  GeneratorFactory
	Clock clock.Clock
	Cfg   WorkerConfig
	// Usage records every call's spend. Nil means SpendRecorder over Jobs and Clock.
	Usage UsageRecorder

	heartbeatInterval time.Duration // 0 means DefaultHeartbeatInterval
	jobDeadline       time.Duration // 0 means DefaultJobDeadline
	rnd               io.Reader     // option and question shuffling; nil means crypto/rand. Used by Run's goroutine only, after every call has finished
	idEntropy         io.Reader     // ULID entropy; nil means crypto/rand
}

func (w *Worker) heartbeatEvery() time.Duration {
	if w.heartbeatInterval > 0 {
		return w.heartbeatInterval
	}
	return DefaultHeartbeatInterval
}

func (w *Worker) deadline() time.Duration {
	if w.jobDeadline > 0 {
		return w.jobDeadline
	}
	return DefaultJobDeadline
}

// Why a run's generation context was cancelled (context.Cause).
var (
	errCancelRequested = errors.New("gen: cancel requested")
	errNotRunning      = errors.New("gen: job is no longer running")
)

// runState is what the calling goroutines publish and the heartbeat goroutine reads. The
// heartbeat goroutine is the only writer of Heartbeat: callers here only update values and
// poke it after a call completes.
type runState struct {
	mu        sync.Mutex
	total     int // questions requested
	results   [][]store.Question
	dropped   []int
	usage     Usage
	cost      int64
	calls     int // calls accounted
	invoked   int // provider calls started (the per-job cap)
	failed    []store.FailedSource
	failedIDs map[string]bool
	poke      chan struct{} // capacity 1: "progress changed, write it now"
}

func newRunState(total, tasks int) *runState {
	return &runState{
		total: total, results: make([][]store.Question, tasks), dropped: make([]int, tasks),
		poke: make(chan struct{}, 1),
	}
}

// notify asks the heartbeat goroutine to write progress now (it coalesces pokes).
func (s *runState) notify() {
	select {
	case s.poke <- struct{}{}:
	default:
	}
}

func (s *runState) addCall(u Usage, cost int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.usage.Input += u.Input
	s.usage.Output += u.Output
	s.usage.CacheRead += u.CacheRead
	s.usage.CacheWrite += u.CacheWrite
	s.cost += cost
	s.calls++
}

// allKept returns every kept question in plan order.
func (s *runState) allKept() []store.Question {
	s.mu.Lock()
	defer s.mu.Unlock()
	var kept []store.Question
	for _, qs := range s.results {
		kept = append(kept, qs...)
	}
	return kept
}

func (s *runState) addFailed(f store.FailedSource) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addFailedLocked(f)
}

func (s *runState) addFailedLocked(f store.FailedSource) {
	if s.failedIDs == nil {
		s.failedIDs = map[string]bool{}
	}
	if !s.failedIDs[f.DocID] {
		s.failedIDs[f.DocID] = true
		s.failed = append(s.failed, f)
	}
}

type runSnapshot struct {
	total, done, calls, dropped int
	usage                       Usage
	cost                        int64
	failed                      []store.FailedSource
}

func (s *runState) snap() runSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	sn := runSnapshot{total: s.total, calls: s.calls, usage: s.usage, cost: s.cost, failed: slices.Clone(s.failed)}
	for i := range s.results {
		sn.done += len(s.results[i])
		sn.dropped += s.dropped[i]
	}
	return sn
}

func (w *Worker) jobUsage(sn runSnapshot) store.JobUsage {
	return store.JobUsage{
		PromptVersion: w.Cfg.PromptVersion, Effort: w.Cfg.Effort,
		InputTokens: sn.usage.Input, OutputTokens: sn.usage.Output,
		CacheReadTokens: sn.usage.CacheRead, CacheWriteTokens: sn.usage.CacheWrite,
		CostMicros: sn.cost, Calls: sn.calls, Requested: sn.total, Kept: sn.done, Dropped: sn.dropped,
		AnySourceFailed: len(sn.failed) > 0,
	}
}

// Run runs one job to a final state. It returns nil for every outcome the job record
// already reflects (done, failed, cancelled, or a duplicate delivery that did nothing);
// an error means the job could not be loaded or a final write failed, so a retry by the
// platform is reasonable (the claim keeps a retry from running the job twice).
func (w *Worker) Run(ctx context.Context, jobID string) error {
	log := slog.With("job_id", jobID)
	job, err := w.Jobs.GetJob(ctx, jobID)
	if errors.Is(err, store.ErrNotFound) {
		log.Warn("worker: job not found (expired?)")
		return nil
	}
	if err != nil {
		return fmt.Errorf("load job: %w", err)
	}
	claimed, err := w.Jobs.ClaimJob(ctx, jobID, w.Clock.Now())
	if err != nil {
		return fmt.Errorf("claim job: %w", err)
	}
	if !claimed {
		log.Info("worker: job is not queued; nothing to do", "status", job.Status)
		return nil
	}

	dctx, cancelDeadline := context.WithTimeout(ctx, w.deadline())
	defer cancelDeadline()
	gctx, cancelGen := context.WithCancelCause(dctx)
	defer cancelGen(nil)

	plan := w.plan(job)
	state := newRunState(requestedTotal(job), len(plan))
	stopHB := w.startHeartbeat(gctx, cancelGen, job, state)
	defer stopHB() // also on a panic in the generator

	qs, genErr := w.safeGenerate(gctx, log, job, plan, state)
	stopHB() // the heartbeat goroutine is gone before any final write

	cause := context.Cause(gctx)
	fctx, cancelFinish := context.WithTimeout(context.WithoutCancel(ctx), finishTimeout)
	defer cancelFinish()
	sn := state.snap()

	// Spend was recorded after every call, so no final spend write is needed on any path.
	switch {
	case errors.Is(cause, errNotRunning):
		// Someone else finished the job (stale failure, cancel): nothing to write.
		log.Info("worker: job is no longer running; stopped")
		return nil
	case errors.Is(genErr, errCancelRequested) || errors.Is(cause, errCancelRequested):
		return w.finish(fctx, log, job, store.JobCancelled, "", sn)
	case genErr != nil:
		// Includes a recovered generator panic: it is failed with the generic message.
		return w.finish(fctx, log, job, store.JobFailed, w.failureMessage(log, ctx, dctx, genErr), sn)
	}

	// Last look before the questions item is written: a cancel or a stale failure that came
	// in after the last call is honoured without leaving a questions item behind.
	if cur, err := w.Jobs.GetJob(fctx, jobID); err == nil {
		switch {
		case cur.Status != store.JobRunning:
			log.Info("worker: job finished elsewhere before the questions were saved", "status", cur.Status)
			return nil
		case cur.CancelRequested:
			return w.finish(fctx, log, job, store.JobCancelled, "", sn)
		}
	}

	// Questions first: a job is never done without its questions item.
	final := w.arrange(job, qs)
	if err := w.Jobs.PutQuestions(fctx, jobID, final); err != nil {
		log.Error("worker: save questions failed", "err", err, "questions", len(final))
		return w.finish(fctx, log, job, store.JobFailed, SaveFailedMessage, sn)
	}

	sn.done = len(final)
	ok, err := w.Jobs.FinishJob(fctx, jobID, store.JobDone, w.finishData(sn, ""))
	if err != nil {
		return fmt.Errorf("finish job: %w", err)
	}
	if ok {
		return nil
	}
	// Refused: cancel was requested after the last check, or the job is no longer running.
	cur, err := w.Jobs.GetJob(fctx, jobID)
	if err != nil {
		return fmt.Errorf("reload job: %w", err)
	}
	if cur.Status == store.JobRunning && cur.CancelRequested {
		return w.finish(fctx, log, job, store.JobCancelled, "", sn)
	}
	log.Info("worker: done was refused; job already finished", "status", cur.Status)
	return nil
}

func requestedTotal(job store.Job) int {
	n := 0
	for _, c := range job.Request.Counts {
		n += c
	}
	if n == 0 {
		return job.Total
	}
	return n
}

func (w *Worker) finishData(sn runSnapshot, msg string) store.JobFinish {
	return store.JobFinish{
		Now: w.Clock.Now(), Done: sn.done, Error: msg, FailedSources: sn.failed,
		Usage: w.jobUsage(sn),
	}
}

func (w *Worker) finish(ctx context.Context, log *slog.Logger, job store.Job, to, msg string, sn runSnapshot) error {
	ok, err := w.Jobs.FinishJob(ctx, job.ID, to, w.finishData(sn, msg))
	if err != nil {
		return fmt.Errorf("finish job %s: %w", to, err)
	}
	if !ok {
		log.Info("worker: final state refused; job already finished", "wanted", to)
	}
	return nil
}

// failureMessage picks the user-facing text for a failed generation and logs the cause.
func (w *Worker) failureMessage(log *slog.Logger, parent, dctx context.Context, err error) string {
	var ue *UserError
	switch {
	case errors.Is(err, errGeneratorPanic):
		return GenerateFailedMessage // already logged with its stack
	case errors.As(err, &ue):
		log.Warn("worker: generation failed", "err", err)
		return ue.Message
	case errors.Is(dctx.Err(), context.DeadlineExceeded) && parent.Err() == nil:
		log.Warn("worker: deadline exceeded")
		return TooLongMessage
	default:
		log.Error("worker: generation failed", "err", err)
		return GenerateFailedMessage
	}
}

func (w *Worker) recorder() UsageRecorder {
	if w.Usage != nil {
		return w.Usage
	}
	return SpendRecorder{Jobs: w.Jobs, Now: w.Clock.Now}
}

// startHeartbeat starts the heartbeat goroutine, the only goroutine that calls
// JobStore.Heartbeat, and returns an idempotent stop function that cancels it and waits
// for it to exit. It writes UpdatedAt with the latest progress on every tick and whenever a
// call completes (state.poke), then reads the job: a refused heartbeat (the job is no longer
// running) or CancelRequested cancels the generation context with the matching cause.
func (w *Worker) startHeartbeat(gctx context.Context, cancelGen context.CancelCauseFunc, job store.Job, st *runState) (stop func()) {
	hctx, cancel := context.WithCancel(gctx)
	done := make(chan struct{})
	log := slog.With("job_id", job.ID)
	go func() {
		defer close(done)
		t := time.NewTicker(w.heartbeatEvery())
		defer t.Stop()
		for {
			select {
			case <-hctx.Done():
				return
			case <-t.C:
			case <-st.poke:
			}
			sn := st.snap()
			p := store.Progress{Done: sn.done, Total: sn.total, Usage: w.jobUsage(sn)}
			switch err := w.Jobs.Heartbeat(hctx, job.ID, w.Clock.Now(), p); {
			case err == nil:
			case errors.Is(err, store.ErrPrecondition), errors.Is(err, store.ErrNotFound):
				cancelGen(errNotRunning)
				return
			case hctx.Err() != nil:
				return
			default:
				log.Warn("worker: heartbeat failed", "err", err)
				continue
			}
			if cur, err := w.Jobs.GetJob(hctx, job.ID); err == nil && cur.CancelRequested {
				cancelGen(errCancelRequested)
				return
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
}

var errGeneratorPanic = errors.New("gen: generator panicked")

// safeGenerate is generate with a panic turned into errGeneratorPanic, so Run still stops
// the heartbeat, keeps the usage recorded so far and fails the job. The panic value and
// stack are logged only; the user sees the generic message. Run returns nil afterwards,
// which keeps Lambda from retrying; InProcessRunner's own recover is defence in depth for
// panics outside this phase and is harmless here (it never sees this one). Panics in the
// concurrent call goroutines are recovered the same way by runTask's caller.
func (w *Worker) safeGenerate(ctx context.Context, log *slog.Logger, job store.Job, plan []CallSpec, st *runState) (qs []store.Question, err error) {
	defer func() {
		if p := recover(); p != nil {
			log.Error("worker: generator panicked", "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
			qs, err = nil, errGeneratorPanic
		}
	}()
	return w.generate(ctx, job, plan, st)
}

// plan is the job's calls: one per mark value with a count, chunked by MaxPerCall.
func (w *Worker) plan(job store.Job) []CallSpec {
	counts := map[int]int{}
	for k, n := range job.Request.Counts {
		if m, err := strconv.Atoi(k); err == nil && n > 0 {
			counts[m] = n
		}
	}
	plan := PlanCalls(counts, w.Cfg.MaxPerCall)
	for i := range plan {
		plan[i].Type = job.Request.Type
	}
	return plan
}

// PlanCalls returns the first-attempt calls of a job (AI-spec 5.3): one per mark value with a
// non-zero count, in ascending marks, with a count above perCall split into chunks of perCall
// (the last chunk takes the rest). perCall 0 or less means 15. CallSpec.Type is left empty.
func PlanCalls(counts map[int]int, perCall int) []CallSpec {
	if perCall <= 0 {
		perCall = defaultMaxPerCall
	}
	marks := make([]int, 0, len(counts))
	for m, n := range counts {
		if n > 0 {
			marks = append(marks, m)
		}
	}
	sort.Ints(marks)
	var plan []CallSpec
	for _, m := range marks {
		for left := counts[m]; left > 0; left -= perCall {
			plan = append(plan, CallSpec{Marks: m, Count: min(left, perCall)})
		}
	}
	return plan
}

// generate checks the job is not cancelled, loads and prepares the sources, enforces the
// caps, then runs the planned calls: the first alone (it writes the prompt cache), the rest
// at most three at a time. It returns every kept question in plan order, with SourceDocID and
// SourceDocName mapped and MCQ options shuffled. Usage of every call, even a failed one, is
// recorded as the call ends.
func (w *Worker) generate(ctx context.Context, job store.Job, plan []CallSpec, st *runState) ([]store.Question, error) {
	log := slog.With("job_id", job.ID)
	if cur, err := w.Jobs.GetJob(ctx, job.ID); err == nil && cur.CancelRequested {
		return nil, errCancelRequested
	}
	// A model with no price cannot be charged to the budget: never run a job without spend accounting.
	model, known := LookupModel(job.Model)
	if !known {
		log.Warn("worker: model has no price; failing the job before any call", "model", job.Model)
		return nil, &UserError{Message: GenerateFailedMessage, Err: errors.New("unpriced model")}
	}
	sources, byLabel, usage, err := w.loadSources(ctx, job, st)
	if err != nil {
		return nil, err
	}
	if err := w.checkSourceCaps(log, usage); err != nil {
		return nil, err
	}
	g, err := w.Gens(job.Model)
	if err != nil {
		return nil, fmt.Errorf("generator for %q: %w", job.Model, err)
	}
	r := &jobRun{
		w: w, log: log, job: job, model: model, g: g, sources: sources, byLabel: byLabel,
		newID: newIDSource(w.Clock.Now, w.idEntropy), st: st, budget: w.Cfg.BudgetMicros,
		callTimeout: w.Cfg.CallTimeout, inputTokens: estimateInputTokens(sources),
	}
	if r.callTimeout <= 0 {
		r.callTimeout = defaultCallTimeout
	}
	if len(plan) == 0 {
		return nil, nil
	}
	if err := r.countInput(ctx, plan[0]); err != nil {
		return nil, err
	}
	if err := r.runAll(ctx, plan); err != nil {
		return nil, err
	}

	// Every call has finished: the shuffles below are the only users of w.rnd.
	kept := st.allKept()
	for i := range kept {
		ShuffleOptions(&kept[i], w.rnd) // after validation (AI-spec 7.3)
	}
	return kept, nil
}

// checkSourceCaps fails a job whose sources exceed the byte or page caps before any call
// (AI-spec 5.3). The create endpoint checks the same caps; this guards a worker whose limits
// differ or whose documents changed.
func (w *Worker) checkSourceCaps(log *slog.Logger, u sourceUsage) error {
	maxBytes, maxPages := w.Cfg.MaxTotalBytes, w.Cfg.MaxPDFPages
	if maxBytes <= 0 {
		maxBytes = defaultMaxTotalBytes
	}
	if maxPages <= 0 {
		maxPages = defaultMaxPDFPages
	}
	if u.bytes > maxBytes || u.pages > maxPages {
		log.Warn("worker: sources exceed the caps", "bytes", u.bytes, "pages", u.pages, "max_bytes", maxBytes, "max_pages", maxPages)
		return &UserError{Message: TooLargeMessage}
	}
	return nil
}

// sourceUsage is what loadSources read, for the cap checks.
type sourceUsage struct {
	bytes int64
	pages int
}

// loadSources reads the job's documents from the blob store, owner-checked, prepares them
// with PrepareSources (AI-spec 5.1: PDF as it is, images downscaled, text cleaned; labels
// doc-1, doc-2, ... in document id order) and records every document that is missing,
// unreadable or fails preparation in the job's failed sources. With none left the job fails
// with NoSourcesMessage (RequireSources). It also returns the raw bytes and the pages of the
// documents that were read.
func (w *Worker) loadSources(ctx context.Context, job store.Job, st *runState) ([]SourceFile, map[string]store.Document, sourceUsage, error) {
	var usage sourceUsage
	got, err := w.Docs.GetDocs(ctx, job.OwnerID, job.Request.SourceIDs)
	if err != nil {
		return nil, nil, usage, fmt.Errorf("load documents: %w", err)
	}
	have := map[string]store.Document{}
	for _, d := range got {
		have[d.ID] = d
	}
	var ready []store.Document
	blobs := map[string][]byte{}
	for _, id := range slices.Compact(slices.Sorted(slices.Values(job.Request.SourceIDs))) {
		d, ok := have[id]
		if !ok || d.OwnerID != job.OwnerID || d.Status != store.DocReady {
			st.addFailed(store.FailedSource{DocID: id, Name: d.Name, Reason: missingFileReason})
			continue
		}
		data, err := w.Blob.Get(ctx, d.S3Key, docs.MaxUploadBytes)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, usage, ctx.Err()
			}
			slog.Warn("worker: document unreadable", "job_id", job.ID, "doc_id", id, "err", err)
			st.addFailed(store.FailedSource{DocID: id, Name: d.Name, Reason: unreadableFileReason})
			continue
		}
		ready = append(ready, d)
		blobs[id] = data
		usage.bytes += int64(len(data))
		usage.pages += d.Pages
	}
	sources, byLabel, failed := PrepareSources(ready, blobs)
	for _, f := range failed {
		st.addFailed(f)
	}
	if err := RequireSources(sources); err != nil {
		return nil, nil, usage, err
	}
	return sources, byLabel, usage, nil
}

// arrange puts the questions in final order (AI-spec 7.3): ascending by marks, or random when
// the request asks for it. Ids were assigned when the questions were validated.
func (w *Worker) arrange(job store.Job, qs []store.Question) []store.Question {
	return OrderQuestions(qs, job.Request.Shuffle, w.rnd)
}
