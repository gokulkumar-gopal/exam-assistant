package gen

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
	"github.com/gokulkumar/exam-assistant/backend/internal/store/memory"
)

const usageModel = "claude-haiku-4-5"

func newCounters(t *testing.T) (*CountersRecorder, *memory.JobStore, *clock.Fake) {
	t.Helper()
	clk := clock.NewFake(t0)
	js := memory.NewJobStore(clk)
	return &CountersRecorder{Jobs: js, Counters: js, Now: clk.Now}, js, clk
}

func snap(t *testing.T, js store.UsageStore, models ...string) store.UsageSnapshot {
	t.Helper()
	sn, err := js.GetUsage(context.Background(), MonthKey(t0), DayKey(t0), models)
	if err != nil {
		t.Fatal(err)
	}
	return sn
}

func TestEveryCallAddsTokensAndCostToMonthAndDayCounters(t *testing.T) {
	r, js, _ := newCounters(t)
	ctx := context.Background()
	calls := []struct {
		u    Usage
		cost int64
	}{
		{Usage{Input: 100, Output: 20, CacheRead: 5, CacheWrite: 7}, 900},
		{Usage{Input: 10, Output: 2}, 90},
		{Usage{Input: 4}, 0}, // a failed call that cost nothing still counts as a call and its tokens
	}
	var want store.UsageDelta
	for _, c := range calls {
		if err := r.RecordCall(ctx, "j1", usageModel, c.u, c.cost, 0, 0); err != nil {
			t.Fatal(err)
		}
		want = want.Add(store.UsageDelta{Calls: 1, InTok: c.u.Input, OutTok: c.u.Output, CacheReadTok: c.u.CacheRead,
			CacheWriteTok: c.u.CacheWrite, CostMicros: c.cost})
	}
	want.Jobs = 1
	sn := snap(t, js, usageModel, "gemini-3.1-flash-lite")
	if sn.Month[usageModel] != want || sn.Day[usageModel] != want {
		t.Fatalf("month %+v day %+v, want %+v", sn.Month[usageModel], sn.Day[usageModel], want)
	}
	if sn.Month["gemini-3.1-flash-lite"] != (store.UsageDelta{}) {
		t.Fatalf("another model was touched: %+v", sn.Month)
	}
	// The month spend guard still moves by the cost of every call.
	if spent, _ := js.SpendThisMonth(ctx, MonthKey(t0)); spent != 990 {
		t.Fatalf("spend = %d, want 990", spent)
	}
}

func TestCountersUseTheUTCMonthAndDayOfNow(t *testing.T) {
	r, js, clk := newCounters(t)
	clk.Set(time.Date(2026, 10, 31, 23, 59, 59, 0, time.UTC))
	if err := r.RecordCall(context.Background(), "j1", usageModel, Usage{Input: 1}, 5, 0, 0); err != nil {
		t.Fatal(err)
	}
	clk.Set(time.Date(2026, 11, 1, 0, 0, 1, 0, time.UTC))
	if err := r.RecordCall(context.Background(), "j1", usageModel, Usage{Input: 1}, 5, 0, 0); err != nil {
		t.Fatal(err)
	}
	oct, _ := js.GetUsage(context.Background(), "202610", "20261031", []string{usageModel})
	nov, _ := js.GetUsage(context.Background(), "202611", "20261101", []string{usageModel})
	if oct.Month[usageModel].Calls != 1 || oct.Day[usageModel].Calls != 1 || nov.Month[usageModel].Calls != 1 || nov.Day[usageModel].Calls != 1 {
		t.Fatalf("oct %+v nov %+v", oct, nov)
	}
	if oct.Month[usageModel].Jobs != 1 || nov.Month[usageModel].Jobs != 0 {
		t.Fatalf("the job is counted once, on its first call: oct %+v nov %+v", oct.Month[usageModel], nov.Month[usageModel])
	}
}

func TestJobsCounterIncrementsOncePerJob(t *testing.T) {
	r, js, _ := newCounters(t)
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		if err := r.RecordCall(ctx, "j1", usageModel, Usage{Input: 1}, 1, 1, 0); err != nil {
			t.Fatal(err)
		}
	}
	sn := snap(t, js, usageModel)
	if sn.Month[usageModel].Jobs != 1 || sn.Day[usageModel].Jobs != 1 || sn.Month[usageModel].Calls != 4 {
		t.Fatalf("a four-call job: %+v", sn.Month[usageModel])
	}
	for i := 0; i < 2; i++ {
		if err := r.RecordCall(ctx, "j2", usageModel, Usage{Input: 1}, 1, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	if sn := snap(t, js, usageModel); sn.Month[usageModel].Jobs != 2 || sn.Day[usageModel].Jobs != 2 {
		t.Fatalf("two jobs: %+v", sn.Month[usageModel])
	}
}

func TestJobsCounterIsCountedOnceWhenCallsRunConcurrently(t *testing.T) {
	r, js, _ := newCounters(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.RecordCall(context.Background(), "j1", usageModel, Usage{Input: 1}, 1, 0, 0); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if sn := snap(t, js, usageModel); sn.Month[usageModel].Jobs != 1 || sn.Month[usageModel].Calls != 8 {
		t.Fatalf("%+v", sn.Month[usageModel])
	}
}

// failingUsage refuses AddUsage the first n times.
type failingUsage struct {
	store.UsageStore
	n int
}

func (f *failingUsage) AddUsage(ctx context.Context, month, day, model string, d store.UsageDelta) error {
	if f.n > 0 {
		f.n--
		return errors.New("boom")
	}
	return f.UsageStore.AddUsage(ctx, month, day, model, d)
}

func TestFailedFirstAddDoesNotLoseTheJobCount(t *testing.T) {
	r, js, _ := newCounters(t)
	r.Counters = &failingUsage{UsageStore: js, n: 1}
	ctx := context.Background()
	if err := r.RecordCall(ctx, "j1", usageModel, Usage{Input: 1}, 1, 0, 0); err == nil {
		t.Fatal("the failed add must be reported")
	}
	if err := r.RecordCall(ctx, "j1", usageModel, Usage{Input: 1}, 1, 0, 0); err != nil {
		t.Fatal(err)
	}
	sn := snap(t, js, usageModel)
	if sn.Month[usageModel].Jobs != 1 || sn.Month[usageModel].Calls != 1 {
		t.Fatalf("the retry carries the job count: %+v", sn.Month[usageModel])
	}
	// The spend of the failed call was still added: the two counters are independent.
	if spent, _ := js.SpendThisMonth(ctx, MonthKey(t0)); spent != 2 {
		t.Fatalf("spend = %d, want 2", spent)
	}
}

func TestSpendIsAddedEvenWhenUsageCountersFailAndTheOtherWayAround(t *testing.T) {
	r, js, _ := newCounters(t)
	r.Counters = &failingUsage{UsageStore: js, n: 1}
	_ = r.RecordCall(context.Background(), "j1", usageModel, Usage{Input: 1}, 7, 0, 0)
	if spent, _ := js.SpendThisMonth(context.Background(), MonthKey(t0)); spent != 7 {
		t.Fatalf("spend = %d, want 7", spent)
	}
}

func TestNilCountersOnlyAddSpend(t *testing.T) {
	r, js, _ := newCounters(t)
	r.Counters = nil
	if err := r.RecordCall(context.Background(), "j1", usageModel, Usage{Input: 1}, 7, 0, 0); err != nil {
		t.Fatal(err)
	}
	if spent, _ := js.SpendThisMonth(context.Background(), MonthKey(t0)); spent != 7 {
		t.Fatalf("spend = %d", spent)
	}
	if sn := snap(t, js, usageModel); sn.Month[usageModel] != (store.UsageDelta{}) {
		t.Fatalf("counters = %+v", sn.Month[usageModel])
	}
}

func TestZeroCostWritesNoSpend(t *testing.T) {
	r, js, _ := newCounters(t)
	if err := r.RecordCall(context.Background(), "j1", usageModel, Usage{}, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if spent, _ := js.SpendThisMonth(context.Background(), MonthKey(t0)); spent != 0 {
		t.Fatalf("spend = %d", spent)
	}
}

// The default recorder of a Worker with Counters set writes the usage counters of the job's
// model for every call, and counts the job once.
func TestWorkerDefaultRecorderWritesUsageCounters(t *testing.T) {
	e := newWEnv(t, map[string]int{"1": 2, "2": 1})
	e.w.Counters = e.raw
	e.gen.fn = func(ctx context.Context, in GenInput) (GenOutput, error) {
		out := defaultOut(in)
		out.Usage = Usage{Input: 1_000, Output: 200}
		return out, nil
	}
	e.run(t)
	j := e.job(t)
	sn, err := e.raw.GetUsage(context.Background(), MonthKey(t0), DayKey(t0), []string{usageModel})
	if err != nil {
		t.Fatal(err)
	}
	got := sn.Month[usageModel]
	if got.Jobs != 1 || got.Calls != int64(j.Usage.Calls) || got.Calls < 2 || got.InTok != j.Usage.InputTokens ||
		got.OutTok != j.Usage.OutputTokens || got.CostMicros != j.Usage.CostMicros || sn.Day[usageModel] != got {
		t.Fatalf("counters %+v day %+v, job usage %+v", got, sn.Day[usageModel], j.Usage)
	}
}
