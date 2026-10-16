// Command lambda is the Lambda entry point: MODE=api serves HTTP through the
// Function URL (payload v2), MODE=worker handles a {"jobId": "..."} event.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"

	"github.com/gokulkumar/exam-assistant/backend/internal/api"
	"github.com/gokulkumar/exam-assistant/backend/internal/config"
)

// workerEvent is the payload LambdaRunner sends to the worker.
type workerEvent struct {
	JobID string `json:"jobId"`
}

// loadConfig is the init-time configuration check. On failure it logs one line (the error
// names the variables and providers involved, never a value) and the caller exits, so a
// worker without a key for an enabled provider never starts.
func loadConfig(getenv func(string) string, logger *slog.Logger) (config.Config, error) {
	cfg, err := config.Load(getenv)
	if err != nil {
		logger.Error("startup config invalid", "err", err)
	}
	return cfg, err
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := loadConfig(os.Getenv, logger)
	if err != nil {
		os.Exit(1)
	}

	switch cfg.Mode {
	case "worker":
		awsCfg, err := awsconfig.LoadDefaultConfig(context.Background())
		if err != nil {
			logger.Error("aws config", "err", err)
			os.Exit(1)
		}
		worker, err := buildWorker(cfg, awsCfg)
		if err != nil {
			logger.Error("worker wiring", "err", err)
			os.Exit(1)
		}
		lambda.Start(handleWorkerEvent(worker))
	default:
		awsCfg, err := awsconfig.LoadDefaultConfig(context.Background())
		if err != nil {
			logger.Error("aws config", "err", err)
			os.Exit(1)
		}
		deps, err := buildAPIDeps(context.Background(), cfg, awsCfg)
		if err != nil {
			logger.Error("api wiring", "err", err)
			os.Exit(1)
		}
		deps.Logger = logger
		handler, err := api.NewHandler(deps)
		if err != nil {
			logger.Error("api wiring", "err", err)
			os.Exit(1)
		}
		lambda.Start(httpadapter.NewV2(handler).ProxyWithContext)
	}
}
