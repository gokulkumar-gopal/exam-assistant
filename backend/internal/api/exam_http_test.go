package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/gokulkumar/exam-assistant/backend/internal/auth"
	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/exams"
	"github.com/gokulkumar/exam-assistant/backend/internal/store/memory"
)

func newExamEnv(t *testing.T) (http.Handler, *http.Cookie, *memory.ExamStore) {
	t.Helper()
	users := memory.NewUserStore()
	clk := clock.NewFake(authT0)
	es := memory.NewExamStore()
	h := NewRouter(Deps{
		Logger: silentLogger(), Clock: clk, Users: users,
		Auth:           auth.NewService(users, auth.NewBcryptHasher(bcrypt.MinCost), clk),
		Sessions:       auth.NewSessions([]byte(authSecret), "local"),
		Exams:          exams.NewService(users, es, memory.NewAttemptStore(), clk),
		AllowedOrigins: []string{"http://localhost:3000"},
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, jsonReq("POST", "/auth/signup", signupBody))
	c := sessionCookie(rec)
	if c == nil {
		t.Fatalf("signup failed: %s", rec.Body)
	}
	return h, c, es
}

const examBody = `{"title":"Cells","type":"mcq","closesInSeconds":3600,"questions":[` +
	`{"id":"client","marks":1,"text":"Q?","options":["a","b","c","d"],"correctIndex":1,"sourceDocId":"d","sourceDocName":"B.pdf"}]}`

func postExam(h http.Handler, body, key string, c *http.Cookie, origin string) *httptest.ResponseRecorder {
	req := jsonReq("POST", "/exams", body)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if c != nil {
		req.AddCookie(c)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestCreateExam201ThenReplay200(t *testing.T) {
	h, c, es := newExamEnv(t)
	rec := postExam(h, examBody, "11111111-2222-4333-8444-555555555555", c, "http://localhost:3000")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out struct {
		ID            string   `json:"id"`
		UnknownEmails []string `json:"unknownEmails"`
		ServerTime    string   `json:"serverTime"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.ID != "11111111-2222-4333-8444-555555555555" || out.UnknownEmails == nil ||
		out.ServerTime != authT0.UTC().Format(time.RFC3339) {
		t.Fatalf("%s", rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"unknownEmails":[]`) {
		t.Fatalf("unknownEmails must be []: %s", rec.Body)
	}
	if loc := rec.Header().Get("Location"); loc != "/exams/11111111-2222-4333-8444-555555555555" {
		t.Errorf("Location %q", loc)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control %q", cc)
	}
	rec = postExam(h, examBody, "11111111-2222-4333-8444-555555555555", c, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("replay status %d: %s", rec.Code, rec.Body)
	}
	if rec.Code == http.StatusOK && !strings.Contains(rec.Body.String(), `"id":"11111111-2222-4333-8444-555555555555"`) {
		t.Fatalf("replay body: %s", rec.Body)
	}
	x, err := es.GetExam(context.Background(), "11111111-2222-4333-8444-555555555555")
	if err != nil || x.Questions[0].ID == "client" || x.Questions[0].ID == "" {
		t.Fatalf("stored exam %+v err %v", x, err)
	}
}

func TestCreateExamErrorsAndGuards(t *testing.T) {
	h, c, _ := newExamEnv(t)
	if rec := postExam(h, examBody, "", nil, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("no session: %d", rec.Code)
	}
	if rec := postExam(h, examBody, "", c, "https://evil.example"); rec.Code != http.StatusForbidden {
		t.Errorf("bad origin: %d", rec.Code)
	}
	rec := postExam(h, `{"title":"x"}`, "", c, "")
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"fields"`) {
		t.Errorf("validation: %d %s", rec.Code, rec.Body)
	}
	if rec := postExam(h, `{"title":"x","bogus":1}`, "", c, ""); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("unknown field: %d", rec.Code)
	}
	if rec := postExam(h, examBody, "short", c, ""); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad key: %d", rec.Code)
	}
	big := `{"title":"` + strings.Repeat("x", 3<<20) + `"}`
	if rec := postExam(h, big, "", c, ""); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("oversize body: %d", rec.Code)
	}
	for _, m := range []string{"PUT", "DELETE"} {
		req := httptest.NewRequest(m, "/exams", nil)
		req.AddCookie(c)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD, POST" ||
			!strings.Contains(rec.Body.String(), "method_not_allowed") {
			t.Errorf("%s: %d allow=%q %s", m, rec.Code, rec.Header().Get("Allow"), rec.Body)
		}
	}
	// another user's key is a bare 409 that says nothing about the exam
	if rec := postExam(h, examBody, "owned-key-1", c, ""); rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	su := httptest.NewRecorder()
	h.ServeHTTP(su, jsonReq("POST", "/auth/signup", `{"name":"Bo","email":"bo@example.com","password":"correct horse","role":"teacher"}`))
	c2 := sessionCookie(su)
	rec = postExam(h, strings.Replace(examBody, "Cells", "Mine", 1), "owned-key-1", c2, "")
	if rec.Code != http.StatusConflict || strings.Contains(rec.Body.String(), "Cells") ||
		!strings.Contains(rec.Body.String(), `"code":"conflict"`) {
		t.Errorf("other creator: %d %s", rec.Code, rec.Body)
	}
}

func getList(h http.Handler, scope string, c *http.Cookie) *httptest.ResponseRecorder {
	req := jsonReq("GET", "/exams?scope="+scope, "")
	if c != nil {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestListExamsHandler(t *testing.T) {
	h, c, _ := newExamEnv(t)
	if rec := getList(h, "open", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no session: %d", rec.Code)
	}
	if rec := postExam(h, examBody, "", c, ""); rec.Code != http.StatusCreated {
		t.Fatalf("create: %s", rec.Body)
	}
	rec := getList(h, "open", c)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	var out struct {
		ServerTime string           `json:"serverTime"`
		Exams      []map[string]any `json:"exams"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.ServerTime != authT0.UTC().Format(time.RFC3339) || len(out.Exams) != 1 {
		t.Fatalf("%v %s", err, rec.Body)
	}
	if out.Exams[0]["attemptStatus"] != "not_started" || out.Exams[0]["counts"] != nil {
		t.Fatalf("%s", rec.Body)
	}
	rec = getList(h, "created", c)
	if !strings.Contains(rec.Body.String(), `"counts":{"submitted":0,"inProgress":0,"notStarted":1,"declined":0}`) ||
		strings.Contains(rec.Body.String(), "attemptStatus") {
		t.Fatalf("%s", rec.Body)
	}
	rec = getList(h, "closed", c)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"exams":[]`) {
		t.Fatalf("empty list must be []: %s", rec.Body)
	}
	if rec := getList(h, "bogus", c); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"scope"`) {
		t.Errorf("bad scope: %d %s", rec.Code, rec.Body)
	}
	req := jsonReq("DELETE", "/exams?scope=open", "")
	req.AddCookie(c)
	r2 := httptest.NewRecorder()
	h.ServeHTTP(r2, req)
	if r2.Code != http.StatusMethodNotAllowed || !strings.Contains(r2.Header().Get("Content-Type"), "json") || !strings.Contains(r2.Header().Get("Allow"), "GET") {
		t.Errorf("405: %d %v", r2.Code, r2.Header())
	}
}
