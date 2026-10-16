package gen

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/docs"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// Messages the worker writes to a job.
const (
	TooLongMessage        = "That took too long. Try again."
	GenerateFailedMessage = "We couldn't generate questions. Try again."
	NoSourcesMessage      = "We couldn't read any of the selected files."
	SaveFailedMessage     = "We couldn't save your questions. Try again."

	unreadableFileReason = "We couldn't read this file."
	modelSkippedReason   = "The model couldn't use this file."
	missingFileReason    = "This file is no longer available."
)

// Defaults for the injectable worker timings (backend-spec 7.2 step 3).
const (
	DefaultJobDeadline       = 8 * time.Minute
	DefaultHeartbeatInterval = 20 * time.Second
	defaultMaxPerCall        = 15 // AI-spec 5.3
	// finishTimeout bounds the writes that end a job. They run on a context detached from
	// the job's, which may already be past its deadline.
	finishTimeout = 15 * time.Second
)

// WorkerConfig is the part of the process configuration the worker needs.
type WorkerConfig struct {
	MaxPerCall int    // questions per Generate call (AI-spec 5.3); 0 means 15
	Effort     string // recorded on the job's usage
}

// Worker runs generation jobs: claim, heartbeat, deadline, calls and finish
// (backend-spec 7.2 steps 2 to 7). What the model is asked and how sources are prepared
// belongs to the Generator (B-2.5 onward); loadSources is the minimal stand-in until then.
type Worker struct {
	Jobs  store.JobStore
	Docs  store.DocStore
	Blob  docs.Blob
	Gens  GeneratorFactory
	Clock clock.Clock
	Cfg   WorkerConfig

	heartbeatInterval time.Duration                    // 0 means DefaultHeartbeatInterval
	jobDeadline       time.Duration                    // 0 means DefaultJobDeadline
	shuffle           func(n int, swap func(i, j int)) // nil means math/rand/v2
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

// runState is what the generating goroutine publishes and the heartbeat goroutine reads.
// The heartbeat goroutine is the only writer of Heartbeat: callers here only update values.
type runState struct {
	mu        sync.Mutex
	total     int
	done      int
	usage     Usage
	calls     int
	dropped   int
	failed    []store.FailedSource
	failedIDs map[string]bool
}

func (s *runState) addCall(u Usage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.usage.Input += u.Input
	s.usage.Output += u.Output
	s.usage.CacheRead += u.CacheRead
	s.usage.CacheWrite += u.CacheWrite
	s.calls++
}

func (s *runState) setDone(n, dropped int) {
	s.mu.Lock()
	s.done, s.dropped = n, dropped
	s.mu.Unlock()
}

func (s *runState) addFailed(f store.FailedSource) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	failed                      []store.FailedSource
}

func (s *runState) snap() runSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return runSnapshot{s.total, s.done, s.calls, s.dropped, s.usage, slices.Clone(s.failed)}
}

func (w *Worker) jobUsage(model Model, known bool, sn runSnapshot) store.JobUsage {
	u := store.JobUsage{
		Effort: w.Cfg.Effort, InputTokens: sn.usage.Input, OutputTokens: sn.usage.Output,
		CacheReadTokens: sn.usage.CacheRead, CacheWriteTokens: sn.usage.CacheWrite,
		Calls: sn.calls, Requested: sn.total, Kept: sn.done, Dropped: sn.dropped,
		AnySourceFailed: len(sn.failed) > 0,
	}
	if known {
		u.CostMicros = CostMicros(model, sn.usage)
	}
	return u
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

	model, known := LookupModel(job.Model)
	state := &runState{total: requestedTotal(job)}
	stopHB := w.startHeartbeat(gctx, cancelGen, job, model, known, state)
	defer stopHB() // also on a panic in the generator

	qs, genErr := w.generate(gctx, job, state)
	stopHB() // the heartbeat goroutine is gone before any final write

	cause := context.Cause(gctx)
	fctx, cancelFinish := context.WithTimeout(context.WithoutCancel(ctx), finishTimeout)
	defer cancelFinish()
	sn := state.snap()

	switch {
	case errors.Is(cause, errNotRunning):
		// Someone else finished the job (stale failure, cancel): nothing to write.
		w.recordSpend(fctx, log, job, model, known, sn)
		log.Info("worker: job is no longer running; stopped")
		return nil
	case errors.Is(genErr, errCancelRequested) || errors.Is(cause, errCancelRequested):
		w.recordSpend(fctx, log, job, model, known, sn)
		return w.finish(fctx, log, job, store.JobCancelled, "", model, known, sn)
	case genErr != nil:
		w.recordSpend(fctx, log, job, model, known, sn)
		return w.finish(fctx, log, job, store.JobFailed, w.failureMessage(log, ctx, dctx, genErr), model, known, sn)
	}

	// Questions first: a job is never done without its questions item.
	final := w.arrange(job, qs)
	if err := w.Jobs.PutQuestions(fctx, jobID, final); err != nil {
		log.Error("worker: save questions failed", "err", err, "questions", len(final))
		w.recordSpend(fctx, log, job, model, known, sn)
		return w.finish(fctx, log, job, store.JobFailed, SaveFailedMessage, model, known, sn)
	}
	// Spend is recorded after the questions are safe. A failure here is logged and does not
	// fail the job: the user has valid questions and the tokens are already paid for, so
	// failing would only waste them (AI-spec 10.4 asks for an estimate that errs on the
	// safe side, and the provider-side limit backs the budget up).
	w.recordSpend(fctx, log, job, model, known, sn)

	sn.done = len(final)
	ok, err := w.Jobs.FinishJob(fctx, jobID, store.JobDone, w.finishData(model, known, sn, ""))
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
		return w.finish(fctx, log, job, store.JobCancelled, "", model, known, sn)
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

func (w *Worker) finishData(model Model, known bool, sn runSnapshot, msg string) store.JobFinish {
	return store.JobFinish{
		Now: w.Clock.Now(), Done: sn.done, Error: msg, FailedSources: sn.failed,
		Usage: w.jobUsage(model, known, sn),
	}
}

func (w *Worker) finish(ctx context.Context, log *slog.Logger, job store.Job, to, msg string, model Model, known bool, sn runSnapshot) error {
	ok, err := w.Jobs.FinishJob(ctx, job.ID, to, w.finishData(model, known, sn, msg))
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

// recordSpend adds the job's actual usage cost to the month's spend (AI-spec 10.4). Errors
// are logged only, with ids and amounts, never keys.
func (w *Worker) recordSpend(ctx context.Context, log *slog.Logger, job store.Job, model Model, known bool, sn runSnapshot) {
	if !known {
		log.Error("worker: unknown model; spend not recorded", "model", job.Model)
		return
	}
	micros := CostMicros(model, sn.usage)
	if micros <= 0 {
		return
	}
	month := MonthKey(w.Clock.Now())
	if _, err := w.Jobs.AddSpend(ctx, month, micros); err != nil {
		log.Error("worker: recording spend failed", "month", month, "micros", micros, "err", err)
	}
}

// startHeartbeat starts the heartbeat goroutine, the only goroutine that calls
// JobStore.Heartbeat, and returns an idempotent stop function that cancels it and waits
// for it to exit. On every tick it writes UpdatedAt with the latest progress and then
// reads the job: a refused heartbeat (the job is no longer running) or CancelRequested
// cancels the generation context with the matching cause.
func (w *Worker) startHeartbeat(gctx context.Context, cancelGen context.CancelCauseFunc, job store.Job, model Model, known bool, st *runState) (stop func()) {
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
			}
			sn := st.snap()
			p := store.Progress{Done: sn.done, Total: sn.total, Usage: w.jobUsage(model, known, sn)}
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

// generate loads the sources and calls the Generator once per mark value (ascending) and
// chunk, sequentially (concurrency arrives with the real generators, B-2.5). It returns
// every kept question with SourceDocID and SourceDocName already mapped. Usage of every
// call, even a failed one, is added to st.
func (w *Worker) generate(ctx context.Context, job store.Job, st *runState) ([]store.Question, error) {
	sources, byLabel, err := w.loadSources(ctx, job, st)
	if err != nil {
		return nil, err
	}
	g, err := w.Gens.For(job.Model)
	if err != nil {
		return nil, fmt.Errorf("generator for %q: %w", job.Model, err)
	}
	chunk := w.Cfg.MaxPerCall
	if chunk <= 0 {
		chunk = defaultMaxPerCall
	}

	var marks []int
	for k, n := range job.Request.Counts {
		if m, err := strconv.Atoi(k); err == nil && n > 0 {
			marks = append(marks, m)
		}
	}
	sort.Ints(marks)

	var kept []store.Question
	var avoid []string
	dropped := 0
	for _, m := range marks {
		for left := job.Request.Counts[strconv.Itoa(m)]; left > 0; {
			n := min(left, chunk)
			left -= n
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if cur, err := w.Jobs.GetJob(ctx, job.ID); err == nil && cur.CancelRequested {
				return nil, errCancelRequested
			}
			out, err := g.Generate(ctx, GenInput{
				Sources: sources, Type: job.Request.Type, Marks: m, Count: n,
				Avoid: slices.Clone(avoid), Model: job.Model,
			})
			st.addCall(out.Usage)
			if err != nil {
				return nil, err
			}
			for _, label := range out.UnreadableSources {
				if d, ok := byLabel[label]; ok {
					st.addFailed(store.FailedSource{DocID: d.ID, Name: d.Name, Reason: modelSkippedReason})
				}
			}
			for _, q := range out.Questions {
				d, ok := byLabel[q.SourceDocID]
				if !ok {
					dropped++ // an unknown label is dropped (AI-spec 7)
					continue
				}
				q.SourceDocID, q.SourceDocName = d.ID, d.Name
				kept = append(kept, q)
				avoid = append(avoid, q.Text)
			}
			st.setDone(len(kept), dropped)
		}
	}
	if len(kept) == 0 {
		return nil, errors.New("generator returned no usable questions")
	}
	return kept, nil
}

// loadSources reads the job's documents from the blob store, owner-checked, in a stable
// order (sorted by document id) and labels them doc-1, doc-2, ... The bytes are sent as
// they are: preprocessing (AI-spec 5.1) arrives in B-2.5. Unreadable documents go to the
// failed sources; with none left the job fails.
func (w *Worker) loadSources(ctx context.Context, job store.Job, st *runState) ([]SourceFile, map[string]store.Document, error) {
	got, err := w.Docs.GetDocs(ctx, job.OwnerID, job.Request.SourceIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("load documents: %w", err)
	}
	have := map[string]store.Document{}
	for _, d := range got {
		have[d.ID] = d
	}
	ids := slices.Clone(job.Request.SourceIDs)
	sort.Strings(ids)
	var files []SourceFile
	byLabel := map[string]store.Document{}
	for _, id := range ids {
		d, ok := have[id]
		if !ok || d.OwnerID != job.OwnerID || d.Status != store.DocReady {
			st.addFailed(store.FailedSource{DocID: id, Name: d.Name, Reason: missingFileReason})
			continue
		}
		data, err := w.Blob.Get(ctx, d.S3Key, docs.MaxUploadBytes)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			slog.Warn("worker: document unreadable", "job_id", job.ID, "doc_id", id, "err", err)
			st.addFailed(store.FailedSource{DocID: id, Name: d.Name, Reason: unreadableFileReason})
			continue
		}
		label := "doc-" + strconv.Itoa(len(files)+1)
		files = append(files, SourceFile{Label: label, ContentType: d.ContentType, Data: data})
		byLabel[label] = d
	}
	if len(files) == 0 {
		return nil, nil, &UserError{Message: NoSourcesMessage}
	}
	return files, byLabel, nil
}

// arrange sorts the questions ascending by marks (or shuffles them when the request asks
// for it) and gives each a new id.
func (w *Worker) arrange(job store.Job, qs []store.Question) []store.Question {
	out := slices.Clone(qs)
	if job.Request.Shuffle {
		sh := w.shuffle
		if sh == nil {
			sh = func(n int, swap func(i, j int)) { rand.Shuffle(n, swap) }
		}
		sh(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	} else {
		sort.SliceStable(out, func(i, j int) bool { return out[i].Marks < out[j].Marks })
	}
	for i := range out {
		out[i].ID = ulid.Make().String()
	}
	return out
}
