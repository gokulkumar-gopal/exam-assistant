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
	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/config"
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
	_ = cfg // consumed by services as later tasks wire them into api.Deps

	// The InProcessRunner is wired into Deps here when the generation task adds it.
	handler := api.NewRouter(api.Deps{Logger: logger, Clock: clock.Real{}})

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
