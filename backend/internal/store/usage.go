package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// UsageDelta is what one LLM call adds to a usage counter, and what a counter holds.
// Jobs is 1 on the first call of a job and 0 after that.
type UsageDelta struct {
	Jobs, Calls, InTok, OutTok, CacheReadTok, CacheWriteTok, CostMicros int64
}

// UsageSnapshot is the counters of one month and one UTC day, keyed by model id. Every model
// asked for has an entry in both maps (zero when nothing was counted).
type UsageSnapshot struct {
	Month, Day map[string]UsageDelta
}

// MaxRecentToday is the most jobs RecentToday returns (backend-spec 8.6).
const MaxRecentToday = 50

// UsageStore keeps the per-model usage counters behind GET /ai/usage (backend-spec 6.2, 8.6;
// AI-spec 11). Counters are items of the jobs table: usage#<yyyymm>#<modelId> (40 days) and
// usage#<yyyymmdd>#<modelId> (3 days). Month and day are UTC.
type UsageStore interface {
	// AddUsage atomically adds d to the month counter and the day counter of model, both or
	// neither. The counters are created on first use with their TTL; later adds never move it.
	// month is yyyymm, day is yyyymmdd and must fall in month. Every field of d is >= 0.
	AddUsage(ctx context.Context, month, day, model string, d UsageDelta) error
	// GetUsage reads the month and day counters of each model (duplicates ignored). Missing or
	// expired counters read as zero.
	GetUsage(ctx context.Context, month, day string, models []string) (UsageSnapshot, error)
	// RecentToday returns the jobs created on day (yyyymmdd, UTC), newest first, at most limit
	// (limit <= 0 or above MaxRecentToday means MaxRecentToday). The summaries carry the model
	// and the usage totals (tokens, cost, calls) and UpdatedAt, never the owner, the sources
	// or the request. The read is eventually consistent.
	RecentToday(ctx context.Context, day string, limit int) ([]JobSummary, error)
}

// ValidateUsageAdd reports a malformed AddUsage call (a programming error).
func ValidateUsageAdd(month, day, model string, d UsageDelta) error {
	if len(month) != 6 || len(day) != 8 || day[:6] != month {
		return errors.New("store: usage needs a yyyymm month and a yyyymmdd day inside it")
	}
	if err := ValidateUsageModel(model); err != nil {
		return err
	}
	for _, v := range []int64{d.Jobs, d.Calls, d.InTok, d.OutTok, d.CacheReadTok, d.CacheWriteTok, d.CostMicros} {
		if v < 0 {
			return fmt.Errorf("store: usage delta must not be negative: %+v", d)
		}
	}
	return nil
}

// ValidateUsageModel reports a model id that cannot be part of a counter id.
func ValidateUsageModel(model string) error {
	if model == "" || strings.Contains(model, "#") {
		return fmt.Errorf("store: invalid usage model id %q", model)
	}
	return nil
}

// Add returns d plus o.
func (d UsageDelta) Add(o UsageDelta) UsageDelta {
	return UsageDelta{d.Jobs + o.Jobs, d.Calls + o.Calls, d.InTok + o.InTok, d.OutTok + o.OutTok,
		d.CacheReadTok + o.CacheReadTok, d.CacheWriteTok + o.CacheWriteTok, d.CostMicros + o.CostMicros}
}
