package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/oklog/ulid/v2"
	"google.golang.org/api/idtoken"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// Messages for Google sign-in (backend-spec 8.2).
const (
	GoogleFailedMessage   = "Google sign-in failed. Try again."
	GoogleLinkedElsewhere = "This email is already linked to another sign-in. Use the original sign-in method."
)

// GoogleClaims are the only fields Google sign-in trusts: the verified token's claims.
type GoogleClaims struct {
	Sub, Email, Name string
	EmailVerified    bool
}

// IDTokenVerifier validates a Google ID token. Tests use a fake.
type IDTokenVerifier interface {
	Verify(ctx context.Context, credential string) (GoogleClaims, error)
}

type googleVerifier struct{ clientID string }

// NewIDTokenVerifier returns the production verifier: signature, expiry and audience
// are checked by idtoken.Validate against clientID, the issuer is checked explicitly.
func NewIDTokenVerifier(clientID string) IDTokenVerifier { return googleVerifier{clientID: clientID} }

func (v googleVerifier) Verify(ctx context.Context, credential string) (GoogleClaims, error) {
	if v.clientID == "" {
		return GoogleClaims{}, errors.New("google: no client id configured")
	}
	p, err := idtoken.Validate(ctx, credential, v.clientID)
	if err != nil {
		return GoogleClaims{}, err
	}
	return claimsFromPayload(p, v.clientID)
}

// claimsFromPayload re-checks audience and issuer on an already signature-checked payload
// and extracts the claims. Errors never include token contents.
func claimsFromPayload(p *idtoken.Payload, clientID string) (GoogleClaims, error) {
	if p == nil {
		return GoogleClaims{}, errors.New("google: empty payload")
	}
	if clientID == "" || p.Audience != clientID {
		return GoogleClaims{}, errors.New("google: audience mismatch")
	}
	if p.Issuer != "accounts.google.com" && p.Issuer != "https://accounts.google.com" {
		return GoogleClaims{}, errors.New("google: issuer mismatch")
	}
	c := GoogleClaims{Sub: p.Subject}
	c.Email, _ = p.Claims["email"].(string)
	c.Name, _ = p.Claims["name"].(string)
	switch ev := p.Claims["email_verified"].(type) {
	case bool:
		c.EmailVerified = ev
	case string:
		c.EmailVerified = ev == "true"
	}
	return c, nil
}

// WithGoogle enables Google sign-in on the service and returns it.
func (s *Service) WithGoogle(v IDTokenVerifier) *Service {
	s.google = v
	return s
}

// GoogleEnabled reports whether a verifier is configured.
func (s *Service) GoogleEnabled() bool { return s.google != nil }

// Google signs in (or creates, or links) the user behind a verified Google ID token.
// Only verified token claims are used; role matters only when creating an account.
func (s *Service) Google(ctx context.Context, credential, role string) (store.User, bool, error) {
	fail := apperr.Unauthenticated(GoogleFailedMessage)
	if s.google == nil || credential == "" {
		return store.User{}, false, fail
	}
	claims, err := s.google.Verify(ctx, credential)
	if err != nil {
		return store.User{}, false, fail
	}
	email := NormalizeEmail(claims.Email)
	if !claims.EmailVerified || email == "" || claims.Sub == "" {
		return store.User{}, false, fail
	}
	name := strings.TrimSpace(claims.Name)
	if name == "" {
		name = email
		if i := strings.IndexByte(email, '@'); i >= 0 {
			name = email[:i]
		}
	}
	name = truncateRunes(name, 80)

	// Known Google user.
	u, err := s.users.GetByGoogleSub(ctx, claims.Sub)
	if err == nil {
		return u, false, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return store.User{}, false, apperr.Internal(err)
	}

	// Existing account with this email.
	u, err = s.users.GetByEmail(ctx, email)
	switch {
	case err == nil:
		if u.GoogleSub != "" {
			return store.User{}, false, apperr.Conflict(GoogleLinkedElsewhere)
		}
		if err := s.users.LinkGoogle(ctx, u.ID, claims.Sub, s.clock.Now().Unix()); err != nil {
			if errors.Is(err, store.ErrConflict) {
				return store.User{}, false, apperr.Conflict(GoogleLinkedElsewhere)
			}
			return store.User{}, false, apperr.Internal(err)
		}
		linked, err := s.users.GetByID(ctx, u.ID)
		if err != nil {
			return store.User{}, false, apperr.Internal(err)
		}
		return linked, false, nil
	case !errors.Is(err, store.ErrNotFound):
		return store.User{}, false, apperr.Internal(err)
	}

	// New account.
	switch role {
	case "student", "parent", "teacher":
	default:
		return store.User{}, false, apperr.RoleRequired()
	}
	nu := store.User{
		ID:        ulid.MustNew(ulid.Timestamp(s.clock.Now()), rand.Reader).String(),
		Name:      name,
		Email:     email,
		GoogleSub: claims.Sub,
		Role:      role,
		CreatedAt: s.clock.Now(),
	}
	if err := s.users.CreateGoogleUser(ctx, nu); err != nil {
		if errors.Is(err, store.ErrConflict) {
			// Lost a race with a concurrent first sign-in; the winner may be this same user.
			if again, gerr := s.users.GetByGoogleSub(ctx, claims.Sub); gerr == nil {
				return again, false, nil
			}
			return store.User{}, false, apperr.Conflict(GoogleLinkedElsewhere)
		}
		return store.User{}, false, apperr.Internal(err)
	}
	return nu, true, nil
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
