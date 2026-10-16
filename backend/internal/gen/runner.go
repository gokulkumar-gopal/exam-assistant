package gen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// InProcessRunner runs jobs in a goroutine of the server process (local `make run` and
// tests). Worker.Run applies the 8-minute deadline itself.
type InProcessRunner struct {
	Worker *Worker
	wg     sync.WaitGroup
}

// Start launches Run and returns at once. The goroutine's context is detached from ctx
// (the HTTP request ends right after the 202) but keeps its values.
func (r *InProcessRunner) Start(ctx context.Context, jobID string) error {
	if r.Worker == nil {
		return errors.New("in-process runner has no worker")
	}
	jctx := context.WithoutCancel(ctx)
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer func() {
			if p := recover(); p != nil {
				slog.Error("worker panicked", "job_id", jobID, "panic", fmt.Sprint(p))
				mctx, cancel := context.WithTimeout(context.WithoutCancel(jctx), finishTimeout)
				defer cancel()
				if err := r.Worker.Jobs.MarkFailedIfQueuedOrRunning(mctx, jobID, GenerateFailedMessage); err != nil {
					slog.Error("marking panicked job failed", "job_id", jobID, "err", err)
				}
			}
		}()
		if err := r.Worker.Run(jctx, jobID); err != nil {
			slog.Error("worker run failed", "job_id", jobID, "err", err)
		}
	}()
	return nil
}

// Wait blocks until every started job has returned (tests, graceful shutdown).
func (r *InProcessRunner) Wait() { r.wg.Wait() }

// LambdaInvoker is the part of the AWS Lambda client LambdaRunner uses; *lambda.Client
// satisfies it.
type LambdaInvoker interface {
	Invoke(ctx context.Context, in *lambda.InvokeInput, opts ...func(*lambda.Options)) (*lambda.InvokeOutput, error)
}

// LambdaRunner starts a job by invoking the worker function asynchronously (backend-spec
// 5). Delivery is at-least-once; the worker's claim makes a duplicate harmless.
type LambdaRunner struct {
	Client       LambdaInvoker
	FunctionName string
}

type workerPayload struct {
	JobID string `json:"jobId"`
}

// startTimeout bounds the Invoke call so POST /generate answers within its own limit.
const startTimeout = 10 * time.Second

// Start invokes the worker with {"jobId": ...}. Any error means the job did not start.
func (r LambdaRunner) Start(ctx context.Context, jobID string) error {
	if r.Client == nil || r.FunctionName == "" {
		return errors.New("lambda runner is not configured")
	}
	payload, err := json.Marshal(workerPayload{JobID: jobID})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	out, err := r.Client.Invoke(ctx, &lambda.InvokeInput{
		FunctionName: &r.FunctionName, InvocationType: types.InvocationTypeEvent, Payload: payload,
	})
	if err != nil {
		return fmt.Errorf("invoke worker: %w", err)
	}
	if out == nil {
		return errors.New("invoke worker: empty response")
	}
	if out.StatusCode != 202 {
		return fmt.Errorf("invoke worker: unexpected status %d", out.StatusCode)
	}
	return nil
}
