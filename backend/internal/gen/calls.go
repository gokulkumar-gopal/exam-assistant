package gen

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// This file is the per-job call engine: the planned calls, their retries, validation,
// budget checks and spend accounting.

// Call caps of a job (AI-spec 5.3, backend-spec 7.2 step 4): at most ten first-attempt calls
// (five mark values, each at most two chunks) and each may be retried once, so twenty in all.
// A planned call may need a max_tokens retry and a shortfall follow-up, so the cap is on the
// job, counted when a call starts.
const (
	maxFirstAttemptCalls = 10
	maxCallsPerJob       = 2 * maxFirstAttemptCalls
)

// jobRun is the state shared by the calls of one job.
type jobRun struct {
	w       *Worker
	log     *slog.Logger
	job     store.Job
	model   Model
	g       Generator
	sources []SourceFile
	byLabel map[string]store.Document
	newID   func() string
	st      *runState
	// inputTokens is what a failed call that reports no usage is charged: the provider's count
	// of the shared prefix, or an estimate from the bytes sent (AI-spec 10.4).
	inputTokens int64
	budget      int64
	callTimeout time.Duration
}

// countInput enforces the input-token cap with the generator's own count when it has one
// (AI-spec 10.2), and keeps the count as the charge of a failed call without usage.
func (r *jobRun) countInput(ctx context.Context, first CallSpec) error {
	tc, ok := r.g.(TokenCounter)
	if !ok {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, r.callTimeout)
	defer cancel()
	n, err := tc.CountTokens(cctx, r.input(first, first.Count, nil))
	if err != nil {
		if ctx.Err() == nil && errors.Is(cctx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("counting tokens exceeded its timeout: %w", ErrTimeout)
		}
		return r.terminal(ctx, err)
	}
	limit := r.w.Cfg.MaxInputTokens
	if limit <= 0 {
		limit = defaultMaxInputTokens
	}
	if n > limit {
		r.log.Warn("worker: input tokens exceed the cap", "tokens", n, "max", limit)
		return &UserError{Message: TooLargeMessage}
	}
	r.inputTokens = int64(n)
	return nil
}

func (r *jobRun) input(spec CallSpec, count int, avoid []string) GenInput {
	return GenInput{Sources: r.sources, Type: r.job.Request.Type, Marks: spec.Marks, Count: count, Avoid: avoid, Model: r.job.Model}
}

// runAll runs the first call alone and the others at most maxConcurrentCalls at a time. The
// first error stops the rest (their contexts are cancelled) and is the one returned.
func (r *jobRun) runAll(ctx context.Context, plan []CallSpec) error {
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := r.safeTask(rctx, 0, plan[0]); err != nil {
		return err
	}
	var (
		once     sync.Once
		firstErr error
		wg       sync.WaitGroup
	)
	sem := make(chan struct{}, maxConcurrentCalls)
launch:
	for i := 1; i < len(plan); i++ {
		select {
		case sem <- struct{}{}:
		case <-rctx.Done():
			break launch
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if err := r.safeTask(rctx, i, plan[i]); err != nil {
				once.Do(func() { firstErr = err; cancel() })
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}

// safeTask is runTask with a panic turned into errGeneratorPanic: a panic in one of the call
// goroutines would otherwise take the whole process down.
func (r *jobRun) safeTask(ctx context.Context, idx int, spec CallSpec) (err error) {
	defer func() {
		if p := recover(); p != nil {
			r.log.Error("worker: generator panicked", "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
			err = errGeneratorPanic
		}
	}()
	return r.runTask(ctx, idx, spec)
}

type callKind int

const (
	callOK callKind = iota
	callInvalid
	callMaxTokens
)

// runTask makes the calls of one planned call: the call itself, one retry with half the count
// after max_tokens, and one follow-up for what is still missing (AI-spec 7.4, 8). At most three
// Generate calls, one of each kind.
func (r *jobRun) runTask(ctx context.Context, idx int, spec CallSpec) error {
	var kept []store.Question
	var droppedTexts []string
	need, ask := spec.Count, spec.Count
	retriedMax, followedUp, dropped := false, false, 0
	for attempt := 1; ; attempt++ {
		if err := r.beforeCall(ctx); err != nil {
			return err
		}
		avoid := r.avoidFor(idx, kept, droppedTexts)
		res := r.invoke(ctx, r.input(spec, ask, avoid))
		kind, termErr := r.classify(ctx, res)

		callKept, bad := 0, 0
		if termErr == nil && kind != callMaxTokens {
			for _, label := range res.out.UnreadableSources {
				if d, ok := r.byLabel[label]; ok {
					r.st.addFailed(store.FailedSource{DocID: d.ID, Name: d.Name, Reason: modelSkippedReason})
				}
			}
			add, n := r.commit(idx, spec, res.out.Questions, need, &kept, &droppedTexts, &dropped)
			callKept, bad = len(add), n
		}
		r.account(idx, spec.Marks, attempt, ask, res, kind, termErr, callKept, bad)
		if termErr != nil {
			return termErr
		}
		if kind == callMaxTokens {
			if retriedMax {
				return fmt.Errorf("model output was cut off again after a retry with a smaller count")
			}
			retriedMax = true
			ask = (ask + 1) / 2
			continue
		}
		need = spec.Count - len(kept)
		if need <= 0 {
			return nil
		}
		if followedUp {
			r.log.Warn("worker: still short after the follow-up call",
				"call", idx+1, "marks", spec.Marks, "requested", spec.Count, "kept", len(kept))
			return &UserError{Message: ShortfallMessage}
		}
		followedUp = true
		ask = need
	}
}

// commit validates a call's output against every other call's kept questions and publishes
// the result, in one step under the lock, so calls that finish together cannot both keep the
// same question. The unlock is deferred: a panic in validation (it runs on untrusted model
// output) is recovered by safeTask and must not leave the lock held.
func (r *jobRun) commit(idx int, spec CallSpec, raw []RawQuestion, need int, kept *[]store.Question, droppedTexts *[]string, dropped *int) (added []store.Question, bad int) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	all := r.othersLocked(idx)
	all = append(all, textsOf(*kept)...)
	all = append(all, *droppedTexts...)
	add, n, dt := validate(raw, CallSpec{Type: spec.Type, Marks: spec.Marks}, r.byLabel, all, r.newID)
	if extra := len(add) - need; extra > 0 {
		// More valid questions than asked: keep the first need (AI-spec 7.2 wants exactly the count).
		n += extra
		add = add[:need]
	}
	*kept = append(*kept, add...)
	*dropped += n
	*droppedTexts = append(*droppedTexts, dt...)
	r.st.results[idx], r.st.dropped[idx] = slices.Clone(*kept), *dropped
	return add, n
}

func textsOf(qs []store.Question) []string {
	out := make([]string, len(qs))
	for i, q := range qs {
		out[i] = q.Text
	}
	return out
}

// othersLocked returns the texts kept by every other planned call. st.mu must be held.
func (r *jobRun) othersLocked(idx int) []string {
	var out []string
	for i, qs := range r.st.results {
		if i != idx {
			out = append(out, textsOf(qs)...)
		}
	}
	return out
}

// others is othersLocked under the state lock (deferred unlock).
func (r *jobRun) others(idx int) []string {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	return r.othersLocked(idx)
}

// avoidFor is the Avoid list of a call: the questions the other calls have kept so far, this
// call's own kept questions and the texts it had dropped (AI-spec 7.4). Calls running at the
// same time cannot see each other's questions yet; validation catches those duplicates.
func (r *jobRun) avoidFor(idx int, kept []store.Question, droppedTexts []string) []string {
	out := r.others(idx)
	out = append(out, textsOf(kept)...)
	return append(out, droppedTexts...)
}

// beforeCall stops a job that was cancelled and a job whose month is used up (AI-spec 10.4,
// backend-spec 7.2 step 4). Reading the spend fails closed: without it the budget cannot be
// honoured.
func (r *jobRun) beforeCall(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if cur, err := r.w.Jobs.GetJob(ctx, r.job.ID); err == nil && cur.CancelRequested {
		return errCancelRequested
	}
	if r.budget <= 0 {
		return &UserError{Message: BudgetMessage}
	}
	spent, err := r.w.Jobs.SpendThisMonth(ctx, MonthKey(r.w.Clock.Now()))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("read monthly spend: %w", err)
	}
	if spent >= r.budget {
		r.log.Warn("worker: monthly budget used up; stopping the job", "spent_micros", spent, "budget_micros", r.budget)
		return &UserError{Message: BudgetMessage}
	}
	return r.reserveCall()
}

// reserveCall counts a provider call that is about to be made and refuses the one past the
// per-job cap. Counting happens under the state lock, so concurrent calls cannot overshoot.
func (r *jobRun) reserveCall() error {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	if r.st.invoked >= maxCallsPerJob {
		r.log.Warn("worker: call budget of the job exhausted", "calls", r.st.invoked, "max", maxCallsPerJob)
		return &UserError{Message: GenerateFailedMessage}
	}
	r.st.invoked++
	return nil
}

type callResult struct {
	out      GenOutput
	err      error
	latency  time.Duration
	timedOut bool // the call's own deadline passed while the job's context was still live
}

// invoke makes one Generate call under the per-call timeout.
func (r *jobRun) invoke(ctx context.Context, in GenInput) callResult {
	cctx, cancel := context.WithTimeout(ctx, r.callTimeout)
	defer cancel()
	start := time.Now()
	out, err := r.g.Generate(cctx, in)
	return callResult{
		out: out, err: err, latency: time.Since(start),
		timedOut: err != nil && ctx.Err() == nil && errors.Is(cctx.Err(), context.DeadlineExceeded),
	}
}

// classify applies the generator error contract to a call: callMaxTokens for a cut-off
// output, callInvalid for output that could not be read (its questions count as missing), and
// a terminal error, already carrying the user's message, for everything that ends the job.
func (r *jobRun) classify(ctx context.Context, res callResult) (callKind, error) {
	switch {
	case res.out.StopReason == StopRefusal, errors.Is(res.err, ErrRefusal):
		// The refusal category is the adapter's to log: GenOutput carries only the stop reason.
		r.log.Warn("worker: model refused the request", "stop_reason", res.out.StopReason, "request_id", res.out.RequestID)
		return callOK, &UserError{Message: RefusalMessage, Err: ErrRefusal}
	case res.out.StopReason == StopMaxTokens, errors.Is(res.err, ErrMaxTokens):
		return callMaxTokens, nil
	case errors.Is(res.err, ErrInvalidOutput):
		return callInvalid, nil
	case res.err != nil:
		if res.timedOut {
			return callOK, r.terminal(ctx, fmt.Errorf("call exceeded its timeout: %w", ErrTimeout))
		}
		return callOK, r.terminal(ctx, res.err)
	}
	return callOK, nil
}

// terminal maps a failed Generate or CountTokens call to the error that ends the job.
func (r *jobRun) terminal(ctx context.Context, err error) error {
	switch {
	case ctx.Err() != nil:
		return err // cancel, deadline or "not running": Run decides from the context's cause
	case errors.Is(err, ErrTimeout):
		r.log.Warn("worker: provider call timed out", "err", err)
		return &UserError{Message: TooLongMessage, Err: err}
	case errors.Is(err, ErrRateLimited):
		r.log.Warn("worker: provider is busy", "err", err)
		return &UserError{Message: BusyMessage, Err: err}
	case errors.Is(err, ErrAuth):
		// A deployment error, not a user error: loud in the logs, generic for the user.
		r.log.Error("worker: the AI provider rejected our credentials; check the API key and its permissions", "err", err)
		return &UserError{Message: GenerateFailedMessage, Err: err}
	}
	return err
}

// account charges and logs one finished call, before the next call starts. A failed call that
// reports no usage is charged its counted input (AI-spec 10.4), except when the provider
// refused it before processing (bad key, rate limit), which costs nothing.
func (r *jobRun) account(idx, marks, attempt, asked int, res callResult, kind callKind, termErr error, kept, dropped int) {
	u := res.out.Usage
	failed := termErr != nil || kind != callOK
	if failed && u == (Usage{}) && !errors.Is(res.err, ErrAuth) && !errors.Is(res.err, ErrRateLimited) {
		u = Usage{Input: r.inputTokens}
	}
	cost := CostMicros(r.model, u) // generate refuses a model with no price before any call
	r.st.addCall(u, cost)

	rctx, cancel := context.WithTimeout(context.Background(), finishTimeout)
	defer cancel()
	if err := r.w.recorder().RecordCall(rctx, r.job.ID, r.job.Model, u, cost, kept, dropped); err != nil {
		// The tokens are paid for whatever happens here: log, do not fail the job.
		r.log.Error("worker: recording spend failed", "err", err, "micros", cost)
	}
	r.st.notify()

	// AI-spec 9.4: counts and ids only, never file contents, prompts, output or question text.
	r.log.Info("worker: call finished",
		"call", idx+1, "attempt", attempt, "marks", marks, "model", r.job.Model,
		"prompt_version", r.w.Cfg.PromptVersion, "asked", asked,
		"input_tokens", u.Input, "output_tokens", u.Output, "cache_read_tokens", u.CacheRead, "cache_write_tokens", u.CacheWrite,
		"stop_reason", res.out.StopReason, "latency_ms", res.latency.Milliseconds(), "request_id", res.out.RequestID,
		"kept", kept, "dropped", dropped, "failed", failed)
}
