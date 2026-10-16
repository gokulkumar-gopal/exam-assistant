package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func (e *declEnv) submitReq(body string, c *http.Cookie) *httptest.ResponseRecorder {
	ct := ""
	if body != "" {
		ct = "application/json"
	}
	return e.do("POST", "/exams/as-key-1/submit", body, ct, c, "")
}

func TestSubmitOverHTTP(t *testing.T) {
	e, qid := asSetup(t)
	body := `{"answers":{"` + qid + `":"b"},"lastQuestionIndex":0}`
	if rec := e.submitReq(body, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no session: %d", rec.Code)
	}
	e.clk.Set(authT0.Add(30 * time.Minute))
	rec := e.submitReq(body, e.student)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"status": "submitted", "score": float64(1), "scoreBeforePeeks": float64(1), "maxScore": float64(1),
		"submittedAt": authT0.Add(30 * time.Minute).UTC().Format(time.RFC3339), "autoSubmitted": false,
		"serverTime": authT0.Add(30 * time.Minute).UTC().Format(time.RFC3339)}
	if len(out) != len(want) {
		t.Fatalf("keys: %s", rec.Body)
	}
	for k, v := range want {
		if out[k] != v {
			t.Fatalf("%s = %v, want %v (%s)", k, out[k], v, rec.Body)
		}
	}
	for _, leak := range []string{"correctIndex", "correctAnswer", "modelAnswer"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Fatalf("leak %s", leak)
		}
	}
}

func TestSubmitIdempotentResubmitOverHTTP(t *testing.T) {
	e, qid := asSetup(t)
	e.clk.Set(authT0.Add(10 * time.Minute))
	first := e.submitReq(`{"answers":{"`+qid+`":"b"},"lastQuestionIndex":0}`, e.student)
	e.clk.Set(authT0.Add(20 * time.Minute))
	again := e.submitReq(`{"answers":{"`+qid+`":"a"},"lastQuestionIndex":0}`, e.student)
	if first.Code != 200 || again.Code != 200 {
		t.Fatalf("%d %d", first.Code, again.Code)
	}
	var a, b map[string]any
	_ = json.Unmarshal(first.Body.Bytes(), &a)
	_ = json.Unmarshal(again.Body.Bytes(), &b)
	delete(a, "serverTime")
	delete(b, "serverTime")
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Fatalf("%s vs %s", ja, jb)
	}
}

func TestSubmitEmptyBodyUsesSavedOverHTTP(t *testing.T) {
	e, qid := asSetup(t)
	if rec := e.put(`{"answers":{"`+qid+`":"b"},"lastQuestionIndex":0}`, e.student); rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	rec := e.submitReq("", e.student)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"score":1`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestSubmit404sAreByteIdenticalOverHTTP(t *testing.T) {
	e, qid := asSetup(t)
	body := `{"answers":{"` + qid + `":"b"},"lastQuestionIndex":0}`
	if rec := e.post("/exams/as-key-1/decline", "", e.student); rec.Code != http.StatusNoContent {
		t.Fatal(rec.Body)
	}
	declined := e.submitReq(body, e.student)
	creator := e.submitReq(body, e.teacher)
	stranger := e.submitReq(body, e.other)
	missing := e.do("POST", "/exams/nope/submit", body, "application/json", e.student, "")
	for _, r := range []*httptest.ResponseRecorder{declined, creator, stranger, missing} {
		if r.Code != http.StatusNotFound || r.Body.String() != missing.Body.String() {
			t.Fatalf("%d %s vs %s", r.Code, r.Body, missing.Body)
		}
	}
}

func TestSubmitClosedOverHTTP(t *testing.T) {
	e, qid := asSetup(t)
	if rec := e.put(`{"answers":{"`+qid+`":"b"},"lastQuestionIndex":0}`, e.student); rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	e.clk.Set(authT0.Add(time.Hour + 59*time.Second))
	if rec := e.submitReq(`{"answers":{"`+qid+`":"a"},"lastQuestionIndex":0}`, e.student); rec.Code != 200 {
		t.Fatalf("grace: %d %s", rec.Code, rec.Body)
	}
	// a second attempt row: a fresh exam, never submitted, then past the cutoff
	e2, qid2 := asSetup(t)
	if rec := e2.put(`{"answers":{"`+qid2+`":"b"},"lastQuestionIndex":0}`, e2.student); rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	e2.clk.Set(authT0.Add(time.Hour + 60*time.Second))
	rec := e2.submitReq(`{"answers":{"`+qid2+`":"a"},"lastQuestionIndex":0}`, e2.student)
	if rec.Code != http.StatusConflict || errCode(t, rec) != "exam_closed" {
		t.Fatalf("closed: %d %s", rec.Code, rec.Body)
	}
	got := e2.do("GET", "/exams/as-key-1", "", "", e2.student, "").Body.String()
	if !strings.Contains(got, `"status":"submitted"`) || !strings.Contains(got, `"answers":{"`+qid2+`":"b"}`) {
		t.Fatalf("saved answers should have been auto-submitted: %s", got)
	}
}

func TestSubmitBadRequestsOverHTTP(t *testing.T) {
	e, qid := asSetup(t)
	// 415
	if rec := e.do("POST", "/exams/as-key-1/submit", `{"answers":{}}`, "text/plain", e.student, ""); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("415: %d", rec.Code)
	}
	// CSRF
	if rec := e.do("POST", "/exams/as-key-1/submit", `{}`, "application/json", e.student, "https://evil.example"); rec.Code != http.StatusForbidden {
		t.Errorf("csrf: %d", rec.Code)
	}
	// 405
	rec := e.do("GET", "/exams/as-key-1/submit", "", "", e.student, "")
	if rec.Code != http.StatusMethodNotAllowed || errCode(t, rec) != "method_not_allowed" {
		t.Errorf("405: %d %s", rec.Code, rec.Body)
	}
	for name, body := range map[string]string{
		"unknown field":      `{"answers":{},"x":1}`,
		"not json":           `{`,
		"unknown question":   `{"answers":{"zzz":"b"},"lastQuestionIndex":0}`,
		"null answer":        `{"answers":{"` + qid + `":null},"lastQuestionIndex":0}`,
		"index out of range": `{"answers":{},"lastQuestionIndex":7}`,
		"trailing data":      `{} {}`,
		"oversize body":      `{"answers":{"` + qid + `":"` + strings.Repeat("x", 401<<10) + `"}}`,
	} {
		rec := e.submitReq(body, e.student)
		if rec.Code != http.StatusUnprocessableEntity || errCode(t, rec) != "validation" {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	// none of the rejected calls submitted anything
	if got := e.do("GET", "/exams/as-key-1", "", "", e.student, "").Body.String(); !strings.Contains(got, `"status":"not_started"`) {
		t.Fatalf("%s", got)
	}
}
