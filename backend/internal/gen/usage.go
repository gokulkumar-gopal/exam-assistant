package gen

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// UsageRecorder is told about every Generate call, including retries and calls that failed,
// as soon as the call is over and before the next one starts (AI-spec 10.4, 11). costMicros
// is what the call is charged: CostMicros of u, or, for a failed call that reported no usage,
// of its counted input. kept and dropped are the questions of this call that passed and
// failed validation (both 0 for a call that produced none). An error is logged by the worker
// and never fails the job: the tokens are already paid for.
type UsageRecorder interface {
	RecordCall(ctx context.Context, jobID, model string, u Usage, costMicros int64, kept, dropped int) error
}

// CountersRecorder is the UsageRecorder of the worker. For every call it
//
//   - adds costMicros to the month's quota#global#<yyyymm> spend counter with
//     JobStore.AddSpend (UTC month of Now; a zero cost writes nothing), which the budget guard
//     reads, and
//   - adds the call's tokens, cost and one call to the month and day usage counters of the
//     job's model with UsageStore.AddUsage (backend-spec 6.2, AI-spec 11), plus one job on the
//     first call of each job only.
//
// The two writes are independent: both are attempted and a failure of either is returned
// (joined) without hiding the other. The job's own Usage record (tokens, cost, calls, kept,
// dropped) is not written here: the worker keeps the running totals and its single heartbeat
// writer stores them after every call and at the end, because the store has no atomic add for
// the job item and a second writer would race the heartbeat (B-2.4).
//
// A nil Counters skips the usage counters (the spend counter is always kept). The zero value
// is ready to use; use it through a pointer and share one per worker, because it remembers
// which jobs it has already counted.
type CountersRecorder struct {
	Jobs     store.JobStore
	Counters store.UsageStore
	Now      func() time.Time

	mu      sync.Mutex
	counted map[string]bool // job ids whose Jobs +1 was written, or is being written
}

var _ UsageRecorder = (*CountersRecorder)(nil)

// RecordCall adds the call to the spend and usage counters.
func (r *CountersRecorder) RecordCall(ctx context.Context, jobID, model string, u Usage, costMicros int64, _, _ int) error {
	now := r.Now()
	month, day := MonthKey(now), DayKey(now)
	var errs []error
	if costMicros > 0 {
		if _, err := r.Jobs.AddSpend(ctx, month, costMicros); err != nil {
			errs = append(errs, fmt.Errorf("add spend %d to %s for job %s (%s): %w", costMicros, month, jobID, model, err))
		}
	}
	if r.Counters != nil {
		d := store.UsageDelta{Calls: 1, InTok: u.Input, OutTok: u.Output, CacheReadTok: u.CacheRead,
			CacheWriteTok: u.CacheWrite, CostMicros: costMicros}
		first := r.claimFirst(jobID)
		if first {
			d.Jobs = 1
		}
		if err := r.Counters.AddUsage(ctx, month, day, model, d); err != nil {
			if first {
				r.release(jobID) // the next call of this job carries the +1
			}
			errs = append(errs, fmt.Errorf("add usage to %s/%s for job %s (%s): %w", month, day, jobID, model, err))
		}
	}
	return errors.Join(errs...)
}

// claimFirst reports whether this is the first call of the job to ask, and marks the job.
func (r *CountersRecorder) claimFirst(jobID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.counted[jobID] {
		return false
	}
	if r.counted == nil {
		r.counted = map[string]bool{}
	}
	r.counted[jobID] = true
	return true
}

func (r *CountersRecorder) release(jobID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.counted, jobID)
}

// approxBytesPerToken is the estimate used for the input of a failed call when the generator
// cannot count tokens. Four bytes a token is the usual figure for English text; PDFs and
// images are cruder, which the headroom below the provider's own limit absorbs (AI-spec 10.4).
const approxBytesPerToken = 4

// estimateInputTokens is the bytes sent divided by approxBytesPerToken, rounded up.
func estimateInputTokens(sources []SourceFile) int64 {
	var n int64
	for _, s := range sources {
		n += int64(len(s.Bytes))
	}
	return (n + approxBytesPerToken - 1) / approxBytesPerToken
}
