package gen

import (
	"testing"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

func TestQuotaKeysAreUTC(t *testing.T) {
	loc := time.FixedZone("x", -8*3600)
	now := time.Date(2026, 10, 31, 20, 0, 0, 0, loc) // 04:00 UTC on 1 November
	if DayKey(now) != "20261101" || MonthKey(now) != "202611" {
		t.Fatalf("keys = %s %s", DayKey(now), MonthKey(now))
	}
}

func TestQuotaLimitsCheck(t *testing.T) {
	l := QuotaLimits{MaxUser: 3, MaxRegen: 10, MaxGlobal: 10, BudgetUSD: 15}
	now := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	q := l.Check("u1", now, true)
	want := store.QuotaCheck{UserID: "u1", Day: "20261016", Month: "202610", Regen: true, MaxUser: 3, MaxRegen: 10, MaxGlobal: 10, BudgetMicros: 15_000_000}
	if q != want {
		t.Fatalf("got %+v want %+v", q, want)
	}
	if got := (QuotaLimits{BudgetUSD: 0.1 + 0.2}).Check("u", now, false).BudgetMicros; got != 300_000 {
		t.Fatalf("budget micros = %d", got)
	}
}

func TestQuotaMessages(t *testing.T) {
	if BudgetMessage != "The monthly AI budget has been used. Try again next month." || QuotaMessage(store.QuotaBudget) != BudgetMessage {
		t.Fatal("budget message")
	}
	seen := map[string]bool{}
	for _, k := range []string{store.QuotaUser, store.QuotaRegen, store.QuotaGlobal, store.QuotaBudget} {
		m := QuotaMessage(k)
		if m == "" || seen[m] {
			t.Fatalf("kind %s: message %q empty or duplicate", k, m)
		}
		seen[m] = true
	}
}
