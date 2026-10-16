package gen

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
	"github.com/gokulkumar/exam-assistant/backend/internal/store/memory"
)

type usageEnv struct {
	svc *UsageService
	js  *memory.JobStore
	clk *clock.Fake
}

func newUsageEnv(t *testing.T) usageEnv {
	t.Helper()
	clk := clock.NewFake(t0) // 2026-10-16 23:30 UTC
	js := memory.NewJobStore(clk)
	return usageEnv{&UsageService{Usage: js, Jobs: js, BudgetUSD: 15}, js, clk}
}

func (e usageEnv) view(t *testing.T) UsageView {
	t.Helper()
	v, err := e.svc.View(context.Background(), e.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (e usageEnv) add(t *testing.T, month, day, model string, d store.UsageDelta) {
	t.Helper()
	if err := e.js.AddUsage(context.Background(), month, day, model, d); err != nil {
		t.Fatal(err)
	}
}

func (e usageEnv) seedJob(t *testing.T, id, owner, status, model string, created, updated time.Time, mut func(*store.Job)) {
	t.Helper()
	j := store.Job{ID: id, OwnerID: owner, Kind: store.KindGenerate, Model: model, Day: DayKey(created), Status: status,
		CreatedAt: created, UpdatedAt: updated, Request: store.GenerateRequest{SourceIDs: []string{"doc-1"}, Type: "mcq"}}
	if mut != nil {
		mut(&j)
	}
	q := store.QuotaCheck{UserID: owner, Day: j.Day, Month: j.Day[:6], MaxUser: 1000, MaxRegen: 1000, MaxGlobal: 1000, BudgetMicros: 1 << 60}
	if err := e.js.CreateJobWithQuota(context.Background(), j, q); err != nil {
		t.Fatal(err)
	}
}

func keysOf(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func TestUsageShapeMatchesSpec(t *testing.T) {
	e := newUsageEnv(t)
	e.seedJob(t, "j1", "u1", store.JobDone, usageModel, t0, t0, nil)
	b, err := json.Marshal(e.view(t))
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		t.Fatal(err)
	}
	eq := func(name string, raw json.RawMessage, want ...string) {
		t.Helper()
		sort.Strings(want)
		if got := keysOf(t, raw); !reflect.DeepEqual(got, want) {
			t.Errorf("%s keys = %v, want %v", name, got, want)
		}
	}
	eq("top", b, "serverTime", "month", "today", "byModel", "running", "recent")
	eq("month", top["month"], "key", "spendUsd", "budgetUsd", "jobs", "calls", "tokens")
	eq("today", top["today"], "jobs", "calls", "spendUsd", "tokens")
	var m map[string]json.RawMessage
	_ = json.Unmarshal(top["month"], &m)
	eq("tokens", m["tokens"], "input", "output", "cacheRead", "cacheWrite")
	var by, rec []json.RawMessage
	_ = json.Unmarshal(top["byModel"], &by)
	_ = json.Unmarshal(top["recent"], &rec)
	eq("byModel[0]", by[0], "id", "name", "jobs", "calls", "spendUsd", "tokens")
	if len(rec) != 1 {
		t.Fatalf("recent = %d", len(rec))
	}
	eq("recent[0]", rec[0], "createdAt", "kind", "model", "status", "calls", "tokens", "costUsd")
	if !strings.Contains(string(top["serverTime"]), "2026-10-16T23:30:00Z") {
		t.Errorf("serverTime = %s", top["serverTime"])
	}
}

func TestMonthSpendFromCountersAndBudgetFromConfig(t *testing.T) {
	e := newUsageEnv(t)
	ctx := context.Background()
	if _, err := e.js.AddSpend(ctx, MonthKey(t0), 3_420_000); err != nil {
		t.Fatal(err)
	}
	if _, err := e.js.AddSpend(ctx, "202609", 9_000_000); err != nil { // another month never leaks in
		t.Fatal(err)
	}
	e.svc.BudgetUSD = 15
	v := e.view(t)
	if v.Month.SpendUSD != 3.42 || v.Month.BudgetUSD != 15 || v.Month.Key != "2026-10" {
		t.Fatalf("month = %+v", v.Month)
	}
	e.svc.BudgetUSD = 12.5
	if got := e.view(t).Month.BudgetUSD; got != 12.5 {
		t.Fatalf("budget = %v, want the configured 12.5", got)
	}
}

func TestTodaySumsPerModelDayCounters(t *testing.T) {
	e := newUsageEnv(t)
	m, d := MonthKey(t0), DayKey(t0)
	e.add(t, m, d, "claude-haiku-4-5", store.UsageDelta{Jobs: 1, Calls: 4, InTok: 100, OutTok: 20, CacheReadTok: 5, CacheWriteTok: 7, CostMicros: 120_000})
	e.add(t, m, d, "claude-opus-5-5", store.UsageDelta{Jobs: 1, Calls: 5, InTok: 50, OutTok: 10, CostMicros: 190_000})
	e.add(t, m, "20261001", "claude-opus-5-5", store.UsageDelta{Jobs: 7, Calls: 70, CostMicros: 1_000_000}) // earlier in the month: not today
	v := e.view(t)
	want := TodayUsage{Jobs: 2, Calls: 9, SpendUSD: 0.31, Tokens: TokensView{Input: 150, Output: 30, CacheRead: 5, CacheWrite: 7}}
	if v.Today != want {
		t.Fatalf("today = %+v, want %+v", v.Today, want)
	}
	if v.Month.Jobs != 9 || v.Month.Calls != 79 || v.Month.Tokens.Input != 150 {
		t.Fatalf("month = %+v", v.Month)
	}
}

func TestByModelListsEveryCatalogModelCheapestFirst(t *testing.T) {
	e := newUsageEnv(t)
	e.add(t, MonthKey(t0), DayKey(t0), "claude-opus-5-5", store.UsageDelta{Jobs: 1, Calls: 2, CostMicros: 5_000_000})
	v := e.view(t)
	cat := Catalog()
	if len(v.ByModel) != len(cat) {
		t.Fatalf("%d rows, want %d", len(v.ByModel), len(cat))
	}
	// Cheapest first by price (input then output), not merely catalog order: the UI does not sort.
	for i, r := range v.ByModel {
		if r.ID != cat[i].ID || r.Name != cat[i].Name {
			t.Errorf("row %d = %s/%s, want %s/%s", i, r.ID, r.Name, cat[i].ID, cat[i].Name)
		}
		if i > 0 {
			a, b := cat[i-1], cat[i]
			if a.InPerMTok > b.InPerMTok || (a.InPerMTok == b.InPerMTok && a.OutPerMTok > b.OutPerMTok) {
				t.Errorf("catalog not cheapest first at %d", i)
			}
		}
	}
	last := v.ByModel[len(v.ByModel)-1]
	if last.ID != "claude-opus-5-5" || last.Jobs != 1 || last.Calls != 2 || last.SpendUSD != 5 {
		t.Errorf("opus row = %+v", last)
	}
	if v.ByModel[0].Jobs != 0 || v.ByModel[0].Calls != 0 {
		t.Errorf("unused model row not zero: %+v", v.ByModel[0])
	}
}

func TestRunningExcludesStaleAndFinished(t *testing.T) {
	e := newUsageEnv(t)
	now := t0
	e.seedJob(t, "a", "u", store.JobRunning, usageModel, now.Add(-time.Hour), now.Add(-time.Minute), nil)            // running
	e.seedJob(t, "b", "u", store.JobQueued, usageModel, now.Add(-time.Hour), now.Add(-StaleAfter), nil)              // exactly at the limit: not stale
	e.seedJob(t, "c", "u", store.JobRunning, usageModel, now.Add(-time.Hour), now.Add(-StaleAfter-time.Second), nil) // stale
	e.seedJob(t, "d", "u", store.JobQueued, usageModel, now.Add(-time.Hour), now.Add(-time.Hour), nil)               // stale
	for i, st := range []string{store.JobDone, store.JobFailed, store.JobCancelled} {
		e.seedJob(t, fmt.Sprintf("f%d", i), "u", st, usageModel, now.Add(-time.Hour), now, nil)
	}
	v := e.view(t)
	if v.Running != 2 {
		t.Fatalf("running = %d, want 2", v.Running)
	}
	if len(v.Recent) != 7 {
		t.Fatalf("recent = %d", len(v.Recent))
	}
}

func TestRecentNewestFirstMax50(t *testing.T) {
	e := newUsageEnv(t)
	start := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 60; i++ {
		e.seedJob(t, fmt.Sprintf("j%02d", i), "u", store.JobDone, usageModel, start.Add(time.Duration(i)*time.Minute), t0, nil)
	}
	v := e.view(t)
	if len(v.Recent) != 50 {
		t.Fatalf("recent = %d, want 50", len(v.Recent))
	}
	if want := start.Add(59 * time.Minute).Format(time.RFC3339); v.Recent[0].CreatedAt != want {
		t.Errorf("first = %s, want newest %s", v.Recent[0].CreatedAt, want)
	}
	for i := 1; i < len(v.Recent); i++ {
		if v.Recent[i-1].CreatedAt < v.Recent[i].CreatedAt {
			t.Fatalf("not newest first at %d", i)
		}
	}
}

func TestRecentCarriesTotalsAndMarksStaleFailed(t *testing.T) {
	e := newUsageEnv(t)
	e.seedJob(t, "a", "u", store.JobRunning, usageModel, t0.Add(-time.Hour), t0.Add(-time.Hour), func(j *store.Job) {
		j.Usage = store.JobUsage{Calls: 4, InputTokens: 10, OutputTokens: 2, CacheReadTokens: 3, CacheWriteTokens: 4, CostMicros: 120_000}
	})
	r := e.view(t).Recent[0]
	want := RecentJob{CreatedAt: t0.Add(-time.Hour).Format(time.RFC3339), Kind: "generate", Model: usageModel, Status: "failed",
		Calls: 4, Tokens: TokensView{10, 2, 3, 4}, CostUSD: 0.12}
	if r != want {
		t.Fatalf("recent = %+v, want %+v", r, want)
	}
}

func TestAggregateOnlyNoUserOrDocOrQuestionData(t *testing.T) {
	e := newUsageEnv(t)
	e.seedJob(t, "job-secret-id", "owner-secret-id", store.JobFailed, usageModel, t0, t0, func(j *store.Job) {
		j.Request = store.GenerateRequest{SourceIDs: []string{"doc-secret-id"}, Type: "mcq", Avoid: []string{"question-secret-text"}}
		j.FailedSources = []store.FailedSource{{DocID: "doc-secret-id", Name: "doc-secret-name.pdf", Reason: "reason-secret"}}
		j.Error = "error-secret-text"
	})
	e.add(t, MonthKey(t0), DayKey(t0), usageModel, store.UsageDelta{Jobs: 1, Calls: 1})
	b, err := json.Marshal(e.view(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"secret", "owner", "job-"} {
		if strings.Contains(string(b), s) {
			t.Errorf("response contains %q: %s", s, b)
		}
	}
}

func TestEmptyUsageAllZeros(t *testing.T) {
	e := newUsageEnv(t)
	b, _ := json.Marshal(e.view(t))
	var v UsageView
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	if v.Month.SpendUSD != 0 || v.Month.Jobs != 0 || v.Month.Calls != 0 || v.Month.Tokens != (TokensView{}) ||
		v.Today != (TodayUsage{}) || v.Running != 0 {
		t.Fatalf("not zero: %s", b)
	}
	if !strings.Contains(string(b), `"recent":[]`) || strings.Contains(string(b), `"byModel":null`) {
		t.Fatalf("recent must be [] not null: %s", b)
	}
	if len(v.ByModel) != len(Catalog()) {
		t.Fatalf("byModel = %d", len(v.ByModel))
	}
}

// countingStore records every call, so the test can prove the view is a few keyed reads and
// one day-index query.
type countingStore struct {
	store.UsageStore
	store.JobStore
	getUsage, recent, spend int
	models                  []string
	limit                   int
}

func (c *countingStore) GetUsage(ctx context.Context, m, d string, models []string) (store.UsageSnapshot, error) {
	c.getUsage++
	c.models = models
	return c.UsageStore.GetUsage(ctx, m, d, models)
}
func (c *countingStore) RecentToday(ctx context.Context, d string, limit int) ([]store.JobSummary, error) {
	c.recent++
	c.limit = limit
	return c.UsageStore.RecentToday(ctx, d, limit)
}
func (c *countingStore) SpendThisMonth(ctx context.Context, m string) (int64, error) {
	c.spend++
	return c.JobStore.SpendThisMonth(ctx, m)
}

func TestDoesNotScan(t *testing.T) {
	e := newUsageEnv(t)
	c := &countingStore{UsageStore: e.js, JobStore: e.js}
	svc := &UsageService{Usage: c, Jobs: c, BudgetUSD: 15}
	if _, err := svc.View(context.Background(), t0); err != nil {
		t.Fatal(err)
	}
	if c.getUsage != 1 || c.recent != 1 || c.spend != 1 || c.limit != store.MaxRecentToday || len(c.models) != len(Catalog()) {
		t.Fatalf("calls: %+v", c)
	}
}
