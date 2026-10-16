package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/gokulkumar/exam-assistant/backend/internal/auth"
	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/docs"
	"github.com/gokulkumar/exam-assistant/backend/internal/store/memory"
)

const presignQuery = "X-Amz-Signature=SIGSECRET123&X-Amz-Credential=AKIDSECRET456&X-Amz-Security-Token=TOKSECRET789"

// signedBlob returns presigned URLs carrying a query string and can fail on demand.
type signedBlob struct {
	*docs.MemBlob
	mu      sync.Mutex
	failPut error
}

func (b *signedBlob) PresignPut(ctx context.Context, key, ct string, size int64, ttl time.Duration) (string, error) {
	b.mu.Lock()
	err := b.failPut
	b.mu.Unlock()
	if err != nil {
		return "", err
	}
	u, err := b.MemBlob.PresignPut(ctx, key, ct, size, ttl)
	return u + "?" + presignQuery, err
}

// syncBuf is a goroutine-safe log sink.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func TestLogsNeverContainSecretsCookiesOrBodies(t *testing.T) {
	var logs syncBuf
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	users := memory.NewUserStore()
	clk := clock.NewFake(authT0)
	blob := &signedBlob{MemBlob: docs.NewMemBlob()}
	svc := auth.NewService(users, auth.NewBcryptHasher(bcrypt.MinCost), clk)
	h := NewRouter(Deps{
		Logger: logger, Clock: clk, Users: users, Auth: svc,
		Sessions:       auth.NewSessions([]byte(authSecret), "production"),
		Docs:           docs.NewService(memory.NewDocStore(clk), blob, clk),
		AllowedOrigins: []string{"https://app.example.com"},
	})

	const password = "hunter2-Passw0rd-unique"
	const email = "private.person@example.com"
	const fileName = "Confidential-Biology-Notes.pdf"
	var secrets []string // substrings that must never appear in any log line
	secrets = append(secrets, password, email, fileName, "SIGSECRET123", "AKIDSECRET456", "TOKSECRET789",
		presignQuery, "X-Amz-Signature", "Set-Cookie", "Cookie", "mem.test", authSecret)

	send := func(req *http.Request) *httptest.ResponseRecorder {
		req.Header.Set("Origin", "https://app.example.com")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	signup := send(jsonReq("POST", "/auth/signup",
		`{"name":"Private Name","email":"`+email+`","password":"`+password+`","role":"student"}`))
	if signup.Code != 201 {
		t.Fatalf("signup %d: %s", signup.Code, signup.Body)
	}
	secrets = append(secrets, "Private Name")

	login := send(jsonReq("POST", "/auth/login", `{"email":"`+email+`","password":"`+password+`"}`))
	if login.Code != 200 {
		t.Fatalf("login %d: %s", login.Code, login.Body)
	}
	cookie := sessionCookie(login)
	if cookie == nil || cookie.Value == "" {
		t.Fatal("no session cookie")
	}
	secrets = append(secrets, cookie.Value, cookie.Value[strings.Index(cookie.Value, ".")+1:])

	// Failed login with a wrong password.
	bad := send(jsonReq("POST", "/auth/login", `{"email":"`+email+`","password":"wrong-Passw0rd-unique"}`))
	if bad.Code != 401 {
		t.Fatalf("bad login %d", bad.Code)
	}
	secrets = append(secrets, "wrong-Passw0rd-unique")

	uploadBody := `{"name":"` + fileName + `","type":"application/pdf","sizeBytes":4321}`
	up := jsonReq("POST", "/documents/upload-url", uploadBody)
	up.AddCookie(cookie)
	rec := send(up)
	if rec.Code != 201 {
		t.Fatalf("upload-url %d: %s", rec.Code, rec.Body)
	}
	var out struct {
		DocID     string `json:"docId"`
		UploadURL string `json:"uploadUrl"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || !strings.Contains(out.UploadURL, "SIGSECRET123") {
		t.Fatalf("response lacks the signed URL: %s", rec.Body)
	}
	secrets = append(secrets, out.UploadURL)

	// CSRF-rejected request: a valid cookie, a hostile origin and a body.
	csrf := jsonReq("POST", "/documents/upload-url", `{"name":"csrf-body-marker.pdf","type":"application/pdf","sizeBytes":1}`)
	csrf.AddCookie(cookie)
	csrf.Header.Set("Origin", "https://evil.example")
	crec := httptest.NewRecorder()
	h.ServeHTTP(crec, csrf)
	if crec.Code != 403 {
		t.Fatalf("csrf status %d", crec.Code)
	}
	secrets = append(secrets, "csrf-body-marker")

	// Wrong content type, and a malformed body.
	ct := httptest.NewRequest("POST", "/auth/login", strings.NewReader("password="+password))
	ct.Header.Set("Content-Type", "text/plain")
	send(ct)
	send(jsonReq("POST", "/auth/login", `{"email":"`+email+`","password":"`+password+`",bogus-marker`))
	secrets = append(secrets, "bogus-marker")

	// A 5xx path: a 5xx response: the request body and cookie are not.
	blob.mu.Lock()
	blob.failPut = errors.New("blob unavailable")
	blob.mu.Unlock()
	fail := jsonReq("POST", "/documents/upload-url", uploadBody)
	fail.AddCookie(cookie)
	if r := send(fail); r.Code < 500 {
		t.Fatalf("failing blob status %d: %s", r.Code, r.Body)
	}

	// A query string on the request path must not be logged either.
	q := httptest.NewRequest("GET", "/health?token=QUERYSECRET&"+presignQuery, nil)
	q.AddCookie(cookie)
	send(q)
	secrets = append(secrets, "QUERYSECRET")

	out2 := logs.String()
	if !strings.Contains(out2, `"msg":"request"`) {
		t.Fatalf("logger captured nothing useful: %s", out2)
	}
	for _, s := range secrets {
		if strings.Contains(out2, s) {
			t.Errorf("log output contains %q:\n%s", s, out2)
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(out2), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line is not JSON: %s", line)
		}
		for k := range m {
			switch strings.ToLower(k) {
			case "body", "cookie", "set-cookie", "headers", "query", "url", "password", "token", "authorization":
				t.Errorf("log line has field %q: %s", k, line)
			}
		}
	}
}
