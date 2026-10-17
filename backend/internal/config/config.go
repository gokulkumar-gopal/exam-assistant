// Package config loads environment variables once into a single struct and
// fails fast on anything missing or invalid (backend-spec 11, AI-spec 12).
package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gokulkumar/exam-assistant/backend/internal/gen"
)

// MinJWTSecretBytes is the shortest accepted JWT_SECRET.
const MinJWTSecretBytes = 32

// LocalDevOrigin is the only http:// origin ENV=production accepts.
const LocalDevOrigin = "http://localhost:3000"

// Config is the whole process configuration.
type Config struct {
	Env      string // "local" or "production"
	Mode     string // "api" or "worker"
	Stage    string
	S3Bucket string

	JWTSecret          string
	AllowedOrigins     []string
	GoogleClientID     string
	WorkerFunctionName string

	Providers    []string // enabled LLM providers: "anthropic", "gemini"
	LLMAPIKey    string
	GeminiAPIKey string
	LLMModel     string
	LLMEffort    string

	LLMTimeoutSeconds   int
	LLMMonthlyBudgetUSD float64

	GenMaxFiles        int
	GenMaxTotalBytes   int64
	GenMaxPDFPages     int
	GenMaxInputTokens  int
	GenMaxPerCall      int
	GenMaxRunning      int
	GenMaxPerDay       int
	GenMaxRegenPerDay  int
	GenMaxGlobalPerDay int
}

// Load reads configuration through getenv. It returns one error listing every problem.
func Load(getenv func(string) string) (Config, error) {
	var errs []error
	fail := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	get := func(k string) string { return strings.TrimSpace(getenv(k)) }
	require := func(k string) string {
		v := get(k)
		if v == "" {
			fail("%s is required", k)
		}
		return v
	}
	intVar := func(k string, def int) int {
		s := get(k)
		if s == "" {
			return def
		}
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			fail("%s must be a positive integer, got %q", k, s)
			return def
		}
		return n
	}

	var c Config
	c.Env = require("ENV")
	if c.Env != "" && c.Env != "local" && c.Env != "production" {
		fail("ENV must be local or production, got %q", c.Env)
	}
	c.Stage = require("STAGE")
	c.S3Bucket = require("S3_BUCKET")

	c.Mode = get("MODE")
	if c.Mode == "" {
		c.Mode = "api"
	}
	if c.Mode != "api" && c.Mode != "worker" {
		fail("MODE must be api or worker, got %q", c.Mode)
	}

	provs := get("LLM_PROVIDERS")
	if provs == "" {
		provs = "anthropic"
	}
	for _, p := range strings.Split(provs, ",") {
		p = strings.TrimSpace(p)
		switch p {
		case "":
		case "anthropic", "gemini":
			c.Providers = append(c.Providers, p)
		default:
			fail("LLM_PROVIDERS has unknown provider %q", p)
		}
	}
	if len(c.Providers) == 0 {
		fail("LLM_PROVIDERS must enable at least one provider (anthropic, gemini)")
	}

	switch c.Mode {
	case "api":
		c.JWTSecret = require("JWT_SECRET")
		// HS256 wants a key at least as long as the hash output. The value is never echoed.
		if c.JWTSecret != "" && len(c.JWTSecret) < MinJWTSecretBytes {
			fail("JWT_SECRET must be at least %d bytes", MinJWTSecretBytes)
		}
		c.GoogleClientID = require("GOOGLE_CLIENT_ID")
		for _, o := range strings.Split(get("ALLOWED_ORIGINS"), ",") {
			if o = strings.TrimSpace(o); o != "" {
				c.AllowedOrigins = append(c.AllowedOrigins, o)
			}
		}
		if len(c.AllowedOrigins) == 0 {
			fail("ALLOWED_ORIGINS is required")
		}
		if c.Env == "production" {
			// Production origins must be HTTPS; the one plain-HTTP exception is the
			// local dev frontend (backend-spec 8.2, 12). Cookies are Secure in production
			// regardless (auth.NewSessions).
			for _, o := range c.AllowedOrigins {
				if strings.HasPrefix(strings.ToLower(o), "http://") && o != LocalDevOrigin {
					fail("ALLOWED_ORIGINS entry %q must use https:// when ENV=production", o)
				}
			}
		}
		c.WorkerFunctionName = get("WORKER_FUNCTION_NAME")
	case "worker":
		for _, p := range c.Providers {
			switch p {
			case "anthropic":
				if c.LLMAPIKey = get("LLM_API_KEY"); c.LLMAPIKey == "" {
					fail("LLM_API_KEY is required for provider anthropic")
				}
			case "gemini":
				if c.GeminiAPIKey = get("GEMINI_API_KEY"); c.GeminiAPIKey == "" {
					fail("GEMINI_API_KEY is required for provider gemini")
				}
			}
		}
	}

	c.LLMModel = get("LLM_MODEL")
	// Applies in api and worker mode alike: both must agree on the default (AI-spec 3.2, 12).
	// With no provider the error above already says so; do not add a second, confusing one.
	if len(c.Providers) > 0 {
		if _, err := gen.DefaultModel(gen.Enabled(c.Providers), c.LLMModel); err != nil {
			fail("%v", err)
		}
	}

	// An empty LLM_EFFORT takes the default: getenv cannot tell unset from empty.
	c.LLMEffort = get("LLM_EFFORT")
	if c.LLMEffort == "" {
		c.LLMEffort = "medium"
	}
	switch c.LLMEffort {
	case "low", "medium", "high":
	default:
		fail("LLM_EFFORT must be low, medium or high, got %q", c.LLMEffort)
	}

	c.LLMTimeoutSeconds = intVar("LLM_TIMEOUT_SECONDS", 120)
	c.GenMaxFiles = intVar("GEN_MAX_FILES", 10)
	c.GenMaxTotalBytes = int64(intVar("GEN_MAX_TOTAL_BYTES", 20971520))
	c.GenMaxPDFPages = intVar("GEN_MAX_PDF_PAGES", 100)
	c.GenMaxInputTokens = intVar("GEN_MAX_INPUT_TOKENS", 200000)
	c.GenMaxPerCall = intVar("GEN_MAX_PER_CALL", 15)
	c.GenMaxRunning = intVar("GEN_MAX_RUNNING", 2)
	c.GenMaxPerDay = intVar("GEN_MAX_PER_DAY", 3)
	c.GenMaxRegenPerDay = intVar("GEN_MAX_REGEN_PER_DAY", 10)
	c.GenMaxGlobalPerDay = intVar("GEN_MAX_GLOBAL_PER_DAY", 10)

	c.LLMMonthlyBudgetUSD = 15
	if s := get("LLM_MONTHLY_BUDGET_USD"); s != "" {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || f <= 0 {
			fail("LLM_MONTHLY_BUDGET_USD must be a positive number, got %q", s)
		} else {
			c.LLMMonthlyBudgetUSD = f
		}
	}

	if len(errs) > 0 {
		return Config{}, fmt.Errorf("invalid configuration: %w", errors.Join(errs...))
	}
	return c, nil
}

// GenLimits returns the generation limits (backend-spec 7.5, 8.4) for gen.NewService.
func (c Config) GenLimits() gen.Limits {
	return gen.Limits{
		MaxSources: c.GenMaxFiles, MaxTotalBytes: c.GenMaxTotalBytes, MaxPDFPages: c.GenMaxPDFPages,
		MaxRunning: c.GenMaxRunning,
		Quota: gen.QuotaLimits{
			MaxUser: c.GenMaxPerDay, MaxRegen: c.GenMaxRegenPerDay, MaxGlobal: c.GenMaxGlobalPerDay,
			BudgetUSD: c.LLMMonthlyBudgetUSD,
		},
	}
}
