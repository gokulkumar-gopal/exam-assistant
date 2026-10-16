// Command server is the local HTTP server (and container-host entry point).
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/api"
	"github.com/gokulkumar/exam-assistant/backend/internal/auth"
	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/config"
	"github.com/gokulkumar/exam-assistant/backend/internal/docs"
	"github.com/gokulkumar/exam-assistant/backend/internal/exams"
	"github.com/gokulkumar/exam-assistant/backend/internal/gen"
	"github.com/gokulkumar/exam-assistant/backend/internal/gen/prompt"
	"github.com/gokulkumar/exam-assistant/backend/internal/gen/providers"
	"github.com/gokulkumar/exam-assistant/backend/internal/store/memory"
)

func main() {
	fakeLLM := flag.Bool("fake-llm", false, "generate deterministic fake questions instead of calling a provider (wins over any configured provider key)")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// cmd/server ignores MODE: it always serves HTTP.
	cfg, err := config.Load(func(k string) string {
		if k == "MODE" {
			return "api"
		}
		return os.Getenv(k)
	})
	if err != nil {
		logger.Error("config", "err", err)
		os.Exit(1)
	}

	// The provider keys are only loaded by config in worker mode; the local server runs jobs
	// in-process, so it reads them itself.
	cfg.LLMAPIKey, cfg.GeminiAPIKey = os.Getenv("LLM_API_KEY"), os.Getenv("GEMINI_API_KEY")

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080" // loopback only: the local blob endpoint is unauthenticated
	}
	baseURL := os.Getenv("LOCAL_BASE_URL")
	if baseURL == "" {
		baseURL = baseURLFromAddr(addr)
	}
	handler, err := buildHandler(cfg, logger, baseURL, *fakeLLM)
	if err != nil {
		logger.Error("config", "err", err)
		os.Exit(1)
	}
	srv := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	logger.Info("listening", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server", "err", err)
		os.Exit(1)
	}
}

// pickFactory chooses the generators of the local server. -fake-llm always wins and gives
// gen.FakeGenerator. Otherwise, when an enabled provider has its key (LLM_API_KEY for
// Anthropic, GEMINI_API_KEY for Gemini) it is providers.New(cfg), as in cmd/lambda; with
// none every job fails with the generic message.
func pickFactory(cfg config.Config, logger *slog.Logger, fakeLLM bool) gen.GeneratorFactory {
	switch {
	case fakeLLM:
		return gen.FixedFactory(gen.FakeGenerator{})
	case hasProviderKey(cfg):
		return providers.New(cfg)
	}
	logger.Warn("no generator configured: jobs will fail; set LLM_API_KEY or GEMINI_API_KEY, or start with -fake-llm for offline generation")
	return gen.UnconfiguredFactory
}

func hasProviderKey(cfg config.Config) bool {
	return (slices.Contains(cfg.Providers, gen.ProviderAnthropic) && cfg.LLMAPIKey != "") ||
		(slices.Contains(cfg.Providers, gen.ProviderGemini) && cfg.GeminiAPIKey != "")
}

// newExamService builds the in-memory exam service with the lazy finalizer (backend-spec 9.2).
func newExamService(users *memory.UserStore, clk clock.Clock) *exams.Service {
	attempts := memory.NewAttemptStore()
	svc := exams.NewService(users, memory.NewExamStore(), attempts, clk)
	svc.SetFinalizer(exams.NewFinalizer(attempts))
	return svc
}

// buildHandler wires the local server: in-memory stores, the Docs service on an in-memory
// blob served at /local-blob (baseURL is where browsers reach this server), and the
// in-process job runner. The generator is chosen by pickFactory.
func buildHandler(cfg config.Config, logger *slog.Logger, baseURL string, fakeLLM bool) (http.Handler, error) {
	users := memory.NewUserStore()
	clk := clock.Real{}
	jobs := memory.NewJobStore(clk)
	docStore := memory.NewDocStore(clk)
	blob := newLocalBlob(docs.NewMemBlob(), baseURL)

	gens := pickFactory(cfg, logger, fakeLLM)
	runner := &gen.InProcessRunner{Worker: &gen.Worker{
		Jobs: jobs, Counters: jobs, Docs: docStore, Blob: blob, Gens: gens, Clock: clk,
		Cfg: gen.WorkerConfig{
			MaxPerCall: cfg.GenMaxPerCall, Effort: cfg.LLMEffort, PromptVersion: prompt.PromptVersion,
			CallTimeout: time.Duration(cfg.LLMTimeoutSeconds) * time.Second, MaxTotalBytes: cfg.GenMaxTotalBytes,
			MaxPDFPages: cfg.GenMaxPDFPages, MaxInputTokens: cfg.GenMaxInputTokens,
			BudgetMicros: gen.BudgetMicros(cfg.LLMMonthlyBudgetUSD),
		},
	}}

	svc := auth.NewService(users, auth.NewBcryptHasher(auth.BcryptCost), clk)
	if cfg.GoogleClientID != "" {
		// Without GOOGLE_CLIENT_ID the route is not registered and /auth/google is the JSON 404.
		svc.WithGoogle(auth.NewIDTokenVerifier(cfg.GoogleClientID))
	}
	models := gen.Enabled(cfg.Providers)
	defModel, err := gen.DefaultModel(models, cfg.LLMModel)
	if err != nil {
		return nil, err
	}
	examSvc := newExamService(users, clk)
	apiHandler := api.NewRouter(api.Deps{
		Logger:         logger,
		Clock:          clk,
		Users:          users,
		Auth:           svc,
		Sessions:       auth.NewSessions([]byte(cfg.JWTSecret), cfg.Env),
		Docs:           docs.NewService(docStore, blob, clk),
		Jobs:           jobs,
		Exams:          examSvc,
		Runner:         runner,
		Gen:            gen.NewService(jobs, docStore, runner, clk, models, defModel.ID, cfg.GenLimits()),
		Models:         models,
		DefaultModelID: defModel.ID,
		AllowedOrigins: cfg.AllowedOrigins,
	})
	mux := http.NewServeMux()
	mux.Handle(localBlobPrefix, blob.handler())
	mux.Handle("/", apiHandler)
	return mux, nil
}
