package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGetExamOverHTTP(t *testing.T) {
	e := newDeclEnv(t, false)
	e.newExam(t, e.teacher, "get-key-1", `"asha@example.com"`)
	get := func(path string, c *http.Cookie) *httptest.ResponseRecorder {
		return e.do("GET", path, "", "", c, "")
	}
	if rec := get("/exams/get-key-1", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no session: %d", rec.Code)
	}
	rec := get("/exams/get-key-1", e.student)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("assignee: %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	var out struct {
		ServerTime string `json:"serverTime"`
		Exam       struct {
			ID             string
			Title          string
			Type           string
			TotalMarks     int
			AcceptingUntil string
			MaxPeeks       *int
			CreatedBy      map[string]string
			Questions      []map[string]any
		}
		Attempt    map[string]any
		KeyVisible *bool
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Exam.ID != "get-key-1" || out.Exam.Type != "mcq" ||
		out.Exam.AcceptingUntil == "" || out.Exam.MaxPeeks == nil || out.Exam.CreatedBy["name"] != "Tia" ||
		len(out.Exam.CreatedBy) != 2 || len(out.Exam.Questions) != 1 || out.Attempt["status"] != "not_started" ||
		out.KeyVisible == nil || *out.KeyVisible || out.ServerTime != authT0.UTC().Format(time.RFC3339) {
		t.Fatalf("%v %s", err, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "correctIndex") || strings.Contains(rec.Body.String(), "asha@") ||
		strings.Contains(rec.Body.String(), "tia@") {
		t.Fatalf("leak: %s", rec.Body)
	}
	cr := get("/exams/get-key-1", e.teacher)
	if cr.Code != http.StatusOK || !strings.Contains(cr.Body.String(), `"correctIndex":1`) ||
		!strings.Contains(cr.Body.String(), `"attempt":null`) || !strings.Contains(cr.Body.String(), `"keyVisible":true`) {
		t.Fatalf("creator: %d %s", cr.Code, cr.Body)
	}
	// declined, stranger and missing: byte-identical 404
	if rec := e.post("/exams/get-key-1/decline", "", e.student); rec.Code != http.StatusNoContent {
		t.Fatal(rec.Body)
	}
	declined := get("/exams/get-key-1", e.student)
	stranger := get("/exams/get-key-1", e.other)
	missing := get("/exams/no-such-exam", e.other)
	if declined.Code != http.StatusNotFound || stranger.Code != http.StatusNotFound || missing.Code != http.StatusNotFound {
		t.Fatalf("codes %d %d %d", declined.Code, stranger.Code, missing.Code)
	}
	for _, r := range []*httptest.ResponseRecorder{stranger, missing} {
		if r.Body.String() != declined.Body.String() || r.Header().Get("Content-Type") != declined.Header().Get("Content-Type") ||
			r.Header().Get("Cache-Control") != declined.Header().Get("Cache-Control") {
			t.Errorf("404s differ: %q vs %q", r.Body, declined.Body)
		}
	}
	for _, m := range []string{"POST", "PUT", "DELETE"} {
		r := e.do(m, "/exams/get-key-1", "", "application/json", e.teacher, "")
		if r.Code != http.StatusMethodNotAllowed || !strings.Contains(r.Header().Get("Content-Type"), "json") || !strings.Contains(r.Header().Get("Allow"), "GET") {
			t.Errorf("%s: %d %v", m, r.Code, r.Header())
		}
	}
}

// Hidden keys on the exact handler bytes, MCQ and free text, for an assignee and for a creator
// taking their own exam (both have an unfinished attempt row).
func TestGetExamHiddenKeysOverHTTPBytes(t *testing.T) {
	e := newDeclEnv(t, false)
	freeBody := `{"title":"Essay","type":"free","closesInSeconds":3600,"assigneeEmails":["asha@example.com","tia@example.com"],"questions":[` +
		`{"marks":2,"text":"Why?","modelAnswer":"ZZ-SECRET-MODEL","sourceDocId":"d","sourceDocName":"B.pdf"}]}`
	mcqBody := strings.Replace(examBody, `"questions"`, `"assigneeEmails":["asha@example.com","tia@example.com"],"questions"`, 1)
	for id, body := range map[string]string{"leak-mcq-1": mcqBody, "leak-free-1": freeBody} {
		if rec := postExam(e.h, body, id, e.teacher, ""); rec.Code != http.StatusCreated {
			t.Fatalf("%s: %d %s", id, rec.Code, rec.Body)
		}
		for who, c := range map[string]*http.Cookie{"assignee": e.student, "creator taking own": e.teacher} {
			rec := e.do("GET", "/exams/"+id, "", "", c, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("%s %s: %d %s", id, who, rec.Code, rec.Body)
			}
			low := strings.ToLower(rec.Body.String())
			for _, bad := range []string{"correctindex", "modelanswer", "zz-secret-model", `"correct`, "explanation"} {
				if strings.Contains(low, bad) {
					t.Errorf("%s %s: %q in %s", id, who, bad, rec.Body)
				}
			}
			if !strings.Contains(low, `"keyvisible":false`) {
				t.Errorf("%s %s: keyVisible: %s", id, who, rec.Body)
			}
		}
	}
}
