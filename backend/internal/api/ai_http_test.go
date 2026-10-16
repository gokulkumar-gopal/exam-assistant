package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/gokulkumar/exam-assistant/backend/internal/auth"
	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/docs"
	"github.com/gokulkumar/exam-assistant/backend/internal/gen"
	"github.com/gokulkumar/exam-assistant/backend/internal/store/memory"
)

type modelsOut struct {
	Models []struct {
		ID            string  `json:"id"`
		Name          string  `json:"name"`
		Provider      string  `json:"provider"`
		Tier          string  `json:"tier"`
		PriceIn       float64 `json:"priceInPerMTok"`
		PriceOut      float64 `json:"priceOutPerMTok"`
		ApproxJobCost struct {
			Small float64 `json:"small"`
			Heavy float64 `json:"heavy"`
		} `json:"approxJobCost"`
		Default bool `json:"default"`
	} `json:"models"`
	ServerTime string `json:"serverTime"`
}

func aiDeps(t *testing.T, providers []string, llmModel string) (Deps, *clock.Fake) {
	t.Helper()
	users := memory.NewUserStore()
	clk := clock.NewFake(authT0)
	enabled := gen.Enabled(providers)
	def, err := gen.DefaultModel(enabled, llmModel)
	if err != nil {
		t.Fatal(err)
	}
	return Deps{
		Logger: silentLogger(), Clock: clk, Users: users,
		Auth:           auth.NewService(users, auth.NewBcryptHasher(bcrypt.MinCost), clk),
		Sessions:       auth.NewSessions([]byte(authSecret), "local"),
		Docs:           docs.NewService(memory.NewDocStore(clk), docs.NewMemBlob(), clk),
		Models:         enabled,
		DefaultModelID: def.ID,
		AllowedOrigins: []string{"http://localhost:3000"},
	}, clk
}

func getModels(t *testing.T, h http.Handler, signedIn bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/ai/models", nil)
	if signedIn {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, jsonReq("POST", "/auth/signup", signupBody))
		c := sessionCookie(rec)
		if c == nil {
			t.Fatalf("signup failed: %s", rec.Body)
		}
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestModelsEndpointOnlyEnabledExactlyOneDefault(t *testing.T) {
	deps, _ := aiDeps(t, []string{"anthropic"}, "claude-sonnet-5-5")
	rec := getModels(t, NewRouter(deps), true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	var out modelsOut
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	var got []string
	defaults := 0
	for _, m := range out.Models {
		got = append(got, m.ID)
		if m.Default {
			defaults++
			if m.ID != "claude-sonnet-5-5" {
				t.Errorf("default = %s, want LLM_MODEL claude-sonnet-5-5", m.ID)
			}
		}
		if m.Name == "" || m.Provider != "anthropic" || m.Tier == "" || m.PriceIn <= 0 || m.PriceOut <= 0 ||
			m.ApproxJobCost.Small <= 0 || m.ApproxJobCost.Heavy <= m.ApproxJobCost.Small {
			t.Errorf("incomplete model %+v", m)
		}
	}
	if strings.Join(got, ",") != "claude-haiku-4-5,claude-sonnet-5-5,claude-opus-5-5" {
		t.Errorf("models = %v (gemini must not be listed)", got)
	}
	if defaults != 1 {
		t.Errorf("%d defaults, want exactly 1", defaults)
	}
	if want := authT0.Format(time.RFC3339); out.ServerTime != want {
		t.Errorf("serverTime = %q, want %q", out.ServerTime, want)
	}
}

func TestModelsEndpointShapeAndDefaultIsCheapest(t *testing.T) {
	deps, clk := aiDeps(t, []string{"gemini", "anthropic"}, "")
	clk.Set(authT0.Add(90 * time.Minute))
	rec := getModels(t, NewRouter(deps), true)
	var raw struct {
		Models     []map[string]json.RawMessage `json:"models"`
		ServerTime string                       `json:"serverTime"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw.Models) != 4 {
		t.Fatalf("%d models", len(raw.Models))
	}
	for _, k := range []string{"id", "name", "provider", "tier", "priceInPerMTok", "priceOutPerMTok", "approxJobCost", "default"} {
		if _, ok := raw.Models[0][k]; !ok {
			t.Errorf("missing key %q", k)
		}
	}
	if len(raw.Models[0]) != 8 {
		t.Errorf("extra keys: %v", raw.Models[0])
	}
	if string(raw.Models[0]["id"]) != `"gemini-3.1-flash-lite"` || string(raw.Models[0]["default"]) != "true" {
		t.Errorf("first = %v", raw.Models[0])
	}
	if raw.ServerTime != authT0.Add(90*time.Minute).Format(time.RFC3339) {
		t.Errorf("serverTime %q does not follow the injected clock", raw.ServerTime)
	}
}

func TestModelsRequiresLogin(t *testing.T) {
	deps, _ := aiDeps(t, []string{"anthropic"}, "")
	rec := getModels(t, NewRouter(deps), false)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

func TestNewHandlerRefusesNoModels(t *testing.T) {
	deps, _ := aiDeps(t, []string{"anthropic"}, "")
	deps.Models = nil
	if h, err := NewHandler(deps); err == nil || h != nil || !strings.Contains(err.Error(), "Models") {
		t.Fatalf("nil Models: h=%v err=%v", h, err)
	}
	deps, _ = aiDeps(t, []string{"anthropic"}, "")
	deps.DefaultModelID = "claude-opus-5-5-typo"
	if _, err := NewHandler(deps); err == nil || !strings.Contains(err.Error(), "DefaultModelID") {
		t.Fatalf("default not among models: %v", err)
	}
	deps, _ = aiDeps(t, []string{"anthropic"}, "")
	if _, err := NewHandler(deps); err != nil {
		t.Fatalf("complete deps: %v", err)
	}
}
