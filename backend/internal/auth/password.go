// Package auth holds password hashing, session tokens and the signup/login service.
package auth

import (
	"golang.org/x/crypto/bcrypt"
)

// BcryptCost is the production bcrypt cost (backend-spec 10).
const BcryptCost = 10

// Password limits are in bytes: bcrypt only uses the first 72 bytes of its input.
const (
	MinPasswordBytes = 8
	MaxPasswordBytes = 72
)

// Hasher hashes and compares passwords. It is an interface so tests can observe calls.
type Hasher interface {
	Hash(password string) (string, error)
	// Compare returns nil only when password matches hash.
	Compare(hash, password string) error
}

type bcryptHasher struct{ cost int }

// NewBcryptHasher returns a bcrypt Hasher. Production uses BcryptCost; tests may use a lower cost.
func NewBcryptHasher(cost int) Hasher { return bcryptHasher{cost: cost} }

func (b bcryptHasher) Hash(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), b.cost)
	return string(h), err
}

func (bcryptHasher) Compare(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

// checkPassword returns a field message when the password length (in bytes) is out of range.
func checkPassword(pw string) string {
	if n := len(pw); n < MinPasswordBytes || n > MaxPasswordBytes {
		return "Must be 8 to 72 bytes"
	}
	return ""
}
