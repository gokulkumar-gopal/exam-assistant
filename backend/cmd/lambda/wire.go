package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/lambda"

	"github.com/gokulkumar/exam-assistant/backend/internal/api"
	"github.com/gokulkumar/exam-assistant/backend/internal/auth"
	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/config"
	"github.com/gokulkumar/exam-assistant/backend/internal/docs"
	"github.com/gokulkumar/exam-assistant/backend/internal/exams"
	"github.com/gokulkumar/exam-assistant/backend/internal/gen"
	"github.com/gokulkumar/exam-assistant/backend/internal/store/dynamo"
)

// googleVerifyTimeout bounds one Google ID-token verification. On a cold start the first
// Verify downloads Google's signing certificates; the Function URL answers at most 29 s,
// so the call must give up well before that and return a normal sign-in error.
const googleVerifyTimeout = 10 * time.Second

// newVerifier builds the production Google verifier; tests replace it so nothing touches the network.
var newVerifier = auth.NewIDTokenVerifier

// deadlineVerifier gives every Verify call a context deadline.
type deadlineVerifier struct {
	inner   auth.IDTokenVerifier
	timeout time.Duration
}

func (v deadlineVerifier) Verify(ctx context.Context, credential string) (auth.GoogleClaims, error) {
	ctx, cancel := context.WithTimeout(ctx, v.timeout)
	defer cancel()
	return v.inner.Verify(ctx, credential)
}

// buildAPIDeps wires the real AWS-backed dependencies for MODE=api: DynamoDB users and
// documents tables (<STAGE>-users, <STAGE>-documents), the S3 bucket, auth with the real
// clock, and Google sign-in. It makes no network calls.
func buildAPIDeps(_ context.Context, cfg config.Config, awsCfg aws.Config) (api.Deps, error) {
	clk := clock.Real{}
	models := gen.Enabled(cfg.Providers)
	def, err := gen.DefaultModel(models, cfg.LLMModel)
	if err != nil {
		return api.Deps{}, err
	}
	stores := dynamo.New(dynamodb.NewFromConfig(awsCfg), cfg.Stage)
	svc := auth.NewService(stores, auth.NewBcryptHasher(auth.BcryptCost), clk).
		WithGoogle(deadlineVerifier{inner: newVerifier(cfg.GoogleClientID), timeout: googleVerifyTimeout})
	if cfg.WorkerFunctionName == "" {
		return api.Deps{}, errors.New("WORKER_FUNCTION_NAME is required in api mode")
	}
	var runner gen.JobRunner = gen.LambdaRunner{Client: lambda.NewFromConfig(awsCfg), FunctionName: cfg.WorkerFunctionName}
	deps := api.Deps{
		Logger:         slog.Default(),
		Clock:          clk,
		Users:          stores,
		Auth:           svc,
		Sessions:       auth.NewSessions([]byte(cfg.JWTSecret), cfg.Env),
		Docs:           docs.NewService(stores, docs.NewS3Blob(awsCfg, cfg.S3Bucket), clk),
		Jobs:           stores,
		Exams:          exams.NewService(stores, stores, stores, clk),
		Runner:         runner,
		Gen:            gen.NewService(stores, stores, runner, clk, models, def.ID, cfg.GenLimits()),
		Models:         models,
		DefaultModelID: def.ID,
		AllowedOrigins: cfg.AllowedOrigins,
	}
	if err := validateAPIDeps(deps); err != nil {
		return api.Deps{}, err
	}
	return deps, nil
}

// validateAPIDeps is the startup check: Lambda API mode requires every piece, including
// Google sign-in (unlike the local server, where it is optional).
func validateAPIDeps(d api.Deps) error {
	err := d.Validate()
	if d.Auth != nil && !d.Auth.GoogleEnabled() {
		err = errors.Join(err, errors.New("google sign-in is not configured"))
	}
	return err
}
