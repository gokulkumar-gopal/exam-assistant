package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/oklog/ulid/v2"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// LoginFailedMessage is the only message a failed password login ever returns.
const LoginFailedMessage = "Email or password is incorrect."

// SignupInput is the signup request.
type SignupInput struct {
	Name, Email, Password, Role string
}

// Service implements password signup and login.
type Service struct {
	users     store.UserStore
	hasher    Hasher
	clock     clock.Clock
	dummyHash string
}

// NewService builds the service. It hashes a throwaway password once so that login
// can spend the same bcrypt time on unknown and Google-only accounts.
func NewService(users store.UserStore, hasher Hasher, clk clock.Clock) *Service {
	dummy, err := hasher.Hash("dummy-password-for-timing")
	if err != nil {
		// Only possible with an invalid cost; fail at startup, not per request.
		panic("auth: cannot create dummy hash: " + err.Error())
	}
	return &Service{users: users, hasher: hasher, clock: clk, dummyHash: dummy}
}

// NormalizeEmail trims and lowercases an email. Every store call goes through it.
func NormalizeEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

func validEmail(e string) bool {
	at := strings.IndexByte(e, '@')
	return len(e) <= 254 && at > 0 && at < len(e)-1 && strings.Count(e, "@") == 1 &&
		!strings.ContainsAny(e, " \t\r\n,;<>()\"")
}

// Signup validates, creates a password account and returns the stored user.
func (s *Service) Signup(ctx context.Context, in SignupInput) (store.User, error) {
	email := NormalizeEmail(in.Email)
	name := strings.TrimSpace(in.Name)
	fields := map[string]string{}
	if n := utf8.RuneCountInString(name); n < 1 || n > 80 {
		fields["name"] = "Must be 1 to 80 characters"
	}
	if !validEmail(email) {
		fields["email"] = "Enter a valid email address"
	}
	if msg := checkPassword(in.Password); msg != "" {
		fields["password"] = msg
	}
	switch in.Role {
	case "student", "parent", "teacher":
	default:
		fields["role"] = "Must be student, parent or teacher"
	}
	if len(fields) > 0 {
		return store.User{}, apperr.Validation("Check the highlighted fields.", fields)
	}

	hash, err := s.hasher.Hash(in.Password)
	if err != nil {
		return store.User{}, apperr.Internal(err)
	}
	u := store.User{
		ID:           ulid.MustNew(ulid.Timestamp(s.clock.Now()), rand.Reader).String(),
		Name:         name,
		Email:        email,
		PasswordHash: hash,
		Role:         in.Role,
		CreatedAt:    s.clock.Now(),
	}
	if err := s.users.CreateUser(ctx, u); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return store.User{}, apperr.Conflict("An account with that email already exists.")
		}
		return store.User{}, apperr.Internal(err)
	}
	return u, nil
}

// Login checks a password. Wrong password, unknown email and Google-only accounts all
// return the same error after one bcrypt comparison.
func (s *Service) Login(ctx context.Context, email, password string) (store.User, error) {
	fail := apperr.Unauthenticated(LoginFailedMessage)
	u, err := s.users.GetByEmail(ctx, NormalizeEmail(email))
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return store.User{}, apperr.Internal(err)
	}
	if err != nil || u.PasswordHash == "" {
		_ = s.hasher.Compare(s.dummyHash, password)
		return store.User{}, fail
	}
	if s.hasher.Compare(u.PasswordHash, password) != nil {
		return store.User{}, fail
	}
	return u, nil
}
