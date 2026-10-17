package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// asSetup creates exam "as-key-1" assigned to Asha and returns its question id.
func asSetup(t *testing.T) (*declEnv, string) {
	t.Helper()
	e := newDeclEnv(t, false)
	e.newExam(t, e.teacher, "as-key-1", `"asha@example.com"`)
	rec := e.do("GET", "/exams/as-key-1", "", "", e.student, "")
	var out struct {
		Exam struct{ Questions []struct{ ID string } }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Exam.Questions) != 1 {
		t.Fatalf("%v %s", err, rec.Body)
	}
	return e, out.Exam.Questions[0].ID
}

func (e *declEnv) put(body string, c *http.Cookie) *httptest.ResponseRecorder {
	return e.do("PUT", "/exams/as-key-1/attempt", body, "application/json", c, "")
}

func TestAutosaveOverHTTP(t *testing.T) {
	e, qid := asSetup(t)
	body := `{"answers":{"` + qid + `":"b"},"lastQuestionIndex":0}`
	if rec := e.put(body, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no session: %d", rec.Code)
	}
	rec := e.put(body, e.student)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	var out map[string]string
	want := authT0.UTC().Format(time.RFC3339)
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out["savedAt"] != want || out["serverTime"] != want || len(out) != 2 {
		t.Fatalf("%v %s", err, rec.Body)
	}
	got := e.do("GET", "/exams/as-key-1", "", "", e.student, "").Body.String()
	if !strings.Contains(got, `"answers":{"`+qid+`":"b"}`) || !strings.Contains(got, `"status":"in_progress"`) {
		t.Fatalf("not persisted: %s", got)
	}
}

func TestAutosave404sAreByteIdenticalOverHTTP(t *testing.T) {
	e, qid := asSetup(t)
	body := `{"answers":{"` + qid + `":"b"},"lastQuestionIndex":0}`
	if rec := e.post("/exams/as-key-1/decline", "", e.student); rec.Code != http.StatusNoContent {
		t.Fatal(rec.Body)
	}
	declined := e.put(body, e.student)
	creator := e.put(body, e.teacher)
	stranger := e.put(body, e.other)
	missing := e.do("PUT", "/exams/nope/attempt", body, "application/json", e.student, "")
	for _, r := range []*httptest.ResponseRecorder{declined, creator, stranger, missing} {
		if r.Code != http.StatusNotFound || r.Body.String() != missing.Body.String() {
			t.Fatalf("%d %s vs %s", r.Code, r.Body, missing.Body)
		}
	}
}

func TestAutosaveClosedAndSubmittedOverHTTP(t *testing.T) {
	e, qid := asSetup(t)
	body := `{"answers":{"` + qid + `":"b"},"lastQuestionIndex":0}`
	e.clk.Set(authT0.Add(time.Hour + 59*time.Second)) // inside the grace window
	if rec := e.put(body, e.student); rec.Code != http.StatusOK {
		t.Fatalf("grace: %d %s", rec.Code, rec.Body)
	}
	e.clk.Set(authT0.Add(time.Hour + 60*time.Second))
	if rec := e.put(body, e.student); rec.Code != http.StatusConflict || errCode(t, rec) != "exam_closed" {
		t.Fatalf("closed: %d %s", rec.Code, rec.Body)
	}

	e2, qid2 := asSetup(t)
	rows, _ := e2.attempts.ListByExam(t.Context(), "as-key-1")
	if len(rows) != 1 {
		t.Fatal(rows)
	}
	if err := e2.attempts.Save(t.Context(), "as-key-1", rows[0].UserID, 1, map[string]string{}, 0, authT0); err != nil {
		t.Fatal(err)
	}
	if err := e2.attempts.Submit(t.Context(), store.Attempt{AttemptNo: 1, ExamID: "as-key-1", UserID: rows[0].UserID,
		Status: store.AttemptSubmitted, Answers: map[string]string{}, Revealed: []string{}}); err != nil {
		t.Fatal(err)
	}
	rec := e2.put(`{"answers":{"`+qid2+`":"b"},"lastQuestionIndex":0}`, e2.student)
	if rec.Code != http.StatusConflict || errCode(t, rec) != "conflict" {
		t.Fatalf("submitted: %d %s", rec.Code, rec.Body)
	}
}

func TestAutosaveValidationOverHTTP(t *testing.T) {
	e, qid := asSetup(t)
	cases := map[string]string{
		"unknown id":    `{"answers":{"zzz":"b"},"lastQuestionIndex":0}`,
		"non-string":    `{"answers":{"` + qid + `":3},"lastQuestionIndex":0}`,
		"null answers":  `{"answers":null,"lastQuestionIndex":0}`,
		"index range":   `{"answers":{},"lastQuestionIndex":1}`,
		"index float":   `{"answers":{},"lastQuestionIndex":0.5}`,
		"unknown field": `{"answers":{},"lastQuestionIndex":0,"x":1}`,
		"not json":      `nope`,
		"long value":    `{"answers":{"` + qid + `":"` + strings.Repeat("a", 5001) + `"},"lastQuestionIndex":0}`,
	}
	for name, b := range cases {
		if rec := e.put(b, e.student); rec.Code != http.StatusUnprocessableEntity || errCode(t, rec) != "validation" {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	if rec := e.put(`{"answers":{"`+qid+`":"`+strings.Repeat("a", 5000)+`"},"lastQuestionIndex":0}`, e.student); rec.Code != http.StatusOK {
		t.Errorf("5000: %d %s", rec.Code, rec.Body)
	}
}

// TestAutosaveBodyCapBoundary: the cap is 400 KiB (409600 bytes). A body of exactly that size is
// accepted, one byte more is 422 validation (backend-spec 8.0 maps oversize to 422, never 413).
// The bodies are valid JSON padded with trailing whitespace to the exact size.
func TestAutosaveBodyCapBoundary(t *testing.T) {
	e, qid := asSetup(t)
	core := `{"answers":{"` + qid + `":"x"},"lastQuestionIndex":0}`
	padTo := func(n int) string { return core + strings.Repeat(" ", n-len(core)) }
	if rec := e.put(padTo(400<<10), e.student); rec.Code != http.StatusOK {
		t.Fatalf("at cap: %d %s", rec.Code, rec.Body)
	}
	rec := e.put(padTo(400<<10+1), e.student)
	if rec.Code != http.StatusUnprocessableEntity || errCode(t, rec) != "validation" || !strings.Contains(rec.Body.String(), "too large") {
		t.Fatalf("cap+1: %d %s", rec.Code, rec.Body)
	}
	if rec := e.put(`{"answers":{"`+qid+`":"`+strings.Repeat("a", 5000)+`"},"lastQuestionIndex":0}`, e.student); rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestAutosaveStrictFieldsOverHTTP(t *testing.T) {
	e, qid := asSetup(t)
	for name, b := range map[string]string{
		"missing index": `{"answers":{}}`,
		"null index":    `{"answers":{},"lastQuestionIndex":null}`,
		"null value":    `{"answers":{"` + qid + `":null},"lastQuestionIndex":0}`,
		"number value":  `{"answers":{"` + qid + `":1},"lastQuestionIndex":0}`,
		"empty body":    ``,
	} {
		rec := e.put(b, e.student)
		if rec.Code != http.StatusUnprocessableEntity || errCode(t, rec) != "validation" || rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: %d %v %s", name, rec.Code, rec.Header(), rec.Body)
		}
	}
	rec := e.put(`{"answers":{}}`, e.student)
	if !strings.Contains(rec.Body.String(), `"lastQuestionIndex"`) {
		t.Errorf("missing index must name the field: %s", rec.Body)
	}
	long := e.put(`{"answers":{"`+qid+`":"`+strings.Repeat("a", 5001)+`"},"lastQuestionIndex":0}`, e.student)
	if !strings.Contains(long.Body.String(), `"answers[`+qid+`]"`) {
		t.Errorf("too-long answer must name its question: %s", long.Body)
	}
}

func TestAutosaveTransportRulesOverHTTP(t *testing.T) {
	e, qid := asSetup(t)
	body := `{"answers":{"` + qid + `":"b"},"lastQuestionIndex":0}`
	if rec := e.do("PUT", "/exams/as-key-1/attempt", body, "text/plain", e.student, ""); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("415: %d", rec.Code)
	}
	if rec := e.do("PUT", "/exams/as-key-1/attempt", body, "application/json", e.student, "https://evil.example"); rec.Code != http.StatusForbidden {
		t.Errorf("csrf: %d", rec.Code)
	}
	if rec := e.do("PUT", "/exams/as-key-1/attempt", body, "application/json", e.student, "http://localhost:3000"); rec.Code != http.StatusOK {
		t.Errorf("allowed origin: %d", rec.Code)
	}
	if rec := e.do("PUT", "/exams/as-key-1/attempt", body, "", e.student, ""); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("missing content type: %d", rec.Code)
	}
	for _, m := range []string{"POST", "PATCH", "DELETE", "GET", "HEAD"} {
		rec := e.do(m, "/exams/as-key-1/attempt", "", "", e.student, "")
		if rec.Code != http.StatusMethodNotAllowed || !strings.Contains(rec.Header().Get("Allow"), "PUT") ||
			rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: %d %v", m, rec.Code, rec.Header())
		}
		if m != "HEAD" && errCode(t, rec) != "method_not_allowed" {
			t.Errorf("%s: %s", m, rec.Body)
		}
	}
	if rec := e.put(body, e.student); rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("no-store")
	}
}
