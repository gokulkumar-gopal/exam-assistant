#!/bin/sh
# Smoke test (backend-spec 13): sign up, /me, upload a PDF, generate questions, make a practice
# exam, take it and read the result. Needs only curl. Exits 1 at the first failing step.
#
# Usage:   make smoke BASE_URL=http://127.0.0.1:8080
#          BASE_URL=https://api.example.com SMOKE_ORIGIN=https://app.example.com sh scripts/smoke.sh
# SMOKE_ORIGIN is sent as the Origin header on every request and must be in the server's
# ALLOWED_ORIGINS (default: the one `make run` allows).
set -u

if [ -z "${BASE_URL:-}" ]; then
	echo "usage: BASE_URL=<url> $0   (e.g. make smoke BASE_URL=http://localhost:8080)" >&2
	exit 2
fi
base=${BASE_URL%/}
origin=${SMOKE_ORIGIN:-http://localhost:3000}
poll_limit=480 # seconds: 8 minutes for generation
tmp=$(mktemp -d "${TMPDIR:-/tmp}/smoke.XXXXXX") || exit 1
trap 'rm -rf "$tmp"' EXIT INT TERM
jar=$tmp/cookies
step=start

fail() {
	echo "FAIL: $step: $1" >&2
	exit 1
}

# call METHOD PATH [BODY]: sets $code and $resp. Fails the current step when the server is unreachable.
call() {
	if [ $# -ge 3 ]; then
		resp=$(curl -sS --connect-timeout 5 --max-time 30 -X "$1" -b "$jar" -c "$jar" \
			-H "Origin: $origin" -H 'Content-Type: application/json' -d "$3" \
			-w '\n%{http_code}' "$base$2" 2>&1)
	else
		resp=$(curl -sS --connect-timeout 5 --max-time 30 -X "$1" -b "$jar" -c "$jar" \
			-H "Origin: $origin" -w '\n%{http_code}' "$base$2" 2>&1)
	fi
	rc=$?
	[ $rc -eq 0 ] || fail "could not reach $base: $resp"
	code=${resp##*
}
	resp=${resp%
*}
}

# expect CODE: the step fails unless the last call returned CODE.
expect() {
	[ "$code" = "$1" ] || fail "HTTP $code, want $1: $resp"
}

# jstr KEY: first "KEY":"value" string in $resp (compact JSON).
# Go's encoding/json HTML-escapes &, < and > as \u0026, \u003c, \u003e. A presigned
# S3 URL is full of &, so those escapes must be decoded or the URL is unusable.
jstr() {
	printf '%s' "$resp" | sed -n 's/.*"'"$1"'":"\([^"]*\)".*/\1/p' | head -n 1 |
		sed -e 's/\\u0026/\&/g' -e 's/\\u003c/</g' -e 's/\\u003e/>/g'
}

# need NAME VALUE: the step fails when a value that should have been in the response is empty.
need() {
	[ -n "$2" ] || fail "no $1 in response: $resp"
}

ok() {
	echo "ok: $step"
}

# A one-page PDF with a correct xref table, built inline.
make_pdf() {
	o1='1 0 obj
<< /Type /Catalog /Pages 2 0 R >>
endobj
'
	o2='2 0 obj
<< /Type /Pages /Kids [3 0 R] /Count 1 >>
endobj
'
	o3='3 0 obj
<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] >>
endobj
'
	head='%PDF-1.4
'
	off1=${#head}
	off2=$((off1 + ${#o1}))
	off3=$((off2 + ${#o2}))
	xref=$((off3 + ${#o3}))
	{
		printf '%s%s%s%s' "$head" "$o1" "$o2" "$o3"
		printf 'xref\n0 4\n0000000000 65535 f \n'
		printf '%010d 00000 n \n' "$off1" "$off2" "$off3"
		printf 'trailer\n<< /Size 4 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n' "$xref"
	} >"$tmp/doc.pdf"
}

step=health
call GET /health
expect 200
ok

step=signup
email="smoke-$(date +%s)-$$@example.com"
call POST /auth/signup "{\"name\":\"Smoke Test\",\"email\":\"$email\",\"password\":\"smoke-pass-123\",\"role\":\"student\"}"
expect 201
ok

step=me
call GET /me
expect 200
case $resp in *"\"email\":\"$email\""*) ;; *) fail "signed-in user is not $email: $resp" ;; esac
ok

step=upload-url
make_pdf
size=$(wc -c <"$tmp/doc.pdf" | tr -d ' ')
call POST /documents/upload-url "{\"name\":\"smoke.pdf\",\"type\":\"application/pdf\",\"sizeBytes\":$size}"
expect 201
docid=$(jstr docId)
need docId "$docid"
upload=$(jstr uploadUrl)
need uploadUrl "$upload"
ok

step=upload-put
# The presigned URL carries its own auth: no cookie, no Origin. Content-Type is signed into it.
put=$(curl -sS --connect-timeout 5 --max-time 60 -X PUT -H 'Content-Type: application/pdf' \
	--data-binary "@$tmp/doc.pdf" -o /dev/null -w '%{http_code}' "$upload" 2>&1) || fail "$put"
case $put in 2??) ;; *) fail "HTTP $put, want 2xx from the presigned URL" ;; esac
ok

step=complete
call POST "/documents/$docid/complete" '{}'
expect 200
ok

step=list-documents
call GET /documents
expect 200
case $resp in *"\"id\":\"$docid\""*) ;; *) fail "document $docid not in the list: $resp" ;; esac
ok

step=generate
call POST /generate "{\"sourceIds\":[\"$docid\"],\"type\":\"mcq\",\"counts\":{\"1\":2,\"2\":1},\"shuffle\":false}"
expect 202
job=$(jstr jobId)
need jobId "$job"
ok

step=generate-poll
waited=0
while :; do
	call GET "/generate/$job"
	expect 200
	status=$(jstr status)
	case $status in
	done) break ;;
	failed | cancelled) fail "job $status: $resp" ;;
	esac
	[ $waited -lt $poll_limit ] || fail "job still '$status' after $((poll_limit / 60)) minutes"
	sleep 2
	waited=$((waited + 2))
done
# Keep the questions array for the exam, ids included: the server ignores client ids and mints its own.
questions=$(printf '%s' "$resp" | sed -n 's/.*"questions":\(\[{.*}\]\),"[A-Za-z]*":.*/\1/p')
case $questions in "[{"*) ;; *) fail "done but no questions: $resp" ;; esac
ok

step=create-exam
call POST /exams "{\"title\":\"Smoke practice exam\",\"type\":\"mcq\",\"closesInSeconds\":3600,\"questions\":$questions}"
expect 201
exam=$(jstr id)
need id "$exam"
ok

step=list-open
# The open-scope listing reads an index that can lag a moment: retry for up to 10 seconds.
tries=0
while :; do
	call GET "/exams?scope=open"
	expect 200
	case $resp in *"\"id\":\"$exam\""*) break ;; esac
	tries=$((tries + 1))
	[ $tries -lt 10 ] || fail "exam $exam not in the open list after 10 s: $resp"
	sleep 1
done
ok

step=exam-detail
call GET "/exams/$exam"
expect 200
qid=$(printf '%s' "$resp" | sed -n 's/.*"questions":\[{"id":"\([^"]*\)".*/\1/p')
need "question id" "$qid"
ok

step=autosave
call PUT "/exams/$exam/attempt" "{\"answers\":{\"$qid\":\"Option A\"},\"lastQuestionIndex\":0}"
expect 200
ok

step=submit
call POST "/exams/$exam/submit" "{\"answers\":{\"$qid\":\"Option A\"},\"lastQuestionIndex\":0}"
expect 200
case $resp in *'"status":"submitted"'*) ;; *) fail "status is not submitted: $resp" ;; esac
ok

step=result
call GET "/exams/$exam/result"
expect 200
case $resp in *'"status":"submitted"'*'"maxScore":'*) ;; *) fail "result is missing status or maxScore: $resp" ;; esac
ok

step=retake
call POST "/exams/$exam/retake"
expect 200
case $resp in *'"attemptNo":2'*) ;; *) fail "retake did not start attempt 2: $resp" ;; esac
ok

step=retake-attempt-2
call PUT "/exams/$exam/attempt" "{\"answers\":{\"$qid\":\"Option B\"},\"lastQuestionIndex\":0}"
expect 200
call POST "/exams/$exam/submit" "{\"answers\":{\"$qid\":\"Option B\"},\"lastQuestionIndex\":0}"
expect 200
case $resp in *'"status":"submitted"'*) ;; *) fail "attempt 2 is not submitted: $resp" ;; esac
ok

step=result-attempt-1
call GET "/exams/$exam/result?attempt=1"
expect 200
case $resp in *'"attemptNo":1,'*'"yourAnswer":"Option A"'*) ;; *) fail "attempt 1 result is missing its attemptNo or its own answer: $resp" ;; esac
ok

step=result-attempt-2
call GET "/exams/$exam/result?attempt=2"
expect 200
case $resp in *'"attemptNo":2,'*'"yourAnswer":"Option B"'*) ;; *) fail "attempt 2 result is missing its attemptNo or its own answer: $resp" ;; esac
# attempts[] lists both attempts, oldest first.
case $resp in *'"attempts":[{"attemptNo":1,'*'{"attemptNo":2,'*) ;; *) fail "result attempts[] does not list both attempts: $resp" ;; esac
ok

echo "all steps passed"
