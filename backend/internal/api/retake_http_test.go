package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func (e *declEnv) retakeReq(c *http.Cookie) *httptest.ResponseRecorder {
	return e.do("POST", "/exams/as-key-1/retake", "", "", c, "")
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%v: %s", err, rec.Body)
	}
	return out
}

func TestRetakeFlowOverHTTP(t *testing.T) {
	e, qid := asSetup(t)
	if rec := e.retakeReq(nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d", rec.Code)
	}
	e.clk.Set(authT0.Add(10 * time.Minute))
	if rec := e.submitReq(`{"answers":{"`+qid+`":"a"},"lastQuestionIndex":0}`, e.student); rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	rec := e.retakeReq(e.student)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	out := decodeBody(t, rec)
	if len(out) != 2 || out["attemptNo"] != float64(2) || out["serverTime"] != authT0.Add(10*time.Minute).UTC().Format(time.RFC3339) {
		t.Fatalf("retake body: %s", rec.Body)
	}
	if again := decodeBody(t, e.retakeReq(e.student)); again["attemptNo"] != float64(2) { // idempotent while unfinished
		t.Fatalf("second retake: %v", again)
	}

	// GET /exams/{id} shows the latest attempt and the count.
	got := decodeBody(t, e.do("GET", "/exams/as-key-1", "", "", e.student, ""))
	att := got["attempt"].(map[string]any)
	if got["attemptCount"] != float64(2) || att["attemptNo"] != float64(2) || att["status"] != "not_started" {
		t.Fatalf("get: %v", got)
	}

	// Autosave + submit attempt 2 with the right answer.
	e.clk.Set(authT0.Add(20 * time.Minute))
	if rec := e.put(`{"answers":{"`+qid+`":"b"},"lastQuestionIndex":0}`, e.student); rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	if rec := e.submitReq(`{"answers":{"`+qid+`":"b"},"lastQuestionIndex":0}`, e.student); rec.Code != 200 {
		t.Fatal(rec.Body)
	}

	// The default result is attempt 2; ?attempt=1 returns attempt 1 with its own answer and marks.
	def := decodeBody(t, e.resultReq(e.student))
	if def["attemptNo"] != float64(2) || def["bestAttemptNo"] != float64(2) || def["score"] != float64(1) {
		t.Fatalf("default result: %v", def)
	}
	atts := def["attempts"].([]any)
	if len(atts) != 2 || atts[0].(map[string]any)["attemptNo"] != float64(1) || atts[0].(map[string]any)["score"] != float64(0) ||
		atts[1].(map[string]any)["score"] != float64(1) || atts[1].(map[string]any)["maxScore"] != float64(1) {
		t.Fatalf("attempts[]: %v", atts)
	}
	rec = e.do("GET", "/exams/as-key-1/result?attempt=1", "", "", e.student, "")
	one := decodeBody(t, rec)
	if rec.Code != 200 || one["attemptNo"] != float64(1) || one["score"] != float64(0) ||
		one["questions"].([]any)[0].(map[string]any)["yourAnswer"] != "a" {
		t.Fatalf("?attempt=1: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "correct") {
		t.Fatalf("the key must stay hidden while the exam is open: %s", rec.Body)
	}
	for q, want := range map[string]int{"?attempt=3": 404, "?attempt=0": 422, "?attempt=x": 422, "?attempt=-1": 422} {
		if rec := e.do("GET", "/exams/as-key-1/result"+q, "", "", e.student, ""); rec.Code != want {
			t.Fatalf("%s: %d %s", q, rec.Code, rec.Body)
		}
	}

	// The list item: one per exam with the count, best score and canRetake.
	list := decodeBody(t, e.do("GET", "/exams?scope=closed", "", "", e.student, ""))["exams"].([]any)
	it := list[0].(map[string]any)
	if len(list) != 1 || it["attemptCount"] != float64(2) || it["score"] != float64(1) || it["canRetake"] != true || it["attemptStatus"] != "submitted" {
		t.Fatalf("list: %v", list)
	}

	// The creator's status: best score, count and history, never answers.
	st := e.do("GET", "/exams/as-key-1/status", "", "", e.teacher, "")
	if st.Code != 200 || strings.Contains(st.Body.String(), `"answers"`) || strings.Contains(st.Body.String(), "yourAnswer") {
		t.Fatalf("status: %d %s", st.Code, st.Body)
	}
	row := decodeBody(t, st)["attempts"].([]any)[0].(map[string]any)
	if row["attemptCount"] != float64(2) || row["score"] != float64(1) || len(row["history"].([]any)) != 2 {
		t.Fatalf("status row: %v", row)
	}
}

func TestRetakeErrorsOverHTTP(t *testing.T) {
	e, qid := asSetup(t)
	e.clk.Set(authT0.Add(10 * time.Minute))
	if rec := e.submitReq(`{"answers":{"`+qid+`":"b"},"lastQuestionIndex":0}`, e.student); rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	// 404: a non-holder and the creator without a row look the same as a missing exam.
	missing := e.do("POST", "/exams/nope/retake", "", "", e.student, "")
	for name, c := range map[string]*http.Cookie{"stranger": e.other, "creator": e.teacher} {
		rec := e.retakeReq(c)
		if rec.Code != http.StatusNotFound || rec.Body.String() != missing.Body.String() {
			t.Fatalf("%s: %d %s vs %s", name, rec.Code, rec.Body, missing.Body)
		}
	}
	// 409 exam_closed at acceptingUntil.
	e.clk.Set(authT0.Add(time.Hour + time.Minute))
	rec := e.retakeReq(e.student)
	if rec.Code != http.StatusConflict || errCode(t, rec) != "exam_closed" {
		t.Fatalf("after the deadline: %d %s", rec.Code, rec.Body)
	}
	list := decodeBody(t, e.do("GET", "/exams?scope=closed", "", "", e.student, ""))["exams"].([]any)
	if it := list[0].(map[string]any); it["canRetake"] != false || it["attemptStatus"] != "submitted" {
		t.Fatalf("list after the deadline: %v", it)
	}
}

func TestRetakeIs404ForADeclinerOverHTTP(t *testing.T) {
	e, _ := asSetup(t)
	if rec := e.post("/exams/as-key-1/decline", "", e.student); rec.Code != http.StatusNoContent {
		t.Fatal(rec.Body)
	}
	missing := e.do("POST", "/exams/nope/retake", "", "", e.student, "")
	if rec := e.retakeReq(e.student); rec.Code != http.StatusNotFound || rec.Body.String() != missing.Body.String() {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}
