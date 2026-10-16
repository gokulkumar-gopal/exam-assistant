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

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		logger.Error("config", "err", err)
		os.Exit(1)
	}

	switch cfg.Mode {
	case "worker":
		lambda.Start(func(ctx context.Context, ev workerEvent) error {
			// The job runner lands in B-2.4; until then the worker only records the event.
			logger.Info("worker event received", "job_id", ev.JobID)
			return nil
		})
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
