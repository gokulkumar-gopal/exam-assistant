package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/config"
	"github.com/gokulkumar/exam-assistant/backend/internal/gen"
	"github.com/gokulkumar/exam-assistant/backend/internal/gen/prompt"
)

type recordingRunner struct {
	ids []string
	err error
}

func (r *recordingRunner) Run(_ context.Context, id string) error {
	r.ids = append(r.ids, id)
	return r.err
}

func TestWorkerModeEventParsed(t *testing.T) {
	r := &recordingRunner{}
	h := handleWorkerEvent(r)
	if err := h(context.Background(), []byte(`{"jobId":"01J123"}`)); err != nil {
		t.Fatal(err)
	}
	if len(r.ids) != 1 || r.ids[0] != "01J123" {
		t.Errorf("Run called with %v", r.ids)
	}
}

func TestWorkerModeRejectsBadEvents(t *testing.T) {
	for _, body := range []string{``, `not json`, `{}`, `{"jobId":""}`, `{"jobId":7}`, `[]`} {
		r := &recordingRunner{}
		if err := handleWorkerEvent(r)(context.Background(), []byte(body)); err == nil {
			t.Errorf("%q: expected an error", body)
		}
		if len(r.ids) != 0 {
			t.Errorf("%q: Run was called", body)
		}
	}
}

func TestWorkerModePropagatesRunError(t *testing.T) {
	boom := errors.New("final write failed")
	if err := handleWorkerEvent(&recordingRunner{err: boom})(context.Background(), []byte(`{"jobId":"j"}`)); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}

func TestBuildWorkerWiresStoresBlobAndProviderFactory(t *testing.T) {
	cfg := testCfg()
	cfg.Mode = "worker"
	cfg.GenMaxPerCall = 12
	cfg.LLMEffort = "low"
	cfg.LLMTimeoutSeconds = 90
	cfg.GenMaxTotalBytes = 1234
	cfg.GenMaxPDFPages = 7
	cfg.GenMaxInputTokens = 4321
	cfg.LLMMonthlyBudgetUSD = 12.5
	w, err := buildWorker(cfg, anonymous())
	if err != nil {
		t.Fatal(err)
	}
	if w.Jobs == nil || w.Counters == nil || w.Docs == nil || w.Blob == nil || w.Clock == nil || w.Gens == nil {
		t.Fatalf("worker not fully wired: %+v", w)
	}
	want := gen.WorkerConfig{
		MaxPerCall: 12, Effort: "low", PromptVersion: prompt.PromptVersion, CallTimeout: 90 * time.Second,
		MaxTotalBytes: 1234, MaxPDFPages: 7, MaxInputTokens: 4321, BudgetMicros: 12_500_000,
	}
	if w.Cfg != want {
		t.Errorf("cfg = %+v, want %+v", w.Cfg, want)
	}
	// testCfg has no provider keys: the real factory refuses with a plain error naming the variable.
	if _, err := w.Gens("claude-haiku-4-5"); err == nil || errors.Is(err, gen.ErrGeneratorsNotConfigured) || !strings.Contains(err.Error(), "LLM_API_KEY") {
		t.Errorf("factory error = %v", err)
	}
	cfg.LLMAPIKey = "sk-ant-x"
	cfg.Providers = []string{"anthropic", "gemini"}
	cfg.GeminiAPIKey = "gem-x"
	w, err = buildWorker(cfg, anonymous())
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"claude-haiku-4-5", "gemini-3.1-flash-lite"} {
		if g, err := w.Gens(m); err != nil || g == nil {
			t.Errorf("%s: g=%v err=%v", m, g, err)
		}
	}
}

func TestBuildWorkerFailsFastOnMissingConfig(t *testing.T) {
	for name, mutate := range map[string]func(*config.Config){
		"stage":     func(c *config.Config) { c.Stage = "" },
		"bucket":    func(c *config.Config) { c.S3Bucket = "" },
		"providers": func(c *config.Config) { c.Providers = nil },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := testCfg()
			mutate(&cfg)
			if _, err := buildWorker(cfg, anonymous()); err == nil || !strings.Contains(strings.ToLower(err.Error()), name[:4]) {
				t.Errorf("err = %v", err)
			}
		})
	}
}
