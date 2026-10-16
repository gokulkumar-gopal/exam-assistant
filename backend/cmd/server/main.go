// Command server is the local HTTP server (and container-host entry point).
package main

import (
	"context"
	"errors"
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
	"github.com/gokulkumar/exam-assistant/backend/internal/store/memory"
)

func main() {
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

	// The local server keeps users in memory; the DynamoDB store is wired for deployed stages later.
	users := memory.NewUserStore()
	clk := clock.Real{}
	// The InProcessRunner is wired into Deps here when the generation task adds it.
	svc := auth.NewService(users, auth.NewBcryptHasher(auth.BcryptCost), clk)
	if cfg.GoogleClientID != "" {
		// Without GOOGLE_CLIENT_ID the route is not registered and /auth/google is the JSON 404.
		svc.WithGoogle(auth.NewIDTokenVerifier(cfg.GoogleClientID))
	}
	handler := api.NewRouter(api.Deps{
		Logger:         logger,
		Clock:          clk,
		Users:          users,
		Auth:           svc,
		Sessions:       auth.NewSessions([]byte(cfg.JWTSecret), cfg.Env),
		AllowedOrigins: cfg.AllowedOrigins,
	})

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
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
