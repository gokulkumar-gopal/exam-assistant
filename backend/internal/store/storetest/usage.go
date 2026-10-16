package storetest

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// UsageJobStore is a store that holds jobs and usage counters (they share the jobs table).
type UsageJobStore interface {
	store.JobStore
	store.UsageStore
}

// UsageFactory returns a fresh, empty UsageJobStore whose clock reads T0.
type UsageFactory func(t *testing.T) UsageJobStore

// RunUsageStoreSuite runs every UsageStore contract test.
func RunUsageStoreSuite(t *testing.T, f UsageFactory) {
	t.Run("AddUsageAccumulatesPerModelMonthAndDay", func(t *testing.T) { AddUsageAccumulatesPerModelMonthAndDay(t, f) })
	t.Run("GetUsageReadsZeroForUnknownModels", func(t *testing.T) { GetUsageReadsZeroForUnknownModels(t, f) })
	t.Run("AddUsageRejectsMalformedInput", func(t *testing.T) { AddUsageRejectsMalformedInput(t, f) })
	t.Run("AddUsageDoesNotTouchSpend", func(t *testing.T) { AddUsageDoesNotTouchSpend(t, f) })
	t.Run("ConcurrentAddUsageLosesNothing", func(t *testing.T) { ConcurrentAddUsageLosesNothing(t, f) })
	t.Run("RecentTodayNewestFirstWithLimit", func(t *testing.T) { RecentTodayNewestFirstWithLimit(t, f) })
	t.Run("RecentTodayCarriesModelAndUsage", func(t *testing.T) { RecentTodayCarriesModelAndUsage(t, f) })
}

const (
	mA = "claude-haiku-4-5"
	mB = "gemini-3.1-flash-lite"
)

func get(t *testing.T, s store.UsageStore, mo, d string, models ...string) store.UsageSnapshot {
	t.Helper()
	sn, err := s.GetUsage(ctx, mo, d, models)
	noErr(t, err)
	return sn
}

func AddUsageAccumulatesPerModelMonthAndDay(t *testing.T, f UsageFactory) {
	s := f(t)
	d1 := store.UsageDelta{Jobs: 1, Calls: 1, InTok: 100, OutTok: 20, CacheReadTok: 5, CacheWriteTok: 7, CostMicros: 900}
	d2 := store.UsageDelta{Calls: 1, InTok: 10, OutTok: 2, CostMicros: 90}
	noErr(t, s.AddUsage(ctx, "202610", "20261016", mA, d1))
	noErr(t, s.AddUsage(ctx, "202610", "20261016", mA, d2))
	noErr(t, s.AddUsage(ctx, "202610", "20261017", mA, d1)) // next day, same month
	noErr(t, s.AddUsage(ctx, "202610", "20261016", mB, d2))

	sn := get(t, s, "202610", "20261016", mA, mB)
	if got, want := sn.Month[mA], d1.Add(d2).Add(d1); got != want {
		t.Errorf("month %s = %+v, want %+v", mA, got, want)
	}
	if got, want := sn.Day[mA], d1.Add(d2); got != want {
		t.Errorf("day %s = %+v, want %+v", mA, got, want)
	}
	if sn.Month[mB] != d2 || sn.Day[mB] != d2 {
		t.Errorf("model B: month %+v day %+v, want %+v", sn.Month[mB], sn.Day[mB], d2)
	}
	if got := get(t, s, "202610", "20261017", mA).Day[mA]; got != d1 {
		t.Errorf("day 17 = %+v, want %+v", got, d1)
	}
	if got := get(t, s, "202611", "20261101", mA); got.Month[mA] != (store.UsageDelta{}) || got.Day[mA] != (store.UsageDelta{}) {
		t.Errorf("another month must be empty: %+v", got)
	}
}

func GetUsageReadsZeroForUnknownModels(t *testing.T, f UsageFactory) {
	s := f(t)
	sn := get(t, s, "202610", "20261016", mA, mB, mA)
	if len(sn.Month) != 2 || len(sn.Day) != 2 {
		t.Fatalf("want one zero entry per distinct model, got %+v", sn)
	}
	for _, m := range []string{mA, mB} {
		if _, ok := sn.Month[m]; !ok {
			t.Errorf("month has no entry for %s", m)
		}
		if _, ok := sn.Day[m]; !ok {
			t.Errorf("day has no entry for %s", m)
		}
	}
	if sn := get(t, s, "202610", "20261016"); len(sn.Month) != 0 || len(sn.Day) != 0 {
		t.Errorf("no models asked, got %+v", sn)
	}
	if _, err := s.GetUsage(ctx, "202610", "20261016", []string{"a#b"}); err == nil {
		t.Error("a model id with '#' must be refused")
	}
}

func AddUsageRejectsMalformedInput(t *testing.T, f UsageFactory) {
	s := f(t)
	ok := store.UsageDelta{Calls: 1}
	for name, err := range map[string]error{
		"short month":     s.AddUsage(ctx, "2026", "20261016", mA, ok),
		"short day":       s.AddUsage(ctx, "202610", "2026101", mA, ok),
		"day outside":     s.AddUsage(ctx, "202610", "20261101", mA, ok),
		"empty model":     s.AddUsage(ctx, "202610", "20261016", "", ok),
		"hash in model":   s.AddUsage(ctx, "202610", "20261016", "a#b", ok),
		"negative tokens": s.AddUsage(ctx, "202610", "20261016", mA, store.UsageDelta{InTok: -1}),
	} {
		if err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if sn := get(t, s, "202610", "20261016", mA); sn.Month[mA] != (store.UsageDelta{}) {
		t.Errorf("a refused add left a counter: %+v", sn)
	}
}

func AddUsageDoesNotTouchSpend(t *testing.T, f UsageFactory) {
	s := f(t)
	noErr(t, s.AddUsage(ctx, "202610", "20261016", mA, store.UsageDelta{Calls: 1, CostMicros: 500}))
	spent, err := s.SpendThisMonth(ctx, "202610")
	noErr(t, err)
	if spent != 0 {
		t.Fatalf("spend = %d; the usage counters are separate from spendMicros", spent)
	}
}

func ConcurrentAddUsageLosesNothing(t *testing.T, f UsageFactory) {
	s := f(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			noErr(t, s.AddUsage(ctx, "202610", "20261016", mA, store.UsageDelta{Calls: 1, InTok: 3, CostMicros: 7}))
		}()
	}
	wg.Wait()
	sn := get(t, s, "202610", "20261016", mA)
	want := store.UsageDelta{Calls: 20, InTok: 60, CostMicros: 140}
	if sn.Month[mA] != want || sn.Day[mA] != want {
		t.Fatalf("month %+v day %+v, want %+v", sn.Month[mA], sn.Day[mA], want)
	}
}

func jobAt(id, owner, dayKey string, at time.Time) store.Job {
	j := job(id, owner)
	j.Day, j.CreatedAt, j.UpdatedAt = dayKey, at, at
	return j
}

func RecentTodayNewestFirstWithLimit(t *testing.T, f UsageFactory) {
	s := f(t)
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("j%d", i)
		mustCreate(t, s, jobAt(id, fmt.Sprintf("u%d", i), day, T0.Add(time.Duration(i)*time.Minute)), quota(fmt.Sprintf("u%d", i)))
	}
	other := jobAt("jy", "u9", "20261015", T0.Add(-24*time.Hour))
	q := quota("u9")
	q.Day, q.Month = "20261015", "202610"
	mustCreate(t, s, other, q)
	noErr(t, s.AddUsage(ctx, month, day, mA, store.UsageDelta{Calls: 1})) // counters must never show up as jobs
	noErr(t, s.PutQuestions(ctx, "j1", nil))                              // nor may question items

	got, err := s.RecentToday(ctx, day, 3)
	noErr(t, err)
	if len(got) != 3 || got[0].ID != "j4" || got[1].ID != "j3" || got[2].ID != "j2" {
		t.Fatalf("limit 3 = %+v, want j4 j3 j2", got)
	}
	all, err := s.RecentToday(ctx, day, 0)
	noErr(t, err)
	if len(all) != 5 || all[4].ID != "j0" {
		t.Fatalf("limit 0 = %+v, want the 5 jobs of the day, newest first", all)
	}
	none, err := s.RecentToday(ctx, "20300101", 50)
	noErr(t, err)
	if len(none) != 0 {
		t.Fatalf("empty day = %+v", none)
	}
}

func RecentTodayCarriesModelAndUsage(t *testing.T, f UsageFactory) {
	s := f(t)
	mustCreate(t, s, jobAt("j1", "u1", day, T0), quota("u1"))
	ok, err := s.ClaimJob(ctx, "j1", T0.Add(time.Second))
	noErr(t, err)
	if !ok {
		t.Fatal("claim refused")
	}
	u := store.JobUsage{InputTokens: 11, OutputTokens: 22, CacheReadTokens: 3, CacheWriteTokens: 4, CostMicros: 555, Calls: 2}
	hb := T0.Add(5 * time.Second)
	noErr(t, s.Heartbeat(ctx, "j1", hb, store.Progress{Done: 1, Total: 4, Usage: u}))

	got, err := s.RecentToday(ctx, day, 50)
	noErr(t, err)
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	g := got[0]
	if g.ID != "j1" || g.Status != store.JobRunning || g.Kind != store.KindGenerate || g.Model != mA ||
		!g.CreatedAt.Equal(T0) || !g.UpdatedAt.Equal(hb) {
		t.Errorf("summary = %+v", g)
	}
	if g.Usage.InputTokens != 11 || g.Usage.OutputTokens != 22 || g.Usage.CacheReadTokens != 3 ||
		g.Usage.CacheWriteTokens != 4 || g.Usage.CostMicros != 555 || g.Usage.Calls != 2 {
		t.Errorf("usage = %+v", g.Usage)
	}
}
