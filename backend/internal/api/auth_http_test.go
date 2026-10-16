package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/gokulkumar/exam-assistant/backend/internal/auth"
	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
	"github.com/gokulkumar/exam-assistant/backend/internal/store/memory"
)

var authT0 = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

const authSecret = "0123456789abcdef0123456789abcdef"

type authEnv struct {
	h     http.Handler
	users *memory.UserStore
	clk   *clock.Fake
	sess  *auth.Sessions
}

func newAuthEnv(t *testing.T, env string) *authEnv {
	t.Helper()
	users := memory.NewUserStore()
	clk := clock.NewFake(authT0)
	sess := auth.NewSessions([]byte(authSecret), env)
	svc := auth.NewService(users, auth.NewBcryptHasher(bcrypt.MinCost), clk)
	h := NewRouter(Deps{
		Logger: silentLogger(), Clock: clk, Users: users, Auth: svc, Sessions: sess,
		AllowedOrigins: []string{"http://localhost:3000", "https://app.example.com"},
	})
	return &authEnv{h: h, users: users, clk: clk, sess: sess}
}

func jsonReq(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

const signupBody = `{"name":"Asha","email":"asha@example.com","password":"correct horse","role":"student"}`

func sessionCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == "session" {
			return c
		}
	}
	return nil
}

func TestSignupCreatesUserAndCookie(t *testing.T) {
	e := newAuthEnv(t, "production")
	rec := do(e.h, jsonReq("POST", "/auth/signup", signupBody))
	if rec.Code != 201 {
		t.Fatalf("status = %d body %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		User map[string]any `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.User["email"] != "asha@example.com" || resp.User["name"] != "Asha" || resp.User["role"] != "student" || resp.User["id"] == "" {
		t.Errorf("user = %v", resp.User)
	}
	if len(resp.User) != 4 || strings.Contains(rec.Body.String(), "assword") {
		t.Errorf("user has extra fields: %s", rec.Body.String())
	}
	c := sessionCookie(rec)
	if c == nil {
		t.Fatal("no session cookie")
	}
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.MaxAge != 604800 || c.Domain != "" {
		t.Errorf("cookie = %+v", c)
	}
	// The raw Set-Cookie header carries every attribute.
	raw := rec.Header().Get("Set-Cookie")
	for _, want := range []string{"HttpOnly", "Secure", "SameSite=Lax", "Path=/", "Max-Age=604800"} {
		if !strings.Contains(raw, want) {
			t.Errorf("Set-Cookie %q lacks %s", raw, want)
		}
	}
	if strings.Contains(raw, "Domain") {
		t.Errorf("Set-Cookie has Domain: %s", raw)
	}
	// The cookie works on /me.
	req := httptest.NewRequest("GET", "/me", nil)
	req.AddCookie(c)
	if rec := do(e.h, req); rec.Code != 200 {
		t.Errorf("/me with new cookie = %d", rec.Code)
	}
}

func TestSignupLocalCookieNotSecure(t *testing.T) {
	e := newAuthEnv(t, "local")
	rec := do(e.h, jsonReq("POST", "/auth/signup", signupBody))
	if c := sessionCookie(rec); c == nil || c.Secure {
		t.Errorf("cookie = %+v, want non-secure in local", c)
	}
}

func TestSignupDuplicateEmail409(t *testing.T) {
	e := newAuthEnv(t, "production")
	do(e.h, jsonReq("POST", "/auth/signup", signupBody))
	rec := do(e.h, jsonReq("POST", "/auth/signup", strings.Replace(signupBody, "asha@", "  ASHA@", 1)))
	var b errBody
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	if rec.Code != 409 || b.Error.Code != "conflict" || b.Error.Message != "An account with that email already exists." {
		t.Errorf("status %d body %s", rec.Code, rec.Body.String())
	}
	if sessionCookie(rec) != nil {
		t.Error("cookie set on failed signup")
	}
}

func TestSignupValidation(t *testing.T) {
	e := newAuthEnv(t, "production")
	mk := func(name, pw, role string) string {
		b, _ := json.Marshal(map[string]string{"name": name, "email": "v@example.com", "password": pw, "role": role})
		return string(b)
	}
	cases := map[string]struct{ body, field string }{
		"7 byte password":            {mk("A", strings.Repeat("a", 7), "student"), "password"},
		"73 byte password":           {mk("A", strings.Repeat("a", 73), "student"), "password"},
		"30 runes of 3 bytes (90 B)": {mk("A", strings.Repeat("€", 30), "student"), "password"},
		"role admin":                 {mk("A", "password1", "admin"), "role"},
		"empty name":                 {mk("", "password1", "student"), "name"},
		"81 char name":               {mk(strings.Repeat("a", 81), "password1", "student"), "name"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := do(e.h, jsonReq("POST", "/auth/signup", tc.body))
			var b errBody
			_ = json.Unmarshal(rec.Body.Bytes(), &b)
			if rec.Code != 422 || b.Error.Code != "validation" || b.Error.Fields[tc.field] == "" {
				t.Errorf("status %d body %s", rec.Code, rec.Body.String())
			}
			if sessionCookie(rec) != nil {
				t.Error("cookie set on invalid signup")
			}
		})
	}
	// Unknown fields are rejected on create endpoints.
	rec := do(e.h, jsonReq("POST", "/auth/signup", `{"name":"A","email":"v@example.com","password":"password1","role":"student","admin":true}`))
	if rec.Code != 422 {
		t.Errorf("unknown field: status %d", rec.Code)
	}
	// Nothing was stored by any failed attempt.
	if _, err := e.users.GetByEmail(context.Background(), "v@example.com"); err == nil {
		t.Error("user stored despite validation failure")
	}
}

func login(e *authEnv, email, pw string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(map[string]string{"email": email, "password": pw})
	return do(e.h, jsonReq("POST", "/auth/login", string(b)))
}

func TestLoginSuccessSetsCookie(t *testing.T) {
	e := newAuthEnv(t, "production")
	do(e.h, jsonReq("POST", "/auth/signup", signupBody))
	rec := login(e, " Asha@Example.com ", "correct horse")
	if rec.Code != 200 || sessionCookie(rec) == nil || !strings.Contains(rec.Body.String(), `"asha@example.com"`) {
		t.Errorf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestLoginWrongPasswordAndUnknownEmailIdentical(t *testing.T) {
	e := newAuthEnv(t, "production")
	do(e.h, jsonReq("POST", "/auth/signup", signupBody))
	wrong := login(e, "asha@example.com", "not the password")
	unknown := login(e, "nobody@example.com", "not the password")
	if wrong.Code != 401 || wrong.Code != unknown.Code || wrong.Body.String() != unknown.Body.String() {
		t.Errorf("wrong: %d %s / unknown: %d %s", wrong.Code, wrong.Body.String(), unknown.Code, unknown.Body.String())
	}
	var b errBody
	_ = json.Unmarshal(wrong.Body.Bytes(), &b)
	if b.Error.Code != "unauthenticated" || b.Error.Message != "Email or password is incorrect." {
		t.Errorf("body = %s", wrong.Body.String())
	}
	if sessionCookie(wrong) != nil || sessionCookie(unknown) != nil {
		t.Error("cookie set on failed login")
	}
}

func TestGoogleOnlyAccountPasswordLoginIdentical(t *testing.T) {
	e := newAuthEnv(t, "production")
	if err := e.users.CreateGoogleUser(context.Background(), store.User{ID: "g1", Name: "G", Email: "g@example.com", GoogleSub: "sub1", Role: "parent"}); err != nil {
		t.Fatal(err)
	}
	g := login(e, "g@example.com", "some password")
	u := login(e, "nobody@example.com", "some password")
	if g.Code != 401 || g.Code != u.Code || g.Body.String() != u.Body.String() {
		t.Errorf("google-only: %d %s / unknown: %d %s", g.Code, g.Body.String(), u.Code, u.Body.String())
	}
}

func TestLogoutClearsCookie204(t *testing.T) {
	e := newAuthEnv(t, "production")
	rec := do(e.h, jsonReq("POST", "/auth/logout", ""))
	if rec.Code != 204 || rec.Body.Len() != 0 {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
	c := sessionCookie(rec)
	if c == nil || c.Value != "" || c.MaxAge >= 0 || !c.HttpOnly || !c.Secure || c.Path != "/" || c.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie = %+v", c)
	}
}

func TestMe401WithoutCookie(t *testing.T) {
	e := newAuthEnv(t, "production")
	for name, mod := range map[string]func(*http.Request){
		"none":    func(*http.Request) {},
		"garbage": func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "session", Value: "abc.def.ghi"}) },
		"other":   func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "other", Value: "x"}) },
	} {
		req := httptest.NewRequest("GET", "/me", nil)
		mod(req)
		rec := do(e.h, req)
		var b errBody
		_ = json.Unmarshal(rec.Body.Bytes(), &b)
		if rec.Code != 401 || b.Error.Code != "unauthenticated" {
			t.Errorf("%s: status %d body %s", name, rec.Code, rec.Body.String())
		}
	}
}

func TestMeReturnsUserAndServerTime(t *testing.T) {
	e := newAuthEnv(t, "production")
	rec := do(e.h, jsonReq("POST", "/auth/signup", signupBody))
	req := httptest.NewRequest("GET", "/me", nil)
	req.AddCookie(sessionCookie(rec))
	rec = do(e.h, req)
	var resp struct {
		User       map[string]any `json:"user"`
		ServerTime string         `json:"serverTime"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if resp.ServerTime != "2026-10-16T12:00:00Z" || resp.User["email"] != "asha@example.com" {
		t.Errorf("resp = %+v", resp)
	}
}

func TestMeRejectsOldIat(t *testing.T) {
	e := newAuthEnv(t, "production")
	u := store.User{ID: "u1", Name: "A", Email: "a@x.com", Role: "student", SessionsValidAfter: authT0.Unix()}
	if err := e.users.CreateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	me := func(issued time.Time) int {
		req := httptest.NewRequest("GET", "/me", nil)
		req.AddCookie(e.sess.Issue(u, issued))
		return do(e.h, req).Code
	}
	if got := me(authT0.Add(-time.Second)); got != 401 {
		t.Errorf("iat = validAfter-1: status %d, want 401", got)
	}
	if got := me(authT0); got != 200 {
		t.Errorf("iat = validAfter: status %d, want 200", got)
	}
}

func TestMeRejectsDeletedUserAndExpiredToken(t *testing.T) {
	e := newAuthEnv(t, "production")
	req := httptest.NewRequest("GET", "/me", nil)
	req.AddCookie(e.sess.Issue(store.User{ID: "ghost", Role: "student"}, authT0))
	if rec := do(e.h, req); rec.Code != 401 {
		t.Errorf("unknown user: status %d", rec.Code)
	}
	u := store.User{ID: "u1", Name: "A", Email: "a@x.com", Role: "student"}
	_ = e.users.CreateUser(context.Background(), u)
	cookie := e.sess.Issue(u, authT0)
	e.clk.Advance(7 * 24 * time.Hour)
	req = httptest.NewRequest("GET", "/me", nil)
	req.AddCookie(cookie)
	if rec := do(e.h, req); rec.Code != 401 {
		t.Errorf("expired: status %d", rec.Code)
	}
}

func TestCSRFRejectsForeignOrigin(t *testing.T) {
	e := newAuthEnv(t, "production")
	for _, origin := range []string{"https://evil.example.com", "null", "http://localhost:3000.evil.com", "http://localhost:3001"} {
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
			req := jsonReq(method, "/auth/logout", "")
			req.Header.Set("Origin", origin)
			rec := do(e.h, req)
			var b errBody
			_ = json.Unmarshal(rec.Body.Bytes(), &b)
			if rec.Code != 403 || b.Error.Code != "forbidden" {
				t.Errorf("%s Origin %q: status %d body %s", method, origin, rec.Code, rec.Body.String())
			}
		}
	}
	// Rejected before any handler: signup with a foreign origin creates nothing.
	req := jsonReq("POST", "/auth/signup", signupBody)
	req.Header.Set("Origin", "https://evil.example.com")
	if rec := do(e.h, req); rec.Code != 403 {
		t.Errorf("signup foreign origin: %d", rec.Code)
	}
	if _, err := e.users.GetByEmail(context.Background(), "asha@example.com"); err == nil {
		t.Error("user created despite foreign origin")
	}
}

func TestCSRFAllowsNoOrigin(t *testing.T) {
	e := newAuthEnv(t, "production")
	if rec := do(e.h, jsonReq("POST", "/auth/logout", "")); rec.Code != 204 {
		t.Errorf("status %d", rec.Code)
	}
}

func TestCSRFAllowsConfiguredOrigin(t *testing.T) {
	e := newAuthEnv(t, "production")
	for _, origin := range []string{"http://localhost:3000", "https://app.example.com"} {
		req := jsonReq("POST", "/auth/logout", "")
		req.Header.Set("Origin", origin)
		if rec := do(e.h, req); rec.Code != 204 {
			t.Errorf("Origin %q: status %d", origin, rec.Code)
		}
	}
	// GET is never subject to the check.
	req := httptest.NewRequest("GET", "/health", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	if rec := do(e.h, req); rec.Code != 200 {
		t.Errorf("GET with foreign origin: %d", rec.Code)
	}
}

func TestWrongMethodIsJSON405WithAllow(t *testing.T) {
	e := newAuthEnv(t, "production")
	cases := []struct{ method, path, allow string }{
		{"POST", "/health", "GET, HEAD"},
		{"GET", "/auth/login", "POST"},
		{"DELETE", "/me", "GET, HEAD"},
		{"PUT", "/auth/signup", "POST"},
	}
	for _, tc := range cases {
		rec := do(e.h, httptest.NewRequest(tc.method, tc.path, nil))
		var b errBody
		if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
			t.Errorf("%s %s: body not JSON: %q", tc.method, tc.path, rec.Body.String())
			continue
		}
		if rec.Code != 405 || b.Error.Code != "method_not_allowed" || b.Error.Message == "" {
			t.Errorf("%s %s: status %d body %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Allow"); got != tc.allow {
			t.Errorf("%s %s: Allow = %q, want %q", tc.method, tc.path, got, tc.allow)
		}
		if !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
			t.Errorf("%s %s: Content-Type %q", tc.method, tc.path, rec.Header().Get("Content-Type"))
		}
	}
	// Unknown path stays 404.
	if rec := do(e.h, httptest.NewRequest("POST", "/nope", nil)); rec.Code != 404 {
		t.Errorf("unknown path: %d", rec.Code)
	}
}

func TestNoSecretsInLogs(t *testing.T) {
	var buf strings.Builder
	e := newAuthEnv(t, "production")
	e.h = NewRouter(Deps{
		Logger: slog.New(slog.NewJSONHandler(&buf, nil)), Clock: e.clk, Users: e.users,
		Auth:     auth.NewService(e.users, auth.NewBcryptHasher(bcrypt.MinCost), e.clk),
		Sessions: e.sess, AllowedOrigins: []string{"http://localhost:3000"},
	})
	rec := do(e.h, jsonReq("POST", "/auth/signup", strings.Replace(signupBody, "correct horse", "hunter2-hunter2", 1)))
	login(e, "asha@example.com", "wrong-hunter2")
	req := httptest.NewRequest("GET", "/me", nil)
	req.AddCookie(sessionCookie(rec))
	do(e.h, req)
	logs := buf.String()
	for _, secret := range []string{"hunter2", sessionCookie(rec).Value, authSecret} {
		if strings.Contains(logs, secret) {
			t.Errorf("logs contain %q", secret)
		}
	}
	if !strings.Contains(logs, `"user_id"`) {
		t.Errorf("authenticated request log lacks user_id: %s", logs)
	}
}
