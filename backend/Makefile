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

.PHONY: build test run smoke ai-eval build-ApiFunction build-WorkerFunction check-env deploy destroy

build:
	go build ./...

test:
	go test ./...

run:
	@# Build then run the binary: `go run` does not forward SIGTERM to the server it starts.
	go build -o $(SERVER_BIN) ./cmd/server
	exec env $(LOCAL_ENV) $(SERVER_BIN)

smoke:
	@sh scripts/smoke.sh

ai-eval:
	@echo "ai-eval: cmd/ai-eval does not exist yet (added in task B-2.11)." >&2
	@exit 1

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
