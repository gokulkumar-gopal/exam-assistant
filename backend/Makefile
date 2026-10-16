# Deployment values live in .env.deploy.<stage>; the file is optional so
# `make test` works on a fresh clone.
STAGE ?= dev
AWS_REGION ?= us-east-1
-include .env.deploy.$(STAGE)
export

# Local env block for `make run` only. Fake values, never real secrets.
# They override the shell for `make run`.
LOCAL_ENV = \
	ENV=local \
	STAGE=local \
	S3_BUCKET=local-bucket \
	JWT_SECRET=local-only-fake-jwt-secret-not-for-deploy \
	GOOGLE_CLIENT_ID=local-fake-client-id.apps.googleusercontent.com \
	ALLOWED_ORIGINS=http://localhost:3000

SERVER_BIN ?= $(or $(TMPDIR),/tmp)/exam-assistant-server
# Local runs use the offline fake generator (no provider generators exist yet). Override with SERVER_FLAGS=.
SERVER_FLAGS ?= -fake-llm

.PHONY: build test run smoke ai-eval build-ApiFunction build-WorkerFunction check-env deploy destroy

build:
	go build ./...

test:
	go test ./...

run:
	@# Build then run the binary: `go run` does not forward SIGTERM to the server it starts.
	go build -o $(SERVER_BIN) ./cmd/server
	exec env $(LOCAL_ENV) $(SERVER_BIN) $(SERVER_FLAGS)

smoke:
	@sh scripts/smoke.sh

# Golden-set run (AI-spec 13.2). Calls the real provider API and spends money: by hand only.
# Needs MODEL=<catalog id> and the provider key (LLM_API_KEY or GEMINI_API_KEY) in the shell
# or .env.deploy.<stage>. Files go in testdata/ai (see its README). Optional: CSV=out.csv,
# MAX_USD=<spend cap, default 5; an Opus run needs e.g. MAX_USD=12; above 15 also needs ALLOW_HIGH_CAP=1>.
ai-eval:
	@test -n "$(MODEL)" || { echo "Usage: make ai-eval MODEL=<catalog id>   (for example MODEL=claude-haiku-4-5)" >&2; exit 1; }
	AI_EVAL=1 go run ./cmd/ai-eval -model "$(MODEL)" $(if $(CSV),-csv "$(CSV)") $(if $(MAX_USD),-max-usd "$(MAX_USD)") $(if $(ALLOW_HIGH_CAP),-allow-high-cap)

# SAM calls these (BuildMethod: makefile) with ARTIFACTS_DIR set; both functions
# are the same binary.
build-ApiFunction build-WorkerFunction:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -tags lambda.norpc -o $(ARTIFACTS_DIR)/bootstrap ./cmd/lambda

# The leading + makes make run this test even under `make -n`.
check-env:
	+@test -f .env.deploy.$(STAGE) || { echo "Missing backend/.env.deploy.$(STAGE). See deployment-spec.md section 8.1."; exit 1; }

deploy: check-env
	sam build
	./scripts/write-samconfig.sh $(STAGE)
	sam deploy --config-file .samconfig.$(STAGE).toml \
	  --stack-name exam-assistant-$(STAGE) --region $(AWS_REGION) \
	  --capabilities CAPABILITY_IAM --resolve-s3 --no-confirm-changeset --no-fail-on-empty-changeset
	@aws cloudformation describe-stacks --stack-name exam-assistant-$(STAGE) --region $(AWS_REGION) \
	  --query "Stacks[0].Outputs" --output table

# Empties the bucket first (a non-empty bucket blocks stack deletion), then deletes the stack.
# STAGE=prod needs CONFIRM=yes. The guard is a make-time $(error), so it also fires under -n.
destroy:
	$(if $(filter prod,$(STAGE)),$(if $(filter yes,$(CONFIRM)),,$(error Refusing to destroy prod. Re-run with CONFIRM=yes to proceed)))aws s3 rm s3://exam-assistant-$(STAGE)-$$(aws sts get-caller-identity --query Account --output text) --recursive
	sam delete --stack-name exam-assistant-$(STAGE) --region $(AWS_REGION)
