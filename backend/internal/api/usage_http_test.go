package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/gen"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
	"github.com/gokulkumar/exam-assistant/backend/internal/store/memory"
)

func signedUpCookie(t *testing.T, h http.Handler, role string) *http.Cookie {
	t.Helper()
	body := strings.Replace(signupBody, `"student"`, `"`+role+`"`, 1)
	rec := do(h, jsonReq("POST", "/auth/signup", body))
	c := sessionCookie(rec)
	if c == nil {
		t.Fatalf("signup %s failed: %s", role, rec.Body)
	}
	return c
}

func getUsage(h http.Handler, c *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/ai/usage", nil)
	if c != nil {
		req.AddCookie(c)
	}
	return do(h, req)
}

func TestUsageRequiresLoginAnyRole(t *testing.T) {
	deps, _ := aiDeps(t, []string{"anthropic"}, "")
	h := NewRouter(deps)
	if rec := getUsage(h, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d %s", rec.Code, rec.Body)
	}
	for _, role := range []string{"student", "parent", "teacher"} {
		deps, _ := aiDeps(t, []string{"anthropic"}, "")
		h := NewRouter(deps)
		if rec := getUsage(h, signedUpCookie(t, h, role)); rec.Code != http.StatusOK {
			t.Errorf("%s: %d %s", role, rec.Code, rec.Body)
		}
	}
}

func TestUsageNoStoreAndServerTime(t *testing.T) {
	deps, _ := aiDeps(t, []string{"anthropic"}, "")
	h := NewRouter(deps)
	rec := getUsage(h, signedUpCookie(t, h, "student"))
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	var v struct {
		ServerTime string `json:"serverTime"`
		Month      struct {
			Key       string  `json:"key"`
			BudgetUSD float64 `json:"budgetUsd"`
		} `json:"month"`
		ByModel []json.RawMessage `json:"byModel"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.ServerTime != authT0.Format(time.RFC3339) || v.Month.Key != authT0.Format("2006-01") || v.Month.BudgetUSD != 15 {
		t.Errorf("body = %s", rec.Body)
	}
	if len(v.ByModel) != len(gen.Catalog()) {
		t.Errorf("byModel = %d", len(v.ByModel))
	}
}

func TestUsageWrongMethodIs405(t *testing.T) {
	deps, _ := aiDeps(t, []string{"anthropic"}, "")
	h := NewRouter(deps)
	req := httptest.NewRequest("POST", "/ai/usage", nil)
	req.AddCookie(signedUpCookie(t, h, "student"))
	rec := do(h, req)
	var b errBody
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	if rec.Code != 405 || b.Error.Code != "method_not_allowed" || !strings.Contains(rec.Header().Get("Allow"), "GET") {
		t.Fatalf("%d %s allow=%q", rec.Code, rec.Body, rec.Header().Get("Allow"))
	}
}

type failingUsage struct{ store.UsageStore }

func (failingUsage) GetUsage(context.Context, string, string, []string) (store.UsageSnapshot, error) {
	return store.UsageSnapshot{}, context.DeadlineExceeded
}

func TestUsageStoreErrorIsJSON500WithoutCause(t *testing.T) {
	deps, _ := aiDeps(t, []string{"anthropic"}, "")
	js := memory.NewJobStore(deps.Clock)
	deps.Usage = &gen.UsageService{Usage: failingUsage{js}, Jobs: js, BudgetUSD: 15}
	h := NewRouter(deps)
	rec := getUsage(h, signedUpCookie(t, h, "student"))
	var b errBody
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	if rec.Code != 500 || b.Error.Code != "internal" || strings.Contains(rec.Body.String(), "deadline") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}
