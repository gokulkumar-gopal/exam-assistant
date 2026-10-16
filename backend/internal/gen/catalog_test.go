package gen_test

import (
	"math"
	"testing"

	"github.com/gokulkumar/exam-assistant/backend/internal/gen"
)

func ids(ms []gen.Model) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func byID(t *testing.T, id string) gen.Model {
	t.Helper()
	for _, m := range gen.Catalog() {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("model %q not in catalog", id)
	return gen.Model{}
}

func TestCatalogOrderCheapestFirst(t *testing.T) {
	want := []string{"gemini-3.1-flash-lite", "claude-haiku-4-5", "claude-sonnet-5-5", "claude-opus-5-5"}
	cat := gen.Catalog()
	if !eq(ids(cat), want) {
		t.Fatalf("catalog = %v, want %v", ids(cat), want)
	}
	for i := 1; i < len(cat); i++ {
		if cat[i].InPerMTok+cat[i].OutPerMTok < cat[i-1].InPerMTok+cat[i-1].OutPerMTok {
			t.Errorf("%s is cheaper than %s", cat[i].ID, cat[i-1].ID)
		}
	}
	// The spec's table (AI-spec 3.2), verbatim.
	spec := map[string]struct {
		name, provider, tier string
		in, out              float64
	}{
		"gemini-3.1-flash-lite": {"Gemini 3.1 Flash-Lite", "gemini", "cheapest", 0.25, 1.5},
		"claude-haiku-4-5":      {"Claude Haiku 4.5", "anthropic", "cheap", 1, 5},
		"claude-sonnet-5-5":     {"Claude Sonnet 5.5", "anthropic", "balanced", 2, 10},
		"claude-opus-5-5":       {"Claude Opus 5.5", "anthropic", "most capable", 4, 20},
	}
	for _, m := range cat {
		s := spec[m.ID]
		if m.Name != s.name || m.Provider != s.provider || m.Tier != s.tier || m.InPerMTok != s.in || m.OutPerMTok != s.out {
			t.Errorf("%s = %+v, spec %+v", m.ID, m, s)
		}
	}
}

func TestCatalogReturnsACopy(t *testing.T) {
	gen.Catalog()[0].ID = "mutated"
	if gen.Catalog()[0].ID != "gemini-3.1-flash-lite" {
		t.Error("Catalog exposes its backing slice")
	}
}

func TestEnabledByProviders(t *testing.T) {
	if got := ids(gen.Enabled([]string{"anthropic"})); !eq(got, []string{"claude-haiku-4-5", "claude-sonnet-5-5", "claude-opus-5-5"}) {
		t.Errorf("anthropic only = %v", got)
	}
	if got := ids(gen.Enabled([]string{"gemini", "anthropic"})); !eq(got, []string{"gemini-3.1-flash-lite", "claude-haiku-4-5", "claude-sonnet-5-5", "claude-opus-5-5"}) {
		t.Errorf("gemini,anthropic = %v", got)
	}
	if got := gen.Enabled(nil); len(got) != 0 {
		t.Errorf("no providers = %v", got)
	}
}

func TestDefaultIsCheapestEnabled(t *testing.T) {
	m, err := gen.DefaultModel(gen.Enabled([]string{"gemini", "anthropic"}), "")
	if err != nil || m.ID != "gemini-3.1-flash-lite" {
		t.Errorf("gemini+anthropic: %v %v", m.ID, err)
	}
	m, err = gen.DefaultModel(gen.Enabled([]string{"anthropic"}), "")
	if err != nil || m.ID != "claude-haiku-4-5" {
		t.Errorf("anthropic only: %v %v", m.ID, err)
	}
	if _, err := gen.DefaultModel(nil, ""); err == nil {
		t.Error("no enabled models must be an error")
	}
}

func TestDefaultHonoursLLMModel(t *testing.T) {
	m, err := gen.DefaultModel(gen.Enabled([]string{"gemini", "anthropic"}), "claude-sonnet-5-5")
	if err != nil || m.ID != "claude-sonnet-5-5" {
		t.Errorf("got %v %v", m.ID, err)
	}
}

func TestDefaultErrorsWhenNotEnabled(t *testing.T) {
	for _, id := range []string{"gemini-3.1-flash-lite", "no-such-model"} {
		if _, err := gen.DefaultModel(gen.Enabled([]string{"anthropic"}), id); err == nil {
			t.Errorf("LLM_MODEL=%q must be rejected when not enabled", id)
		}
	}
}

func TestCostMicrosGemini(t *testing.T) {
	m := byID(t, "gemini-3.1-flash-lite")
	if got := gen.CostMicros(m, gen.Usage{Input: 1_000_000, Output: 1_000_000}); got != 1_750_000 {
		t.Errorf("got %d, want 1750000", got)
	}
}

func TestCostMicrosOpusWithCache(t *testing.T) {
	m := byID(t, "claude-opus-5-5")
	if got := gen.CostMicros(m, gen.Usage{CacheWrite: 1_000_000}); got != 5_000_000 {
		t.Errorf("cacheWrite 1M = %d, want 5000000", got)
	}
	if got := gen.CostMicros(m, gen.Usage{CacheRead: 1_000_000}); got != 400_000 {
		t.Errorf("cacheRead 1M = %d, want 400000", got)
	}
	if got := gen.CostMicros(m, gen.Usage{Input: 1_000_000, Output: 1_000_000}); got != 24_000_000 {
		t.Errorf("in+out = %d, want 24000000", got)
	}
}

func TestCostMicrosIsIntegerExactAndRoundsHalfUp(t *testing.T) {
	g := byID(t, "gemini-3.1-flash-lite")
	// 1 input token = 0.25 micro-dollars: rounds to 0. 2 tokens = 0.5: rounds up to 1.
	cases := []struct {
		u    gen.Usage
		want int64
	}{
		{gen.Usage{}, 0},
		{gen.Usage{Input: 1}, 0},
		{gen.Usage{Input: 2}, 1},
		{gen.Usage{Input: 3}, 1},
		{gen.Usage{Output: 1}, 2}, // 1.5 micro-dollars
		{gen.Usage{Input: 7_000_003, Output: 3_000_001}, 6_250_002}, // 1750000.75 + 4500001.5 = 6250002.25, rounded once on the total
	}
	for _, c := range cases {
		if got := gen.CostMicros(g, c.u); got != c.want {
			t.Errorf("%+v = %d, want %d", c.u, got, c.want)
		}
	}
}

func TestCostMicrosGeminiCacheReadDiscount(t *testing.T) {
	g := byID(t, "gemini-3.1-flash-lite")
	if got := gen.CostMicros(g, gen.Usage{CacheRead: 1_000_000}); got != 25_000 {
		t.Errorf("gemini cacheRead 1M = %d, want 25000", got)
	}
	// Gemini has no cache-write surcharge.
	if got := gen.CostMicros(g, gen.Usage{CacheWrite: 1_000_000}); got != 250_000 {
		t.Errorf("gemini cacheWrite 1M = %d, want 250000", got)
	}
}

func TestApproxJobCostGeminiSmall(t *testing.T) {
	small, heavy := gen.ApproxJobCost(byID(t, "gemini-3.1-flash-lite"))
	if math.Abs(small-0.03) > 0.005 {
		t.Errorf("small = %v, want 0.03 +- 0.005", small)
	}
	if math.Abs(heavy-0.10) > 0.01 {
		t.Errorf("heavy = %v, want 0.10 +- 0.01", heavy)
	}
}

func TestApproxJobCostOrdersByPrice(t *testing.T) {
	prevS, prevH := 0.0, 0.0
	for _, m := range gen.Catalog() {
		s, h := gen.ApproxJobCost(m)
		if s <= 0 || h <= s || s < prevS || h < prevH {
			t.Errorf("%s: small %v heavy %v (prev %v %v)", m.ID, s, h, prevS, prevH)
		}
		prevS, prevH = s, h
	}
}
