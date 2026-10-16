package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// revSetup is asSetup with maxPeeks 1 (an assigned exam defaults to peeking off).
func revSetup(t *testing.T, maxPeeks string) (*declEnv, string) {
	t.Helper()
	e := newDeclEnv(t, false)
	body := strings.Replace(examBody, `"questions"`, `"maxPeeks":`+maxPeeks+`,"assigneeEmails":["asha@example.com"],"questions"`, 1)
	if rec := postExam(e.h, body, "as-key-1", e.teacher, ""); rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	rec := e.do("GET", "/exams/as-key-1", "", "", e.student, "")
	var out struct {
		Exam struct{ Questions []struct{ ID string } }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Exam.Questions) != 1 {
		t.Fatalf("%v %s", err, rec.Body)
	}
	return e, out.Exam.Questions[0].ID
}

func TestRevealTurnedOffOverHTTP(t *testing.T) {
	e, qid := asSetup(t) // assigned, maxPeeks not given: 0
	rec := e.post("/exams/as-key-1/reveal", `{"questionId":"`+qid+`"}`, e.student)
	if rec.Code != http.StatusForbidden || errCode(t, rec) != "forbidden" || !strings.Contains(rec.Body.String(), "Peeking is turned off for this exam.") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestRevealOverHTTP(t *testing.T) {
	e, qid := revSetup(t, "1")
	body := `{"questionId":"` + qid + `"}`
	path := "/exams/as-key-1/reveal"
	if rec := e.post(path, body, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no session: %d", rec.Code)
	}
	if rec := e.do("POST", path, body, "application/json", e.student, "https://evil.example"); rec.Code != http.StatusForbidden {
		t.Errorf("bad origin: %d", rec.Code)
	}
	if rec := e.do("GET", path, "", "", e.student, ""); rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "POST" {
		t.Errorf("GET: %d %q", rec.Code, rec.Header().Get("Allow"))
	}
	for _, bad := range []string{`{"questionId":"` + qid + `","extra":1}`, `{}`, `{"questionId":"nope"}`, `not json`} {
		rec := e.post(path, bad, e.student)
		if rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", bad, rec.Code, rec.Body)
		}
	}
	if rec := e.post(path, `{"questionId":"nope"}`, e.student); rec.Code != 422 || !strings.Contains(rec.Body.String(), `"questionId"`) {
		t.Errorf("unknown question: %d %s", rec.Code, rec.Body)
	}
	rec := e.post(path, body, e.student)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["questionId"] != qid || out["correctIndex"] != float64(1) || out["peeksLeft"] != float64(0) ||
		out["serverTime"] != authT0.UTC().Format(time.RFC3339) || len(out) != 4 {
		t.Fatalf("%s", rec.Body)
	}
	// Repeat is free; resume shows it.
	if rec := e.post(path, body, e.student); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"peeksLeft":0`) {
		t.Fatalf("repeat: %d %s", rec.Code, rec.Body)
	}
	got := e.do("GET", "/exams/as-key-1", "", "", e.student, "").Body.String()
	if !strings.Contains(got, `"revealed":[{"questionId":"`+qid+`","correctIndex":1}]`) || !strings.Contains(got, `"peeksLeft":0`) || !strings.Contains(got, `"status":"in_progress"`) {
		t.Fatalf("resume: %s", got)
	}
}

func TestReveal404sAreByteIdenticalOverHTTP(t *testing.T) {
	e, qid := revSetup(t, "1")
	body := `{"questionId":"` + qid + `"}`
	if rec := e.post("/exams/as-key-1/decline", "", e.student); rec.Code != http.StatusNoContent {
		t.Fatal(rec.Code)
	}
	declined := e.post("/exams/as-key-1/reveal", body, e.student)
	creator := e.post("/exams/as-key-1/reveal", body, e.teacher)
	stranger := e.post("/exams/as-key-1/reveal", body, e.other)
	missing := e.post("/exams/nope/reveal", body, e.student)
	if declined.Code != 404 {
		t.Fatalf("%d %s", declined.Code, declined.Body)
	}
	for _, r := range []string{creator.Body.String(), stranger.Body.String(), missing.Body.String()} {
		if r != declined.Body.String() {
			t.Fatalf("404 bodies differ: %q vs %q", r, declined.Body.String())
		}
	}
}

func TestRevealClosedOverHTTP(t *testing.T) {
	e, qid := revSetup(t, "1")
	e.clk.Set(authT0.Add(time.Hour + 60*time.Second))
	rec := e.post("/exams/as-key-1/reveal", `{"questionId":"`+qid+`"}`, e.student)
	if rec.Code != http.StatusConflict || errCode(t, rec) != "exam_closed" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}
