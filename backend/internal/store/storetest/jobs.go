package storetest

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// JobFactory returns a fresh, empty JobStore whose clock reads T0.
type JobFactory func(t *testing.T) store.JobStore

// T0 is the fixed "now" the job factories use.
var T0 = time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)

const (
	day   = "20261016"
	month = "202610"
)

func job(id, owner string) store.Job {
	return store.Job{ID: id, OwnerID: owner, Kind: store.KindGenerate, Model: "claude-haiku-4-5", Day: day,
		Status: store.JobQueued, Request: store.GenerateRequest{SourceIDs: []string{"d1"}, Type: "mcq", Counts: map[string]int{"1": 4}},
		Total: 4, CreatedAt: T0, UpdatedAt: T0, TTL: T0.Add(24 * time.Hour).Unix()}
}

func quota(user string) store.QuotaCheck {
	return store.QuotaCheck{UserID: user, Day: day, Month: month, MaxUser: 3, MaxRegen: 10, MaxGlobal: 10, BudgetMicros: 15_000_000}
}

func wantQuota(t *testing.T, err error, kind string) {
	t.Helper()
	var qe *store.QuotaError
	if !errors.As(err, &qe) || qe.Kind != kind {
		t.Fatalf("error = %v, want QuotaError{%s}", err, kind)
	}
}

// RunJobStoreSuite runs every JobStore contract test.
func RunJobStoreSuite(t *testing.T, f JobFactory) {
	t.Run("CreateAndGet", func(t *testing.T) { CreateAndGet(t, f) })
	t.Run("UserDailyGenerateLimit3", func(t *testing.T) { UserDailyGenerateLimit3(t, f) })
	t.Run("RegenCounterSeparate10", func(t *testing.T) { RegenCounterSeparate10(t, f) })
	t.Run("GlobalLimit10", func(t *testing.T) { GlobalLimit10(t, f) })
	t.Run("BudgetRefusalWhenSpendAtOrAboveBudget", func(t *testing.T) { BudgetRefusalWhenSpendAtOrAboveBudget(t, f) })
	t.Run("RefusedJobLeavesNoCounterBehind", func(t *testing.T) { RefusedJobLeavesNoCounterBehind(t, f) })
	t.Run("QuotaIsPerUserAndPerDay", func(t *testing.T) { QuotaIsPerUserAndPerDay(t, f) })
	t.Run("ConcurrentCreatesRespectLimit", func(t *testing.T) { ConcurrentCreatesRespectLimit(t, f) })
	t.Run("ClaimHeartbeatFinish", func(t *testing.T) { ClaimHeartbeatFinish(t, f) })
	t.Run("FinishNeverOverwrites", func(t *testing.T) { FinishNeverOverwrites(t, f) })
	t.Run("CancelIsOwnerScopedAndIdempotent", func(t *testing.T) { CancelIsOwnerScopedAndIdempotent(t, f) })
	t.Run("MarkFailedIfQueuedOrRunning", func(t *testing.T) { MarkFailedIfQueuedOrRunning(t, f) })
	t.Run("Questions", func(t *testing.T) { Questions(t, f) })
	t.Run("ListOwnerJobs", func(t *testing.T) { ListOwnerJobs(t, f) })
	t.Run("Spend", func(t *testing.T) { Spend(t, f) })
}

func mustCreate(t *testing.T, s store.JobStore, j store.Job, q store.QuotaCheck) {
	t.Helper()
	noErr(t, s.CreateJobWithQuota(ctx, j, q))
}

func CreateAndGet(t *testing.T, f JobFactory) {
	s := f(t)
	j := job("j1", "u1")
	mustCreate(t, s, j, quota("u1"))
	got, err := s.GetJob(ctx, "j1")
	noErr(t, err)
	if got.OwnerID != "u1" || got.Status != store.JobQueued || got.Model != "claude-haiku-4-5" || got.Day != day ||
		got.Total != 4 || got.Request.Counts["1"] != 4 || got.Request.Type != "mcq" || !got.CreatedAt.Equal(T0) || got.TTL != j.TTL {
		t.Fatalf("got %+v", got)
	}
	_, err = s.GetJob(ctx, "nope")
	is(t, err, store.ErrNotFound)
	_, err = s.GetJob(ctx, "j1#questions")
	is(t, err, store.ErrNotFound)
	is(t, s.CreateJobWithQuota(ctx, j, quota("u1")), store.ErrConflict)
}

func UserDailyGenerateLimit3(t *testing.T, f JobFactory) {
	s := f(t)
	for i := 0; i < 3; i++ {
		mustCreate(t, s, job(fmt.Sprintf("j%d", i), "u1"), quota("u1"))
	}
	wantQuota(t, s.CreateJobWithQuota(ctx, job("j3", "u1"), quota("u1")), store.QuotaUser)
	if _, err := s.GetJob(ctx, "j3"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("refused job exists: %v", err)
	}
}

func RegenCounterSeparate10(t *testing.T, f JobFactory) {
	s := f(t)
	q := quota("u1")
	q.MaxGlobal = 100
	for i := 0; i < 3; i++ {
		mustCreate(t, s, job(fmt.Sprintf("g%d", i), "u1"), q)
	}
	q.Regen = true
	for i := 0; i < 10; i++ {
		r := job(fmt.Sprintf("r%d", i), "u1")
		r.Kind = store.KindRegenerate
		mustCreate(t, s, r, q)
	}
	wantQuota(t, s.CreateJobWithQuota(ctx, job("r10", "u1"), q), store.QuotaRegen)
	// the generate counter is still full and independent
	q.Regen = false
	wantQuota(t, s.CreateJobWithQuota(ctx, job("g3", "u1"), q), store.QuotaUser)
}

func GlobalLimit10(t *testing.T, f JobFactory) {
	s := f(t)
	for i := 0; i < 10; i++ {
		u := fmt.Sprintf("u%d", i)
		mustCreate(t, s, job("j"+u, u), quota(u))
	}
	wantQuota(t, s.CreateJobWithQuota(ctx, job("jx", "ux"), quota("ux")), store.QuotaGlobal)
}

func BudgetRefusalWhenSpendAtOrAboveBudget(t *testing.T, f JobFactory) {
	s := f(t)
	q := quota("u1")
	total, err := s.AddSpend(ctx, month, q.BudgetMicros-1)
	noErr(t, err)
	if total != q.BudgetMicros-1 {
		t.Fatalf("total = %d", total)
	}
	mustCreate(t, s, job("j1", "u1"), q) // budget-1 allows
	_, err = s.AddSpend(ctx, month, 1)
	noErr(t, err)
	wantQuota(t, s.CreateJobWithQuota(ctx, job("j2", "u1"), q), store.QuotaBudget) // spend == budget refuses
	_, err = s.AddSpend(ctx, month, 500)
	noErr(t, err)
	wantQuota(t, s.CreateJobWithQuota(ctx, job("j2", "u1"), q), store.QuotaBudget) // above refuses
	// a new month has its own counter
	q.Month, q.Day = "202611", "20261101"
	j3 := job("j3", "u1")
	j3.Day = q.Day
	mustCreate(t, s, j3, q)
	// a zero budget refuses everything
	q.BudgetMicros = 0
	j4 := job("j4", "u1")
	j4.Day = q.Day
	wantQuota(t, s.CreateJobWithQuota(ctx, j4, q), store.QuotaBudget)
}

func RefusedJobLeavesNoCounterBehind(t *testing.T, f JobFactory) {
	s := f(t)
	q := quota("u1")
	_, err := s.AddSpend(ctx, month, q.BudgetMicros)
	noErr(t, err)
	for i := 0; i < 5; i++ { // five budget refusals must not consume the user or global counters
		wantQuota(t, s.CreateJobWithQuota(ctx, job(fmt.Sprintf("x%d", i), "u1"), q), store.QuotaBudget)
		if _, err := s.GetJob(ctx, fmt.Sprintf("x%d", i)); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("refused job exists: %v", err)
		}
	}
	q.BudgetMicros *= 2
	for i := 0; i < 3; i++ {
		mustCreate(t, s, job(fmt.Sprintf("j%d", i), "u1"), q)
	}
	wantQuota(t, s.CreateJobWithQuota(ctx, job("j3", "u1"), q), store.QuotaUser)
	// a user-limit refusal does not consume global slots either: 3 used, 7 left
	for i := 0; i < 20; i++ {
		_ = s.CreateJobWithQuota(ctx, job(fmt.Sprintf("y%d", i), "u1"), q)
	}
	for i := 0; i < 7; i++ {
		u := fmt.Sprintf("o%d", i)
		qo := quota(u)
		qo.BudgetMicros = q.BudgetMicros
		mustCreate(t, s, job("j"+u, u), qo)
	}
	qz := quota("oz")
	qz.BudgetMicros = q.BudgetMicros
	wantQuota(t, s.CreateJobWithQuota(ctx, job("jz", "oz"), qz), store.QuotaGlobal)
}

func QuotaIsPerUserAndPerDay(t *testing.T, f JobFactory) {
	s := f(t)
	for i := 0; i < 3; i++ {
		mustCreate(t, s, job(fmt.Sprintf("a%d", i), "u1"), quota("u1"))
	}
	mustCreate(t, s, job("b0", "u2"), quota("u2"))
	q := quota("u1")
	q.Day = "20261017"
	c0 := job("c0", "u1")
	c0.Day = q.Day
	mustCreate(t, s, c0, q)
}

func ConcurrentCreatesRespectLimit(t *testing.T, f JobFactory) {
	s := f(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := s.CreateJobWithQuota(ctx, job(fmt.Sprintf("j%d", i), "u1"), quota("u1"))
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				ok++
			} else {
				var qe *store.QuotaError
				if !errors.As(err, &qe) {
					t.Errorf("unexpected error: %v", err)
				}
			}
		}(i)
	}
	wg.Wait()
	if ok != 3 {
		t.Fatalf("%d jobs created, want 3", ok)
	}
}

func ClaimHeartbeatFinish(t *testing.T, f JobFactory) {
	s := f(t)
	mustCreate(t, s, job("j1", "u1"), quota("u1"))
	// heartbeat before the claim writes nothing
	is(t, s.Heartbeat(ctx, "j1", T0.Add(time.Second), store.Progress{Done: 1}), store.ErrPrecondition)
	is(t, s.Heartbeat(ctx, "nope", T0, store.Progress{}), store.ErrNotFound)
	got, _ := s.GetJob(ctx, "j1")
	if got.Done != 0 || !got.UpdatedAt.Equal(T0) {
		t.Fatalf("heartbeat wrote on a queued job: %+v", got)
	}
	ok, err := s.ClaimJob(ctx, "j1", T0.Add(2*time.Second))
	noErr(t, err)
	if !ok {
		t.Fatal("claim failed")
	}
	ok, err = s.ClaimJob(ctx, "j1", T0.Add(3*time.Second))
	noErr(t, err)
	if ok {
		t.Fatal("second claim must fail")
	}
	ok, err = s.ClaimJob(ctx, "nope", T0)
	noErr(t, err)
	if ok {
		t.Fatal("claim of a missing job must fail")
	}
	hb := T0.Add(20 * time.Second)
	noErr(t, s.Heartbeat(ctx, "j1", hb, store.Progress{Done: 2, Total: 4, Usage: store.JobUsage{Calls: 1, InputTokens: 100, OutputTokens: 50, CostMicros: 777}}))
	got, _ = s.GetJob(ctx, "j1")
	if got.Status != store.JobRunning || got.Done != 2 || got.Total != 4 || !got.UpdatedAt.Equal(hb) || got.Usage.Calls != 1 || got.Usage.CostMicros != 777 || got.Usage.InputTokens != 100 {
		t.Fatalf("got %+v", got)
	}
	fin := T0.Add(time.Minute)
	ok, err = s.FinishJob(ctx, "j1", store.JobDone, store.JobFinish{Now: fin, Done: 4, FailedSources: []store.FailedSource{{DocID: "d2", Name: "x.pdf", Reason: "unreadable"}}, Usage: store.JobUsage{Calls: 2, CostMicros: 900}})
	noErr(t, err)
	if !ok {
		t.Fatal("finish not applied")
	}
	got, _ = s.GetJob(ctx, "j1")
	if got.Status != store.JobDone || got.Done != 4 || !got.UpdatedAt.Equal(fin) || len(got.FailedSources) != 1 || got.FailedSources[0].Name != "x.pdf" || got.Usage.CostMicros != 900 {
		t.Fatalf("got %+v", got)
	}
	// no heartbeat after done
	is(t, s.Heartbeat(ctx, "j1", fin.Add(time.Second), store.Progress{Done: 1}), store.ErrPrecondition)
}

func FinishNeverOverwrites(t *testing.T, f JobFactory) {
	s := f(t)
	// queued cannot finish
	mustCreate(t, s, job("q", "u1"), quota("u1"))
	ok, err := s.FinishJob(ctx, "q", store.JobDone, store.JobFinish{Now: T0})
	noErr(t, err)
	if ok {
		t.Fatal("queued job finished")
	}
	// failed stays failed
	mustCreate(t, s, job("f", "u1"), quota("u1"))
	_, _ = s.ClaimJob(ctx, "f", T0)
	ok, _ = s.FinishJob(ctx, "f", store.JobFailed, store.JobFinish{Now: T0, Error: "That took too long. Try again."})
	if !ok {
		t.Fatal("running -> failed not applied")
	}
	ok, err = s.FinishJob(ctx, "f", store.JobDone, store.JobFinish{Now: T0})
	noErr(t, err)
	if ok {
		t.Fatal("failed job overwritten with done")
	}
	got, _ := s.GetJob(ctx, "f")
	if got.Status != store.JobFailed || got.Error != "That took too long. Try again." {
		t.Fatalf("got %+v", got)
	}
	// a cancel request blocks done but still allows cancelled and failed
	mustCreate(t, s, job("c", "u2"), quota("u2"))
	_, _ = s.ClaimJob(ctx, "c", T0)
	noErr(t, s.RequestCancel(ctx, "u2", "c"))
	ok, _ = s.FinishJob(ctx, "c", store.JobDone, store.JobFinish{Now: T0})
	if ok {
		t.Fatal("done applied although cancel was requested")
	}
	ok, _ = s.FinishJob(ctx, "c", store.JobCancelled, store.JobFinish{Now: T0})
	if !ok {
		t.Fatal("running -> cancelled not applied")
	}
	ok, _ = s.FinishJob(ctx, "c", store.JobFailed, store.JobFinish{Now: T0, Error: "x"})
	if ok {
		t.Fatal("cancelled job overwritten")
	}
	got, _ = s.GetJob(ctx, "c")
	if got.Status != store.JobCancelled {
		t.Fatalf("status = %s", got.Status)
	}
	if _, err := s.FinishJob(ctx, "c", "queued", store.JobFinish{}); err == nil {
		t.Fatal("invalid target status accepted")
	}
	ok, err = s.FinishJob(ctx, "missing", store.JobDone, store.JobFinish{Now: T0})
	noErr(t, err)
	if ok {
		t.Fatal("finish of a missing job applied")
	}
}

func CancelIsOwnerScopedAndIdempotent(t *testing.T, f JobFactory) {
	s := f(t)
	mustCreate(t, s, job("j1", "u1"), quota("u1"))
	is(t, s.RequestCancel(ctx, "intruder", "j1"), store.ErrNotFound)
	is(t, s.RequestCancel(ctx, "u1", "nope"), store.ErrNotFound)
	got, _ := s.GetJob(ctx, "j1")
	if got.CancelRequested {
		t.Fatal("foreign cancel took effect")
	}
	noErr(t, s.RequestCancel(ctx, "u1", "j1"))
	noErr(t, s.RequestCancel(ctx, "u1", "j1"))
	got, _ = s.GetJob(ctx, "j1")
	if !got.CancelRequested || got.Status != store.JobQueued || !got.UpdatedAt.Equal(T0) {
		t.Fatalf("got %+v", got) // cancel must not move UpdatedAt (staleness) or status
	}
}

func MarkFailedIfQueuedOrRunning(t *testing.T, f JobFactory) {
	s := f(t)
	mustCreate(t, s, job("q", "u1"), quota("u1"))
	noErr(t, s.MarkFailedIfQueuedOrRunning(ctx, "q", "We couldn't start that. Try again."))
	got, _ := s.GetJob(ctx, "q")
	if got.Status != store.JobFailed || got.Error != "We couldn't start that. Try again." {
		t.Fatalf("got %+v", got)
	}
	mustCreate(t, s, job("r", "u1"), quota("u1"))
	_, _ = s.ClaimJob(ctx, "r", T0)
	noErr(t, s.MarkFailedIfQueuedOrRunning(ctx, "r", "boom"))
	got, _ = s.GetJob(ctx, "r")
	if got.Status != store.JobFailed {
		t.Fatalf("status = %s", got.Status)
	}
	mustCreate(t, s, job("d", "u1"), quota("u1"))
	_, _ = s.ClaimJob(ctx, "d", T0)
	_, _ = s.FinishJob(ctx, "d", store.JobDone, store.JobFinish{Now: T0})
	noErr(t, s.MarkFailedIfQueuedOrRunning(ctx, "d", "late"))
	got, _ = s.GetJob(ctx, "d")
	if got.Status != store.JobDone || got.Error != "" {
		t.Fatalf("done job overwritten: %+v", got)
	}
	is(t, s.MarkFailedIfQueuedOrRunning(ctx, "missing", "x"), store.ErrNotFound)
}

func Questions(t *testing.T, f JobFactory) {
	s := f(t)
	_, err := s.GetQuestions(ctx, "j1")
	is(t, err, store.ErrNotFound)
	one := 1
	qs := []store.Question{
		{ID: "q1", Marks: 1, Text: "Why?", Options: []string{"a", "b", "c", "d"}, CorrectIndex: &one, SourceDocID: "d1", SourceDocName: "Bio.pdf"},
		{ID: "q2", Marks: 3, Text: "Explain.", ModelAnswer: "Because.", SourceDocID: "d1", SourceDocName: "Bio.pdf"},
	}
	noErr(t, s.PutQuestions(ctx, "j1", qs))
	got, err := s.GetQuestions(ctx, "j1")
	noErr(t, err)
	if len(got) != 2 || got[0].ID != "q1" || got[0].CorrectIndex == nil || *got[0].CorrectIndex != 1 || len(got[0].Options) != 4 ||
		got[1].CorrectIndex != nil || got[1].ModelAnswer != "Because." {
		t.Fatalf("got %+v", got)
	}
	// the questions item is not a job
	_, err = s.GetJob(ctx, "j1")
	is(t, err, store.ErrNotFound)
}

func ListOwnerJobs(t *testing.T, f JobFactory) {
	s := f(t)
	q := quota("u1")
	q.MaxUser, q.MaxGlobal = 100, 100
	for _, id := range []string{"01B", "01A", "01C"} {
		mustCreate(t, s, job(id, "u1"), q)
	}
	mustCreate(t, s, job("01Z", "u2"), quota("u2"))
	noErr(t, s.PutQuestions(ctx, "01A", nil))
	_, _ = s.ClaimJob(ctx, "01B", T0.Add(time.Second))
	got, err := s.ListOwnerJobs(ctx, "u1")
	noErr(t, err)
	if len(got) != 3 || got[0].ID != "01C" || got[1].ID != "01B" || got[2].ID != "01A" {
		t.Fatalf("got %+v", got)
	}
	if got[1].Status != store.JobRunning || got[2].Status != store.JobQueued || got[0].Kind != store.KindGenerate ||
		!got[2].CreatedAt.Equal(T0) || got[2].UpdatedAt.IsZero() {
		t.Fatalf("summary fields: %+v", got)
	}
	none, err := s.ListOwnerJobs(ctx, "nobody")
	noErr(t, err)
	if len(none) != 0 {
		t.Fatalf("got %+v", none)
	}
}

func Spend(t *testing.T, f JobFactory) {
	s := f(t)
	v, err := s.SpendThisMonth(ctx, month)
	noErr(t, err)
	if v != 0 {
		t.Fatalf("spend = %d", v)
	}
	a, err := s.AddSpend(ctx, month, 1500)
	noErr(t, err)
	b, err := s.AddSpend(ctx, month, 2500)
	noErr(t, err)
	if a != 1500 || b != 4000 {
		t.Fatalf("totals %d %d", a, b)
	}
	v, _ = s.SpendThisMonth(ctx, month)
	if v != 4000 {
		t.Fatalf("spend = %d", v)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = s.AddSpend(ctx, month, 10) }()
	}
	wg.Wait()
	v, _ = s.SpendThisMonth(ctx, month)
	if v != 4200 {
		t.Fatalf("concurrent spend = %d, want 4200", v)
	}
	if _, err := s.AddSpend(ctx, month, -1); err == nil {
		t.Fatal("negative spend accepted")
	}
	other, _ := s.SpendThisMonth(ctx, "202611")
	if other != 0 {
		t.Fatalf("other month = %d", other)
	}
}
