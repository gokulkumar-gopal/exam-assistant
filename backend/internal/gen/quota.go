package gen

import (
	"math"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// BudgetMessage is shown when the monthly spend has reached the budget, both for a
// refused create and for a worker that stops mid-job (backend-spec 7.5, AI-spec 10.4).
const BudgetMessage = "The monthly AI budget has been used. Try again next month."

// QuotaMessage is the end-user text of a store.QuotaError kind.
func QuotaMessage(kind string) string {
	switch kind {
	case store.QuotaBudget:
		return BudgetMessage
	case store.QuotaUser:
		return "You've reached today's limit for generating questions. Try again tomorrow."
	case store.QuotaRegen:
		return "You've reached today's limit for regenerating questions. Try again tomorrow."
	default: // store.QuotaGlobal
		return "Question generation has reached its limit for today. Try again tomorrow."
	}
}

// BudgetMicros converts the budget in USD to whole micro-dollars.
func BudgetMicros(usd float64) int64 { return int64(math.Round(usd * 1e6)) }

// DayKey is the UTC day of t as yyyymmdd, the quota and day-index key.
func DayKey(t time.Time) string { return t.UTC().Format("20060102") }

// MonthKey is the UTC month of t as yyyymm, the spend counter key.
func MonthKey(t time.Time) string { return t.UTC().Format("200601") }

// QuotaLimits are the configured limits (GEN_MAX_PER_DAY, GEN_MAX_REGEN_PER_DAY,
// GEN_MAX_GLOBAL_PER_DAY, LLM_MONTHLY_BUDGET_USD).
type QuotaLimits struct {
	MaxUser, MaxRegen, MaxGlobal int
	BudgetUSD                    float64
}

// Check builds the store.QuotaCheck for a job created by userID at now.
func (l QuotaLimits) Check(userID string, now time.Time, regen bool) store.QuotaCheck {
	return store.QuotaCheck{
		UserID: userID, Day: DayKey(now), Month: MonthKey(now), Regen: regen,
		MaxUser: l.MaxUser, MaxRegen: l.MaxRegen, MaxGlobal: l.MaxGlobal,
		BudgetMicros: BudgetMicros(l.BudgetUSD),
	}
}
