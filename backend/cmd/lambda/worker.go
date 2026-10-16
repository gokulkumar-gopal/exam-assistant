package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/config"
	"github.com/gokulkumar/exam-assistant/backend/internal/docs"
	"github.com/gokulkumar/exam-assistant/backend/internal/gen"
	"github.com/gokulkumar/exam-assistant/backend/internal/gen/prompt"
	"github.com/gokulkumar/exam-assistant/backend/internal/store/dynamo"
)

// jobRunner is what the worker handler needs from gen.Worker.
type jobRunner interface {
	Run(ctx context.Context, jobID string) error
}

// handleWorkerEvent decodes the {"jobId": "..."} event LambdaRunner sends and runs the
// job. A malformed event is an error (the platform retries asynchronous events a couple
// of times, then drops them; the job, if any, goes stale and reads as failed).
func handleWorkerEvent(r jobRunner) func(ctx context.Context, payload json.RawMessage) error {
	return func(ctx context.Context, payload json.RawMessage) error {
		var ev workerEvent
		if err := json.Unmarshal(payload, &ev); err != nil {
			return fmt.Errorf("worker event is not valid: %w", err)
		}
		if ev.JobID == "" {
			return errors.New("worker event has no jobId")
		}
		return r.Run(ctx, ev.JobID)
	}
}

// buildWorker wires the MODE=worker dependencies: the DynamoDB stores, the S3 blob and
// the generator factory. It makes no network calls and fails fast when something the
// worker cannot run without is missing. Until the provider generators exist (B-2.5 to
// B-2.7) the factory is gen.UnconfiguredFactory, so every job fails with the generic
// message and the log says "generators not configured".
func buildWorker(cfg config.Config, awsCfg aws.Config) (*gen.Worker, error) {
	var errs []error
	if cfg.Stage == "" {
		errs = append(errs, errors.New("STAGE is required"))
	}
	if cfg.S3Bucket == "" {
		errs = append(errs, errors.New("S3_BUCKET is required"))
	}
	if len(cfg.Providers) == 0 {
		errs = append(errs, errors.New("LLM_PROVIDERS must enable a provider"))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("worker wiring: %w", err)
	}
	stores := dynamo.New(dynamodb.NewFromConfig(awsCfg), cfg.Stage)
	return &gen.Worker{
		Jobs:  stores,
		Docs:  stores,
		Blob:  docs.NewS3Blob(awsCfg, cfg.S3Bucket),
		Gens:  gen.UnconfiguredFactory,
		Clock: clock.Real{},
		Cfg: gen.WorkerConfig{
			MaxPerCall: cfg.GenMaxPerCall, Effort: cfg.LLMEffort, PromptVersion: prompt.PromptVersion,
			CallTimeout: time.Duration(cfg.LLMTimeoutSeconds) * time.Second, MaxTotalBytes: cfg.GenMaxTotalBytes,
			MaxPDFPages: cfg.GenMaxPDFPages, MaxInputTokens: cfg.GenMaxInputTokens,
			BudgetMicros: gen.BudgetMicros(cfg.LLMMonthlyBudgetUSD),
		},
	}, nil
}
