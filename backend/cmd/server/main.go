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
	"syscall"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/api"
	"github.com/gokulkumar/exam-assistant/backend/internal/auth"
	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/config"
	"github.com/gokulkumar/exam-assistant/backend/internal/docs"
	"github.com/gokulkumar/exam-assistant/backend/internal/gen"
	"github.com/gokulkumar/exam-assistant/backend/internal/store/memory"
)

func main() {
	fakeLLM := flag.Bool("fake-llm", false, "generate deterministic fake questions instead of calling a provider (the only generator until the provider generators exist)")
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

// buildHandler wires the local server: in-memory stores, the Docs service on an in-memory
// blob served at /local-blob (baseURL is where browsers reach this server), and the
// in-process job runner. With fakeLLM the generator is gen.FakeGenerator; without it
// every job fails with the generic message because no provider generator exists yet.
func buildHandler(cfg config.Config, logger *slog.Logger, baseURL string, fakeLLM bool) (http.Handler, error) {
	users := memory.NewUserStore()
	clk := clock.Real{}
	jobs := memory.NewJobStore(clk)
	docStore := memory.NewDocStore(clk)
	blob := newLocalBlob(docs.NewMemBlob(), baseURL)

	var gens gen.GeneratorFactory = gen.UnconfiguredFactory{}
	if fakeLLM {
		gens = gen.FixedFactory{Generator: gen.FakeGenerator{}}
	} else {
		logger.Warn("no generator configured: jobs will fail; start with -fake-llm for offline generation")
	}
	runner := &gen.InProcessRunner{Worker: &gen.Worker{
		Jobs: jobs, Docs: docStore, Blob: blob, Gens: gens, Clock: clk,
		Cfg: gen.WorkerConfig{MaxPerCall: cfg.GenMaxPerCall, Effort: cfg.LLMEffort},
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
	apiHandler := api.NewRouter(api.Deps{
		Logger:         logger,
		Clock:          clk,
		Users:          users,
		Auth:           svc,
		Sessions:       auth.NewSessions([]byte(cfg.JWTSecret), cfg.Env),
		Docs:           docs.NewService(docStore, blob, clk),
		Jobs:           jobs,
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
