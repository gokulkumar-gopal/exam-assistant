package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gokulkumar/exam-assistant/backend/internal/config"
	"github.com/gokulkumar/exam-assistant/backend/internal/gen"
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

func TestBuildWorkerWiresStoresBlobAndPlaceholderGenerators(t *testing.T) {
	cfg := testCfg()
	cfg.Mode = "worker"
	cfg.GenMaxPerCall = 12
	cfg.LLMEffort = "low"
	w, err := buildWorker(cfg, anonymous())
	if err != nil {
		t.Fatal(err)
	}
	if w.Jobs == nil || w.Docs == nil || w.Blob == nil || w.Clock == nil || w.Gens == nil {
		t.Fatalf("worker not fully wired: %+v", w)
	}
	if w.Cfg.MaxPerCall != 12 || w.Cfg.Effort != "low" {
		t.Errorf("cfg = %+v", w.Cfg)
	}
	if _, err := w.Gens.For("claude-haiku-4-5"); !errors.Is(err, gen.ErrGeneratorsNotConfigured) {
		t.Errorf("placeholder factory error = %v", err)
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
