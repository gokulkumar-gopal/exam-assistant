package memory

import (
	"context"
	"slices"
	"strings"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// usageCounter is one usage item: its totals and the unix time it expires (set when the item
// is first created, like if_not_exists on the DynamoDB ttl attribute).
type usageCounter struct {
	d   store.UsageDelta
	ttl int64
}

var _ store.UsageStore = (*JobStore)(nil)

func usageKey(period, model string) string { return period + "#" + model }

func (s *JobStore) AddUsage(_ context.Context, month, day, model string, d store.UsageDelta) error {
	if err := store.ValidateUsageAdd(month, day, model, d); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clk.Now()
	bump := func(key string, life int64) {
		c, ok := s.usage[key]
		if !ok || now.Unix() >= c.ttl { // an expired item is gone: start over like a new item
			c = usageCounter{ttl: now.Unix() + life}
		}
		c.d = c.d.Add(d)
		s.usage[key] = c
	}
	bump(usageKey(month, model), int64(store.QuotaMonthTTL.Seconds()))
	bump(usageKey(day, model), int64(store.QuotaDayTTL.Seconds()))
	return nil
}

func (s *JobStore) GetUsage(_ context.Context, month, day string, models []string) (store.UsageSnapshot, error) {
	for _, m := range models {
		if err := store.ValidateUsageModel(m); err != nil {
			return store.UsageSnapshot{}, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clk.Now().Unix()
	read := func(period, model string) store.UsageDelta {
		if c, ok := s.usage[usageKey(period, model)]; ok && now < c.ttl {
			return c.d
		}
		return store.UsageDelta{}
	}
	sn := store.UsageSnapshot{Month: map[string]store.UsageDelta{}, Day: map[string]store.UsageDelta{}}
	for _, m := range models {
		sn.Month[m], sn.Day[m] = read(month, m), read(day, m)
	}
	return sn, nil
}

func (s *JobStore) RecentToday(_ context.Context, day string, limit int) ([]store.JobSummary, error) {
	if limit <= 0 || limit > store.MaxRecentToday {
		limit = store.MaxRecentToday
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []store.JobSummary
	for id := range s.jobs {
		j, ok := s.live(id)
		if !ok || j.Day != day {
			continue
		}
		u := j.Usage
		out = append(out, store.JobSummary{ID: j.ID, Status: j.Status, Kind: j.Kind, CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt,
			Model: j.Model, Usage: store.JobUsage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens,
				CacheReadTokens: u.CacheReadTokens, CacheWriteTokens: u.CacheWriteTokens, CostMicros: u.CostMicros, Calls: u.Calls}})
	}
	// createdAt descending like the day-index; ids (ULIDs) break ties.
	slices.SortFunc(out, func(a, b store.JobSummary) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(b.ID, a.ID)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
