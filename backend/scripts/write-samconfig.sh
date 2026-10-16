#!/bin/sh
# usage: write-samconfig.sh <stage>   (variables come from .env.deploy.<stage> via make)
# Writes backend/.samconfig.<stage>.toml (mode 600). Secrets never reach a command line or stdout.
set -eu
umask 077

if [ "$#" -ne 1 ]; then echo "usage: write-samconfig.sh <stage>  (dev or prod)" >&2; exit 2; fi
stage="$1"
case "$stage" in
  dev|prod) ;;
  *) echo "stage must be dev or prod" >&2; exit 2 ;;
esac
dir=$(cd "$(dirname "$0")/.." && pwd)
out="$dir/.samconfig.$stage.toml"

need() { # need VAR: fail with a named message when unset or empty
  eval "v=\${$1:-}"
  [ -n "$v" ] || { echo "$1 is required" >&2; exit 1; }
}
# Values go into a TOML string holding space-separated Key=Value pairs, so a quote,
# backslash or whitespace inside a value would corrupt the file or split a parameter.
safe() {
  eval "v=\${$1:-}"
  case "$v" in
    *[[:space:]\"\\]*) echo "$1 must not contain whitespace, quotes or backslashes" >&2; exit 1 ;;
  esac
}

need JWT_SECRET
need GOOGLE_CLIENT_ID
need ALLOWED_ORIGINS
providers="${LLM_PROVIDERS:-anthropic}"
case ",$providers," in *,anthropic,*) [ -n "${LLM_API_KEY:-}" ] || { echo "LLM_API_KEY is required when anthropic is enabled" >&2; exit 1; } ;; esac
case ",$providers," in *,gemini,*)    [ -n "${GEMINI_API_KEY:-}" ] || { echo "GEMINI_API_KEY is required when gemini is enabled" >&2; exit 1; } ;; esac
for n in JWT_SECRET GOOGLE_CLIENT_ID ALLOWED_ORIGINS LLM_API_KEY GEMINI_API_KEY LLM_MODEL \
         API_RESERVED_CONCURRENCY WORKER_RESERVED_CONCURRENCY; do safe "$n"; done
case "$providers" in *[[:space:]\"\\]*) echo "LLM_PROVIDERS must not contain whitespace, quotes or backslashes" >&2; exit 1 ;; esac

extra=""
if [ -n "${LLM_API_KEY:-}" ]; then extra="$extra LlmApiKey=$LLM_API_KEY"; fi
if [ -n "${GEMINI_API_KEY:-}" ]; then extra="$extra GeminiApiKey=$GEMINI_API_KEY"; fi
if [ -n "${LLM_MODEL:-}" ]; then extra="$extra LlmModel=$LLM_MODEL"; fi
if [ -n "${API_RESERVED_CONCURRENCY:-}" ]; then extra="$extra ApiReservedConcurrency=$API_RESERVED_CONCURRENCY"; fi
if [ -n "${WORKER_RESERVED_CONCURRENCY:-}" ]; then extra="$extra WorkerReservedConcurrency=$WORKER_RESERVED_CONCURRENCY"; fi

# Write to a temp file in the same directory, then rename, so a failure never leaves a partial file.
tmp=$(mktemp "$dir/.samconfig.$stage.XXXXXX")
trap 'rm -f "$tmp"' EXIT
{
  echo 'version = 0.1'
  echo '[default.deploy.parameters]'
  # Values containing commas are wrapped in \" so SAM keeps each as one parameter.
  printf 'parameter_overrides = "Stage=%s JwtSecret=%s LlmProviders=\\"%s\\" GoogleClientId=%s AllowedOrigins=\\"%s\\" FrontendOrigins=\\"%s\\"%s"\n' \
    "$stage" "$JWT_SECRET" "$providers" "$GOOGLE_CLIENT_ID" "$ALLOWED_ORIGINS" "$ALLOWED_ORIGINS" "$extra"
} > "$tmp"
mv "$tmp" "$out"
trap - EXIT
echo "wrote $out"
