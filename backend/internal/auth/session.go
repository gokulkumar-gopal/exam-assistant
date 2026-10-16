package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// Session cookie constants (backend-spec 8.2).
const (
	CookieName = "session"
	SessionTTL = 7 * 24 * time.Hour
)

// ErrInvalidToken is returned for every malformed, forged or expired token; the cause is never exposed.
var ErrInvalidToken = errors.New("auth: invalid session token")

// Claims are the JWT claims of a session.
type Claims struct {
	Sub  string `json:"sub"`
	Role string `json:"role"`
	Iat  int64  `json:"iat"`
	Exp  int64  `json:"exp"`
}

// Sessions issues and verifies HS256 session tokens and builds the session cookie.
type Sessions struct {
	secret []byte
	secure bool
}

// NewSessions returns Sessions signing with secret. Cookies are Secure unless env is exactly "local".
func NewSessions(secret []byte, env string) *Sessions {
	return &Sessions{secret: append([]byte(nil), secret...), secure: env != "local"}
}

var jwtHeader = base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))

func (s *Sessions) sign(signing string) []byte {
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte(signing))
	return m.Sum(nil)
}

// Issue returns the session cookie for u, issued at now.
func (s *Sessions) Issue(u store.User, now time.Time) *http.Cookie {
	exp := now.Add(SessionTTL)
	payload, _ := json.Marshal(Claims{Sub: u.ID, Role: u.Role, Iat: now.Unix(), Exp: exp.Unix()})
	signing := jwtHeader + "." + base64.RawURLEncoding.EncodeToString(payload)
	token := signing + "." + base64.RawURLEncoding.EncodeToString(s.sign(signing))
	c := s.base()
	c.Value = token
	c.Expires = exp
	c.MaxAge = int(SessionTTL / time.Second)
	return c
}

// Clear returns a cookie that removes the session cookie.
func (s *Sessions) Clear() *http.Cookie {
	c := s.base()
	c.MaxAge = -1
	c.Expires = time.Unix(0, 0).UTC()
	return c
}

func (s *Sessions) base() *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// Parse verifies token and returns its claims. The algorithm is pinned to HS256:
// "none" and every other alg are rejected before any signature work.
func (s *Sessions) Parse(token string, now time.Time) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, ErrInvalidToken
	}
	hdrRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	var hdr struct {
		Alg string `json:"alg"`
	}
	if json.Unmarshal(hdrRaw, &hdr) != nil || hdr.Alg != "HS256" {
		return Claims{}, ErrInvalidToken
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(sig, s.sign(parts[0]+"."+parts[1])) {
		return Claims{}, ErrInvalidToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	var c Claims
	if json.Unmarshal(payload, &c) != nil || c.Sub == "" || c.Exp == 0 || now.Unix() >= c.Exp {
		return Claims{}, ErrInvalidToken
	}
	return c, nil
}
