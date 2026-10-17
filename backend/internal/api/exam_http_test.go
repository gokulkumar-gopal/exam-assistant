package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/gokulkumar/exam-assistant/backend/internal/auth"
	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/exams"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
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

// Questions edited from generation output carry the generated id and display fields: all are
// accepted, ids are replaced, and any other unknown question field is still a 422.
func TestCreateExamQuestionEchoAndStrictness(t *testing.T) {
	h, c, es := newExamEnv(t)
	key := "22222222-3333-4444-8555-666666666666"
	if rec := postExam(h, examBody, key, c, ""); rec.Code != http.StatusCreated {
		t.Fatalf("echoed id: %d %s", rec.Code, rec.Body)
	}
	x, err := es.GetExam(context.Background(), key)
	if err != nil || x.Questions[0].ID == "client" || x.Questions[0].ID == "" {
		t.Fatalf("stored %+v err %v", x, err)
	}
	bad := strings.Replace(examBody, `"id":"client",`, `"id":"client","bogus":1,`, 1)
	if rec := postExam(h, bad, "", c, ""); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("unknown question field: %d", rec.Code)
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

func postJSON(h http.Handler, path, body string, c *http.Cookie) *httptest.ResponseRecorder {
	req := jsonReq("POST", path, body)
	if c != nil {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestDeclineAndAssignHandlers(t *testing.T) {
	h, c, _ := newExamEnv(t) // c: student Asha
	teacher := httptest.NewRecorder()
	h.ServeHTTP(teacher, jsonReq("POST", "/auth/signup", `{"name":"Tia","email":"tia@example.com","password":"correct horse","role":"teacher"}`))
	tc := sessionCookie(teacher)
	body := strings.Replace(examBody, `"questions"`, `"assigneeEmails":["asha@example.com"],"questions"`, 1)
	rec := postExam(h, body, "assign-key-1", tc, "")
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	// auth, CSRF, methods
	if rec := postJSON(h, "/exams/assign-key-1/decline", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("decline no session: %d", rec.Code)
	}
	if rec := postJSON(h, "/exams/assign-key-1/assign", `{"emails":["x@y.com"]}`, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("assign no session: %d", rec.Code)
	}
	req := jsonReq("POST", "/exams/assign-key-1/decline", "")
	req.AddCookie(c)
	req.Header.Set("Origin", "https://evil.example")
	bad := httptest.NewRecorder()
	h.ServeHTTP(bad, req)
	if bad.Code != http.StatusForbidden {
		t.Errorf("bad origin: %d", bad.Code)
	}
	for _, p := range []string{"/exams/assign-key-1/decline", "/exams/assign-key-1/assign"} {
		req := httptest.NewRequest("GET", p, nil)
		req.AddCookie(c)
		r := httptest.NewRecorder()
		h.ServeHTTP(r, req)
		if r.Code != http.StatusMethodNotAllowed || r.Header().Get("Allow") != "POST" || !strings.Contains(r.Body.String(), "method_not_allowed") {
			t.Errorf("GET %s: %d allow=%q %s", p, r.Code, r.Header().Get("Allow"), r.Body)
		}
	}
	// assign: creator only (foreign 404), 422, 200 with arrays
	if rec := postJSON(h, "/exams/assign-key-1/assign", `{"emails":["x@y.com"]}`, c); rec.Code != http.StatusNotFound {
		t.Errorf("assign by non-creator: %d", rec.Code)
	}
	if rec := postJSON(h, "/exams/assign-key-1/assign", `{"emails":[]}`, tc); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("assign empty: %d", rec.Code)
	}
	rec = postJSON(h, "/exams/assign-key-1/assign", `{"emails":["asha@example.com","ghost@example.com"]}`, tc)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"assigned":[]`) ||
		!strings.Contains(rec.Body.String(), `"alreadyAssigned":["asha@example.com"]`) ||
		!strings.Contains(rec.Body.String(), `"unknown":["ghost@example.com"]`) {
		t.Errorf("assign: %d %s", rec.Code, rec.Body)
	}
	// decline: 404 for non-assignee and missing, 204 twice
	if rec := postJSON(h, "/exams/assign-key-1/decline", "", tc); rec.Code != http.StatusNotFound {
		t.Errorf("decline by creator: %d", rec.Code)
	}
	if rec := postJSON(h, "/exams/missing-exam/decline", "", c); rec.Code != http.StatusNotFound {
		t.Errorf("decline missing: %d", rec.Code)
	}
	for i := 0; i < 2; i++ {
		if rec := postJSON(h, "/exams/assign-key-1/decline", "", c); rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
			t.Errorf("decline %d: %d %s", i, rec.Code, rec.Body)
		}
	}
}

type declEnv struct {
	h        http.Handler
	clk      *clock.Fake
	attempts *memory.AttemptStore
	student  *http.Cookie // Asha (student)
	teacher  *http.Cookie // Tia (teacher)
	other    *http.Cookie // Bo (teacher, unrelated)
}

func newDeclEnv(t *testing.T, _ bool) *declEnv {
	t.Helper()
	users := memory.NewUserStore()
	clk := clock.NewFake(authT0)
	at := memory.NewAttemptStore()
	esvc := exams.NewService(users, memory.NewExamStore(), at, clk)
	esvc.SetFinalizer(exams.NewFinalizer(at))
	h := NewRouter(Deps{
		Logger: silentLogger(), Clock: clk, Users: users,
		Auth:           auth.NewService(users, auth.NewBcryptHasher(bcrypt.MinCost), clk),
		Sessions:       auth.NewSessions([]byte(authSecret), "local"),
		Exams:          esvc,
		AllowedOrigins: []string{"http://localhost:3000"},
	})
	signup := func(name, email, role string) *http.Cookie {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, jsonReq("POST", "/auth/signup", `{"name":"`+name+`","email":"`+email+`","password":"correct horse","role":"`+role+`"}`))
		c := sessionCookie(rec)
		if c == nil {
			t.Fatal(rec.Body)
		}
		return c
	}
	e := &declEnv{h: h, clk: clk, attempts: at}
	e.student = signup("Asha", "asha@example.com", "student")
	e.teacher = signup("Tia", "tia@example.com", "teacher")
	e.other = signup("Bo", "bo@example.com", "teacher")
	return e
}

func (e *declEnv) newExam(t *testing.T, c *http.Cookie, key, assignees string) {
	t.Helper()
	body := examBody
	if assignees != "" {
		body = strings.Replace(examBody, `"questions"`, `"assigneeEmails":[`+assignees+`],"questions"`, 1)
	}
	if rec := postExam(e.h, body, key, c, ""); rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
}

func (e *declEnv) do(method, path, body, ctype string, c *http.Cookie, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if c != nil {
		req.AddCookie(c)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *declEnv) post(path, body string, c *http.Cookie) *httptest.ResponseRecorder {
	return e.do("POST", path, body, "application/json", c, "")
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var b struct {
		Error struct {
			Code    string            `json:"code"`
			Message string            `json:"message"`
			Fields  map[string]string `json:"fields"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("%v: %s", err, rec.Body)
	}
	return b.Error.Code
}

func TestDeclineConflictsOverHTTP(t *testing.T) {
	e := newDeclEnv(t, false)
	e.newExam(t, e.teacher, "decl-key-1", `"asha@example.com"`)
	e.newExam(t, e.teacher, "decl-key-2", `"asha@example.com"`)
	e.newExam(t, e.teacher, "decl-key-3", `"asha@example.com"`)
	ctx := context.Background()
	// find Asha's id through the attempt rows
	rows, _ := e.attempts.ListByExam(ctx, "decl-key-1")
	uid := rows[0].UserID
	if err := e.attempts.Save(ctx, "decl-key-1", uid, 1, map[string]string{"a": "b"}, 0, authT0); err != nil {
		t.Fatal(err)
	}
	rec := e.post("/exams/decl-key-1/decline", "", e.student)
	if rec.Code != http.StatusConflict || errCode(t, rec) != "conflict" || !strings.Contains(rec.Body.String(), "You can only decline an exam you have not started.") {
		t.Errorf("in_progress: %d %s", rec.Code, rec.Body)
	}
	if err := e.attempts.Save(ctx, "decl-key-2", uid, 1, map[string]string{}, 0, authT0); err != nil {
		t.Fatal(err)
	}
	if err := e.attempts.Submit(ctx, store.Attempt{AttemptNo: 1, ExamID: "decl-key-2", UserID: uid, Status: store.AttemptSubmitted, Answers: map[string]string{}, Revealed: []string{}}); err != nil {
		t.Fatal(err)
	}
	rec = e.post("/exams/decl-key-2/decline", "", e.student)
	if rec.Code != http.StatusConflict || errCode(t, rec) != "conflict" {
		t.Errorf("submitted: %d %s", rec.Code, rec.Body)
	}
	// after the deadline: exam_closed for a not-started row, 204 for an already declined one
	if rec := e.post("/exams/decl-key-3/decline", "", e.student); rec.Code != http.StatusNoContent {
		t.Fatalf("decline: %d", rec.Code)
	}
	e.newExam(t, e.teacher, "decl-key-4", `"asha@example.com"`)
	e.clk.Advance(2 * time.Hour)
	rec = e.post("/exams/decl-key-4/decline", "", e.student)
	if rec.Code != http.StatusConflict || errCode(t, rec) != "exam_closed" {
		t.Errorf("closed: %d %s", rec.Code, rec.Body)
	}
	if rec := e.post("/exams/decl-key-3/decline", "", e.student); rec.Code != http.StatusNoContent {
		t.Errorf("declined after close: %d", rec.Code)
	}
}

func TestAssignErrorPathsOverHTTP(t *testing.T) {
	e := newDeclEnv(t, false)
	e.newExam(t, e.teacher, "asgn-key-1", "")
	p := "/exams/asgn-key-1/assign"
	// 415, CSRF, bad bodies
	if rec := e.do("POST", p, `{"emails":["a@b.com"]}`, "text/plain", e.teacher, ""); rec.Code != http.StatusUnsupportedMediaType || errCode(t, rec) != "unsupported_media_type" {
		t.Errorf("415: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("POST", p, `{"emails":["a@b.com"]}`, "application/json", e.teacher, "https://evil.example"); rec.Code != http.StatusForbidden {
		t.Errorf("csrf: %d", rec.Code)
	}
	for name, body := range map[string]string{
		"malformed": `{"emails":`, "unknown field": `{"emails":["a@b.com"],"x":1}`, "null": `{"emails":null}`,
		"empty object": `{}`, "wrong type": `{"emails":"a@b.com"}`,
	} {
		if rec := e.post(p, body, e.teacher); rec.Code != http.StatusUnprocessableEntity || errCode(t, rec) != "validation" {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	// the cap over HTTP: a teacher may assign 50; 51 is a field error
	var many []string
	for i := 0; i < 51; i++ {
		many = append(many, fmt.Sprintf(`"u%d@example.com"`, i))
	}
	rec := e.post(p, `{"emails":[`+strings.Join(many, ",")+`]}`, e.teacher)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"emails":"You can assign an exam to at most 50 people."`) {
		t.Errorf("cap: %d %s", rec.Code, rec.Body)
	}
	// 404 bodies for foreign and missing exams are byte for byte the same, whatever the input
	for _, body := range []string{`{"emails":["a@b.com"]}`, `{"emails":null}`} {
		foreign := e.post(p, body, e.other)
		missing := e.post("/exams/no-such-exam/assign", body, e.other)
		if foreign.Code != http.StatusNotFound || foreign.Body.String() != missing.Body.String() {
			t.Errorf("assign 404: %d %q vs %d %q", foreign.Code, foreign.Body, missing.Code, missing.Body)
		}
	}
	df := e.post("/exams/asgn-key-1/decline", "", e.other)
	dm := e.post("/exams/no-such-exam/decline", "", e.other)
	if df.Code != http.StatusNotFound || df.Body.String() != dm.Body.String() {
		t.Errorf("decline 404: %q vs %q", df.Body, dm.Body)
	}
	// exam_closed comes before the 422 checks
	e.clk.Advance(time.Hour + 60*time.Second)
	for _, body := range []string{`{"emails":null}`, `{"emails":["a@b.com"]}`} {
		if rec := e.post(p, body, e.teacher); rec.Code != http.StatusConflict || errCode(t, rec) != "exam_closed" {
			t.Errorf("closed %s: %d %s", body, rec.Code, rec.Body)
		}
	}
}

// A student creator gets the same bytes whether a not-allowed account (a teacher) or no account
// holds the email.
func TestAssignUnknownByteIdenticalOverHTTP(t *testing.T) {
	run := func(registerTeacher bool) (int, string, string) {
		e := newDeclEnv(t, false)
		if registerTeacher {
			rec := httptest.NewRecorder()
			e.h.ServeHTTP(rec, jsonReq("POST", "/auth/signup", `{"name":"Tee","email":"tee@example.com","password":"correct horse","role":"teacher"}`))
		}
		e.newExam(t, e.student, "unk-key-1", "")
		rec := e.post("/exams/unk-key-1/assign", `{"emails":["Tee@Example.com ","ghost@example.com"]}`, e.student)
		return rec.Code, rec.Header().Get("Cache-Control") + rec.Header().Get("Content-Type"), rec.Body.String()
	}
	c1, h1, b1 := run(true)
	c2, h2, b2 := run(false)
	if c1 != http.StatusOK || c1 != c2 || h1 != h2 || b1 != b2 {
		t.Errorf("%d %q %q vs %d %q %q", c1, h1, b1, c2, h2, b2)
	}
	if strings.TrimSpace(b1) != `{"assigned":[],"alreadyAssigned":[],"unknown":["Tee@Example.com","ghost@example.com"]}` {
		t.Errorf("body %s", b1)
	}
}

func TestStatusOverHTTP(t *testing.T) {
	e := newDeclEnv(t, false)
	e.newExam(t, e.teacher, "stat-key-1", `"asha@example.com"`)
	get := func(path string, c *http.Cookie) *httptest.ResponseRecorder {
		return e.do("GET", path, "", "", c, "")
	}
	if rec := get("/exams/stat-key-1/status", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no session: %d", rec.Code)
	}
	rec := get("/exams/stat-key-1/status", e.teacher)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("creator: %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	var out struct {
		ServerTime string `json:"serverTime"`
		Exam       struct{ ID string }
		Counts     map[string]int
		Attempts   []map[string]any
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Exam.ID != "stat-key-1" || out.Counts["notStarted"] != 1 ||
		len(out.Attempts) != 1 || out.Attempts[0]["email"] != "asha@example.com" || out.Attempts[0]["status"] != "not_started" ||
		out.ServerTime != authT0.UTC().Format(time.RFC3339) {
		t.Fatalf("%v %s", err, rec.Body)
	}
	if rec := get("/exams/stat-key-1/status", e.student); rec.Code != http.StatusForbidden || errCode(t, rec) != "forbidden" {
		t.Errorf("assignee: %d %s", rec.Code, rec.Body)
	}
	unrelated := get("/exams/stat-key-1/status", e.other)
	missing := get("/exams/no-such-exam/status", e.other)
	if unrelated.Code != http.StatusNotFound || missing.Code != http.StatusNotFound ||
		unrelated.Body.String() != missing.Body.String() ||
		unrelated.Header().Get("Content-Type") != missing.Header().Get("Content-Type") ||
		unrelated.Header().Get("Cache-Control") != missing.Header().Get("Cache-Control") {
		t.Errorf("404s differ: %d %q vs %d %q", unrelated.Code, unrelated.Body, missing.Code, missing.Body)
	}
	r405 := e.do("POST", "/exams/stat-key-1/status", "", "application/json", e.teacher, "")
	if r405.Code != http.StatusMethodNotAllowed || !strings.Contains(r405.Header().Get("Content-Type"), "json") || !strings.Contains(r405.Header().Get("Allow"), "GET") {
		t.Errorf("405: %d %v", r405.Code, r405.Header())
	}
}
