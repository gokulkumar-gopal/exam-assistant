package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/auth"
	"github.com/gokulkumar/exam-assistant/backend/internal/config"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

func prodAPIEnv() map[string]string {
	m := apiEnv()
	m["ENV"] = "production"
	m["ALLOWED_ORIGINS"] = "https://app.example.com"
	return m
}

func TestProductionRequiresSecureCookieAndHTTPSOrigins(t *testing.T) {
	ok := map[string]string{
		"https only":            "https://app.example.com",
		"https plus local dev":  "https://app.example.com, http://localhost:3000",
		"only the local dev":    "http://localhost:3000",
		"uppercase https":       "HTTPS://app.example.com",
		"http localhost others": "",
	}
	delete(ok, "http localhost others")
	for name, origins := range ok {
		m := prodAPIEnv()
		m["ALLOWED_ORIGINS"] = origins
		if _, err := config.Load(env(m)); err != nil {
			t.Errorf("%s: rejected: %v", name, err)
		}
	}
	bad := []string{
		"http://app.example.com",
		"https://app.example.com,http://app.example.com",
		"http://localhost:3001",
		"http://localhost",
		"http://localhost:3000/",
		"HTTP://app.example.com",
	}
	for _, origins := range bad {
		m := prodAPIEnv()
		m["ALLOWED_ORIGINS"] = origins
		_, err := config.Load(env(m))
		if err == nil || !strings.Contains(err.Error(), "ALLOWED_ORIGINS") || !strings.Contains(err.Error(), "https://") {
			t.Errorf("origins %q: err = %v", origins, err)
		}
	}
	// The same http origins are fine outside production.
	m := apiEnv()
	m["ALLOWED_ORIGINS"] = "http://app.example.com"
	if _, err := config.Load(env(m)); err != nil {
		t.Errorf("local env rejected http origin: %v", err)
	}
	// Every bad origin is reported, together with other problems.
	m = prodAPIEnv()
	m["ALLOWED_ORIGINS"] = "http://a.example.com,http://b.example.com"
	delete(m, "GOOGLE_CLIENT_ID")
	_, err := config.Load(env(m))
	if err == nil || !strings.Contains(err.Error(), "a.example.com") || !strings.Contains(err.Error(), "b.example.com") || !strings.Contains(err.Error(), "GOOGLE_CLIENT_ID") {
		t.Errorf("errors not aggregated: %v", err)
	}
	// ENV is validated, so the only way to run without ENV=local is a Secure cookie.
	for _, e := range []string{"production", "staging", ""} {
		c := auth.NewSessions([]byte(prodAPIEnv()["JWT_SECRET"]), e).Issue(store.User{ID: "u"}, time.Unix(1, 0))
		if !c.Secure || !c.HttpOnly {
			t.Errorf("ENV=%q cookie = %+v, want Secure and HttpOnly", e, c)
		}
	}
	m = prodAPIEnv()
	m["ENV"] = "staging"
	if _, err := config.Load(env(m)); err == nil {
		t.Error("ENV=staging accepted")
	}
}

func TestProductionRejectsShortJWTSecret(t *testing.T) {
	m := prodAPIEnv()
	m["JWT_SECRET"] = "short-secret-value-31-bytes-xxxx"[:31]
	_, err := config.Load(env(m))
	if err == nil || !strings.Contains(err.Error(), "JWT_SECRET") || !strings.Contains(err.Error(), "32") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), m["JWT_SECRET"]) {
		t.Errorf("error echoes the secret: %v", err)
	}
	m["JWT_SECRET"] = strings.Repeat("k", 32)
	if _, err := config.Load(env(m)); err != nil {
		t.Errorf("32-byte secret rejected: %v", err)
	}
}

func TestAPIModeNeverReadsLLMKeys(t *testing.T) {
	m := prodAPIEnv()
	m["LLM_PROVIDERS"] = "anthropic,gemini"
	var read []string
	getenv := func(k string) string { read = append(read, k); return m[k] }
	if _, err := config.Load(getenv); err != nil {
		t.Fatalf("api config needs no LLM keys: %v", err)
	}
	for _, k := range read {
		if k == "LLM_API_KEY" || k == "GEMINI_API_KEY" {
			t.Errorf("API-mode load read %s", k)
		}
	}
	m["LLM_API_KEY"], m["GEMINI_API_KEY"] = "sk-secret-a", "gem-secret-b"
	cfg, err := config.Load(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLMAPIKey != "" || cfg.GeminiAPIKey != "" {
		t.Errorf("API config carries LLM keys: %+v", cfg)
	}
}
