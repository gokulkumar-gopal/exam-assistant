#!/bin/sh
# Smoke test. Health-only stub; the full flow is added in B-4.6.
# Usage: BASE_URL=http://localhost:8080 sh scripts/smoke.sh
set -u

if [ -z "${BASE_URL:-}" ]; then
	echo "usage: BASE_URL=<url> $0   (e.g. make smoke BASE_URL=http://localhost:8080)" >&2
	exit 2
fi
base=${BASE_URL%/}

body=$(curl -sS --connect-timeout 5 --max-time 15 -w '\n%{http_code}' "$base/health") || {
	echo "FAIL: could not reach $base/health" >&2
	exit 1
}
code=${body##*
}
body=${body%
*}

if [ "$code" != "200" ]; then
	echo "FAIL: GET /health returned HTTP $code, want 200" >&2
	exit 1
fi
# Tolerate JSON whitespace.
compact=$(printf '%s' "$body" | tr -d ' \t\r\n')
if [ "$compact" != '{"status":"ok"}' ]; then
	echo "FAIL: GET /health body was '$body', want {\"status\":\"ok\"}" >&2
	exit 1
fi

echo OK
