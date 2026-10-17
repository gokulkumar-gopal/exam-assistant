package config_test

import (
	"strings"
	"testing"

	"github.com/gokulkumar/exam-assistant/backend/internal/config"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func apiEnv() map[string]string {
	return map[string]string{
		"ENV":              "local",
		"STAGE":            "dev",
		"S3_BUCKET":        "bucket",
		"JWT_SECRET":       "0123456789abcdef0123456789abcdef",
		"ALLOWED_ORIGINS":  "http://localhost:3000, https://app.example.com",
		"GOOGLE_CLIENT_ID": "client-id",
	}
}

func workerEnv() map[string]string {
	return map[string]string{
		"ENV":         "production",
		"STAGE":       "prod",
		"S3_BUCKET":   "bucket",
		"MODE":        "worker",
		"LLM_API_KEY": "sk-test",
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := config.Load(env(apiEnv()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Mode != "api" {
		t.Errorf("Mode = %q, want api", cfg.Mode)
	}
	if cfg.GenMaxPerDay != 3 {
		t.Errorf("GenMaxPerDay = %d, want 3", cfg.GenMaxPerDay)
	}
	if cfg.GenMaxRegenPerDay != 10 {
		t.Errorf("GenMaxRegenPerDay = %d, want 10", cfg.GenMaxRegenPerDay)
	}
	if cfg.GenMaxGlobalPerDay != 10 {
		t.Errorf("GenMaxGlobalPerDay = %d, want 10", cfg.GenMaxGlobalPerDay)
	}
	if cfg.GenMaxRunning != 2 {
		t.Errorf("GenMaxRunning = %d, want 2", cfg.GenMaxRunning)
	}
	if cfg.LLMMonthlyBudgetUSD != 15 {
		t.Errorf("LLMMonthlyBudgetUSD = %v, want 15", cfg.LLMMonthlyBudgetUSD)
	}
	if cfg.LLMTimeoutSeconds != 120 {
		t.Errorf("LLMTimeoutSeconds = %d, want 120", cfg.LLMTimeoutSeconds)
	}
	if cfg.LLMEffort != "medium" {
		t.Errorf("LLMEffort = %q, want medium", cfg.LLMEffort)
	}
	if cfg.GenMaxTotalBytes != 20971520 {
		t.Errorf("GenMaxTotalBytes = %d, want 20971520", cfg.GenMaxTotalBytes)
	}
	if cfg.GenMaxPDFPages != 100 {
		t.Errorf("GenMaxPDFPages = %d, want 100", cfg.GenMaxPDFPages)
	}
	if cfg.GenMaxFiles != 10 {
		t.Errorf("GenMaxFiles = %d, want 10", cfg.GenMaxFiles)
	}
	if cfg.GenMaxInputTokens != 200000 {
		t.Errorf("GenMaxInputTokens = %d, want 200000", cfg.GenMaxInputTokens)
	}
	if cfg.GenMaxPerCall != 15 {
		t.Errorf("GenMaxPerCall = %d, want 15", cfg.GenMaxPerCall)
	}
	if len(cfg.Providers) != 1 || cfg.Providers[0] != "anthropic" {
		t.Errorf("Providers = %v, want [anthropic]", cfg.Providers)
	}
	if len(cfg.AllowedOrigins) != 2 || cfg.AllowedOrigins[1] != "https://app.example.com" {
		t.Errorf("AllowedOrigins = %v, want trimmed 2 origins", cfg.AllowedOrigins)
	}
	if cfg.Env != "local" || cfg.Stage != "dev" || cfg.S3Bucket != "bucket" {
		t.Errorf("required fields not loaded: %+v", cfg)
	}
}

func TestLoadConfigOverrides(t *testing.T) {
	m := apiEnv()
	m["GEN_MAX_PER_DAY"] = "7"
	m["LLM_MONTHLY_BUDGET_USD"] = "2.5"
	m["LLM_PROVIDERS"] = "gemini, anthropic"
	cfg, err := config.Load(env(m))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.GenMaxPerDay != 7 || cfg.LLMMonthlyBudgetUSD != 2.5 {
		t.Errorf("overrides not applied: %+v", cfg)
	}
	if len(cfg.Providers) != 2 || cfg.Providers[0] != "gemini" || cfg.Providers[1] != "anthropic" {
		t.Errorf("Providers = %v", cfg.Providers)
	}
}

func TestLoadConfigWorkerNeedsNoAPIVars(t *testing.T) {
	cfg, err := config.Load(env(workerEnv()))
	if err != nil {
		t.Fatalf("Load worker: %v", err)
	}
	if cfg.Mode != "worker" || cfg.LLMAPIKey != "sk-test" {
		t.Errorf("unexpected worker config: %+v", cfg)
	}
}

func TestLoadConfigFailsFast(t *testing.T) {
	cases := []struct {
		name    string
		base    func() map[string]string
		mutate  func(map[string]string)
		wantSub string
	}{
		{"missing ENV", apiEnv, func(m map[string]string) { delete(m, "ENV") }, "ENV"},
		{"bad ENV", apiEnv, func(m map[string]string) { m["ENV"] = "staging" }, "ENV"},
		{"missing STAGE", apiEnv, func(m map[string]string) { delete(m, "STAGE") }, "STAGE"},
		{"missing S3_BUCKET", apiEnv, func(m map[string]string) { delete(m, "S3_BUCKET") }, "S3_BUCKET"},
		{"bad MODE", apiEnv, func(m map[string]string) { m["MODE"] = "both" }, "MODE"},
		{"api missing JWT_SECRET", apiEnv, func(m map[string]string) { delete(m, "JWT_SECRET") }, "JWT_SECRET"},
		{"api missing ALLOWED_ORIGINS", apiEnv, func(m map[string]string) { delete(m, "ALLOWED_ORIGINS") }, "ALLOWED_ORIGINS"},
		{"api missing GOOGLE_CLIENT_ID", apiEnv, func(m map[string]string) { delete(m, "GOOGLE_CLIENT_ID") }, "GOOGLE_CLIENT_ID"},
		{"worker anthropic without LLM_API_KEY", workerEnv, func(m map[string]string) { delete(m, "LLM_API_KEY") }, "LLM_API_KEY"},
		{"worker gemini without GEMINI_API_KEY", workerEnv, func(m map[string]string) {
			m["LLM_PROVIDERS"] = "gemini"
		}, "GEMINI_API_KEY"},
		{"no provider enabled", apiEnv, func(m map[string]string) { m["LLM_PROVIDERS"] = " , " }, "LLM_PROVIDERS"},
		{"unknown provider", apiEnv, func(m map[string]string) { m["LLM_PROVIDERS"] = "openai" }, "LLM_PROVIDERS"},
		{"non-numeric cap", apiEnv, func(m map[string]string) { m["GEN_MAX_PER_DAY"] = "lots" }, "GEN_MAX_PER_DAY"},
		{"zero cap", apiEnv, func(m map[string]string) { m["GEN_MAX_RUNNING"] = "0" }, "GEN_MAX_RUNNING"},
		{"short JWT_SECRET", apiEnv, func(m map[string]string) { m["JWT_SECRET"] = "0123456789abcdef0123456789abcde" }, "JWT_SECRET"},
		{"bad effort", apiEnv, func(m map[string]string) { m["LLM_EFFORT"] = "extreme" }, "LLM_EFFORT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.base()
			tc.mutate(m)
			_, err := config.Load(env(m))
			if err == nil {
				t.Fatal("Load succeeded, want error")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not mention %q", err, tc.wantSub)
			}
		})
	}
}

func TestLoadConfigAPIModeDoesNotRequireWorkerKeys(t *testing.T) {
	m := apiEnv()
	m["LLM_PROVIDERS"] = "gemini,anthropic"
	if _, err := config.Load(env(m)); err != nil {
		t.Fatalf("api mode must not need provider keys: %v", err)
	}
}

func TestJWTSecretLengthIsMeasuredInBytes(t *testing.T) {
	m := apiEnv()
	m["JWT_SECRET"] = strings.Repeat("a", 32)
	if _, err := config.Load(env(m)); err != nil {
		t.Errorf("32-byte secret rejected: %v", err)
	}
	// 11 runes of 3 bytes = 33 bytes is long enough; 10 runes = 30 bytes is not.
	m["JWT_SECRET"] = strings.Repeat("€", 11)
	if _, err := config.Load(env(m)); err != nil {
		t.Errorf("33-byte secret rejected: %v", err)
	}
	m["JWT_SECRET"] = strings.Repeat("€", 10)
	if _, err := config.Load(env(m)); err == nil {
		t.Error("30-byte secret accepted")
	}
}

func TestShortJWTSecretErrorDoesNotEchoSecret(t *testing.T) {
	m := apiEnv()
	m["JWT_SECRET"] = "tooshort-but-secret"
	_, err := config.Load(env(m))
	if err == nil || strings.Contains(err.Error(), "tooshort-but-secret") {
		t.Errorf("err = %v", err)
	}
}

func TestWorkerDoesNotNeedJWTSecret(t *testing.T) {
	if _, err := config.Load(env(workerEnv())); err != nil {
		t.Errorf("worker without JWT_SECRET: %v", err)
	}
}

func TestStartupFailsWhenNoProvider(t *testing.T) {
	for _, mode := range []string{"api", "worker"} {
		m := apiEnv()
		if mode == "worker" {
			m = workerEnv()
		}
		m["LLM_PROVIDERS"] = " , "
		_, err := config.Load(env(m))
		if err == nil || !strings.Contains(err.Error(), "at least one provider") {
			t.Errorf("%s mode with no provider: err = %v", mode, err)
		}
	}
}

func TestLLMModelMustBeEnabled(t *testing.T) {
	for _, mode := range []string{"api", "worker"} {
		base := func() map[string]string {
			if mode == "worker" {
				return workerEnv()
			}
			return apiEnv()
		}
		// Set but not enabled (gemini model, anthropic only) and unknown ids both fail clearly.
		for _, id := range []string{"gemini-3.1-flash-lite", "claude-9"} {
			m := base()
			m["LLM_MODEL"] = id
			_, err := config.Load(env(m))
			if err == nil || !strings.Contains(err.Error(), "LLM_MODEL") || !strings.Contains(err.Error(), id) {
				t.Errorf("%s mode LLM_MODEL=%q: err = %v", mode, id, err)
			}
		}
		m := base()
		m["LLM_MODEL"] = "claude-sonnet-5-5"
		c, err := config.Load(env(m))
		if err != nil || c.LLMModel != "claude-sonnet-5-5" {
			t.Errorf("%s mode enabled model: %v", mode, err)
		}
		m = base()
		m["LLM_PROVIDERS"] = "gemini,anthropic"
		m["GEMINI_API_KEY"] = "g-test"
		m["LLM_MODEL"] = "gemini-3.1-flash-lite"
		if _, err := config.Load(env(m)); err != nil {
			t.Errorf("%s mode gemini model with gemini enabled: %v", mode, err)
		}
	}
}

func TestGenLimitsFromDefaults(t *testing.T) {
	m := apiEnv()
	m["MODE"] = "api"
	c, err := config.Load(env(m))
	if err != nil {
		t.Fatal(err)
	}
	l := c.GenLimits()
	if l.MaxSources != 10 || l.MaxTotalBytes != 20971520 || l.MaxPDFPages != 100 || l.MaxRunning != 2 ||
		l.Quota.MaxUser != 3 || l.Quota.MaxRegen != 10 || l.Quota.MaxGlobal != 10 || l.Quota.BudgetUSD != 15 {
		t.Errorf("limits = %+v", l)
	}
}
