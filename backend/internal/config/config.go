// Package config loads environment variables once into a single struct and
// fails fast on anything missing or invalid (backend-spec 11, AI-spec 12).
package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

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

// ModelEnabled is the hook that decides whether an LLM_MODEL value is an enabled
// catalog model for the given providers. Load calls it only when LLM_MODEL is set.
// The model catalog does not exist yet; B-2.1 replaces this with a catalog lookup.
// Until then it accepts any model id.
var ModelEnabled = func(model string, providers []string) bool { return true }

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
		c.GoogleClientID = require("GOOGLE_CLIENT_ID")
		for _, o := range strings.Split(get("ALLOWED_ORIGINS"), ",") {
			if o = strings.TrimSpace(o); o != "" {
				c.AllowedOrigins = append(c.AllowedOrigins, o)
			}
		}
		if len(c.AllowedOrigins) == 0 {
			fail("ALLOWED_ORIGINS is required")
		}
		c.WorkerFunctionName = get("WORKER_FUNCTION_NAME")
	case "worker":
		for _, p := range c.Providers {
			switch p {
			case "anthropic":
				c.LLMAPIKey = require("LLM_API_KEY")
			case "gemini":
				c.GeminiAPIKey = require("GEMINI_API_KEY")
			}
		}
	}

	c.LLMModel = get("LLM_MODEL")
	if c.LLMModel != "" && !ModelEnabled(c.LLMModel, c.Providers) {
		fail("LLM_MODEL %q is not an enabled model", c.LLMModel)
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
	c.GenMaxPDFPages = intVar("GEN_MAX_PDF_PAGES", 40)
	c.GenMaxInputTokens = intVar("GEN_MAX_INPUT_TOKENS", 80000)
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
