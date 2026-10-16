package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/gokulkumar/exam-assistant/backend/internal/auth"
	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/docs"
	"github.com/gokulkumar/exam-assistant/backend/internal/store/memory"
)

type docsEnv struct {
	h      http.Handler
	cookie *http.Cookie
	blob   *docs.MemBlob
	docs   *memory.DocStore
}

func newDocsEnv(t *testing.T) *docsEnv {
	t.Helper()
	users := memory.NewUserStore()
	clk := clock.NewFake(authT0)
	ds := memory.NewDocStore(clk)
	blob := docs.NewMemBlob()
	svc := auth.NewService(users, auth.NewBcryptHasher(bcrypt.MinCost), clk)
	h := NewRouter(Deps{
		Logger: silentLogger(), Clock: clk, Users: users, Auth: svc,
		Sessions:       auth.NewSessions([]byte(authSecret), "local"),
		Docs:           docs.NewService(ds, blob, clk),
		AllowedOrigins: []string{"http://localhost:3000"},
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, jsonReq("POST", "/auth/signup", signupBody))
	c := sessionCookie(rec)
	if c == nil {
		t.Fatalf("signup failed: %s", rec.Body)
	}
	return &docsEnv{h: h, cookie: c, blob: blob, docs: ds}
}

func (e *docsEnv) post(body string, withCookie bool, origin string) *httptest.ResponseRecorder {
	req := jsonReq("POST", "/documents/upload-url", body)
	if withCookie {
		req.AddCookie(e.cookie)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func TestUploadURLRoute201(t *testing.T) {
	e := newDocsEnv(t)
	rec := e.post(`{"name":"Biology.pdf","type":"application/pdf","sizeBytes":481234}`, true, "http://localhost:3000")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out struct {
		DocID     string            `json:"docId"`
		UploadURL string            `json:"uploadUrl"`
		Headers   map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.DocID == "" || out.UploadURL == "" || out.Headers["Content-Type"] != "application/pdf" {
		t.Errorf("body = %s", rec.Body)
	}
	if len(e.blob.Puts) != 1 || e.blob.Puts[0].Size != 481234 {
		t.Errorf("puts = %+v", e.blob.Puts)
	}
}

func TestUploadURLRouteRequiresAuth(t *testing.T) {
	e := newDocsEnv(t)
	rec := e.post(`{"name":"a","type":"text/plain","sizeBytes":1}`, false, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestUploadURLRouteCSRF(t *testing.T) {
	e := newDocsEnv(t)
	rec := e.post(`{"name":"a","type":"text/plain","sizeBytes":1}`, true, "https://evil.example")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d", rec.Code)
	}
	if len(e.blob.Puts) != 0 {
		t.Error("presigned for forbidden origin")
	}
}

func TestUploadURLRouteValidation(t *testing.T) {
	e := newDocsEnv(t)
	for body, field := range map[string]string{
		`{"name":"a","type":"application/zip","sizeBytes":1}`:   "type",
		`{"name":"a","type":"text/plain","sizeBytes":0}`:        "sizeBytes",
		`{"name":"a","type":"text/plain","sizeBytes":20971521}`: "sizeBytes",
		`{"name":"a","type":"text/plain"}`:                      "sizeBytes",
	} {
		rec := e.post(body, true, "")
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: status %d", body, rec.Code)
		}
		var env struct {
			Error struct {
				Fields map[string]string `json:"fields"`
			} `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		if _, ok := env.Error.Fields[field]; !ok {
			t.Errorf("%s: fields = %v", body, env.Error.Fields)
		}
	}
	if rec := e.post(`{"name":"a","bogus":1}`, true, ""); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("unknown field status %d", rec.Code)
	}
}

func TestUploadURLRouteWrongMethodIsJSON405(t *testing.T) {
	e := newDocsEnv(t)
	req := httptest.NewRequest("GET", "/documents/upload-url", nil)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "POST" || !strings.Contains(rec.Body.String(), "method_not_allowed") {
		t.Fatalf("status %d allow %q body %s", rec.Code, rec.Header().Get("Allow"), rec.Body)
	}
}
