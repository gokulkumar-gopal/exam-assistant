// Package docs implements document upload, completion and download rules (backend-spec 8.3).
package docs

import (
	"context"
	"crypto/rand"
	"errors"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/gokulkumar/exam-assistant/backend/internal/apperr"
	"github.com/gokulkumar/exam-assistant/backend/internal/clock"
	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

const (
	// MaxUploadBytes is the largest accepted document (20 MB).
	MaxUploadBytes int64 = 20971520
	// PresignTTL is how long presigned URLs live.
	PresignTTL = 10 * time.Minute
	// PendingTTL is how long a pending row lives before DynamoDB TTL may remove it.
	PendingTTL = 24 * time.Hour
)

// docTypes maps each allowed MIME type to the Document.Type label.
var docTypes = map[string]string{
	"application/pdf": "PDF",
	"image/jpeg":      "JPG",
	"image/png":       "PNG",
	"text/plain":      "TXT",
}

// Service holds the document use cases.
type Service struct {
	docs store.DocStore
	blob Blob
	clk  clock.Clock
}

// NewService builds a Service.
func NewService(docs store.DocStore, blob Blob, clk clock.Clock) *Service {
	return &Service{docs: docs, blob: blob, clk: clk}
}

// UploadInput is the body of POST /documents/upload-url; Type is the MIME type.
type UploadInput struct {
	Name      string
	Type      string
	SizeBytes int64
}

// UploadOut is the 201 response of POST /documents/upload-url.
type UploadOut struct {
	DocID     string
	UploadURL string
	Headers   map[string]string
}

// UploadURL validates the request, records a pending document and returns a presigned PUT URL.
func (s *Service) UploadURL(ctx context.Context, user store.User, in UploadInput) (UploadOut, error) {
	fields := map[string]string{}
	typ, ok := docTypes[in.Type]
	if !ok {
		fields["type"] = "Use a PDF, JPG, PNG or TXT file."
	}
	if in.SizeBytes < 1 || in.SizeBytes > MaxUploadBytes {
		fields["sizeBytes"] = "File size must be between 1 byte and 20 MB."
	}
	if len(fields) > 0 {
		return UploadOut{}, apperr.Validation("The upload request is not valid.", fields)
	}

	now := s.clk.Now()
	id, err := ulid.New(ulid.Timestamp(now), rand.Reader)
	if err != nil {
		return UploadOut{}, apperr.Internal(err)
	}
	docID := id.String()
	key := "users/" + user.ID + "/" + docID + "/" + SanitizeName(in.Name)

	if err := s.docs.PutDoc(ctx, store.Document{
		ID: docID, OwnerID: user.ID, Name: DisplayName(in.Name), Type: typ, ContentType: in.Type,
		SizeBytes: in.SizeBytes, S3Key: key, Status: store.DocPending,
		CreatedAt: now, TTL: now.Add(PendingTTL).Unix(),
	}); err != nil {
		return UploadOut{}, apperr.Internal(err)
	}
	url, err := s.blob.PresignPut(ctx, key, in.Type, in.SizeBytes, PresignTTL)
	if err != nil {
		return UploadOut{}, apperr.Upstream()
	}
	return UploadOut{DocID: docID, UploadURL: url, Headers: map[string]string{"Content-Type": in.Type}}, nil
}

// List returns the caller's ready documents, newest first (pending uploads are hidden).
func (s *Service) List(ctx context.Context, user store.User) ([]store.Document, error) {
	ds, err := s.docs.ListReady(ctx, user.ID, store.MaxListReady)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return ds, nil
}

// FileURL is a presigned GET for a ready document.
type FileURL struct {
	URL       string
	ExpiresAt time.Time
}

// FileURL returns a presigned GET valid for PresignTTL. Another user's, missing and
// not-yet-complete (pending) documents are all 404.
func (s *Service) FileURL(ctx context.Context, user store.User, docID string) (FileURL, error) {
	d, err := s.docs.GetDoc(ctx, user.ID, docID)
	if errors.Is(err, store.ErrNotFound) {
		return FileURL{}, apperr.NotFound()
	}
	if err != nil {
		return FileURL{}, apperr.Internal(err)
	}
	if d.Status != store.DocReady {
		return FileURL{}, apperr.NotFound()
	}
	now := s.clk.Now()
	url, err := s.blob.PresignGet(ctx, d.S3Key, d.ContentType, PresignTTL)
	if err != nil {
		return FileURL{}, apperr.Upstream()
	}
	return FileURL{URL: url, ExpiresAt: now.Add(PresignTTL)}, nil
}

// Delete removes the object, then the row. It is idempotent: a document that is already
// gone, or that belongs to someone else (indistinguishable from gone, since every read is
// owner-scoped), is a silent success with no effect. A pending document of the caller is deleted too.
func (s *Service) Delete(ctx context.Context, user store.User, docID string) error {
	d, err := s.docs.GetDoc(ctx, user.ID, docID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return apperr.Internal(err)
	}
	// Object first: if it fails the row stays, so the delete can be retried.
	if err := s.blob.Delete(ctx, d.S3Key); err != nil {
		return apperr.Upstream()
	}
	if err := s.docs.DeleteDoc(ctx, user.ID, d.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return apperr.Internal(err)
	}
	return nil
}
