package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/gokulkumar/exam-assistant/backend/internal/auth"
	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
	"github.com/gokulkumar/exam-assistant/backend/internal/store/memory"
)

// fakeVerifier maps a credential string to claims, or fails.
type fakeVerifier struct {
	claims map[string]auth.GoogleClaims
	calls  int
}

func (f *fakeVerifier) Verify(_ context.Context, cred string) (auth.GoogleClaims, error) {
	f.calls++
	c, ok := f.claims[cred]
	if !ok {
		return auth.GoogleClaims{}, errors.New("bad token with secret details")
	}
	return c, nil
}

func newGoogleSvc(t *testing.T) (*auth.Service, *memory.UserStore, *clock.Fake, *fakeVerifier) {
	t.Helper()
	users := memory.NewUserStore()
	clk := clock.NewFake(t0)
	fv := &fakeVerifier{claims: map[string]auth.GoogleClaims{
		"tok-asha": {Sub: "sub-asha", Email: "asha@example.com", Name: "Asha G", EmailVerified: true},
	}}
	svc := auth.NewService(users, auth.NewBcryptHasher(bcrypt.MinCost), clk).WithGoogle(fv)
	return svc, users, clk, fv
}

func TestGoogleNewUserWithRole201(t *testing.T) {
	svc, users, _, _ := newGoogleSvc(t)
	u, created, err := svc.Google(context.Background(), "tok-asha", "teacher")
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if u.Email != "asha@example.com" || u.Name != "Asha G" || u.Role != "teacher" || u.GoogleSub != "sub-asha" || u.PasswordHash != "" {
		t.Errorf("user = %+v", u)
	}
	if _, err := users.GetByGoogleSub(context.Background(), "sub-asha"); err != nil {
		t.Errorf("not stored: %v", err)
	}
}

func TestGoogleNewUserWithoutRoleIs422RoleRequiredAndCreatesNothing(t *testing.T) {
	svc, users, _, _ := newGoogleSvc(t)
	_, _, err := svc.Google(context.Background(), "tok-asha", "")
	ae := appErr(t, err)
	if ae.Status != 422 || ae.Code != "role_required" {
		t.Errorf("err = %+v", ae)
	}
	if _, err := users.GetByEmail(context.Background(), "asha@example.com"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("user created: %v", err)
	}
	if _, err := users.GetByGoogleSub(context.Background(), "sub-asha"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("google lock created: %v", err)
	}
}

func TestGoogleInvalidRoleIsRoleRequired(t *testing.T) {
	svc, _, _, _ := newGoogleSvc(t)
	for _, role := range []string{"admin", "Student", " student"} {
		_, _, err := svc.Google(context.Background(), "tok-asha", role)
		if ae := appErr(t, err); ae.Code != "role_required" {
			t.Errorf("role %q: %+v", role, ae)
		}
	}
}

func TestGoogleKnownUser200RoleIgnored(t *testing.T) {
	svc, _, _, _ := newGoogleSvc(t)
	first, _, err := svc.Google(context.Background(), "tok-asha", "student")
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"teacher", "", "bogus"} {
		u, created, err := svc.Google(context.Background(), "tok-asha", role)
		if err != nil || created || u.ID != first.ID || u.Role != "student" {
			t.Errorf("role %q: u=%+v created=%v err=%v", role, u, created, err)
		}
	}
}

func TestGoogleLinksPasswordAccount(t *testing.T) {
	svc, users, clk, _ := newGoogleSvc(t)
	pw, err := svc.Signup(context.Background(), validSignup())
	if err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Hour)
	u, created, err := svc.Google(context.Background(), "tok-asha", "teacher")
	if err != nil || created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if u.ID != pw.ID || u.Role != "student" || u.GoogleSub != "sub-asha" || u.PasswordHash != "" {
		t.Errorf("linked user = %+v", u)
	}
	if u.SessionsValidAfter != clk.Now().Unix() {
		t.Errorf("SessionsValidAfter = %d, want %d", u.SessionsValidAfter, clk.Now().Unix())
	}
	stored, _ := users.GetByID(context.Background(), pw.ID)
	if stored.PasswordHash != "" || stored.SessionsValidAfter != clk.Now().Unix() {
		t.Errorf("stored = %+v", stored)
	}
}

func TestGoogleDifferentSubSameEmail409(t *testing.T) {
	svc, users, _, fv := newGoogleSvc(t)
	if _, _, err := svc.Google(context.Background(), "tok-asha", "student"); err != nil {
		t.Fatal(err)
	}
	fv.claims["tok-other"] = auth.GoogleClaims{Sub: "sub-other", Email: "Asha@Example.com", Name: "Mallory", EmailVerified: true}
	_, _, err := svc.Google(context.Background(), "tok-other", "student")
	ae := appErr(t, err)
	if ae.Status != 409 || ae.Code != "conflict" || ae.Message != "This email is already linked to another sign-in. Use the original sign-in method." {
		t.Errorf("err = %+v", ae)
	}
	u, _ := users.GetByEmail(context.Background(), "asha@example.com")
	if u.GoogleSub != "sub-asha" || u.Name != "Asha G" {
		t.Errorf("user changed: %+v", u)
	}
	if _, err := users.GetByGoogleSub(context.Background(), "sub-other"); !errors.Is(err, store.ErrNotFound) {
		t.Error("second sub was locked")
	}
}

func TestGoogleUnverifiedEmailRejected(t *testing.T) {
	svc, users, _, fv := newGoogleSvc(t)
	if _, err := svc.Signup(context.Background(), validSignup()); err != nil {
		t.Fatal(err)
	}
	fv.claims["tok-asha"] = auth.GoogleClaims{Sub: "sub-asha", Email: "asha@example.com", Name: "A", EmailVerified: false}
	_, _, err := svc.Google(context.Background(), "tok-asha", "student")
	ae := appErr(t, err)
	if ae.Status != 401 || ae.Message != "Google sign-in failed. Try again." {
		t.Errorf("err = %+v", ae)
	}
	u, _ := users.GetByEmail(context.Background(), "asha@example.com")
	if u.GoogleSub != "" || u.PasswordHash == "" {
		t.Errorf("account was linked: %+v", u)
	}
}

func TestGoogleEmptyEmailRejected(t *testing.T) {
	svc, _, _, fv := newGoogleSvc(t)
	for _, email := range []string{"", "   "} {
		fv.claims["tok-asha"] = auth.GoogleClaims{Sub: "sub-asha", Email: email, EmailVerified: true}
		_, _, err := svc.Google(context.Background(), "tok-asha", "student")
		if ae := appErr(t, err); ae.Status != 401 || ae.Message != "Google sign-in failed. Try again." {
			t.Errorf("email %q: %+v", email, ae)
		}
	}
}

func TestGoogleVerifierErrorIs401Generic(t *testing.T) {
	svc, _, _, _ := newGoogleSvc(t)
	_, _, err := svc.Google(context.Background(), "garbage", "student")
	ae := appErr(t, err)
	if ae.Status != 401 || ae.Code != "unauthenticated" || ae.Message != "Google sign-in failed. Try again." {
		t.Errorf("err = %+v", ae)
	}
	if strings.Contains(ae.Error(), "secret details") {
		t.Errorf("verifier detail leaked: %v", ae)
	}
}

func TestGoogleNameFallsBackToEmailLocalPart(t *testing.T) {
	svc, _, _, fv := newGoogleSvc(t)
	fv.claims["tok-asha"] = auth.GoogleClaims{Sub: "sub-asha", Email: " Priya.K@Example.com ", Name: "  ", EmailVerified: true}
	u, _, err := svc.Google(context.Background(), "tok-asha", "parent")
	if err != nil || u.Name != "priya.k" || u.Email != "priya.k@example.com" {
		t.Errorf("u=%+v err=%v", u, err)
	}
}

func TestGoogleLongNameTruncatedTo80(t *testing.T) {
	svc, _, _, fv := newGoogleSvc(t)
	fv.claims["tok-asha"] = auth.GoogleClaims{Sub: "sub-asha", Email: "a@example.com", Name: strings.Repeat("é", 120), EmailVerified: true}
	u, _, err := svc.Google(context.Background(), "tok-asha", "parent")
	if err != nil || len([]rune(u.Name)) != 80 {
		t.Errorf("name runes = %d err=%v", len([]rune(u.Name)), err)
	}
}

func TestGoogleOnlyUserCannotPasswordLogin(t *testing.T) {
	svc, _, _, _ := newGoogleSvc(t)
	if _, _, err := svc.Google(context.Background(), "tok-asha", "student"); err != nil {
		t.Fatal(err)
	}
	_, gErr := svc.Login(context.Background(), "asha@example.com", "anything12")
	_, wErr := svc.Login(context.Background(), "nobody@example.com", "anything12")
	g, w := appErr(t, gErr), appErr(t, wErr)
	if g.Status != 401 || g.Message != "Email or password is incorrect." || g.Message != w.Message || g.Code != w.Code {
		t.Errorf("google-only %+v vs wrong %+v", g, w)
	}
}

func TestGoogleDisabledWithoutVerifier(t *testing.T) {
	users := memory.NewUserStore()
	svc := auth.NewService(users, auth.NewBcryptHasher(bcrypt.MinCost), clock.NewFake(t0))
	if svc.GoogleEnabled() {
		t.Error("enabled without verifier")
	}
	if _, _, err := svc.Google(context.Background(), "x", "student"); appErr(t, err).Status != 401 {
		t.Error("want 401")
	}
}
