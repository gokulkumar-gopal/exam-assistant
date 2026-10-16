package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/gokulkumar/exam-assistant/backend/internal/api"
	"github.com/gokulkumar/exam-assistant/backend/internal/auth"
	"github.com/gokulkumar/exam-assistant/backend/internal/config"
)

type stubVerifier struct{ deadline time.Duration }

func (s *stubVerifier) Verify(ctx context.Context, _ string) (auth.GoogleClaims, error) {
	if d, ok := ctx.Deadline(); ok {
		s.deadline = time.Until(d)
	}
	return auth.GoogleClaims{}, nil
}

func testCfg() config.Config {
	return config.Config{
		Env: "production", Stage: "dev", S3Bucket: "bucket", GoogleClientID: "cid",
		JWTSecret: strings.Repeat("s", 32), Providers: []string{"anthropic"}, AllowedOrigins: []string{"http://localhost:3000"},
	}
}

// anonymous is an AWS config with static fake credentials and no HTTP traffic.
func anonymous() aws.Config {
	return aws.Config{
		Region:      "us-east-1",
		Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) { return aws.Credentials{}, nil }),
	}
}

func TestBuildAPIDepsPopulatesEverything(t *testing.T) {
	stub := &stubVerifier{}
	old := newVerifier
	newVerifier = func(clientID string) auth.IDTokenVerifier {
		if clientID != "cid" {
			t.Errorf("client id = %q", clientID)
		}
		return stub
	}
	defer func() { newVerifier = old }()

	deps, err := buildAPIDeps(context.Background(), testCfg(), anonymous())
	if err != nil {
		t.Fatal(err)
	}
	if err := validateAPIDeps(deps); err != nil {
		t.Fatal(err)
	}
	if _, err := api.NewHandler(deps); err != nil {
		t.Fatal(err)
	}
	if len(deps.Models) != 3 || deps.DefaultModelID != "claude-haiku-4-5" {
		t.Errorf("models = %v default = %q", deps.Models, deps.DefaultModelID)
	}
	if !deps.Auth.GoogleEnabled() || len(deps.AllowedOrigins) != 1 {
		t.Errorf("deps = %+v", deps)
	}
	if deps.Jobs == nil || deps.Gen == nil {
		t.Error("generation dependencies are not wired")
	}
	// Until B-2.4 the runner is the stub, so starting a job fails.
	if err := deps.Runner.Start(context.Background(), "x"); err == nil {
		t.Error("expected the stub runner")
	}
}

func TestDeadlineVerifierBoundsGoogleCall(t *testing.T) {
	stub := &stubVerifier{}
	v := deadlineVerifier{inner: stub, timeout: googleVerifyTimeout}
	if _, err := v.Verify(context.Background(), "tok"); err != nil {
		t.Fatal(err)
	}
	if stub.deadline <= 0 || stub.deadline > googleVerifyTimeout {
		t.Errorf("deadline = %v, want in (0, %v]", stub.deadline, googleVerifyTimeout)
	}
	if googleVerifyTimeout >= 29*time.Second {
		t.Error("timeout must be below the 29 s API limit")
	}
}

func TestValidateAPIDepsFailsOnNilPiece(t *testing.T) {
	old := newVerifier
	newVerifier = func(string) auth.IDTokenVerifier { return &stubVerifier{} }
	defer func() { newVerifier = old }()
	good, err := buildAPIDeps(context.Background(), testCfg(), anonymous())
	if err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*api.Deps){
		"Docs":     func(d *api.Deps) { d.Docs = nil },
		"Jobs":     func(d *api.Deps) { d.Jobs = nil },
		"Gen":      func(d *api.Deps) { d.Gen = nil },
		"Runner":   func(d *api.Deps) { d.Runner = nil },
		"Auth":     func(d *api.Deps) { d.Auth = nil },
		"Users":    func(d *api.Deps) { d.Users = nil },
		"Sessions": func(d *api.Deps) { d.Sessions = nil },
	} {
		d := good
		mut(&d)
		err := validateAPIDeps(d)
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("nil %s: err = %v", name, err)
		}
	}
	// Google is mandatory in Lambda: an Auth service without a verifier fails.
	d := good
	d.Auth = auth.NewService(good.Users, auth.NewBcryptHasher(auth.BcryptCost), good.Clock)
	if err := validateAPIDeps(d); err == nil || !strings.Contains(err.Error(), "google") {
		t.Errorf("no google: %v", err)
	}
}

func TestBuildAPIDepsRejectsDisabledLLMModel(t *testing.T) {
	cfg := testCfg()
	cfg.LLMModel = "gemini-3.1-flash-lite"
	if _, err := buildAPIDeps(context.Background(), cfg, anonymous()); err == nil || !strings.Contains(err.Error(), "LLM_MODEL") {
		t.Fatalf("err = %v", err)
	}
	cfg = testCfg()
	cfg.Providers = nil
	if _, err := buildAPIDeps(context.Background(), cfg, anonymous()); err == nil {
		t.Fatal("no provider must fail")
	}
}
