// Package store defines the repository interfaces services depend on (backend-spec 6.4).
// Each interface has an in-memory fake (package memory) and a DynamoDB implementation;
// both must pass the shared contract suite in package storetest.
package store

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrNotFound means the item does not exist (or is not visible to the owner scope used).
	ErrNotFound = errors.New("store: not found")
	// ErrConflict means a uniqueness or conditional-create check failed.
	ErrConflict = errors.New("store: conflict")
	// ErrPrecondition means the item exists but is not in the state the write requires.
	ErrPrecondition = errors.New("store: precondition failed")
)

// User is the logical user entity (backend-spec 6.1). Email is stored lowercase;
// callers normalize, the store compares exactly.
type User struct {
	ID, Name, Email    string
	PasswordHash       string // empty for Google-only users
	GoogleSub          string // empty for password-only users
	Role               string // "student" | "parent" | "teacher"
	SessionsValidAfter int64  // unix seconds
	CreatedAt          time.Time
}

// Document is the logical document entity (backend-spec 6.1).
type Document struct {
	ID, OwnerID, Name string
	Type              string // "PDF" | "JPG" | "PNG" | "TXT"
	ContentType       string
	SizeBytes         int64
	Pages             int
	S3Key             string
	Status            string // "pending" | "ready"
	CreatedAt         time.Time
	TTL               int64 // unix seconds; set while pending, cleared by MarkReady
}

// Document statuses.
const (
	DocPending = "pending"
	DocReady   = "ready"
)

// MaxListReady is the cap on ListReady results.
const MaxListReady = 500

// UserStore persists users with unique email and unique Google subject.
type UserStore interface {
	// CreateUser writes the user and its email lock (and Google lock when GoogleSub is set)
	// atomically. ErrConflict on a duplicate ID, email, or Google subject.
	CreateUser(ctx context.Context, u User) error
	// CreateGoogleUser is CreateUser for a user that must have a GoogleSub.
	CreateGoogleUser(ctx context.Context, u User) error
	// LinkGoogle atomically takes the Google lock, sets GoogleSub, clears PasswordHash and
	// sets SessionsValidAfter. ErrNotFound if the user is missing; ErrConflict if the user
	// already has a GoogleSub or the subject belongs to another user.
	LinkGoogle(ctx context.Context, userID, sub string, validAfter int64) error
	GetByID(ctx context.Context, id string) (User, error)
	GetByEmail(ctx context.Context, email string) (User, error)
	GetByGoogleSub(ctx context.Context, sub string) (User, error)
	// GetManyByEmail returns found users keyed by the requested email; missing ones are omitted.
	GetManyByEmail(ctx context.Context, emails []string) (map[string]User, error)
	// GetManyByID returns found users keyed by ID; missing ones are omitted.
	GetManyByID(ctx context.Context, ids []string) (map[string]User, error)
}

// DocStore persists documents; every access is owner-scoped.
type DocStore interface {
	// PutDoc writes the document (create or replace).
	PutDoc(ctx context.Context, d Document) error
	// GetDoc returns ErrNotFound for a missing, expired or foreign document.
	GetDoc(ctx context.Context, ownerID, id string) (Document, error)
	// ListReady returns the owner's ready documents, newest first, at most limit
	// (limit <= 0 or > MaxListReady means MaxListReady).
	ListReady(ctx context.Context, ownerID string, limit int) ([]Document, error)
	// GetDocs returns the owner's documents among ids (any status), in request order;
	// missing, expired, foreign and duplicate ids are skipped.
	GetDocs(ctx context.Context, ownerID string, ids []string) ([]Document, error)
	// MarkReady sets Status ready, Pages, and clears TTL. ErrNotFound if missing;
	// ErrPrecondition if the document is not pending.
	MarkReady(ctx context.Context, ownerID, id string, pages int) error
	// DeleteDoc removes the document; ErrNotFound if missing or foreign.
	DeleteDoc(ctx context.Context, ownerID, id string) error
}
