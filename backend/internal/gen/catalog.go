// Package gen holds the model catalog and price table (AI-spec 3.2) and, in later
// tasks, the generation pipeline.
package gen

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

// Provider names, as used in LLM_PROVIDERS.
const (
	ProviderGemini    = "gemini"
	ProviderAnthropic = "anthropic"
)

// Model is one catalog row. Prices are USD per million tokens. SupportsEffort is true for
// the Anthropic models that accept output_config.effort; Haiku 4.5 rejects it with a 400
// (AI-spec 3.2), so none is sent or recorded for it. Gemini has no effort parameter, but
// LLM_EFFORT is mapped to its thinking level; see EffortSent.
type Model struct {
	ID, Name, Provider, Tier string
	InPerMTok, OutPerMTok    float64
	SupportsEffort           bool
}

// Usage is the token counts of one or more provider calls.
type Usage struct{ Input, Output, CacheRead, CacheWrite int64 }

// catalog is the fixed list from AI-spec 3.2, cheapest first.
var catalog = []Model{
	{"gemini-3.1-flash-lite", "Gemini 3.1 Flash-Lite", ProviderGemini, "cheapest", 0.25, 1.5, false},
	{"claude-haiku-4-5", "Claude Haiku 4.5", ProviderAnthropic, "cheap", 1, 5, false},
	{"claude-sonnet-5-5", "Claude Sonnet 5.5", ProviderAnthropic, "balanced", 2, 10, true},
	{"claude-opus-5-5", "Claude Opus 5.5", ProviderAnthropic, "most capable", 4, 20, true},
}

// Catalog returns every model, cheapest first. The slice is a copy.
func Catalog() []Model { return slices.Clone(catalog) }

// Enabled returns the catalog models whose provider is in providers, cheapest first.
func Enabled(providers []string) []Model {
	var out []Model
	for _, m := range catalog {
		if slices.Contains(providers, m.Provider) {
			out = append(out, m)
		}
	}
	return out
}

// DefaultModel returns llmModel if it is set and among enabled, otherwise the cheapest
// enabled model (enabled is ordered cheapest first, as Enabled returns it). It is an
// error if llmModel is set but not enabled, or if nothing is enabled.
func DefaultModel(enabled []Model, llmModel string) (Model, error) {
	if len(enabled) == 0 {
		return Model{}, fmt.Errorf("no model is enabled: enable a provider in LLM_PROVIDERS")
	}
	if llmModel == "" {
		return enabled[0], nil
	}
	for _, m := range enabled {
		if m.ID == llmModel {
			return m, nil
		}
	}
	ids := make([]string, len(enabled))
	for i, m := range enabled {
		ids[i] = m.ID
	}
	return Model{}, fmt.Errorf("LLM_MODEL %q is not an enabled model (enabled: %s)", llmModel, strings.Join(ids, ", "))
}

// Multipliers in percent of the input price.
const (
	plainPct               = 100
	anthropicCacheWritePct = 125 // Anthropic cache writes cost 1.25x input
	cacheReadPct           = 10  // cache reads cost 0.1x input
)

// toMicros converts a USD price to whole micro-dollars (the prices have at most six decimals).
func toMicros(usd float64) int64 { return int64(math.Round(usd * 1e6)) }

// CostMicros is the cost of u on m in whole micro-dollars (1e-6 USD), computed in integer
// arithmetic so nothing drifts. Input and output are charged at the model's prices; cache
// reads at 0.1x input on every provider; cache writes at 1.25x input for Anthropic and 1x
// for Gemini (no write surcharge). The exact total is rounded half up to a whole
// micro-dollar once, at the end. Negative counts are treated as zero. The intermediate
// sum fits in int64 for counts up to about 10^9 tokens per field.
func CostMicros(m Model, u Usage) int64 {
	in, out := toMicros(m.InPerMTok), toMicros(m.OutPerMTok)
	writePct := int64(plainPct)
	if m.Provider == ProviderAnthropic {
		writePct = anthropicCacheWritePct
	}
	pos := func(n int64) int64 { return max(n, 0) }
	// micro-dollars = tokens * (micro-dollars per MTok) / 1e6, scaled by 100 for the percentages.
	scaled := pos(u.Input)*in*plainPct +
		pos(u.Output)*out*plainPct +
		pos(u.CacheWrite)*in*writePct +
		pos(u.CacheRead)*in*cacheReadPct
	const unit = 100 * 1_000_000
	return (scaled + unit/2) / unit
}

// Job-size assumptions for ApproxJobCost (AI-spec 10.3).
const (
	smallInput, smallCalls, smallOutputPerCall = 15_000, 4, 3_000
	heavyInput, heavyCalls, heavyOutputPerCall = 60_000, 5, 4_000
)

// ApproxJobCost is the rough cost in USD of a small job (15,000 input tokens of files,
// four calls of 3,000 output tokens) and a heavy job (60,000 input tokens, five calls of
// 4,000 output tokens) with no cache discount: every call resends the files, so the input
// is charged once per call. Each figure is rounded to whole cents, ties to even, from the
// exact micro-dollar cost. It is an illustration, not a quote (AI-spec 10.3).
func ApproxJobCost(m Model) (small, heavy float64) {
	cost := func(input, calls, outPerCall int64) float64 {
		return dollarsRoundedToCents(CostMicros(m, Usage{Input: input * calls, Output: outPerCall * calls}))
	}
	return cost(smallInput, smallCalls, smallOutputPerCall), cost(heavyInput, heavyCalls, heavyOutputPerCall)
}

func dollarsRoundedToCents(micros int64) float64 {
	const perCent = 10_000
	q, r := micros/perCent, micros%perCent
	if r > perCent/2 || (r == perCent/2 && q%2 == 1) {
		q++
	}
	return float64(q) / 100
}

// LookupModel returns the catalog model with the given id.
func LookupModel(id string) (Model, bool) {
	for _, m := range catalog {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

// GeminiThinkingEffort is the LLM_EFFORT value the Gemini adapter turns into a thinking level
// (low, medium or high), or "" when none is sent (empty or unknown). The adapter and
// EffortSent both use it, so what the job records is what was sent.
func GeminiThinkingEffort(effort string) string {
	switch effort {
	case "low", "medium", "high":
		return effort
	}
	return ""
}

// EffortSent is the effort a call to model carries, which is what the job records (AI-spec 11):
// the configured effort for the Claude models that take it, the thinking level for Gemini,
// and "" for Haiku 4.5 and unknown models.
func EffortSent(model, effort string) string {
	m, ok := LookupModel(model)
	switch {
	case !ok:
		return ""
	case m.Provider == ProviderGemini:
		return GeminiThinkingEffort(effort)
	case m.SupportsEffort:
		return effort
	}
	return ""
}
