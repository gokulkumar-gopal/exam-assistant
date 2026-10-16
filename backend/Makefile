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

.PHONY: build test run smoke ai-eval

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
