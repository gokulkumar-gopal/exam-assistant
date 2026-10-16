package gen

import (
	"context"
	"fmt"
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

// SpendRecorder is the UsageRecorder of the worker: it adds costMicros to the month's
// quota#global#<yyyymm> counter with JobStore.AddSpend (UTC month of Now). The job's own
// Usage record (tokens, cost, calls, kept, dropped) is not written here: the worker keeps the
// running totals and its single heartbeat writer stores them after every call and at the end,
// because the store has no atomic add for the job item and a second writer would race the
// heartbeat (B-2.4).
type SpendRecorder struct {
	Jobs store.JobStore
	Now  func() time.Time
}

// RecordCall adds the call's cost to the month's spend. A zero cost writes nothing.
func (r SpendRecorder) RecordCall(ctx context.Context, jobID, model string, _ Usage, costMicros int64, _, _ int) error {
	if costMicros <= 0 {
		return nil
	}
	month := MonthKey(r.Now())
	if _, err := r.Jobs.AddSpend(ctx, month, costMicros); err != nil {
		return fmt.Errorf("add spend %d to %s for job %s (%s): %w", costMicros, month, jobID, model, err)
	}
	return nil
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
