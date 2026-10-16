package gen

import (
	"context"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// TokensView is the token counts of the usage page.
type TokensView struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
}

// MonthUsage is the "month" block of GET /ai/usage.
type MonthUsage struct {
	Key       string     `json:"key"`
	SpendUSD  float64    `json:"spendUsd"`
	BudgetUSD float64    `json:"budgetUsd"`
	Jobs      int64      `json:"jobs"`
	Calls     int64      `json:"calls"`
	Tokens    TokensView `json:"tokens"`
}

// TodayUsage is the "today" block.
type TodayUsage struct {
	Jobs     int64      `json:"jobs"`
	Calls    int64      `json:"calls"`
	SpendUSD float64    `json:"spendUsd"`
	Tokens   TokensView `json:"tokens"`
}

// ModelUsage is one "byModel" row (this month).
type ModelUsage struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Jobs     int64      `json:"jobs"`
	Calls    int64      `json:"calls"`
	SpendUSD float64    `json:"spendUsd"`
	Tokens   TokensView `json:"tokens"`
}

// RecentJob is one "recent" row. It carries no owner, source, document or question data.
type RecentJob struct {
	CreatedAt string     `json:"createdAt"`
	Kind      string     `json:"kind"`
	Model     string     `json:"model"`
	Status    string     `json:"status"`
	Calls     int        `json:"calls"`
	Tokens    TokensView `json:"tokens"`
	CostUSD   float64    `json:"costUsd"`
}

// UsageView is the body of GET /ai/usage (backend-spec 8.6): aggregates only.
type UsageView struct {
	ServerTime string       `json:"serverTime"`
	Month      MonthUsage   `json:"month"`
	Today      TodayUsage   `json:"today"`
	ByModel    []ModelUsage `json:"byModel"`
	Running    int          `json:"running"`
	Recent     []RecentJob  `json:"recent"`
}

// UsageService builds the usage page from the usage counters, the month spend counter and the
// day-index. It never scans a table.
type UsageService struct {
	Usage     store.UsageStore
	Jobs      store.JobStore
	BudgetUSD float64
}

func usd(micros int64) float64 { return float64(micros) / 1e6 }

func tokensOf(d store.UsageDelta) TokensView {
	return TokensView{Input: d.InTok, Output: d.OutTok, CacheRead: d.CacheReadTok, CacheWrite: d.CacheWriteTok}
}

// View reads the counters of now's UTC month and day for every catalog model (cheapest first,
// so byModel is too, with zero rows for unused models), the month spend and today's jobs.
// running counts the queued or running jobs among them that are not stale (7.2).
func (s *UsageService) View(ctx context.Context, now time.Time) (UsageView, error) {
	now = now.UTC()
	cat := Catalog()
	ids := make([]string, len(cat))
	for i, m := range cat {
		ids[i] = m.ID
	}
	snap, err := s.Usage.GetUsage(ctx, MonthKey(now), DayKey(now), ids)
	if err != nil {
		return UsageView{}, apperr.Internal(err)
	}
	spend, err := s.Jobs.SpendThisMonth(ctx, MonthKey(now))
	if err != nil {
		return UsageView{}, apperr.Internal(err)
	}
	recent, err := s.Usage.RecentToday(ctx, DayKey(now), store.MaxRecentToday)
	if err != nil {
		return UsageView{}, apperr.Internal(err)
	}

	v := UsageView{
		ServerTime: now.Format(time.RFC3339),
		Month:      MonthUsage{Key: now.Format("2006-01"), SpendUSD: usd(spend), BudgetUSD: s.BudgetUSD},
		ByModel:    make([]ModelUsage, 0, len(cat)),
		Recent:     make([]RecentJob, 0, len(recent)),
	}
	var month, day store.UsageDelta
	for _, m := range cat {
		md := snap.Month[m.ID]
		month, day = month.Add(md), day.Add(snap.Day[m.ID])
		v.ByModel = append(v.ByModel, ModelUsage{ID: m.ID, Name: m.Name, Jobs: md.Jobs, Calls: md.Calls,
			SpendUSD: usd(md.CostMicros), Tokens: tokensOf(md)})
	}
	v.Month.Jobs, v.Month.Calls, v.Month.Tokens = month.Jobs, month.Calls, tokensOf(month)
	v.Today = TodayUsage{Jobs: day.Jobs, Calls: day.Calls, SpendUSD: usd(day.CostMicros), Tokens: tokensOf(day)}

	for _, j := range recent {
		status := j.Status
		if (status == store.JobQueued || status == store.JobRunning) && isStale(j.UpdatedAt, now) {
			status = store.JobFailed // the job read endpoint reports a stale job as failed (7.2)
		} else if status == store.JobQueued || status == store.JobRunning {
			v.Running++
		}
		v.Recent = append(v.Recent, RecentJob{
			CreatedAt: j.CreatedAt.UTC().Format(time.RFC3339), Kind: j.Kind, Model: j.Model, Status: status,
			Calls:   j.Usage.Calls,
			Tokens:  TokensView{Input: j.Usage.InputTokens, Output: j.Usage.OutputTokens, CacheRead: j.Usage.CacheReadTokens, CacheWrite: j.Usage.CacheWriteTokens},
			CostUSD: usd(j.Usage.CostMicros),
		})
	}
	return v, nil
}
