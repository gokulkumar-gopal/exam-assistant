package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/gokulkumar/exam-assistant/backend/internal/auth"
	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/store/memory"
)

type fakeGoogle struct{ claims map[string]auth.GoogleClaims }

func (f fakeGoogle) Verify(_ context.Context, cred string) (auth.GoogleClaims, error) {
	if c, ok := f.claims[cred]; ok {
		return c, nil
	}
	return auth.GoogleClaims{}, errors.New("invalid")
}

func newGoogleEnv(t *testing.T) *authEnv {
	t.Helper()
	users := memory.NewUserStore()
	clk := clock.NewFake(authT0)
	sess := auth.NewSessions([]byte(authSecret), "production")
	fg := fakeGoogle{claims: map[string]auth.GoogleClaims{
		"tok":   {Sub: "sub-1", Email: "asha@example.com", Name: "Asha G", EmailVerified: true},
		"other": {Sub: "sub-9", Email: "Asha@example.com", Name: "M", EmailVerified: true},
		"unv":   {Sub: "sub-2", Email: "asha@example.com", Name: "X", EmailVerified: false},
	}}
	svc := auth.NewService(users, auth.NewBcryptHasher(bcrypt.MinCost), clk).WithGoogle(fg)
	h := NewRouter(Deps{Logger: silentLogger(), Clock: clk, Users: users, Auth: svc, Sessions: sess,
		AllowedOrigins: []string{"https://app.example.com"}})
	return &authEnv{h: h, users: users, clk: clk, sess: sess}
}

func errOf(rec *httptest.ResponseRecorder) errBody {
	var b errBody
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	return b
}

func TestGoogleNewUserWithRole201(t *testing.T) {
	e := newGoogleEnv(t)
	rec := do(e.h, jsonReq("POST", "/auth/google", `{"credential":"tok","role":"student"}`))
	if rec.Code != 201 || sessionCookie(rec) == nil {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var resp struct{ User map[string]any }
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.User) != 4 || resp.User["email"] != "asha@example.com" || resp.User["name"] != "Asha G" || resp.User["role"] != "student" {
		t.Errorf("user = %v", resp.User)
	}
	req := httptest.NewRequest("GET", "/me", nil)
	req.AddCookie(sessionCookie(rec))
	if r := do(e.h, req); r.Code != 200 {
		t.Errorf("/me = %d", r.Code)
	}
	// Second sign-in: 200, role ignored.
	rec = do(e.h, jsonReq("POST", "/auth/google", `{"credential":"tok","role":"teacher"}`))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"role":"student"`) {
		t.Errorf("repeat: %d %s", rec.Code, rec.Body.String())
	}
}

func TestGoogleNewUserWithoutRoleIs422RoleRequiredAndCreatesNothing(t *testing.T) {
	e := newGoogleEnv(t)
	for _, body := range []string{`{"credential":"tok"}`, `{"credential":"tok","role":"admin"}`} {
		rec := do(e.h, jsonReq("POST", "/auth/google", body))
		if b := errOf(rec); rec.Code != 422 || b.Error.Code != "role_required" || sessionCookie(rec) != nil {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	if _, err := e.users.GetByEmail(context.Background(), "asha@example.com"); err == nil {
		t.Error("user created")
	}
}

func TestGoogleLinksPasswordAccount(t *testing.T) {
	e := newGoogleEnv(t)
	rec := do(e.h, jsonReq("POST", "/auth/signup", signupBody))
	oldCookie := sessionCookie(rec)
	if oldCookie == nil {
		t.Fatalf("signup: %d", rec.Code)
	}
	e.clk.Advance(time.Minute)
	rec = do(e.h, jsonReq("POST", "/auth/google", `{"credential":"tok","role":"teacher"}`))
	if rec.Code != 200 {
		t.Fatalf("link: %d %s", rec.Code, rec.Body.String())
	}
	fresh := sessionCookie(rec)
	u, _ := e.users.GetByEmail(context.Background(), "asha@example.com")
	if u.PasswordHash != "" || u.GoogleSub != "sub-1" || u.Role != "student" || u.SessionsValidAfter != e.clk.Now().Unix() {
		t.Errorf("user = %+v", u)
	}
	claims, err := e.sess.Parse(fresh.Value, e.clk.Now())
	if err != nil || claims.Iat < u.SessionsValidAfter {
		t.Errorf("fresh iat %d < validAfter %d (%v)", claims.Iat, u.SessionsValidAfter, err)
	}
	for cookie, want := range map[*http.Cookie]int{fresh: 200, oldCookie: 401} {
		req := httptest.NewRequest("GET", "/me", nil)
		req.AddCookie(cookie)
		if r := do(e.h, req); r.Code != want {
			t.Errorf("/me = %d, want %d", r.Code, want)
		}
	}
}

func TestGoogleDifferentSubSameEmail409(t *testing.T) {
	e := newGoogleEnv(t)
	do(e.h, jsonReq("POST", "/auth/google", `{"credential":"tok","role":"student"}`))
	rec := do(e.h, jsonReq("POST", "/auth/google", `{"credential":"other","role":"student"}`))
	b := errOf(rec)
	if rec.Code != 409 || b.Error.Code != "conflict" || b.Error.Message != "This email is already linked to another sign-in. Use the original sign-in method." || sessionCookie(rec) != nil {
		t.Errorf("%d %s", rec.Code, rec.Body.String())
	}
	u, _ := e.users.GetByEmail(context.Background(), "asha@example.com")
	if u.GoogleSub != "sub-1" {
		t.Errorf("user changed: %+v", u)
	}
}

func TestGoogleUnverifiedEmailRejected(t *testing.T) {
	e := newGoogleEnv(t)
	rec := do(e.h, jsonReq("POST", "/auth/google", `{"credential":"unv","role":"student"}`))
	if b := errOf(rec); rec.Code != 401 || b.Error.Message != "Google sign-in failed. Try again." || sessionCookie(rec) != nil {
		t.Errorf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestGoogleVerifierErrorIs401Generic(t *testing.T) {
	e := newGoogleEnv(t)
	for _, body := range []string{`{"credential":"nope"}`, `{"credential":""}`, `{}`} {
		rec := do(e.h, jsonReq("POST", "/auth/google", body))
		if b := errOf(rec); rec.Code != 401 || b.Error.Code != "unauthenticated" || b.Error.Message != "Google sign-in failed. Try again." {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
}

func TestGoogleIgnoresClientSuppliedEmailAndName(t *testing.T) {
	e := newGoogleEnv(t)
	rec := do(e.h, jsonReq("POST", "/auth/google", `{"credential":"tok","role":"parent","email":"victim@example.com","name":"Evil"}`))
	if rec.Code != 201 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if _, err := e.users.GetByEmail(context.Background(), "victim@example.com"); err == nil {
		t.Error("client email used")
	}
	u, err := e.users.GetByEmail(context.Background(), "asha@example.com")
	if err != nil || u.Name != "Asha G" {
		t.Errorf("u=%+v err=%v", u, err)
	}
}

func TestGoogleRouteAbsentWithoutVerifier(t *testing.T) {
	e := newAuthEnv(t, "production")
	rec := do(e.h, jsonReq("POST", "/auth/google", `{"credential":"tok"}`))
	if rec.Code != 404 || errOf(rec).Error.Code != "not_found" {
		t.Errorf("%d %s", rec.Code, rec.Body.String())
	}
}
