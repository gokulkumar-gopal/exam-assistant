# Exam Assistant backend

Go backend for the Exam Assistant. The design is in `../backend-spec.md`; this file only covers running it locally.

## Prerequisites

- Go 1.24 or newer
- `make` and `curl`

## Commands

```sh
make build   # go build ./...
make test    # go test ./...
make run     # start cmd/server on 127.0.0.1:8080 (override with ADDR=)
make smoke BASE_URL=http://127.0.0.1:8080   # check a running backend
make ai-eval # not available until task B-2.11
```

`make run` serves `GET /health`. Override the listen address with `ADDR`, for example `ADDR=:9090 make run`.

## Configuration

All config is read from environment variables by `internal/config` at startup. The process exits on a missing or invalid value.

- `make run` sets fake local values (`ENV=local`, a fake `JWT_SECRET`, and so on) from the `LOCAL_ENV` block in the `Makefile`. They are not secrets and are not valid for any deployed stage.
- Deployment values come from `.env.deploy.<stage>` (`STAGE` defaults to `dev`). The file is optional, so `make test` works on a fresh clone. These files hold secrets and must never be committed; the root `.gitignore` added with the deployment tooling ignores them.

## Test-first rule

Write the failing test first, watch it fail for the right reason, then write the code that makes it pass. Do not add behaviour without a test that covers it.

## Smoke test

`scripts/smoke.sh` needs `BASE_URL`. For now it only checks `/health`; the full sign-up-to-result flow arrives in B-4.6.
