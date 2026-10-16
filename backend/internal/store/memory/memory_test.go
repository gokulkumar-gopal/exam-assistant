package memory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
	"github.com/gokulkumar/exam-assistant/backend/internal/store/memory"
	"github.com/gokulkumar/exam-assistant/backend/internal/store/storetest"
)

var (
	users = func(t *testing.T) store.UserStore { return memory.NewUserStore() }
	docs  = func(t *testing.T) store.DocStore {
		return memory.NewDocStore(clock.NewFake(time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)))
	}
)

func TestCreateUserEmailUnique(t *testing.T) { storetest.CreateUserEmailUnique(t, users) }
func TestGoogleUserLocks(t *testing.T)       { storetest.GoogleUserLocks(t, users) }
func TestLinkGoogleAtomic(t *testing.T)      { storetest.LinkGoogleAtomic(t, users) }
func TestListReadyNewestFirstExcludesPending(t *testing.T) {
	storetest.ListReadyNewestFirstExcludesPending(t, docs)
}
func TestDocsAreOwnerScoped(t *testing.T) { storetest.DocsAreOwnerScoped(t, docs) }

func TestUserStoreContract(t *testing.T) { storetest.RunUserStoreSuite(t, users) }
func TestDocStoreContract(t *testing.T)  { storetest.RunDocStoreSuite(t, docs) }

// TTL is memory-specific: DynamoDB expires items asynchronously, so the contract suite
// cannot assert it against a real table.
func TestPendingDocExpiresAtTTL(t *testing.T) {
	start := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(start)
	s := memory.NewDocStore(clk)
	ctx := context.Background()
	ttl := start.Add(24 * time.Hour).Unix()
	if err := s.PutDoc(ctx, store.Document{ID: "d", OwnerID: "o", Status: store.DocPending, TTL: ttl}); err != nil {
		t.Fatal(err)
	}
	clk.Advance(24*time.Hour - time.Second)
	if _, err := s.GetDoc(ctx, "o", "d"); err != nil {
		t.Fatalf("before TTL: %v", err)
	}
	clk.Advance(time.Second)
	if _, err := s.GetDoc(ctx, "o", "d"); err != store.ErrNotFound {
		t.Fatalf("at TTL: err = %v, want ErrNotFound", err)
	}
	if err := s.MarkReady(ctx, "o", "d", 1); err != store.ErrNotFound {
		t.Fatalf("MarkReady after expiry: %v", err)
	}
	// A document marked ready before expiry has no TTL and never expires.
	if err := s.PutDoc(ctx, store.Document{ID: "e", OwnerID: "o", Status: store.DocPending, TTL: clk.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkReady(ctx, "o", "e", 2); err != nil {
		t.Fatal(err)
	}
	clk.Advance(1000 * time.Hour)
	if _, err := s.GetDoc(ctx, "o", "e"); err != nil {
		t.Fatalf("ready doc expired: %v", err)
	}
}

func TestJobStoreContract(t *testing.T) {
	storetest.RunJobStoreSuite(t, func(t *testing.T) store.JobStore { return memory.NewJobStore(clock.NewFake(storetest.T0)) })
}

func TestCreateJobRejectsDayMismatch(t *testing.T) {
	s := memory.NewJobStore(clock.NewFake(storetest.T0))
	j := store.Job{ID: "j", OwnerID: "u", Day: "20261015"}
	q := store.QuotaCheck{UserID: "u", Day: "20261016", Month: "202610", MaxUser: 3, MaxRegen: 1, MaxGlobal: 1, BudgetMicros: 1}
	err := s.CreateJobWithQuota(context.Background(), j, q)
	var qe *store.QuotaError
	if err == nil || errors.As(err, &qe) || errors.Is(err, store.ErrConflict) {
		t.Fatalf("err = %v", err)
	}
}

func TestExamStoreContract(t *testing.T) {
	storetest.RunExamStoreSuite(t, func(t *testing.T) store.ExamStore { return memory.NewExamStore() })
}

func TestAttemptStoreContract(t *testing.T) {
	storetest.RunAttemptStoreSuite(t, func(t *testing.T) store.AttemptStore { return memory.NewAttemptStore() })
}

func TestUsageStoreContract(t *testing.T) {
	storetest.RunUsageStoreSuite(t, func(t *testing.T) storetest.UsageJobStore {
		return memory.NewJobStore(clock.NewFake(storetest.T0))
	})
}

// Like DynamoDB's ttl attribute: the day counter lasts 3 days from its first add, the month
// counter 40, and later adds do not extend them.
func TestUsageCountersExpire(t *testing.T) {
	clk := clock.NewFake(storetest.T0)
	s := memory.NewJobStore(clk)
	ctx := context.Background()
	d := store.UsageDelta{Calls: 1}
	if err := s.AddUsage(ctx, "202610", "20261016", "m", d); err != nil {
		t.Fatal(err)
	}
	clk.Advance(72*time.Hour - time.Second)
	if err := s.AddUsage(ctx, "202610", "20261016", "m", d); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Second)
	sn, _ := s.GetUsage(ctx, "202610", "20261016", []string{"m"})
	if sn.Day["m"].Calls != 0 || sn.Month["m"].Calls != 2 {
		t.Fatalf("at 3 days: day %+v month %+v", sn.Day["m"], sn.Month["m"])
	}
	clk.Advance(37*24*time.Hour + time.Second) // 40 days after the first add
	sn, _ = s.GetUsage(ctx, "202610", "20261016", []string{"m"})
	if sn.Month["m"].Calls != 0 {
		t.Fatalf("at 40 days: month %+v", sn.Month["m"])
	}
}
