package auth_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/auth"
	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
	"github.com/gokulkumar/exam-assistant/backend/internal/store/memory"
)

var t0 = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

const secret = "0123456789abcdef0123456789abcdef"

// countingHasher wraps a cheap bcrypt hasher and counts calls.
type countingHasher struct {
	inner    auth.Hasher
	hashes   atomic.Int32
	compares atomic.Int32
}

func (c *countingHasher) Hash(pw string) (string, error) {
	c.hashes.Add(1)
	return c.inner.Hash(pw)
}

func (c *countingHasher) Compare(hash, pw string) error {
	c.compares.Add(1)
	return c.inner.Compare(hash, pw)
}

func newSvc(t *testing.T) (*auth.Service, *memory.UserStore, *countingHasher) {
	t.Helper()
	users := memory.NewUserStore()
	h := &countingHasher{inner: auth.NewBcryptHasher(bcrypt.MinCost)}
	return auth.NewService(users, h, clock.NewFake(t0)), users, h
}

func validSignup() auth.SignupInput {
	return auth.SignupInput{Name: "Asha", Email: "asha@example.com", Password: "correct horse", Role: "student"}
}

func appErr(t *testing.T, err error) *apperr.Error {
	t.Helper()
	ae, ok := err.(*apperr.Error)
	if !ok {
		t.Fatalf("error %T %v is not *apperr.Error", err, err)
	}
	return ae
}

func TestEmailNormalised(t *testing.T) {
	svc, users, _ := newSvc(t)
	in := validSignup()
	in.Email = "  A@X.com "
	if _, err := svc.Signup(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	u, err := users.GetByEmail(context.Background(), "a@x.com")
	if err != nil || u.Email != "a@x.com" {
		t.Fatalf("stored user = %+v, err %v", u, err)
	}
	// Login normalises too.
	if _, err := svc.Login(context.Background(), " A@X.COM", "correct horse"); err != nil {
		t.Errorf("login with mixed-case email: %v", err)
	}
	// Duplicate detection sees through case and whitespace.
	in.Email = "a@X.com"
	if _, err := svc.Signup(context.Background(), in); appErr(t, err).Status != 409 {
		t.Errorf("duplicate signup err = %v", err)
	}
}

func TestSignupStoresBcryptCost10Hash(t *testing.T) {
	users := memory.NewUserStore()
	svc := auth.NewService(users, auth.NewBcryptHasher(auth.BcryptCost), clock.NewFake(t0))
	u, err := svc.Signup(context.Background(), validSignup())
	if err != nil {
		t.Fatal(err)
	}
	got, _ := users.GetByID(context.Background(), u.ID)
	if c, err := bcrypt.Cost([]byte(got.PasswordHash)); err != nil || c != 10 {
		t.Errorf("cost = %d, err %v, want 10", c, err)
	}
	if got.SessionsValidAfter != 0 || !got.CreatedAt.Equal(t0) || got.ID == "" {
		t.Errorf("user = %+v", got)
	}
}

func TestPasswordLengthMeasuredInBytes(t *testing.T) {
	svc, _, _ := newSvc(t)
	cases := map[string]struct {
		pw string
		ok bool
	}{
		"7 bytes":            {strings.Repeat("a", 7), false},
		"8 bytes":            {strings.Repeat("a", 8), true},
		"72 bytes":           {strings.Repeat("a", 72), true},
		"73 bytes":           {strings.Repeat("a", 73), false},
		"30 runes, 90 bytes": {strings.Repeat("€", 30), false},
		"24 runes, 72 bytes": {strings.Repeat("€", 24), true},
		"3 runes, 9 bytes":   {"€€€", true},
		"2 runes, 6 bytes":   {"€€", false},
	}
	n := 0
	for name, tc := range cases {
		n++
		email := fmt.Sprintf("pw%d@x.com", n)
		t.Run(name, func(t *testing.T) {
			in := validSignup()
			in.Email = email
			_, err := svc.Signup(context.Background(), in2(in, tc.pw))
			if tc.ok && err != nil {
				t.Fatalf("err = %v", err)
			}
			if !tc.ok {
				ae := appErr(t, err)
				if ae.Status != 422 || ae.Fields["password"] == "" {
					t.Errorf("err = %+v", ae)
				}
			}
		})
	}
}

func in2(in auth.SignupInput, pw string) auth.SignupInput { in.Password = pw; return in }

func TestSignupValidationFields(t *testing.T) {
	svc, users, _ := newSvc(t)
	cases := map[string]struct {
		mut   func(*auth.SignupInput)
		field string
	}{
		"role admin":  {func(i *auth.SignupInput) { i.Role = "admin" }, "role"},
		"empty role":  {func(i *auth.SignupInput) { i.Role = "" }, "role"},
		"empty name":  {func(i *auth.SignupInput) { i.Name = "  " }, "name"},
		"81 chars":    {func(i *auth.SignupInput) { i.Name = strings.Repeat("é", 81) }, "name"},
		"empty email": {func(i *auth.SignupInput) { i.Email = " " }, "email"},
		"bad email":   {func(i *auth.SignupInput) { i.Email = "no-at-sign" }, "email"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			in := validSignup()
			tc.mut(&in)
			_, err := svc.Signup(context.Background(), in)
			ae := appErr(t, err)
			if ae.Status != 422 || ae.Code != "validation" || ae.Fields[tc.field] == "" {
				t.Errorf("err = %+v", ae)
			}
		})
	}
	// 80 characters is fine (counted as characters, not bytes).
	in := validSignup()
	in.Name = strings.Repeat("é", 80)
	if _, err := svc.Signup(context.Background(), in); err != nil {
		t.Errorf("80-char name: %v", err)
	}
	if _, err := users.GetByEmail(context.Background(), "x@nowhere"); err == nil {
		t.Error("unexpected user")
	}
}

func TestLoginRunsBcryptForUnknownEmail(t *testing.T) {
	svc, _, h := newSvc(t)
	before := h.compares.Load()
	_, err := svc.Login(context.Background(), "nobody@example.com", "whatever12")
	if appErr(t, err).Status != 401 {
		t.Fatalf("err = %v", err)
	}
	if got := h.compares.Load() - before; got != 1 {
		t.Errorf("Compare called %d times for unknown email, want 1", got)
	}
}

func TestGoogleOnlyAccountRunsBcryptAndFailsIdentically(t *testing.T) {
	svc, users, h := newSvc(t)
	if err := users.CreateGoogleUser(context.Background(), store.User{ID: "g1", Name: "G", Email: "g@example.com", GoogleSub: "sub1", Role: "teacher"}); err != nil {
		t.Fatal(err)
	}
	before := h.compares.Load()
	_, errG := svc.Login(context.Background(), "g@example.com", "whatever12")
	if got := h.compares.Load() - before; got != 1 {
		t.Errorf("Compare called %d times, want 1", got)
	}
	_, errU := svc.Login(context.Background(), "nobody@example.com", "whatever12")
	a, b := appErr(t, errG), appErr(t, errU)
	if a.Status != b.Status || a.Code != b.Code || a.Message != b.Message || a.Message != "Email or password is incorrect." {
		t.Errorf("google-only %+v vs unknown %+v", a, b)
	}
}

func TestLoginSuccessAndWrongPassword(t *testing.T) {
	svc, _, _ := newSvc(t)
	u, err := svc.Signup(context.Background(), validSignup())
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.Login(context.Background(), "asha@example.com", "correct horse")
	if err != nil || got.ID != u.ID {
		t.Fatalf("login = %+v, %v", got, err)
	}
	_, err = svc.Login(context.Background(), "asha@example.com", "wrong horse!")
	if ae := appErr(t, err); ae.Status != 401 || ae.Message != "Email or password is incorrect." {
		t.Errorf("err = %+v", ae)
	}
}

// --- sessions ---

func TestSessionRoundTripAndCookieAttributes(t *testing.T) {
	s := auth.NewSessions([]byte(secret), "production")
	u := store.User{ID: "u1", Role: "teacher"}
	c := s.Issue(u, t0)
	if c.Name != "session" || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode ||
		c.Path != "/" || c.MaxAge != 604800 || c.Domain != "" {
		t.Errorf("cookie = %+v", c)
	}
	if !c.Expires.Equal(t0.Add(7 * 24 * time.Hour)) {
		t.Errorf("Expires = %v", c.Expires)
	}
	claims, err := s.Parse(c.Value, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if claims.Sub != "u1" || claims.Role != "teacher" || claims.Iat != t0.Unix() || claims.Exp != t0.Add(7*24*time.Hour).Unix() {
		t.Errorf("claims = %+v", claims)
	}
	if _, err := s.Parse(c.Value, t0.Add(7*24*time.Hour)); err == nil {
		t.Error("token accepted at exp")
	}
	if _, err := s.Parse(c.Value, t0.Add(7*24*time.Hour-time.Second)); err != nil {
		t.Errorf("token rejected just before exp: %v", err)
	}
}

func TestCookieSecureOffOnlyLocal(t *testing.T) {
	for env, want := range map[string]bool{"local": false, "production": true, "": true, "Local": true, "staging": true} {
		c := auth.NewSessions([]byte(secret), env).Issue(store.User{ID: "u"}, t0)
		if c.Secure != want {
			t.Errorf("ENV=%q Secure = %v, want %v", env, c.Secure, want)
		}
		clr := auth.NewSessions([]byte(secret), env).Clear()
		if clr.Secure != want || clr.MaxAge >= 0 || clr.Value != "" || !clr.HttpOnly || clr.Path != "/" || clr.SameSite != http.SameSiteLaxMode {
			t.Errorf("ENV=%q clear cookie = %+v", env, clr)
		}
	}
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func forge(t *testing.T, header map[string]any, claims map[string]any, key string) string {
	t.Helper()
	h, _ := json.Marshal(header)
	c, _ := json.Marshal(claims)
	signing := b64(h) + "." + b64(c)
	if key == "" {
		return signing + "."
	}
	m := hmac.New(sha256.New, []byte(key))
	m.Write([]byte(signing))
	return signing + "." + b64(m.Sum(nil))
}

func TestRejectsAlgNoneAndWrongKey(t *testing.T) {
	s := auth.NewSessions([]byte(secret), "production")
	claims := map[string]any{"sub": "u1", "role": "student", "iat": t0.Unix(), "exp": t0.Add(time.Hour).Unix()}
	now := t0.Add(time.Minute)

	good := forge(t, map[string]any{"alg": "HS256", "typ": "JWT"}, claims, secret)
	if _, err := s.Parse(good, now); err != nil {
		t.Fatalf("control token rejected: %v", err)
	}
	bad := map[string]string{
		"alg none":         forge(t, map[string]any{"alg": "none", "typ": "JWT"}, claims, ""),
		"alg none + sig":   forge(t, map[string]any{"alg": "none", "typ": "JWT"}, claims, secret),
		"alg None":         forge(t, map[string]any{"alg": "None", "typ": "JWT"}, claims, ""),
		"alg HS384":        forge(t, map[string]any{"alg": "HS384", "typ": "JWT"}, claims, secret),
		"alg RS256":        forge(t, map[string]any{"alg": "RS256", "typ": "JWT"}, claims, secret),
		"missing alg":      forge(t, map[string]any{"typ": "JWT"}, claims, secret),
		"wrong key":        forge(t, map[string]any{"alg": "HS256", "typ": "JWT"}, claims, "ffffffffffffffffffffffffffffffff"),
		"tampered payload": good[:strings.Index(good, ".")] + "." + b64([]byte(`{"sub":"admin","role":"teacher","iat":1,"exp":99999999999}`)) + good[strings.LastIndex(good, "."):],
		"empty":            "",
		"garbage":          "not-a-jwt",
		"two parts":        "a.b",
		"four parts":       good + ".x",
		"expired":          forge(t, map[string]any{"alg": "HS256"}, map[string]any{"sub": "u1", "role": "student", "iat": 1, "exp": 2}, secret),
		"missing sub":      forge(t, map[string]any{"alg": "HS256"}, map[string]any{"role": "student", "iat": t0.Unix(), "exp": t0.Add(time.Hour).Unix()}, secret),
		"missing exp":      forge(t, map[string]any{"alg": "HS256"}, map[string]any{"sub": "u1", "iat": t0.Unix()}, secret),
		"bad base64 sig":   good[:strings.LastIndex(good, ".")] + ".!!!",
	}
	for name, tok := range bad {
		if _, err := s.Parse(tok, now); err == nil {
			t.Errorf("%s: token accepted", name)
		}
	}
	// A token from another Sessions instance with a different secret is rejected.
	other := auth.NewSessions([]byte("ffffffffffffffffffffffffffffffff"), "production").Issue(store.User{ID: "u1", Role: "student"}, t0)
	if _, err := s.Parse(other.Value, now); err == nil {
		t.Error("token signed with another secret accepted")
	}
}
